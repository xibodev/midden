package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRepliesFitInlineBudgetAndKeepInspectableSource(t *testing.T) {
	root := writeSyntheticClaudeStore(t)
	path := filepath.Join(root, "projects", "E--synthetic-workspace", "11111111-2222-3333-4444-555555555555.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 160; i++ {
		raw, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": "2026-01-02T00:00:00Z", "message": map[string]string{"content": strings.Repeat("詳しい evidence and useful context. ", 80)}})
		if _, err = f.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	req := Request{Capability: "evidence.prepare", Input: json.RawMessage(`{"source":{"tool":"claude","session_id":"11111111-2222-3333-4444-555555555555"},"max_records":80}`),
		ExplicitSourceRoots: true, Roots: map[string]Root{RootClaude: {Path: root, Mode: "ro"}, RootMiddenHome: {Path: t.TempDir(), Mode: "rw"}}}
	env := InvokeAgent(req)
	if !env.OK {
		t.Fatal(env.Error)
	}
	wire, _ := json.Marshal(env)
	if len(wire) > 8192 {
		t.Fatalf("normal reply exceeded 8 KiB: %d", len(wire))
	}
	var preview struct {
		PacketID string `json:"packet_id"`
		View     struct {
			ID string `json:"result_id"`
		} `json:"_view"`
	}
	json.Unmarshal(env.Result, &preview)
	if preview.PacketID == "" || preview.View.ID == "" {
		t.Fatal("compact result lost identity or inspect handle")
	}
	req.Capability = "results.inspect"
	req.Input, _ = json.Marshal(map[string]any{"result_id": preview.View.ID, "path": "/records", "limit": 2})
	page := InvokeAgent(req)
	if !page.OK {
		t.Fatal(page.Error)
	}
	wire, _ = json.Marshal(page)
	if len(wire) > 16384 {
		t.Fatalf("page exceeded hard envelope limit: %d", len(wire))
	}
	var result struct {
		Value []EvidenceRecord `json:"value"`
		Next  *int             `json:"next_offset"`
	}
	json.Unmarshal(page.Result, &result)
	if len(result.Value) == 0 || result.Next == nil {
		t.Fatal("source page is not inspectable or resumable")
	}
}

func TestAgentMutationAcknowledgesRatherThanRepeatingWholeProject(t *testing.T) {
	req, recipe := operatorFixture(t)
	req.Capability = "recipes.inspect"
	req.Input, _ = json.Marshal(map[string]string{"recipe_id": recipe.UID})
	inspection := InvokeAgent(req)
	if !inspection.OK {
		t.Fatal(inspection.Error)
	}
	req.Capability = "recipes.update"
	req.Input, _ = json.Marshal(map[string]any{"recipe_id": recipe.UID, "prompt": strings.Repeat("A detailed plan. ", 2000)})
	env := InvokeAgent(req)
	if !env.OK {
		t.Fatal(env.Error)
	}
	wire, _ := json.Marshal(env)
	if len(wire) > 2048 {
		t.Fatalf("mutation acknowledgement too large: %d", len(wire))
	}
	if strings.Contains(string(env.Result), strings.Repeat("A detailed plan. ", 100)) {
		t.Fatal("mutation echoed the plan")
	}
}
