package main

import (
	"context"
	"strings"
	"testing"
)

func TestApprovedWorkspaceShellCanRunOnTheUIChannel(t *testing.T) {
	app, _, _ := kernelApp(t)
	runtime, err := newKernel(app)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	host := runtime.(*kernelHost)
	result := host.loop.GetRegistry().GetDefaultAgent().Tools.ExecuteWithContext(context.Background(), "exec",
		map[string]any{"action": "run", "command": "echo kernel-exec-fixture"}, "midden-ui", "synthetic", nil)
	if result.IsError || !strings.Contains(result.ForLLM, "kernel-exec-fixture") {
		t.Fatalf("approved UI shell rejected: %s", result.ForLLM)
	}
}
