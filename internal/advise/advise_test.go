package advise

import (
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

func sess(bytes int64, dir string) core.Session {
	now := time.Now()
	return core.Session{
		Tool: core.ToolCopilot, ID: "s", Dir: dir, Title: "t",
		Bytes: bytes, Created: now.AddDate(0, 0, -1), Updated: now,
	}
}

func find(fs []Finding, category string) *Finding {
	for i := range fs {
		if fs[i].Category == category {
			return &fs[i]
		}
	}
	return nil
}

func TestCriticalRiskIsTheTopFinding(t *testing.T) {
	// Losing days of work outranks every efficiency concern.
	in := Input{Sessions: []core.Session{
		sess(700<<20, "."),
		sess(1<<20, "."),
	}}
	fs := Analyse(in)

	if len(fs) == 0 {
		t.Fatal("expected findings")
	}
	if fs[0].Severity != High || fs[0].Category != "risk" {
		t.Errorf("critical risk should sort first, got %+v", fs[0])
	}
	if !strings.Contains(fs[0].Action, "midden brief") {
		t.Error("risk finding should say what to do about it")
	}
}

func TestEveryFindingCitesEvidenceAndAnAction(t *testing.T) {
	// Advice without evidence is unfalsifiable; advice without an action is
	// just an observation.
	in := Input{
		Sessions: []core.Session{sess(700<<20, "."), sess(500<<20, ".")},
		Assayed:  10, Signal: 100 << 20, Exhaust: 900 << 20,
		DupBytes: 200 << 20, Images: 4000, Clusters: 500,
		Artifact: 300 << 20,
	}
	for _, f := range Analyse(in) {
		if strings.TrimSpace(f.Evidence) == "" {
			t.Errorf("%q has no evidence", f.Title)
		}
		if strings.TrimSpace(f.Action) == "" {
			t.Errorf("%q has no action", f.Title)
		}
		if strings.TrimSpace(f.Title) == "" {
			t.Error("finding has no title")
		}
	}
}

func TestExhaustFindingNeedsRealImbalance(t *testing.T) {
	// A healthy signal-to-exhaust ratio should not produce noise.
	balanced := Input{Assayed: 5, Signal: 700 << 20, Exhaust: 100 << 20}
	if f := find(Analyse(balanced), "exhaust"); f != nil {
		t.Errorf("should not flag exhaust when signal dominates: %+v", f)
	}

	skewed := Input{Assayed: 5, Signal: 100 << 20, Exhaust: 900 << 20}
	if f := find(Analyse(skewed), "exhaust"); f == nil {
		t.Error("should flag exhaust when it dominates")
	}
}

func TestDuplicateDetectionNeedsScale(t *testing.T) {
	small := Input{Assayed: 1, Signal: 1 << 20, DupBytes: 1 << 20}
	for _, f := range Analyse(small) {
		if strings.Contains(f.Title, "repeatedly") {
			t.Error("a megabyte of duplication is not worth flagging")
		}
	}
}

func TestImageClusteringOnlyFlagsRedundancy(t *testing.T) {
	// Distinct screenshots are evidence, not waste.
	distinct := Input{Assayed: 1, Images: 300, Clusters: 290, Artifact: 100 << 20}
	for _, f := range Analyse(distinct) {
		if strings.Contains(f.Title, "collapse to") {
			t.Error("should not flag screenshots that are already distinct")
		}
	}

	redundant := Input{Assayed: 1, Images: 4000, Clusters: 400, Artifact: 500 << 20}
	found := false
	for _, f := range Analyse(redundant) {
		if strings.Contains(f.Title, "collapse to") {
			found = true
		}
	}
	if !found {
		t.Error("should flag heavy near-duplicate screenshots")
	}
}

func TestHarvestBeforeDisposalIsRecommended(t *testing.T) {
	// Deleting before harvesting throws away the only record of why.
	in := Input{Assayed: 50, Signal: 500 << 20, Exhaust: 500 << 20}
	f := find(Analyse(in), "harvest")
	if f == nil {
		t.Fatal("should recommend reclaiming before disposal")
	}
	if !strings.Contains(f.Action, "reclaim") {
		t.Errorf("harvest action should point at reclaim: %q", f.Action)
	}
}

func TestConcentrationNeedsEnoughSessions(t *testing.T) {
	// With a handful of sessions, "few sessions hold most bytes" is trivially
	// true and useless.
	in := Input{Sessions: []core.Session{sess(100<<20, "."), sess(1<<20, ".")}}
	if f := find(Analyse(in), "concentration"); f != nil {
		t.Error("concentration should not be reported for a tiny corpus")
	}
}

func TestFindingsSortBySeverityThenSize(t *testing.T) {
	in := Input{
		Sessions: []core.Session{sess(700<<20, "."), sess(500<<20, ".")},
		Assayed:  20, Signal: 100 << 20, Exhaust: 900 << 20, DupBytes: 300 << 20,
	}
	fs := Analyse(in)
	for i := 1; i < len(fs); i++ {
		if fs[i-1].Severity < fs[i].Severity {
			t.Errorf("findings out of severity order at %d: %v then %v",
				i, fs[i-1].Severity, fs[i].Severity)
		}
	}
}

func TestEmptyInputProducesNoNoise(t *testing.T) {
	if fs := Analyse(Input{}); len(fs) != 0 {
		t.Errorf("empty input should produce no findings, got %d", len(fs))
	}
}

func TestSeverityStrings(t *testing.T) {
	for s, want := range map[Severity]string{
		High: "high", Medium: "medium", Low: "low", Info: "info",
	} {
		if s.String() != want {
			t.Errorf("Severity(%d) = %q, want %q", s, s.String(), want)
		}
	}
}
