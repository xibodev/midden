package create

import (
	"strings"
	"testing"
	"time"
)

// TestTwoRunsOfTheSameKindCoexist is the defect this layout fixes.
//
// Output landed in a flat content/<kind>.<ext>, so producing a second ADR
// silently replaced the first. That is data loss on the SECOND use of a
// capability, and nothing reported it -- the caller received a success and a
// path, and the earlier work was gone.
func TestTwoRunsOfTheSameKindCoexist(t *testing.T) {
	first, err := OutputPath(NewRunID(time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)), "adr", ".md")
	if err != nil {
		t.Fatal(err)
	}
	second, err := OutputPath(NewRunID(time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)), "adr", ".md")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two runs produced the same path %q; the second would replace "+
			"the first and the caller would still be told it succeeded", first)
	}
}

// TestRunIDsSortChronologically makes the directory listing the history.
//
// A driver resuming tomorrow should be able to see what happened today by
// looking, without reading an index. Sortable ids are what make that true.
func TestRunIDsSortChronologically(t *testing.T) {
	early := NewRunID(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	later := NewRunID(time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC))
	if !(string(early) < string(later)) {
		t.Errorf("run ids do not sort by time: %q then %q", early, later)
	}
}

// TestOutputPathCannotEscapeTheRoot is the confinement guard.
//
// A name arrives from a caller and becomes a directory entry, so it is exactly
// the input that must not be able to name a location outside a granted root.
func TestOutputPathCannotEscapeTheRoot(t *testing.T) {
	run := NewRunID(time.Now())
	for _, bad := range []string{
		"", "..", ".", "../escape", "sub/dir", `sub\dir`, "/abs", `C:\windows`,
		"has space", "semi;colon", "dot.dot", strings.Repeat("x", 101),
	} {
		if _, err := OutputPath(run, bad, ".md"); err == nil {
			t.Errorf("output name %q was accepted; it must be one safe segment", bad)
		}
	}
	for _, ok := range []string{"adr", "retrieval-pack", "handbook_2"} {
		if _, err := OutputPath(run, ok, ".md"); err != nil {
			t.Errorf("legal name %q was rejected: %v", ok, err)
		}
	}
}

// TestPathIsRunScopedNotFlat pins the shape a driver depends on.
func TestPathIsRunScopedNotFlat(t *testing.T) {
	got, err := OutputPath(RunID("20260909-120000"), "adr", ".md")
	if err != nil {
		t.Fatal(err)
	}
	if want := "content/20260909-120000/adr.md"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
