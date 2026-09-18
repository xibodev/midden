package editorial

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

func fixture(t *testing.T) (Workflow, Project) {
	t.Helper()
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	err = db.PutNuggets([]index.Nugget{
		{UID: "before", Tool: "copilot", SessionID: "session-a", Kind: "decision", Title: "Test immediately", Body: "The initial plan ran live tests before checking deployment.", Confidence: .9, TurnRef: "event:4", CreatedAt: time.Unix(10, 0)},
		{UID: "after", Tool: "copilot", SessionID: "session-a", Kind: "error_fix", Title: "Check deployment first", Body: "The operator corrected the order: verify deployed versions before testing.", Confidence: .95, TurnRef: "event:9", CreatedAt: time.Unix(20, 0)},
		{UID: "async", Tool: "claude", SessionID: "session-b", Kind: "gotcha", Title: "Separate platform failure", Body: "Contract validation passed; the async status endpoint failed independently.", Confidence: .9, TurnRef: "event:3", CreatedAt: time.Unix(30, 0)},
		{UID: "private", Tool: "copilot", SessionID: "unrelated", Kind: "decision", Body: "Unrelated personal material", Confidence: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := Workflow{DB: db}
	p, err := w.Create(CreateRequest{Title: "Migration lessons", Goal: "Explain what changed and why",
		Sources: []Source{{Tool: "copilot", SessionID: "session-a"}}, EvidenceIDs: []string{"before", "after"}})
	if err != nil {
		t.Fatal(err)
	}
	return w, p
}

func analysis() Analysis {
	return Analysis{
		Arcs: []Arc{{ID: "order", Title: "Deployment before validation", Summary: "A corrected diagnostic sequence.", EvidenceIDs: []string{"before", "after"}}},
		Decisions: []Decision{
			{ID: "initial", Statement: "Test immediately", Status: "superseded", Rationale: "Initial assumption", EvidenceIDs: []string{"before"}},
			{ID: "corrected", Statement: "Check deployment first", Status: "accepted", Rationale: "Avoid ineligible tests", EvidenceIDs: []string{"after"}, Supersedes: []string{"initial"}},
		},
		Claims: []Claim{{ID: "lesson", Text: "Deployment eligibility precedes a useful live test.", Status: "supported", SupportingIDs: []string{"after"}}},
		Assets: []Asset{},
		Gaps:   []Gap{},
		Opportunities: []Opportunity{{ID: "post", Title: "The test that could not pass", Hook: "Test the deployment assumption first",
			Audience: "Engineers validating migrations", Purpose: "Avoid misleading live tests", Formats: []string{"post", "slides"},
			ArcIDs: []string{"order"}, ClaimIDs: []string{"lesson"}, DecisionIDs: []string{"initial", "corrected"},
			Rationale: "A concrete correction with two sources", Effort: "small", Risks: []string{"Generalize private product names"}}},
		Chapters: []Chapter{},
	}
}

func TestEditorialSelectionKeepsRecipeUnapprovedAndExcludesUnrelatedEvidence(t *testing.T) {
	w, p := fixture(t)
	p, err := w.Analyze(p.ID, p.Revision, analysis())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := w.Select(SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}})
	if err != nil {
		t.Fatal(err)
	}
	r := selected.Recipe
	if r.Status != refinery.RecipeDraft || !r.ApprovedAt.IsZero() {
		t.Fatalf("selection approved evidence: %+v", r)
	}
	if strings.Join(r.EvidenceIDs, ",") != "after,before" {
		t.Fatalf("unexpected evidence scope: %v", r.EvidenceIDs)
	}
	if !strings.Contains(r.Request, "superseded") || !strings.Contains(r.Request, "Generalize private product names") {
		t.Fatalf("lost editorial context: %s", r.Request)
	}
	reloaded, err := w.Inspect(p.ID)
	if err != nil || len(reloaded.Selections) != 1 {
		t.Fatalf("selection not durable: %+v %v", reloaded, err)
	}
}

func TestEditorialRejectsBrokenClaimsAndSupersession(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*Analysis)
	}{
		{"unknown evidence", func(a *Analysis) { a.Claims[0].SupportingIDs = []string{"private"} }},
		{"unsupported supported claim", func(a *Analysis) { a.Claims[0].SupportingIDs = nil }},
		{"contradiction hidden", func(a *Analysis) { a.Claims[0].ContradictingIDs = []string{"before"} }},
		{"missing superseded decision", func(a *Analysis) { a.Decisions[1].Supersedes = []string{"missing"} }},
		{"cyclic supersession", func(a *Analysis) {
			a.Decisions[0].Supersedes = []string{"corrected"}
			a.Decisions[1].Status = "superseded"
		}},
		{"missing arc", func(a *Analysis) { a.Opportunities[0].ArcIDs = []string{"missing"} }},
		{"unknown format", func(a *Analysis) { a.Opportunities[0].Formats = []string{"imaginary"} }},
		{"unsafe asset", func(a *Analysis) {
			a.Assets = []Asset{{ID: "screenshot", Kind: "image", EvidenceID: "after", Locator: "../../secret", Availability: "referenced"}}
		}},
		{"false complete chapter", func(a *Analysis) {
			a.Chapters = []Chapter{{ID: "one", Title: "One", OpportunityID: "post", Status: "complete", OutputID: "missing"}}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			w, p := fixture(t)
			a := analysis()
			change.edit(&a)
			if _, err := w.Analyze(p.ID, p.Revision, a); err == nil {
				t.Fatal("invalid analysis accepted")
			}
		})
	}
}

