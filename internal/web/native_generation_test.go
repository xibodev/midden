package web

import (
	"github.com/xibodev/facet-studio/pkg/providers"
	"testing"
)

func TestProductionRejectsNonDeliverables(t *testing.T) {
	for _, response := range []*providers.LLMResponse{nil, {Content: "READY"}, {ReasoningContent: "private reasoning", FinishReason: "stop"}, {Content: "half a draft", FinishReason: "length"}, {Content: "call something", ToolCalls: []providers.ToolCall{{Name: "tool"}}}} {
		if _, err := productionContent(response); err == nil {
			t.Fatalf("accepted non-deliverable %+v", response)
		}
	}
	text, err := productionContent(&providers.LLMResponse{Content: "# Complete\n\nAn evidence-grounded article.", FinishReason: "stop"})
	if err != nil || text == "" {
		t.Fatal(text, err)
	}
}
