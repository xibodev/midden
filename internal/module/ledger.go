package module

import (
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/index"
)

// Cost ledger recording for the module path.
//
// This exists because of a gap the operator found: `content.produce` and
// `evidence.extract` spend real money through a user's own AI CLI, and neither
// wrote anything to the ledger `midden cost` reads. The wire reporting was
// honest — cost_known:false, both pointers null, so a host requires approval —
// but honest-at-the-moment and honest-in-the-record are different things, and
// only the first was done.
//
// A user who approves a spend and then asks "what have I spent" must be able
// to see it. An operation that bills and leaves no trace is unauditable, which
// is worse than one that reports an imprecise number.

// modelRun opens a ledger entry for a model-backed module invocation.
//
// It is started BEFORE the model runs, so a call that fails or is killed still
// leaves evidence that it happened. A ledger written only on success under-
// reports exactly the runs a user most wants to find.
func modelRun(op, scope, backend string, estTokens int) cost.Run {
	return cost.Run{
		Op:        op,
		Scope:     scope,
		Backend:   backend,
		EstTokens: estTokens,
		StartedAt: time.Now(),
	}
}

// recordRun closes and stores a ledger entry.
//
// A failure to record is reported as a warning rather than failing the
// invocation: the work is already done and the user already holds the output,
// so discarding it because bookkeeping failed would be the wrong trade. But it
// must be VISIBLE, because a silent bookkeeping failure is how a ledger
// quietly stops being trustworthy.
func recordRun(db *index.DB, run cost.Run, items int, ok bool, note string) []string {
	if db == nil {
		return []string{"model spend was NOT recorded in the cost ledger: the evidence index was unavailable"}
	}
	run.Items = items
	run.EndedAt = time.Now()
	run.OK = ok
	run.Note = strings.TrimSpace(note)

	if err := db.PutRun(run); err != nil {
		return []string{"model spend was NOT recorded in the cost ledger: " + err.Error() +
			". The work completed; only the accounting failed, so `midden cost` will under-report."}
	}
	return nil
}

// scopeLabel describes what a run covered, for the ledger's scope column.
//
// It is built from the request rather than from a constant so a person reading
// `midden cost` later can tell which run was which. "content.produce" alone
// answers nothing when there are twelve of them.
func scopeLabel(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return "module"
	}
	return strings.Join(kept, " ")
}
