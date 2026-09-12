// Package provider implements agent.ToolProvider for Midden's native operations.
//
// This is the NATIVE binding path: it registers Midden's session mining,
// assay, evidence extraction, and content production capabilities directly
// with the Studio kernel, paying no subprocess or external CLI cost to host itself.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xibodev/facet-studio/pkg/agent"
	toolshared "github.com/xibodev/facet-studio/pkg/tools/shared"

	"github.com/mekjr1/midden/internal/module"
)

// MiddenToolProvider implements agent.ToolProvider by wrapping Midden's
// native module operations as Studio-compatible tools.
type MiddenToolProvider struct {
	workspace string
}

// NewMiddenToolProvider creates a new Midden tool provider.
func NewMiddenToolProvider() *MiddenToolProvider {
	return &MiddenToolProvider{}
}

// RegisterTools contributes all Midden capabilities to the Studio agent.
func (p *MiddenToolProvider) RegisterTools(
	workspace string,
	register func(agent.Tool),
) ([]string, func(string) (string, string, []string)) {
	p.workspace = workspace
	desc := module.Describe()

	var summaries []string

	for _, cap := range desc.Capabilities {
		toolName := "midden_" + strings.ReplaceAll(cap.ID, ".", "_")

		var params map[string]any
		if rawSchema, ok := desc.RequestSchemas[cap.RequestSchema]; ok && len(rawSchema) > 0 {
			_ = json.Unmarshal(rawSchema, &params)
		}
		if params == nil {
			params = map[string]any{"type": "object"}
		}

		t := &middenTool{
			name:        toolName,
			capID:       cap.ID,
			description: cap.Summary,
			parameters:  params,
			provider:    p,
		}

		register(t)
		summaries = append(summaries, fmt.Sprintf("%s: %s", toolName, cap.Title))
	}

	return summaries, nil
}

type middenTool struct {
	name        string
	capID       string
	description string
	parameters  map[string]any
	provider    *MiddenToolProvider
}

func (t *middenTool) Name() string                 { return t.name }
func (t *middenTool) Description() string          { return t.description }
func (t *middenTool) Parameters() map[string]any  { return t.parameters }

func (t *middenTool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	inputBytes, err := json.Marshal(args)
	if err != nil {
		return &toolshared.ToolResult{
			ForLLM:  fmt.Sprintf("Error marshaling arguments: %v", err),
			IsError: true,
		}
	}

	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	req := module.Request{
		Protocol:   module.ProtocolID,
		Capability: t.capID,
		RequestID:  reqID,
		Input:      inputBytes,
		Roots:      map[string]module.Root{},
	}

	if t.provider != nil && t.provider.workspace != "" {
		req.Roots[module.RootMiddenHome] = module.Root{
			Path: t.provider.workspace,
			Mode: "rw",
		}
	}

	env := module.Invoke(req)

	if !env.OK {
		msg := "unknown error"
		if env.Error != nil {
			msg = env.Error.Message
		}
		return &toolshared.ToolResult{
			ForLLM:  msg,
			IsError: true,
		}
	}

	return &toolshared.ToolResult{
		ForLLM: string(env.Result),
	}
}
