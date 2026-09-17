package web

import (
	"context"
	"sync"
	"testing"

	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	"github.com/xibodev/facet-studio/pkg/events"
	"github.com/xibodev/facet-studio/pkg/providers"
)

type streamingFixture struct{ streamed bool }

func (*streamingFixture) GetDefaultModel() string { return "fixture" }
func (*streamingFixture) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "nonstreamed"}, nil
}
func (p *streamingFixture) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, chunk func(string)) (*providers.LLMResponse, error) {
	p.streamed = true
	chunk("First")
	chunk("First and final")
	return &providers.LLMResponse{Content: "First and final", FinishReason: "stop"}, ctx.Err()
}

func TestKernelStreamsThroughBrowserAdapter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "fixture"
	cfg.ModelList = []*config.ModelConfig{{ModelName: "fixture", Model: "fixture", Provider: "openai", Streaming: config.ModelStreamingConfig{Enabled: true}}}
	if err := configureChatStreaming(cfg); err != nil {
		t.Fatal(err)
	}
	mb := bus.NewMessageBus()
	var mu sync.Mutex
	var chunks []string
	mb.SetStreamDelegate(chatStreamDelegate{publish: func(event events.Event) {
		if event.Kind == "midden.chat.content" {
			mu.Lock()
			chunks = append(chunks, event.Payload.(map[string]any)["content"].(string))
			mu.Unlock()
		}
	}})
	p := &streamingFixture{}
	loop := agent.NewAgentLoop(cfg, mb, p)
	defer loop.Close()
	answer, err := loop.ProcessDirect(context.Background(), "hello", "stream-fixture")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !p.streamed || len(chunks) < 2 || answer != "First and final" {
		t.Fatalf("streamed=%v chunks=%v answer=%q", p.streamed, chunks, answer)
	}
}
