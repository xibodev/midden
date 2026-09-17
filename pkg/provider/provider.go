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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/facet-studio/pkg/agent"
	toolshared "github.com/xibodev/facet-studio/pkg/tools/shared"

	"github.com/mekjr1/midden/internal/module"
)

// SourceRoots declares explicit filesystem paths to AI CLI session stores.
type SourceRoots struct {
	Claude   string
	Copilot  string
	Opencode string
}

func (s SourceRoots) moduleRoots() map[string]module.Root {
	roots := make(map[string]module.Root)
	if s.Claude != "" {
		clean := filepath.Clean(s.Claude)
		if info, err := os.Stat(clean); err == nil && info.IsDir() {
			roots[module.RootClaude] = module.Root{
				Path: clean,
				Mode: "ro",
			}
		}
	}
	if s.Copilot != "" {
		clean := filepath.Clean(s.Copilot)
		if info, err := os.Stat(clean); err == nil && info.IsDir() {
			roots[module.RootCopilot] = module.Root{
				Path: clean,
				Mode: "ro",
			}
		}
	}
	if s.Opencode != "" {
		clean := filepath.Clean(s.Opencode)
		if info, err := os.Stat(clean); err == nil && !info.IsDir() {
			roots[module.RootOpencode] = module.Root{
				Path: clean,
				Mode: "ro",
			}
		}
	}
	return roots
}

// Option configures a MiddenToolProvider.
type Option func(*MiddenToolProvider)

// WithStateRoot binds standalone tools to the same evidence store as its views.
// Module hosts continue to use the isolated per-workspace default.
func WithStateRoot(root string) Option {
	return func(p *MiddenToolProvider) { p.stateRoot = root }
}

func WithNativeDriver(driver func(context.Context, string) (string, error)) Option {
	return func(p *MiddenToolProvider) { p.nativeDriver = driver }
}

// WithSourceRoots configures explicit source-store roots for the provider.
func WithSourceRoots(roots SourceRoots) Option {
	return func(p *MiddenToolProvider) {
		p.sourceRoots = roots
		p.hasExplicitRoots = true
	}
}

// MiddenToolProvider implements agent.ToolProvider by wrapping Midden's
// native module operations as Studio-compatible tools.
type MiddenToolProvider struct {
	workspace        string
	sourceRoots      SourceRoots
	hasExplicitRoots bool
	stateRoot        string
	nativeDriver     func(context.Context, string) (string, error)
}

// NewMiddenToolProvider creates a new Midden tool provider.
func NewMiddenToolProvider(opts ...Option) *MiddenToolProvider {
	p := &MiddenToolProvider{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// RegisterTools contributes all Midden capabilities to the Studio agent.
func (p *MiddenToolProvider) RegisterTools(
	workspace string,
	register func(agent.Tool),
) ([]string, func(string) (string, string, []string)) {
	p.workspace = workspace
	desc := module.Describe()

	var stateRoot string
	if workspace != "" {
		if sr, err := ModuleStateRoot(workspace); err == nil {
			stateRoot = sr
		}
	}
	if p.stateRoot != "" {
		stateRoot = p.stateRoot
	}

	var activeSourceRoots map[string]module.Root
	if p.hasExplicitRoots {
		activeSourceRoots = p.sourceRoots.moduleRoots()
	}

	var summaries []string
	if p.stateRoot != "" {
		summaries = append(summaries, "Midden standalone uses the embedded kernel for model-backed extraction and production. Do not request installation or selection of an external AI CLI. Installed portable skill examples describe other delivery forms; use the native midden_* tools here. Report source inventory from tool results, including matched/excluded counts, unavailable stores and truncation. A bounded page cannot establish that no critical sessions exist. Risk is a heuristic, not proof that resume will fail. Ask for the goal and exact scope before extraction or creation. Redaction does not establish publication safety.")
	}

	for _, cap := range desc.Capabilities {
		toolName := "midden_" + strings.ReplaceAll(cap.ID, ".", "_")

		var params map[string]any
		if rawSchema, ok := desc.RequestSchemas[cap.RequestSchema]; ok && len(rawSchema) > 0 {
			_ = json.Unmarshal(rawSchema, &params)
		}
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		description := cap.Summary
		if p.stateRoot != "" {
			if cap.ID == module.CapEvidenceExtract {
				description = "Extract bounded, redacted evidence from an exact session scope into the shared Midden index using the embedded kernel model. Assay first, then confirm the scope and intent. No external agent CLI is required."
			}
			if cap.ID == module.CapContentProduce {
				description = "Produce an evidence-grounded output using the embedded kernel model, or a deterministic pack without a model. Call content.types first; confirm the intended output and scope. No external agent CLI is required."
			}
		}

		t := &middenTool{
			name:        toolName,
			capID:       cap.ID,
			description: description,
			parameters:  params,
			provider:    p,
			strictRoots: p.hasExplicitRoots,
			sourceRoots: activeSourceRoots,
			stateRoot:   stateRoot,
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
	strictRoots bool
	sourceRoots map[string]module.Root
	stateRoot   string
}

func (t *middenTool) Name() string               { return t.name }
func (t *middenTool) Description() string        { return t.description }
func (t *middenTool) Parameters() map[string]any { return t.parameters }

func (t *middenTool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	if err := ctx.Err(); err != nil {
		return &toolshared.ToolResult{ForLLM: err.Error(), IsError: true}
	}
	inputBytes, err := json.Marshal(args)
	if err != nil {
		return &toolshared.ToolResult{
			ForLLM:  fmt.Sprintf("Error marshaling arguments: %v", err),
			IsError: true,
		}
	}

	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	req := module.Request{
		Context:             ctx,
		Protocol:            module.ProtocolID,
		Capability:          t.capID,
		RequestID:           reqID,
		Input:               inputBytes,
		Roots:               map[string]module.Root{},
		ExplicitSourceRoots: t.strictRoots,
	}
	if t.provider != nil {
		req.NativeDriver = t.provider.nativeDriver
	}

	if t.stateRoot != "" {
		req.Roots[module.RootMiddenHome] = module.Root{
			Path: t.stateRoot,
			Mode: "rw",
		}
	} else if t.provider != nil && t.provider.workspace != "" {
		req.Roots[module.RootMiddenHome] = module.Root{
			Path: t.provider.workspace,
			Mode: "rw",
		}
	}

	for k, v := range t.sourceRoots {
		req.Roots[k] = v
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
		ForLLM: resultWithWarnings(env.Result, env.Warnings),
	}
}

func resultWithWarnings(raw json.RawMessage, warnings []string) string {
	if len(warnings) == 0 {
		return string(raw)
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return string(raw)
	}
	result["warnings"] = warnings
	encoded, err := json.Marshal(result)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}
