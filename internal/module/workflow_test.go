package module

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/index"
)

func TestWorkflowReadDoesNotCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	env := Invoke(Request{Capability: "recipes.list", Input: json.RawMessage(`{}`), Roots: map[string]Root{RootMiddenHome: {Path: root, Mode: "ro"}}})
	if env.OK {
		t.Fatal("missing state reported as successful inventory")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("read created state: %v", err)
	}
}

func TestWorkflowRejectsIgnoredArguments(t *testing.T) {
	env := Invoke(Request{Capability: "recipes.list", Input: json.RawMessage(`{"decision":"approved"}`)})
	if env.OK || env.Error.Code != ErrInvalidRequest {
		t.Fatalf("ignored arguments accepted: %+v", env)
	}
}

func TestWorkflowCompleteJourneyThroughModule(t *testing.T) {
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutNuggets([]index.Nugget{{UID: "e1", Tool: "claude", SessionID: "fixture", Kind: "decision", Title: "Use explicit scope", Body: "Recover only selected evidence and preserve provenance.", Workspace: "fixture", Confidence: .98, CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	call := func(cap string, input map[string]any) Envelope {
		raw, _ := json.Marshal(input)
		return Invoke(Request{Protocol: ProtocolID, Capability: cap, Input: raw, ConfirmOperator: testOperatorConsent, Roots: map[string]Root{RootMiddenHome: {Path: home, Mode: "rw"}}, ExplicitSourceRoots: true})
	}
	decode := func(env Envelope) map[string]json.RawMessage {
		t.Helper()
		if !env.OK {
			t.Fatalf("invoke: %+v", env.Error)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(env.Result, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	preview := decode(call("recipes.preview", map[string]any{"workspace": "fixture", "output_kinds": []string{"provenance_manifest"}}))
	if string(preview["preview"]) != "true" {
		t.Fatal("preview not marked")
	}
	listed := decode(call("recipes.list", map[string]any{}))
	if string(listed["recipes"]) != "[]" {
		t.Fatal("preview persisted recipe")
	}
	designed := decode(call("recipes.design", map[string]any{"workspace": "fixture", "title": "Fixture recovery", "output_kinds": []string{"provenance_manifest"}, "evidence_ids": []string{"e1"}}))
	var recipe index.Recipe
	json.Unmarshal(designed["recipe"], &recipe)
	if call("recipes.produce", map[string]any{"recipe_id": recipe.UID}).OK {
		t.Fatal("unapproved production succeeded")
	}
	decode(call("recipes.evidence", map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e1"}, "decision": "approved"}))
	produced := decode(call("recipes.produce", map[string]any{"recipe_id": recipe.UID}))
	var outputs []index.RefineryOutput
	json.Unmarshal(produced["outputs"], &outputs)
	if len(outputs) != 1 {
		t.Fatal("missing output")
	}
	o := outputs[0]
	if call("outputs.export", map[string]any{"output_id": o.UID}).OK {
		t.Fatal("draft exported")
	}
	inspected := decode(call("outputs.inspect", map[string]any{"output_id": o.UID}))
	var body, digest string
	json.Unmarshal(inspected["body"], &body)
	json.Unmarshal(inspected["content_digest"], &digest)
	if call("outputs.review", map[string]any{"output_id": o.UID, "body": body, "decision": "reviewed", "expected_digest": "stale"}).OK {
		t.Fatal("stale review accepted")
	}
	decode(call("outputs.review", map[string]any{"output_id": o.UID, "body": body, "decision": "reviewed", "expected_digest": digest, "review_notes": "Inspected the deterministic provenance manifest against the selected evidence."}))
	exported := decode(call("outputs.export", map[string]any{"output_id": o.UID}))
	var path string
	json.Unmarshal(exported["path"], &path)
	if raw, err := os.ReadFile(path); err != nil || string(raw) != body {
		t.Fatalf("export mismatch: %v", err)
	}
	if err = os.WriteFile(o.Path, []byte("changed behind review"), 0600); err != nil {
		t.Fatal(err)
	}
	if call("outputs.export", map[string]any{"output_id": o.UID}).OK {
		t.Fatal("modified reviewed output exported")
	}
}

func TestWorkflowNativeModelAndMissingWriteAuthority(t *testing.T) {
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.PutNuggets([]index.Nugget{{UID: "e", Kind: "decision", Body: "A sufficiently grounded design decision", Title: "Decision", Confidence: 1, Workspace: "w", CreatedAt: time.Now()}})
	raw := json.RawMessage(`{"workspace":"w","output_kinds":["adr"]}`)
	req := Request{Capability: "recipes.design", Input: raw, ConfirmOperator: testOperatorConsent, Roots: map[string]Root{RootMiddenHome: {Path: home, Mode: "ro"}}}
	if env := Invoke(req); env.OK || env.Error.Code != ErrPermissionDenied {
		t.Fatalf("write authority ignored: %+v", env)
	}
	req.Roots[RootMiddenHome] = Root{Path: home, Mode: "rw"}
	env := Invoke(req)
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result struct {
		Recipe index.Recipe `json:"recipe"`
	}
	json.Unmarshal(env.Result, &result)
	req.Capability = "recipes.evidence"
	req.Input, _ = json.Marshal(map[string]any{"recipe_id": result.Recipe.UID, "evidence_ids": []string{"e"}, "decision": "approved"})
	if env = Invoke(req); !env.OK {
		t.Fatal(env.Error)
	}
	req.Capability = "recipes.produce"
	req.Input, _ = json.Marshal(map[string]any{"recipe_id": result.Recipe.UID})
	called := false
	req.NativeDriver = func(ctx context.Context, prompt string) (string, error) {
		called = true
		return "# Decision\nUse explicit scope.", ctx.Err()
	}
	if env = Invoke(req); !env.OK || !called {
		t.Fatalf("native production failed: called=%v env=%+v", called, env.Error)
	}
}
