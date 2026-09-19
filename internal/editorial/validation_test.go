package editorial

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRefutedClaimKeepsContradictingEvidenceOnItsOwnSide(t *testing.T) {
	w, p := fixture(t)
	a := analysis()
	a.Claims[0].Text = "The original deployment-first check was unnecessary."
	a.Claims[0].Status = "refuted"
	a.Claims[0].SupportingIDs = []string{}
	a.Claims[0].ContradictingIDs = []string{"after"}
	p, err := w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Analysis.Claims[0].SupportingIDs) != 0 || p.Analysis.Claims[0].ContradictingIDs[0] != "after" {
		t.Fatal("validator changed evidence meaning")
	}
}

func TestAnalysisValidationReportsMultipleFieldPathsWithoutSaving(t *testing.T) {
	w, p := fixture(t)
	a := analysis()
	a.Arcs[0].EvidenceIDs = nil
	a.Claims[0].SupportingIDs = []string{"missing"}
	a.Opportunities[0].Formats = []string{"invented-format"}
	report, err := w.Validate(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	for _, path := range []string{"analysis.arcs[0].evidence_ids", "analysis.claims[0].supporting_ids", "analysis.opportunities[0].formats"} {
		if !strings.Contains(string(raw), path) {
			t.Errorf("missing issue path %s: %s", path, raw)
		}
	}
	fresh, err := w.Inspect(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Revision != p.Revision || fresh.Analysis != nil {
		t.Fatal("validation changed durable analysis")
	}
}
