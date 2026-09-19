package module

import (
	"encoding/json"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestUnchangedNotebookPackCanReachHostReview(t *testing.T) {
	req, recipe := operatorFixture(t)
	db, err := index.OpenAt(req.Roots[RootMiddenHome].Path)
	if err != nil {
		t.Fatal(err)
	}
	recipe.Outputs = []index.RecipeOutputSpec{{Kind: "notebook_pack", Title: "Notebook", Format: "markdown"}}
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
	call("recipes.evidence", map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e"}, "decision": "approved"})
	produced := call("recipes.compose", map[string]any{"recipe_id": recipe.UID, "drafts": map[string]string{}})
	var out struct {
		Outputs []index.RefineryOutput `json:"outputs"`
	}
	json.Unmarshal(produced.Result, &out)
	id := out.Outputs[0].UID
	inspected := call("outputs.inspect", map[string]any{"output_id": id})
	var view struct {
		Digest string `json:"content_digest"`
	}
	json.Unmarshal(inspected.Result, &view)
	call("outputs.review", map[string]any{"output_id": id, "expected_digest": view.Digest, "decision": "reviewed", "review_notes": "Compared the unchanged notebook pack to the approved evidence items; these are source notes, not verified publication claims."})
	call("outputs.export", map[string]any{"output_id": id})
}
