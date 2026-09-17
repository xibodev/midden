package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/module"
	"github.com/xibodev/facet-studio/pkg/agent"
)

func TestNativeGuidanceIncludesCanonicalOverlayAndDeliveryBinding(t *testing.T) {
	parts, err := (RecoveryGuidance{}).ContributePrompt(context.Background(), agent.PromptBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts=%d", len(parts))
	}
	for _, overlay := range module.Describe().AgentOverlays {
		raw, _ := module.OverlayContent(overlay.Path)
		if !strings.Contains(parts[0].Content, string(raw)) {
			t.Errorf("missing canonical overlay %s", overlay.ID)
		}
	}
	if !strings.Contains(parts[0].Content, nativeRecoveryBinding) {
		t.Fatal("missing native binding")
	}
	if parts[0].Source.ID != (RecoveryGuidance{}).PromptSource().ID {
		t.Fatal("guidance lacks source attribution")
	}
}
