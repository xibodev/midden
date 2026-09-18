package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/module"
)

func agentWorkflowIDs() []string {
	return append(module.AgentCapabilityIDs(),
		"sessions.list", "sessions.assay", "content.types", "evidence.list",
		"recipes.list", "recipes.inspect", "recipes.preview", "recipes.design", "recipes.update",
		"recipes.evidence", "recipes.compose", "outputs.inspect", "outputs.review", "outputs.render", "outputs.export")
}

func agentCapability(id string) (module.Capability, bool) {
	allowed := false
	for _, s := range agentWorkflowIDs() {
		if s == id {
			allowed = true
			break
		}
	}
	if allowed {
		for _, c := range module.Describe().Capabilities {
			if c.ID == id {
				return c, true
			}
		}
	}
	return module.Capability{}, false
}

func runAgent(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(out, "Usage: midden agent list | schema <capability> | <capability> --input <file|-> [--home <state-directory>]\nInput is the capability payload, not a module envelope. The host owns model use. No model subprocess is started.")
		return err
	}
	if args[0] == "list" {
		caps := []module.Capability{}
		for _, id := range agentWorkflowIDs() {
			if c, ok := agentCapability(id); ok {
				caps = append(caps, c)
			}
		}
		return json.NewEncoder(out).Encode(caps)
	}
	if args[0] == "schema" {
		if len(args) != 2 {
			return fmt.Errorf("usage: midden agent schema <capability>")
		}
		c, ok := agentCapability(args[1])
		if !ok {
			return fmt.Errorf("unknown agent capability %q", args[1])
		}
		d := module.Describe()
		return json.NewEncoder(out).Encode(map[string]any{"id": c.ID, "summary": c.Summary, "input_schema": d.RequestSchemas[c.RequestSchema], "result_schema": d.ResultSchemas[c.ResultSchema]})
	}
	cap := args[0]
	if _, ok := agentCapability(cap); !ok {
		return fmt.Errorf("unknown agent capability %q; use midden agent list", cap)
	}
	fs := flag.NewFlagSet("agent "+cap, flag.ContinueOnError)
	home := fs.String("home", index.Dir(), "Midden state directory; separate from source stores")
	input := fs.String("input", "", "payload JSON file, or - for stdin")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	root, err := filepath.Abs(*home)
	if err != nil {
		return err
	}
	raw := []byte(`{}`)
	if *input != "" {
		raw, err = readAgentJSON(*input, in)
		if err != nil {
			return err
		}
	}
	req := module.Request{Protocol: module.ProtocolID, Capability: cap, Input: raw, Roots: map[string]module.Root{module.RootMiddenHome: {Path: root, Mode: "rw"}}}
	env := module.Invoke(req)
	if err = json.NewEncoder(out).Encode(env); err != nil {
		return err
	}
	if !env.OK {
		return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
	}
	return nil
}

func readAgentJSON(path string, in io.Reader) ([]byte, error) {
	const limit = 1 << 20
	reader := in
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
	}
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("request exceeds 1 MiB limit")
	}
	if !json.Valid(raw) || !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return nil, fmt.Errorf("request must be a JSON object")
	}
	return raw, nil
}
