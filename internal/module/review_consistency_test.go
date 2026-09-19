package module

import (
	"encoding/json"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestHostReviewReceiptMatchesEffectiveStructuredEvidence(t *testing.T) {
	req, recipe := operatorFixture(t)
	db, err := index.OpenAt(req.Roots[RootMiddenHome].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutNuggets([]index.Nugget{{UID: "excluded", Kind: "dead_end", Body: "A failed attempt.", Confidence: .4}}); err != nil {
		t.Fatal(err)
	}
	recipe.Outputs = []index.RecipeOutputSpec{{Kind: "sft_pack", Title: "Examples", Format: "jsonl"}}
	recipe.EvidenceIDs = []string{"e", "excluded"}
	if err = db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	db.Close()
	req.ConfirmOperator = testOperatorConsent
	call := func(cap string, input any) Envelope {
		t.Helper()
		req.Capability = cap
		req.Input, _ = json.Marshal(input)
		env := Invoke(req)
		if !env.OK {
			t.Fatalf("%s: %v", cap, env.Error)
		}
		return env
	}
	call("recipes.evidence", map[string]any{"recipe_id": recipe.UID, "evidence_ids": recipe.EvidenceIDs, "decision": "approved"})
	produced := call("recipes.compose", map[string]any{"recipe_id": recipe.UID, "drafts": map[string]string{}})
	var result struct {
		Outputs []index.RefineryOutput `json:"outputs"`
	}
	json.Unmarshal(produced.Result, &result)
	id := result.Outputs[0].UID
	inspected := call("outputs.inspect", map[string]any{"output_id": id})
	var current struct {
		Digest string `json:"content_digest"`
	}
	json.Unmarshal(inspected.Result, &current)
	call("outputs.review", map[string]any{"output_id": id, "expected_digest": current.Digest, "decision": "reviewed", "review_notes": "Checked the eligible structured rows and their evidence. Excluded attempts are not examples."})
	call("outputs.export", map[string]any{"output_id": id})
}
