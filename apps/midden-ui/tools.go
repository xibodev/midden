package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xibodev/compa/pkg/agent"
	"github.com/xibodev/compa/pkg/tools"
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
	args, err := coreToolArgs(input)
	if err != nil {
		return tools.ErrorResult(err.Error())
	}
	if err := t.validate(args); err != nil {
		return tools.ErrorResult(err.Error())
	}
	result, err := t.app.runCore(ctx, args)
	output := result.Stdout
	if result.Stderr != "" {
		output += "\nDiagnostics:\n" + result.Stderr
	}
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("Midden failed: %v\n%s", err, output))
	}
	if result.StdoutClipped || result.StderrClipped {
		output += "\n[Output clipped by host; narrow the query or save an in-workspace output file.]"
	}
	return tools.SilentResult(output)
}
func (t coreTool) validate(args []string) error { return t.app.validateCoreArgs(args) }

// coreToolArgs reads the midden tool's "args" input.
func coreToolArgs(input map[string]any) ([]string, error) {
	switch value := input["args"].(type) {
	case []any:
		args := make([]string, 0, len(value))
		for _, v := range value {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("args must contain strings")
			}
			args = append(args, s)
		}
		return args, nil
	case []string:
		return value, nil
	}
	return nil, fmt.Errorf("args must be a string array")
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
