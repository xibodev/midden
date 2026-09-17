package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/module"
	"github.com/xibodev/facet-studio/pkg/agent"
)

// RecoveryGuidance composes the existing canonical overlay through the kernel's
// public extension interface. The overlay teaches recovery; the binding note
// explains how that capability executes in this delivery form.
type RecoveryGuidance struct{}

const recoveryGuidanceSource agent.PromptSourceID = "midden:recovery"

func (RecoveryGuidance) PromptSource() agent.PromptSourceDescriptor {
	return agent.PromptSourceDescriptor{
		ID: recoveryGuidanceSource, Owner: "midden", Description: "Midden recovery capability and native binding",
		Allowed:         []agent.PromptPlacement{{Layer: agent.PromptLayerInstruction, Slot: agent.PromptSlotWorkspace}},
		StableByDefault: true,
	}
}

func (RecoveryGuidance) ContributePrompt(ctx context.Context, _ agent.PromptBuildRequest) ([]agent.PromptPart, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var content strings.Builder
	for _, overlay := range module.Describe().AgentOverlays {
		raw, ok := module.OverlayContent(overlay.Path)
		if !ok || module.DigestSHA256(raw) != overlay.Digest {
			return nil, fmt.Errorf("recovery overlay %s is missing or mismatched", overlay.ID)
		}
		content.Write(raw)
		content.WriteString("\n\n")
	}
	content.WriteString(module.WorkflowGuidance())
	content.WriteString(nativeRecoveryBinding)
	return []agent.PromptPart{{
		ID: "midden.recovery", Layer: agent.PromptLayerInstruction, Slot: agent.PromptSlotWorkspace,
		Source: agent.PromptSource{ID: recoveryGuidanceSource, Name: "Midden canonical recovery capability"},
		Title:  "Midden recovery workflow", Content: content.String(), Stable: true,
	}}, nil
}

const nativeRecoveryBinding = `Native standalone binding:
This is Midden, composed with the embedded Studio kernel. The portable guidance above also serves external CLI hosts; its external-CLI invocation examples do not apply to this binding.
Use the registered midden_* tools in-process. Evidence extraction and narrative creation use the configured kernel model, not a separate Copilot, Claude or OpenCode executable. Those products are source-store formats here.
The kernel owns models, authentication, sessions, approvals, tool dispatch and cancellation. Runtime & Models exposes its connection controls.
Midden's agent and deterministic workbench share one evidence store. Use existing evidence and saved outputs before proposing another extraction. Ask for the goal, source scope, intended audience and output when those are unclear. Assay before extraction; review coverage before planning; verify a draft against its evidence before treating it as usable. A seed, draft, rendering, reviewed output and exported file are distinct outcomes.
Report inventory from tool results: matched and excluded counts, unavailable stores, truncation and scope-wide risk_counts. Do not infer absence of critical sessions from a bounded page. Risk is a heuristic, not proof that resume will fail. Redaction reduces exposure but never guarantees absence of secrets or suitability for publication.
Do not invent tools or claim to have persisted a recipe, revision, review or export if the available tool interface cannot perform that action. Explain the missing capability and preserve the user's goal instead. Tool authorization is enforced by the kernel's approval callback, not by these instructions.`
