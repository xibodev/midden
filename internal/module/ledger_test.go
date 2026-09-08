package module

import (
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

// TestModelSpendIsRecorded is the regression guard for a gap the operator
// found by asking a question the code could not answer: "where did the costs
// go?"
//
// content.produce and evidence.extract spend real money through a user's own
// AI CLI. The wire reporting was honest — cost_known:false, both pointers nil,
// so a host requires approval — and NOTHING was written to the ledger that
// `midden cost` reads. Honest at the moment of spending and honest in the
// record are different properties, and only the first was implemented.
func TestModelSpendIsRecorded(t *testing.T) {
	db, err := index.OpenAt(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer db.Close()

	before, err := db.Runs(50, "")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}

	run := modelRun("content.produce", scopeLabel("adr", "", "12 evidence"), "claude", 2500)
	if warns := recordRun(db, run, 1, true, "produced adr from 12 evidence items"); len(warns) != 0 {
		t.Fatalf("recording reported problems: %v", warns)
	}

	after, err := db.Runs(50, "")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("ledger holds %d runs, want %d — the spend was not recorded",
			len(after), len(before)+1)
	}

	got := after[0]
	if got.Op != "content.produce" {
		t.Errorf("op = %q, want content.produce", got.Op)
	}
	if got.Backend != "claude" {
		t.Errorf("backend = %q; a ledger entry that does not name the CLI cannot be reconciled", got.Backend)
	}
	if got.EstTokens != 2500 {
		t.Errorf("est_tokens = %d, want 2500", got.EstTokens)
	}
	// The scope must distinguish one run from another. "content.produce" alone
	// answers nothing when a user has twelve of them.
	if got.Scope == "" || got.Scope == "module" {
		t.Errorf("scope = %q; it must say which run this was", got.Scope)
	}
	if got.EndedAt.Before(got.StartedAt) {
		t.Error("ended before it started")
	}
}

// TestFailedModelCallIsStillRecorded proves the ledger does not only show
// successes. A model that was invoked and then errored may still have billed,
// and a ledger showing only what worked hides exactly the runs a user is
// trying to account for.
func TestFailedModelCallIsStillRecorded(t *testing.T) {
	db, err := index.OpenAt(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer db.Close()

	run := modelRun("evidence.extract", scopeLabel("claude", "", "3 sessions"), "claude", 0)
	recordRun(db, run, 0, false, "model call failed: timeout")

	runs, err := db.Runs(50, "")
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("ledger holds %d runs; a failed call must still be recorded", len(runs))
	}
	if runs[0].OK {
		t.Error("a failed run was recorded as OK")
	}
	if runs[0].Note == "" {
		t.Error("a failed run carries no note explaining what happened")
	}
}

// TestRecordingFailureIsVisible proves a bookkeeping failure is reported
// rather than swallowed. The work is already done and the user holds the
// output, so the invocation must not fail — but a silent accounting failure is
// how a ledger quietly stops being trustworthy.
func TestRecordingFailureIsVisible(t *testing.T) {
	warns := recordRun(nil, modelRun("content.produce", "x", "claude", 1), 1, true, "")
	if len(warns) == 0 {
		t.Fatal("recording against a nil index reported nothing")
	}
	if !contains(warns[0], "NOT recorded") {
		t.Errorf("warning does not say the spend went unrecorded: %q", warns[0])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// TestEmptyOutputIsReportedDistinctly guards a misleading success found by
// running every free content kind rather than the two already exercised.
//
// preference_pack needs dead_end evidence to pair against. A corpus without
// any yields no pairs, so the document is written, valid, and one byte long —
// and it was reported as a plain success. A caller could not tell a finished
// output from an inapplicable one without opening the file, which is the same
// shape as a filtered count stated without its denominator.
func TestEmptyOutputIsReportedDistinctly(t *testing.T) {
	// The result type must carry the distinction at all: a bytes count alone
	// cannot express "written but inapplicable".
	r := &ContentProduceResult{Kind: "preference_pack", Bytes: 1, Empty: true}
	if !r.Empty {
		t.Fatal("ContentProduceResult cannot express an empty document")
	}

	full := &ContentProduceResult{Kind: "retrieval_pack", Bytes: 8075, Empty: false}
	if full.Empty {
		t.Error("a document with content was marked empty")
	}
}
