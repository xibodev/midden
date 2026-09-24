package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/tools"
)

type coreTools struct {
	app   *App
	extra []agent.Tool
}

func (p coreTools) RegisterTools(workspace string, register func(agent.Tool)) ([]string, func(string) (string, string, []string)) {
	register(coreTool{p.app})
	for _, tool := range p.extra {
		register(tool)
	}
	binding, _ := json.Marshal(map[string]string{"executable": p.app.opts.Core, "MIDDEN_HOME": filepath.Join(p.app.opts.State, "core"), "bundle": p.app.projected})
	return []string{
		"Midden's canonical outcome skills are installed. Use the relevant skill and its references to investigate and create actual workspace files; the operator supplies the goal, not a sequence of tools.",
		"The midden tool runs the normal deterministic core CLI with scoped source access. Prefer it for source data. Core binding: " + string(binding),
		"Save authored deliverables inside the workspace. Kernel/session state and bundle installation are not artifacts. Use the host's file/image tools to inspect results. Tool denials are final for that operation.",
	}, nil
}

type coreTool struct{ app *App }

func (coreTool) Name() string { return "midden" }
func (coreTool) Description() string {
	return "Run a deterministic Midden CLI data command. Inspect help, discover/read exact sessions, search pinned views, collect records/assets, and inspect/verify/export collections. No model or source-maintenance commands."
}
func (coreTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Normal CLI arguments, for example [\"ls\",\"--days\",\"7\",\"--json\"]. Paths must stay in the workspace."}}, "required": []string{"args"}, "additionalProperties": false}
}
func (t coreTool) Execute(ctx context.Context, input map[string]any) *tools.ToolResult {
	var args []string
	switch value := input["args"].(type) {
	case []any:
		for _, v := range value {
			s, ok := v.(string)
			if !ok {
				return tools.ErrorResult("args must contain strings")
			}
			args = append(args, s)
		}
	case []string:
		args = value
	default:
		return tools.ErrorResult("args must be a string array")
	}
	if err := t.validate(args); err != nil {
		return tools.ErrorResult(err.Error())
	}
	cmd := exec.CommandContext(ctx, t.app.opts.Core, args...)
	cmd.Dir = t.app.opts.Workspace
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "MIDDEN_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "MIDDEN_HOME="+filepath.Join(t.app.opts.State, "core"))
	for key, value := range t.app.opts.SourceEnv {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	var stdout, stderr boundedOutput
	stdout.max = 64 << 10
	stderr.max = 8 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nDiagnostics:\n" + stderr.String()
	}
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("Midden failed: %v\n%s", err, output))
	}
	if stdout.clipped || stderr.clipped {
		output += "\n[Output clipped by host; narrow the query or save an in-workspace output file.]"
	}
	return tools.SilentResult(output)
}
func (t coreTool) validate(args []string) error {
	if len(args) == 0 || len(args) > 100 {
		return fmt.Errorf("provide 1..100 CLI arguments")
	}
	switch args[0] {
	case "ls", "list", "find", "show", "read", "search", "collect", "collection", "assets", "brief", "assay", "usage", "help", "--help", "version":
	default:
		return fmt.Errorf("command is not available through the data-only host tool")
	}
	total := 0
	positional := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		total += len(arg)
		if total > 32768 {
			return fmt.Errorf("arguments exceed size limit")
		}
		key, value, attached := strings.Cut(arg, "=")
		key = "--" + strings.TrimLeft(key, "-")
		switch key {
		case "--state", "--copilot-root", "--claude-root", "--opencode-db", "--sources-only":
			return fmt.Errorf("source/state bindings are controlled by the host")
		}
		if key == "--out" || key == "-out" {
			if !attached {
				if i+1 >= len(args) {
					return fmt.Errorf("--out requires a path")
				}
				value = args[i+1]
			}
			if err := t.app.writePath(value); err != nil {
				return err
			}
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--" {
				return fmt.Errorf("argument separator is not supported by the restricted core tool")
			}
			if !attached {
				switch key {
				case "--json", "--all", "--live", "--assets", "--include-tools", "--help", "--h", "--group", "--sizes", "--handoff":
				case "--tool", "--session", "--query", "--record", "--view", "--limit", "--offset", "--chars", "--before", "--after", "--format", "--out", "--days", "--workspace", "--repo", "--top", "--turns", "--clip", "--scan", "--quote":
					if i+1 >= len(args) {
						return fmt.Errorf("flag %s requires a value", arg)
					}
					i++
				default:
					return fmt.Errorf("unsupported core flag %s; inspect the command help", arg)
				}
			} else {
				switch key {
				case "--json", "--all", "--live", "--assets", "--include-tools", "--help", "--group", "--sizes", "--handoff", "--tool", "--session", "--query", "--record", "--view", "--limit", "--offset", "--chars", "--before", "--after", "--format", "--out", "--days", "--workspace", "--repo", "--top", "--turns", "--clip", "--scan", "--quote":
				default:
					return fmt.Errorf("unsupported core flag %s", key)
				}
			}
			continue
		}
		if args[0] == "collection" {
			if positional >= 2 {
				if err := t.app.writePath(arg); err != nil {
					return err
				}
			}
			positional++
		}
	}
	return nil
}

func withinWorkspace(workspace, name string) error {
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	pending := []string{}
	for {
		if _, err := os.Lstat(path); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return fmt.Errorf("path cannot be resolved")
		}
		pending = append(pending, filepath.Base(path))
		path = parent
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	for i := len(pending) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, pending[i])
	}
	rel, err := filepath.Rel(workspace, resolved)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return fmt.Errorf("output path must be inside the workspace")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") || part == "sessions" {
			return fmt.Errorf("output may not replace host or repository state")
		}
	}
	return nil
}

type boundedOutput struct {
	bytes.Buffer
	max     int
	clipped bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.max - b.Len()
	if remaining < n {
		b.clipped = true
		p = p[:max(remaining, 0)]
	}
	_, err := b.Buffer.Write(p)
	return n, err
}
