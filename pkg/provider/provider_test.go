package provider_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/mekjr1/midden/internal/module"
	"github.com/mekjr1/midden/pkg/provider"
)

func TestMiddenToolProvider_Registration(t *testing.T) {
	p := provider.NewMiddenToolProvider()
	registered := make(map[string]agent.Tool)
	summaries, knowledge := p.RegisterTools("test_workspace", func(tool agent.Tool) {
		registered[tool.Name()] = tool
	})

	desc := module.Describe()
	if len(summaries) != len(desc.Capabilities) {
		t.Fatalf("expected %d summaries, got %d", len(desc.Capabilities), len(summaries))
	}
	if len(registered) != len(desc.Capabilities) {
		t.Fatalf("expected %d registered tools, got %d", len(desc.Capabilities), len(registered))
	}

	for _, cap := range desc.Capabilities {
		toolName := "midden_" + strings.ReplaceAll(cap.ID, ".", "_")
		tool, exists := registered[toolName]
		if !exists {
			t.Errorf("expected tool %q to be registered", toolName)
			continue
		}
		if tool.Name() != toolName {
			t.Errorf("expected name %q, got %q", toolName, tool.Name())
		}
		if tool.Description() == "" {
			t.Errorf("tool %q has empty description", toolName)
		}
		if tool.Parameters() == nil {
			t.Errorf("tool %q has nil parameters", toolName)
		}
	}

	if knowledge != nil {
		t.Errorf("expected nil knowledge func for standalone tool provider")
	}
}

func TestMiddenToolProvider_Execution(t *testing.T) {
	p := provider.NewMiddenToolProvider()
	registered := make(map[string]agent.Tool)
	p.RegisterTools(t.TempDir(), func(tool agent.Tool) {
		registered[tool.Name()] = tool
	})

	typesTool, exists := registered["midden_content_types"]
	if !exists {
		t.Fatal("midden_content_types tool not registered")
	}

	ctx := context.Background()
	res := typesTool.Execute(ctx, map[string]any{})
	if res == nil {
		t.Fatal("expected non-nil ToolResult")
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.ForLLM)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v, raw: %s", err, res.ForLLM)
	}
	if _, ok := parsed["types"]; !ok {
		t.Errorf("expected 'types' key in result, got %v", parsed)
	}
}
