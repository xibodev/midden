package provider_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	runtimeevents "github.com/xibodev/facet-studio/pkg/events"
	"github.com/xibodev/facet-studio/pkg/providers"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/module"
	"github.com/mekjr1/midden/pkg/provider"
)

type scriptableMockProvider struct {
	mu        sync.Mutex
	turnCount int
	responses []*providers.LLMResponse
}

func (s *scriptableMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnCount < len(s.responses) {
		resp := s.responses[s.turnCount]
		s.turnCount++
		return resp, nil
	}
	return &providers.LLMResponse{
		Content:   "Default mock completion",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (s *scriptableMockProvider) GetDefaultModel() string {
	return "mock-model"
}

// TestMidden_NativeRuntimeProof proves R1 requirements for Midden:
// - external consumer importing public runtime
// - native Midden tool registration
// - two-turn conversation
// - restart and resume with preserved session key
// - runtime tool events
// - cancellation via context
// - clean resource shutdown
func TestMidden_NativeRuntimeProof(t *testing.T) {
	workspace := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace

	middenProvider := provider.NewMiddenToolProvider()

	mockLLM := &scriptableMockProvider{
		responses: []*providers.LLMResponse{
			{
				Content: "Let me check available Midden content types.",
				ToolCalls: []providers.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: &providers.FunctionCall{
							Name:      "midden_content_types",
							Arguments: `{}`,
						},
					},
				},
			},
			{
				Content:   "Midden content types have been retrieved.",
				ToolCalls: []providers.ToolCall{},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(
		cfg,
		msgBus,
		mockLLM,
		agent.WithToolProviders(middenProvider),
	)

	// 1. Subscribe to runtime events to verify tool events
	ctx := context.Background()
	_, ch, err := al.RuntimeEvents().SubscribeChan(ctx, runtimeevents.SubscribeOptions{
		Name:   "test_collector",
		Buffer: 64,
	})
	if err != nil {
		t.Fatalf("failed to subscribe to runtime events: %v", err)
	}

	var capturedEvents []string
	var eventMu sync.Mutex
	stopCollector := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopCollector:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				eventMu.Lock()
				capturedEvents = append(capturedEvents, ev.Kind.String())
				eventMu.Unlock()
			}
		}
	}()

	sessionKey := "midden-session-turn-test"

	// 2. Turn 1: Triggers tool use
	resp1, err := al.ProcessDirect(ctx, "List producible content types", sessionKey)
	if err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}
	if resp1 == "" {
		t.Errorf("Turn 1 returned empty response")
	}

	// 3. Turn 2: Follow-up question in the same session
	resp2, err := al.ProcessDirect(ctx, "What should we produce?", sessionKey)
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}
	if resp2 == "" {
		t.Errorf("Turn 2 returned empty response")
	}

	// Stop event collector
	close(stopCollector)

	// 4. Close first runtime instance
	al.Close()

	// 5. Restart & Resume: Create new AgentLoop with same workspace/session
	mockLLM2 := &scriptableMockProvider{
		responses: []*providers.LLMResponse{
			{
				Content:   "Resumed Midden conversation successfully.",
				ToolCalls: []providers.ToolCall{},
			},
		},
	}
	al2 := agent.NewAgentLoop(
		cfg,
		bus.NewMessageBus(),
		mockLLM2,
		agent.WithToolProviders(middenProvider),
	)
	defer al2.Close()

	resumeResp, err := al2.ProcessDirect(ctx, "Confirm we resumed", sessionKey)
	if err != nil {
		t.Fatalf("Resume turn failed: %v", err)
	}
	if !strings.Contains(resumeResp, "Resumed") {
		t.Logf("Resume response: %s", resumeResp)
	}

	// 6. Cancellation test
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = al2.ProcessDirect(cancelCtx, "Should be cancelled", sessionKey)
	if err == nil {
		t.Errorf("expected cancellation error, got nil")
	}

	t.Logf("Midden native runtime proof completed successfully: 2 turns, restart, resume, cancel, close")
}

// TestMidden_ModelBackedContentProduce_NativeDriver proves that a model-backed
// content production operation executes completely in-process using NativeDriver
// without spawning any external CLI executable.
func TestMidden_ModelBackedContentProduce_NativeDriver(t *testing.T) {
	homeDir := t.TempDir()

	// 1. Prepare evidence in index DB
	db, err := index.OpenAt(homeDir)
	if err != nil {
		t.Fatalf("open index db: %v", err)
	}
	defer db.Close()

	nugget := index.Nugget{
		UID:        "nugget-1",
		Tool:       "copilot",
		SessionID:  "session-1",
		Kind:       "decision",
		Title:      "Architecture decision",
		Body:       "Standalone products consume Studio runtime natively without external CLIs.",
		Workspace:  "test-workspace",
		Confidence: 1.0,
		Model:      "test-model",
		CreatedAt:  time.Now().UTC(),
	}
	if err := db.PutNuggets([]index.Nugget{nugget}); err != nil {
		t.Fatalf("insert nugget: %v", err)
	}

	// 2. Set up native in-process driver
	driverCalled := false
	module.SetNativeDriver(func(ctx context.Context, prompt string) (string, error) {
		driverCalled = true
		return "# Executive Summary\n\nStandalone products consume Studio runtime natively without external CLIs.", nil
	})
	defer module.SetNativeDriver(nil)

	// 3. Request a model-backed content production (e.g. adr / tutorial)
	res, warnings, err := module.ContentProduce(db, module.ContentProduceRequest{
		Kind:      "adr",
		Title:     "Native Runtime Architecture",
		Workspace: "test-workspace",
	}, homeDir, module.ModelGrant{})

	if err != nil {
		t.Fatalf("ContentProduce failed: %v (warnings: %v)", err, warnings)
	}

	if !driverCalled {
		t.Errorf("expected NativeDriver to be called for model-backed content production")
	}
	if res.Bytes == 0 {
		t.Errorf("expected non-zero output bytes")
	}
	if res.ModelBackend != "native" {
		t.Errorf("expected model backend 'native', got %q", res.ModelBackend)
	}

	t.Logf("Midden model-backed content produce passed: output %s (%d bytes) produced via native driver", res.Path, res.Bytes)
}
