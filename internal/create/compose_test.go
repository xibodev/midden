package create

import (
	"context"
	"github.com/mekjr1/midden/internal/index"
	"strings"
	"testing"
	"time"
)

func TestAuthoredDraftsRequireCompleteApprovedPlan(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.PutNuggets([]index.Nugget{{UID: "e", Kind: "decision", Title: "Decision", Body: "Use bounded evidence with provenance", Confidence: 1, Workspace: "w", CreatedAt: time.Now()}})
	w := Workflow{DB: db}
	v, err := w.Design(Change{Workspace: "w", OutputKinds: []string{"tutorial", "slides"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	r := v.(map[string]any)["recipe"].(index.Recipe)
	w.Drafts = map[string]string{"tutorial": "# Blog"}
	if _, err = w.Produce(context.Background(), r.UID); err == nil {
		t.Fatal("unapproved authored content accepted")
	}
	if _, err = w.SelectEvidence(Change{RecipeID: r.UID, EvidenceIDs: []string{"e"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Produce(context.Background(), r.UID); err == nil {
		t.Fatal("missing slide draft accepted")
	}
	w.Drafts["slides"] = strings.Repeat("# Slide\n\n- Decision\n\n---\n", 8)
	if _, err = w.Produce(context.Background(), r.UID); err != nil {
		t.Fatal(err)
	}
	outputs, err := db.RefineryOutputs(r.UID, 0)
	if err != nil || len(outputs) != 2 {
		t.Fatal(outputs, err)
	}
	for _, o := range outputs {
		if o.Status != "draft" {
			t.Fatal("authored content auto-approved")
		}
	}
}