func TestEditorialCorpusRefreshRequiresNewAnalysisAndRejectsStaleWriter(t *testing.T) {
	w, p := fixture(t)
	p, err := w.Analyze(p.ID, p.Revision, analysis())
	if err != nil {
		t.Fatal(err)
	}
	oldRevision := p.Revision
	p, err = w.Update(UpdateRequest{ProjectID: p.ID, ExpectedRevision: oldRevision,
		Sources:     []Source{{Tool: "copilot", SessionID: "session-a"}, {Tool: "claude", SessionID: "session-b"}},
		EvidenceIDs: []string{"before", "after", "async"}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.AnalysisStale {
		t.Fatal("scope changed without invalidating analysis")
	}
	if _, err = w.Analyze(p.ID, oldRevision, analysis()); err == nil {
		t.Fatal("stale writer accepted")
	}
	if _, err = w.Select(SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}}); err == nil {
		t.Fatal("outdated analysis selected")
	}
	p, err = w.Analyze(p.ID, p.Revision, analysis())
	if err != nil || p.AnalysisStale {
		t.Fatalf("refresh failed: %+v %v", p, err)
	}
	packet, err := w.Prepare(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if packet.EvidenceCount != 3 || packet.EstInputTokens <= 0 || strings.Contains(packet.Prompt, "Unrelated personal") {
		t.Fatalf("wrong prepared context: %+v", packet)
	}
}

func TestEditorialRequiresExactScopeAndAllEvidence(t *testing.T) {
	w, _ := fixture(t)
	for _, req := range []CreateRequest{
		{Title: "Empty", Goal: "Read all"},
		{Title: "Wrong", Goal: "Review", Sources: []Source{{Tool: "copilot", SessionID: "session-a"}}, EvidenceIDs: []string{"private"}},
		{Title: "Missing", Goal: "Review", Sources: []Source{{Tool: "copilot", SessionID: "session-a"}}, EvidenceIDs: []string{"deleted"}},
	} {
		if _, err := w.Create(req); err == nil {
			t.Fatal("ambiguous scope accepted")
		}
	}
}

func TestEditorialChangedEvidenceCannotBeSilentlySelected(t *testing.T) {
	w, p := fixture(t)
	p, err := w.Analyze(p.ID, p.Revision, analysis())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.DB.SQL().Exec("UPDATE nuggets SET body='Changed underlying evidence' WHERE uid='after'"); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Select(SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}}); err == nil {
		t.Fatal("changed evidence accepted without refresh")
	}
}

func TestEditorialOpenGapsNeedExplicitDisclosure(t *testing.T) {
	w, p := fixture(t)
	a := analysis()
	a.Gaps = []Gap{{ID: "verification", Detail: "Current deployed version is unknown", Status: "open", ClaimIDs: []string{"lesson"}}}
	a.Opportunities[0].GapIDs = []string{"verification"}
	p, err := w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}

	req := SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}}
	if _, err = w.Select(req); err == nil {
		t.Fatal("open gap silently ignored")
	}
	req.AcknowledgeGaps = true
	result, err := w.Select(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Recipe.Request, "Current deployed version is unknown") {
		t.Fatal("gap disappeared from production")
	}
	raw, err := json.Marshal(result.Project)
	if err != nil || strings.Contains(string(raw), `"selections":null`) {
		t.Fatalf("invalid project wire shape: %s %v", raw, err)
	}
}

func TestEditorialRetainsRevisionsAndRejectsChapterCycles(t *testing.T) {
	w, p := fixture(t)
	a := analysis()
	a.Chapters = []Chapter{
		{ID: "intro", Title: "Introduction", OpportunityID: "post", Status: "planned"},
		{ID: "practice", Title: "Practice", OpportunityID: "post", Status: "planned", DependsOn: []string{"intro"}},
	}
	p, err := w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	original, err := w.InspectRevision(p.ID, 1)
	if err != nil || original.Analysis != nil || original.Revision != 1 {
		t.Fatalf("original revision lost: %+v %v", original, err)
	}
	a.Chapters[0].DependsOn = []string{"practice"}
	if _, err = w.Analyze(p.ID, p.Revision, a); err == nil {
		t.Fatal("chapter dependency cycle accepted")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.DB.SaveEditorial(p.ID, 1, raw, nil); err == nil {
		t.Fatal("database accepted a concurrent stale writer")
	}
}

func TestEditorialEmptyOptionalAnalysisCollectionsEncodeAsArrays(t *testing.T) {
	w, p := fixture(t)
	a := analysis()
	a.Assets = nil
	a.Gaps = nil
	a.Chapters = nil
	a.Opportunities[0].Risks = nil
	p, err := w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p.Analysis)
	for _, field := range []string{"assets", "gaps", "chapters", "risks"} {
		if strings.Contains(string(raw), `"`+field+`":null`) {
			t.Fatalf("null collection %s: %s", field, raw)
		}
	}
}
