package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/index"
	middenprovider "github.com/mekjr1/midden/pkg/provider"
	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	runtimeevents "github.com/xibodev/facet-studio/pkg/events"
	"github.com/xibodev/facet-studio/pkg/providers"
)

// Opt-in real browser acceptance. All evidence and generated files belong to a
// temporary test store. No operator sessions or credentials are used.
func TestBrowserWorkflow(t *testing.T) {
	if os.Getenv("MIDDEN_BROWSER_TEST") != "1" {
		t.Skip("set MIDDEN_BROWSER_TEST=1 and install playwright-core to run browser acceptance")
	}
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.PutNuggets([]index.Nugget{{UID: "browser-fixture", Tool: "claude", SessionID: "synthetic-browser-session", Kind: "decision", Title: "Synthetic recovery decision", Body: "Preserve evidence provenance and require review before exporting.", Workspace: "browser-fixture", Confidence: .99, CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db, jobs: NewJobs(db), cache: newSnapshotCache()}
	defer s.Close()
	if os.Getenv("MIDDEN_BROWSER_AGENT_TEST") == "1" {
		t.Setenv(config.EnvHome, filepath.Join(home, "kernel"))
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = filepath.Join(home, "kernel", "workspace")
		loop := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &browserWorkflowModel{}, agent.WithToolProviders(middenprovider.NewMiddenToolProvider(middenprovider.WithStateRoot(home))))
		if err := loop.MountHook(agent.NamedHook("browser-review", browserApprover{s})); err != nil {
			t.Fatal(err)
		}
		s.SetAgentLoop(loop)
	}
	if os.Getenv("MIDDEN_BROWSER_STREAM_TEST") == "1" {
		t.Setenv(config.EnvHome, filepath.Join(home, "kernel"))
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = filepath.Join(home, "kernel", "workspace")
		cfg.Agents.Defaults.ModelName = "fixture"
		cfg.ModelList = []*config.ModelConfig{{ModelName: "fixture", Model: "fixture", Provider: "openai", Streaming: config.ModelStreamingConfig{Enabled: true}}}
		if err := configureChatStreaming(cfg); err != nil {
			t.Fatal(err)
		}
		eb := runtimeevents.NewBus()
		defer eb.Close()
		mb := bus.NewMessageBus()
		loop := agent.NewAgentLoop(cfg, mb, &slowBrowserStream{}, agent.WithRuntimeEvents(eb))
		mb.SetStreamDelegate(chatStreamDelegate{publish: func(e runtimeevents.Event) { eb.PublishNonBlocking(e) }})
		s.SetAgentLoop(loop)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	cmd := exec.Command("node", filepath.Join("..", "..", "scripts", "ui-workflow-browser.cjs"), server.URL)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := db.RefineryOutputs("", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := 1
	if os.Getenv("MIDDEN_BROWSER_AGENT_TEST") == "1" {
		want = 2
	}
	if len(outputs) != want || outputs[0].Status != "exported" {
		t.Fatalf("browser did not finish reviewed export: %+v", outputs)
	}
}

type slowBrowserStream struct{}

func (*slowBrowserStream) GetDefaultModel() string { return "fixture" }
func (*slowBrowserStream) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return nil, fmt.Errorf("expected streaming")
}
func (*slowBrowserStream) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, chunk func(string)) (*providers.LLMResponse, error) {
	chunk("A visible streaming response")
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Second):
		return &providers.LLMResponse{Content: "A visible streaming response completed."}, nil
	}
}

// Scripted model exercises the real kernel dispatch, browser approvals and
// shared workflow without sending fixture data to an external provider.
type browserWorkflowModel struct {
	step                         int
	recipe, output, body, digest string
}

func (*browserWorkflowModel) GetDefaultModel() string { return "browser-fixture-model" }
func (m *browserWorkflowModel) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "tool" {
			continue
		}
		var value struct {
			Recipe  index.Recipe           `json:"recipe"`
			Outputs []index.RefineryOutput `json:"outputs"`
			Body    string                 `json:"body"`
			Digest  string                 `json:"content_digest"`
		}
		if json.Unmarshal([]byte(messages[i].Content), &value) == nil {
			if value.Recipe.UID != "" {
				m.recipe = value.Recipe.UID
			}
			if len(value.Outputs) > 0 {
				m.output = value.Outputs[0].UID
			}
			if value.Body != "" {
				m.body = value.Body
				m.digest = value.Digest
			}
		}
		break
	}
	var name string
	var args map[string]any
	switch m.step {
	case 0:
		name = "midden_recipes_design"
		args = map[string]any{"title": "Agent browser fixture", "workspace": "browser-fixture", "output_kinds": []string{"provenance_manifest"}, "evidence_ids": []string{"browser-fixture"}}
	case 1:
		name = "midden_recipes_evidence"
		args = map[string]any{"recipe_id": m.recipe, "evidence_ids": []string{"browser-fixture"}, "decision": "approved"}
	case 2:
		name = "midden_recipes_produce"
		args = map[string]any{"recipe_id": m.recipe}
	case 3:
		name = "midden_outputs_inspect"
		args = map[string]any{"output_id": m.output}
	case 4:
		name = "midden_outputs_review"
		args = map[string]any{"output_id": m.output, "body": m.body, "expected_digest": m.digest, "decision": "reviewed"}
	case 5:
		name = "midden_outputs_export"
		args = map[string]any{"output_id": m.output, "destination": "local_vault"}
	default:
		return &providers.LLMResponse{Content: "Browser fixture workflow exported with provenance."}, nil
	}
	m.step++
	raw, _ := json.Marshal(args)
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: name, Type: "function", Function: &providers.FunctionCall{Name: name, Arguments: string(raw)}}}}, nil
}
