package reclaim

import (
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

// TestNuggetIdentityIsStableAcrossExtractions proves re-mining is idempotent.
//
// Nugget uid is the primary key, and it used to be random: re-running an
// extraction INSERTed duplicates instead of replacing, so a partially completed
// run could not be safely repeated -- it re-spent a model call AND grew the
// store. An operation whose re-execution is safe needs no durable-resume
// facility, so this property is what makes the recoverability gap a local fix
// rather than a requirement on a runtime.
func TestNuggetIdentityIsStableAcrossExtractions(t *testing.T) {
	s := core.Session{Tool: "claude", ID: "sess-1"}

	first := nuggetUID(s, "decision", "Pick SQLite", "It ships as one file.")
	again := nuggetUID(s, "decision", "Pick SQLite", "It ships as one file.")
	if first != again {
		t.Fatalf("identical evidence produced different ids: %s != %s", first, again)
	}

	// Whitespace must not create a new row: models pad output inconsistently.
	padded := nuggetUID(s, "decision", "  Pick SQLite  ", "It ships as one file.\n")
	if padded != first {
		t.Errorf("whitespace changed the id; re-mining would duplicate the row")
	}
}

// TestDifferentEvidenceGetsDifferentIdentity guards the other direction: the id
// must not collapse genuinely different nuggets onto one row, which would make
// a later extraction silently overwrite an earlier finding.
func TestDifferentEvidenceGetsDifferentIdentity(t *testing.T) {
	s := core.Session{Tool: "claude", ID: "sess-1"}
	base := nuggetUID(s, "decision", "Pick SQLite", "It ships as one file.")

	cases := map[string]string{
		"different body":    nuggetUID(s, "decision", "Pick SQLite", "Different reasoning."),
		"different title":   nuggetUID(s, "decision", "Pick Postgres", "It ships as one file."),
		"different kind":    nuggetUID(s, "gotcha", "Pick SQLite", "It ships as one file."),
		"different session": nuggetUID(core.Session{Tool: "claude", ID: "sess-2"}, "decision", "Pick SQLite", "It ships as one file."),
		"different tool":    nuggetUID(core.Session{Tool: "copilot", ID: "sess-1"}, "decision", "Pick SQLite", "It ships as one file."),
	}
	for name, got := range cases {
		if got == base {
			t.Errorf("%s collided with the base id; a real finding would be overwritten", name)
		}
	}
}
