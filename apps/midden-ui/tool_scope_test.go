package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xibodev/facet-studio/pkg/agent"
)

func TestToolReadsCannotExposeKernelCredentials(t *testing.T) {
	opts := testOptions(t)
	opts.State = filepath.Join(opts.Workspace, ".midden-ui")
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	path := filepath.Join(opts.State, "kernel", "auth.json")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"synthetic":"private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	host := &kernelHost{app: app}
	decision, err := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "read_file", Arguments: map[string]any{"path": path}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Approved {
		t.Fatal("generic read tool could expose the UI credential store")
	}
}
func TestCoreToolCannotChangeItsSourceBinding(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	tool := coreTool{app}
	for _, args := range [][]string{{"prune", "--execute"}, {"read", "-state", "other"}, {"read", "--claude-root=other"}, {"collection", "read", "../outside"}} {
		if err := tool.validate(args); err == nil {
			t.Fatalf("unsafe args accepted: %v", args)
		}
	}
}
