package module

import (
	"encoding/json"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func operatorFixture(t *testing.T) (Request, index.Recipe) {
	t.Helper()
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutNuggets([]index.Nugget{{UID: "e", Tool: "copilot", SessionID: "fixture", Kind: "decision", Title: "Decision", Body: "Check deployment before tests.", Confidence: .9}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	req := Request{Capability: "recipes.design", Input: json.RawMessage(`{"title":"Selected opportunity","evidence_ids":["e"],"output_kinds":["post"]}`),
		Roots: map[string]Root{RootMiddenHome: {Path: home, Mode: "rw"}}}
	env := Invoke(req)
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result struct {
		Recipe index.Recipe `json:"recipe"`
	}
	if err = json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	return req, result.Recipe
}

func TestAutomaticContinuationCannotRecordOperatorApproval(t *testing.T) {
	req, recipe := operatorFixture(t)
	req.Capability = "recipes.evidence"
	req.Input, _ = json.Marshal(map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e"}, "decision": "approved"})
	env := Invoke(req)
	if env.OK || env.Error.Code != "operator_confirmation_required" {
		t.Fatalf("agent-supplied decision became operator approval: %+v", env)
	}
	db, err := index.OpenReadOnly(req.Roots[RootMiddenHome].Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stored, err := db.Recipe(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "draft" || !stored.ApprovedAt.IsZero() {
		t.Fatal("unconfirmed approval changed recipe state")
	}
}

func TestSerializedHumanAssertionDoesNotProvideHostAuthority(t *testing.T) {
	req, recipe := operatorFixture(t)
	req.Capability = "recipes.evidence"
	req.Input, _ = json.Marshal(map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e"}, "decision": "approved", "operator_confirmed": true})
	if Invoke(req).OK {
		t.Fatal("caller asserted its own authority")
	}
}
