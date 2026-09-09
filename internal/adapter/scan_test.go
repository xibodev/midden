package adapter

import (
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

// TestPrefixedIDNarrowsTheScan is the fix for a defect an agent found by USING
// Midden, not by reading it: scoping to one opencode session still walked the
// entire Claude corpus -- 1.27 GB on this machine -- before assaying.
//
// The result was correct and the route was absurd, which reads as a bug even
// when the answer is right.
func TestPrefixedIDNarrowsTheScan(t *testing.T) {
	got := toolsForIDs(core.Scope{IDs: []string{"ses_f916b29d"}}, nil)
	if got == nil {
		t.Fatal("an opencode-prefixed id did not narrow the scan")
	}
	if !got[core.ToolOpencode] {
		t.Error("narrowing excluded the tool the id belongs to")
	}
	if len(got) != 1 {
		t.Errorf("narrowed to %d tools, want 1", len(got))
	}
}

// TestUnrecognisableIDWidensBackToEveryStore is the safety direction, and it
// matters more than the speed.
//
// Claude and Copilot use bare UUIDs that carry no origin. Guessing would
// silently exclude the store the caller wanted, turning a findable session into
// a missing one -- a far worse failure than a slow scan.
func TestUnrecognisableIDWidensBackToEveryStore(t *testing.T) {
	if got := toolsForIDs(core.Scope{IDs: []string{"429662ed-56c5-4c5f"}}, nil); got != nil {
		t.Errorf("a bare UUID narrowed the scan to %v; it could belong to any "+
			"store and narrowing would hide it", got)
	}
	// One unrecognisable id among several must widen the WHOLE scan.
	mixed := toolsForIDs(core.Scope{IDs: []string{"ses_abc", "429662ed"}}, nil)
	if mixed != nil {
		t.Error("a mixed id set narrowed the scan; the unrecognisable id would " +
			"be searched in the wrong stores only")
	}
}

// TestNoIDsMeansNoNarrowing keeps ordinary listing unchanged.
func TestNoIDsMeansNoNarrowing(t *testing.T) {
	if got := toolsForIDs(core.Scope{}, nil); got != nil {
		t.Errorf("an unscoped listing was narrowed to %v", got)
	}
}
