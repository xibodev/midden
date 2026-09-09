package module

import "testing"

// TestCoverageTravelsWithTheSlice pins the fix for a defect an agent found by
// USING Midden and reported in its own words:
//
//	"The model saw 8,340 of 5,502,047 signal bytes -- 0.15%. I can't
//	 distinguish 'this session contained few decisions' from 'my net was too
//	 small to find them'."
//
// Midden reported what it SENT and never what it sent it OUT OF. A thin slice
// and a thin session produce the same small number of nuggets, and nothing said
// which this was -- so an artifact built on 0.15% of a session looked identical
// to one built on all of it.
//
// Same defect as a session count without excluded_noise, in the place that
// decides whether an artifact is worth anything.
func TestCoverageTravelsWithTheSlice(t *testing.T) {
	if got := coveragePercent(8340, 5502047); got != 0.15 {
		t.Errorf("coverage = %v, want 0.15 -- the number that told an agent its "+
			"net was too narrow", got)
	}
	// Two places, because the interesting values are small: a whole-percent
	// figure would render 0.15 as 0 and say nothing at all.
	if got := coveragePercent(1, 1000000); got == 0 {
		t.Error("a tiny but non-zero slice reported 0% coverage; the caller " +
			"cannot tell it from having sent nothing")
	}
	// A session with no signal must not divide by zero or claim full coverage.
	if got := coveragePercent(100, 0); got != 0 {
		t.Errorf("empty signal reported %v%% coverage", got)
	}
	if got := coveragePercent(500, 500); got != 100 {
		t.Errorf("a complete slice reported %v%%, want 100", got)
	}
}
