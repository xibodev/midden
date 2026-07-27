package main

import (
	"flag"
	"fmt"
	"sort"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

// reconcile reads real usage back out of each backend's own store and writes
// it onto the recorded run.
//
// Accounting lags a run by moments, so this is called opportunistically rather
// than inline: any command that touches the ledger first settles what it can.
func reconcile(db *index.DB) int {
	pending, err := db.UnreconciledRuns()
	if err != nil {
		return 0
	}

	settled := 0
	for _, r := range pending {
		var total cost.Usage
		for _, sid := range r.CLISessions {
			u, err := adapter.UsageFor(r.Backend, sid)
			if err != nil || u.Empty() {
				continue
			}
			total.Add(u)
		}
		if total.Empty() {
			continue
		}
		if db.ReconcileRun(r.UID, total) == nil {
			settled++
		}
	}
	return settled
}

// cmdCost shows what Midden has actually spent.
//
// Every figure here is read back from the CLI's own accounting, not estimated.
// The estimate column is kept beside it precisely so the error is visible: the
// first implementation was wrong by 70x and nothing surfaced that.
func cmdCost(args []string) error {
	fs := flag.NewFlagSet("cost", flag.ExitOnError)
	limit := fs.Int("limit", 20, "how many runs to list")
	op := fs.String("op", "", "filter by operation (reclaim|refine)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	settled := reconcile(db)

	runs, err := db.Runs(*limit, *op)
	if err != nil {
		return err
	}
	totals, err := db.Costs()
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(map[string]any{"runs": runs, "totals": totals})
	}

	fmt.Printf("\n  %s\n  %s\n\n", render.Bold("COST LEDGER"), render.Rule(72))

	if totals.Runs == 0 {
		fmt.Printf("  %s\n", render.Dim("nothing spent yet — every deterministic command is free."))
		fmt.Printf("  %s\n\n", render.Dim("only `reclaim` and `refine` call a model."))
		return nil
	}

	// Free commands are the majority of the tool; say so, or the ledger reads
	// as though everything costs something.
	fmt.Printf("  %s %s\n\n", render.Dim("free (deterministic):"),
		render.Dim("ls show doctor watch brief scan assay prune archive advise ui mcp"))

	ops := make([]string, 0, len(totals.ByOp))
	for k := range totals.ByOp {
		ops = append(ops, k)
	}
	sort.Strings(ops)

	fmt.Printf("  %-10s %6s %7s %10s %12s %14s\n",
		render.Dim("operation"), render.Dim("runs"), render.Dim("items"),
		render.Dim("tokens"), render.Dim("charged"), render.Dim("per item"))
	for _, name := range ops {
		o := totals.ByOp[name]
		charged := fmt.Sprintf("%s tok", cost.Compact(o.Tokens))
		perItem := "-"
		if o.AIU > 0 {
			charged = fmt.Sprintf("%.1f AIU", o.AIU)
			if o.Items > 0 {
				perItem = fmt.Sprintf("%.1f AIU", o.PerItem())
			}
		} else if o.USD > 0 {
			charged = fmt.Sprintf("$%.2f", o.USD)
			if o.Items > 0 {
				perItem = fmt.Sprintf("$%.3f", o.PerItem())
			}
		} else if o.Items > 0 {
			perItem = fmt.Sprintf("%s tok", cost.Compact(int64(o.PerItem())))
		}
		fmt.Printf("  %-10s %6d %7d %10s %12s %14s\n",
			name, o.Runs, o.Items, cost.Compact(o.Tokens), charged, perItem)
	}

	fmt.Printf("\n  %-10s %6d %7d %10s %12s\n", render.Bold("total"),
		totals.Runs, totals.Items, cost.Compact(totals.Tokens),
		totalCharge(totals))

	// Calibration is the point of recording all this.
	fmt.Printf("\n  %s\n", render.Bold("estimate accuracy"))
	for _, name := range ops {
		st, err := db.CalibrationFor(name)
		if err != nil || st.Samples == 0 {
			fmt.Printf("    %-10s %s\n", name, render.Dim("not yet calibrated"))
			continue
		}
		fmt.Printf("    %-10s actual is %.0fx the raw prompt estimate  %s\n",
			name, st.MeanFactor,
			render.Dim(fmt.Sprintf("(range %.0f-%.0fx over %d run(s))",
				st.MinFactor, st.MaxFactor, st.Samples)))
	}
	fmt.Printf("    %s\n", render.Dim(
		"future estimates use these factors, so predictions improve as you use the tool"))

	fmt.Printf("\n  %s\n", render.Bold("recent runs"))
	fmt.Printf("  %-12s %-9s %6s %10s %11s %8s %7s  %s\n",
		render.Dim("when"), render.Dim("op"), render.Dim("items"),
		render.Dim("tokens"), render.Dim("charged"), render.Dim("est err"),
		render.Dim("secs"), render.Dim("scope"))

	for _, r := range runs {
		errStr := "-"
		if a := r.Accuracy(); a > 0 {
			errStr = fmt.Sprintf("%.0fx", a)
		}
		status := ""
		if !r.OK {
			status = render.RiskColour(3) + " "
		}
		fmt.Printf("  %-12s %-9s %6d %10s %11s %8s %7.0f  %s%s\n",
			r.StartedAt.Format("01-02 15:04"), r.Op, r.Items,
			cost.Compact(r.Usage.Billable()), r.Usage.Unit(), errStr,
			r.Duration().Seconds(), status, truncate(r.Scope, 26))
	}

	if settled > 0 {
		fmt.Printf("\n  %s\n", render.Dim(fmt.Sprintf("settled %d run(s) against recorded usage", settled)))
	}
	fmt.Println()
	return nil
}

func totalCharge(t index.CostTotals) string {
	switch {
	case t.AIU > 0:
		return fmt.Sprintf("%.1f AIU", t.AIU)
	case t.USD > 0:
		return fmt.Sprintf("$%.2f", t.USD)
	default:
		return "-"
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// estimateFor builds a calibrated prediction for an operation.
func estimateFor(db *index.DB, op string, rawTokens, items int) cost.Estimate {
	stats, err := db.CalibrationFor(op)
	if err != nil {
		stats = nil
	}
	e := cost.Predict(op, rawTokens, stats)
	return cost.PredictPerItem(e, items)
}

// recordRun opens a ledger entry before an operation spends anything.
func recordRun(db *index.DB, op, scope, backend string, est int) cost.Run {
	return cost.Run{
		UID:       index.NewUID(),
		Op:        op,
		Scope:     scope,
		Backend:   backend,
		EstTokens: est,
		StartedAt: time.Now(),
	}
}

// spentCredits reports credits charged so far, used to enforce a hard cap
// mid-run. Reads the ledger rather than an estimate, so the cap is honest.
func spentCredits(db *index.DB) float64 {
	reconcile(db)
	t, err := db.Costs()
	if err != nil {
		return 0
	}
	return t.AIU
}
