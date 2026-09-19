package module

import (
	"encoding/json"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestLocalCompositionDoesNotRequireOrInventHumanApproval(t *testing.T) {
	req, recipe := operatorFixture(t)
	req.Capability = "recipes.compose"
	req.Input, _ = json.Marshal(map[string]any{"recipe_id": recipe.UID, "drafts": map[string]string{"post": "# Local draft\n\nCheck deployment before tests. [E1]"}})
	env := Invoke(req)
	if !env.OK {
		t.Fatalf("reversible local drafting was blocked: %v", env.Error)
	}
	var result struct {
		Outputs []index.RefineryOutput `json:"outputs"`
	}
	json.Unmarshal(env.Result, &result)
	if len(result.Outputs) != 1 || result.Outputs[0].Status != "draft" {
		t.Fatal("draft acquired a review status")
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
	if !stored.ApprovedAt.IsZero() {
		t.Fatal("composition invented evidence approval")
	}
	var count int
	if err = db.SQL().QueryRow("SELECT COUNT(*) FROM host_reviews").Scan(&count); err != nil || count != 0 {
		t.Fatal("composition minted a host receipt", err)
	}
	req.Capability = "outputs.export"
	req.Input, _ = json.Marshal(map[string]string{"output_id": result.Outputs[0].UID})
	if Invoke(req).OK {
		t.Fatal("unreviewed draft was exported")
	}
	req.Capability = "outputs.inspect"
	view := Invoke(req)
	if !view.OK {
		t.Fatal(view.Error)
	}
	var inspected struct {
		Digest string `json:"content_digest"`
	}
	json.Unmarshal(view.Result, &inspected)
	req.ConfirmOperator = testOperatorConsent
	req.Capability = "outputs.review"
	req.Input, _ = json.Marshal(map[string]any{"output_id": result.Outputs[0].UID, "expected_digest": inspected.Digest,
		"decision": "reviewed", "review_notes": "Checked the source statement and its selected citation; this is a scoped process recommendation."})
	if reviewed := Invoke(req); !reviewed.OK {
		t.Fatal(reviewed.Error)
	}
	req.Capability = "outputs.export"
	req.Input, _ = json.Marshal(map[string]string{"output_id": result.Outputs[0].UID})
	if exported := Invoke(req); !exported.OK {
		t.Fatal(exported.Error)
	}
}
