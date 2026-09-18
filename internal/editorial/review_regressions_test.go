package editorial

import (
	"os"
	"testing"

	"github.com/mekjr1/midden/internal/create"
)

func TestEditorialRecipeCannotBroadenApprovedOpportunityEvidence(t *testing.T) {
	w, p := fixture(t)
	p, err := w.Analyze(p.ID, p.Revision, analysis())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := w.Select(SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (create.Workflow{DB: w.DB}).SelectEvidence(create.Change{RecipeID: selected.Recipe.UID, EvidenceIDs: []string{"private"}}, true); err == nil {
		t.Fatal("editorial recipe widened to unrelated evidence")
	}
}

func TestEditorialHandoffAndChapterCheckHistoricalEvidenceMembership(t *testing.T) {
	w, p, id := reviewedChapter(t)
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	o.EvidenceIDs = []string{"private"}
	if err = w.DB.PutRefineryOutput(&o); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Handoff(HandoffRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, Target: "markdown", OutputIDs: []string{id}}); err == nil {
		t.Error("unrelated evidence output entered project handoff")
	}
	if _, err = w.Analyze(p.ID, p.Revision, *p.Analysis); err == nil {
		t.Error("unrelated evidence output completed chapter")
	}
}

func TestEditorialCompletedChapterRequiresUnchangedReviewedBytes(t *testing.T) {
	w, p, id := reviewedChapter(t)
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(o.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Analyze(p.ID, p.Revision, *p.Analysis); err == nil {
		t.Fatal("changed output marked chapter complete")
	}
}

func TestEditorialClaimLinkedGapsCannotBeOmittedByOpportunity(t *testing.T) {
	w, p := fixture(t)
	a := analysis()
	a.Gaps = []Gap{{ID: "verify", Detail: "Verify the claim before generalizing", Status: "open", ClaimIDs: []string{"lesson"}}}
	p, err := w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Select(SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}}); err == nil {
		t.Fatal("claim-linked gap omitted from acknowledgment gate")
	}
}
