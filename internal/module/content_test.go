package module

import (
	"testing"
	"time"
)

// TestDeadlineNeverGoesNegative pins the PROPERTY, not the current numbers.
//
// The headroom guard used to be an independent literal (5s) beside the margin
// (3s). Correct together, and a margin raised past the guard would have returned
// a NEGATIVE deadline silently -- both halves internally consistent while
// disagreeing, which is this project's signature failure.
//
// This asserts the invariant rather than the values, so it keeps working if
// envelopeHeadroom changes and fails if the guard stops tracking it.
func TestDeadlineNeverGoesNegative(t *testing.T) {
	// Sweeping rather than sampling: a hand-picked list misses the window
	// where a drifted guard goes negative. Mutation-checked -- with the guard
	// hardcoded at 5s and the margin raised to 6s, the sampled version passed
	// and this one fails, because the defect lives between the two values.
	for ms := 1; ms <= 20000; ms += 97 {
		got := deadlineOf(Request{DeadlineMS: ms})
		if got <= 0 {
			t.Errorf("deadline_ms=%d produced %v; a non-positive deadline "+
				"cancels the context before any work starts", ms, got)
		}
	}
}

// TestHeadroomIsReservedWhenThereIsRoom proves the margin still does its job:
// a generous deadline must come back reduced, or the module never gets to write
// an envelope before the host kills the tree.
func TestHeadroomIsReservedWhenThereIsRoom(t *testing.T) {
	d := deadlineOf(Request{DeadlineMS: 60000})
	if want := 60*time.Second - envelopeHeadroom; d != want {
		t.Errorf("deadline = %v, want %v; headroom is not being reserved", d, want)
	}
}
