package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

// collectState gathers what the tool knows, so suggestions reflect reality
// rather than a fixed script.
//
// Deliberately cheap: it reads the index and session metadata, never
// transcripts, so any command can call it to print a next step without
// noticeably slowing down.
func collectState() guide.State {
	st := guide.State{}

	sessions, _ := adapter.Collect(core.Scope{IncludeNoise: true})
	st.Sessions = len(sessions)

	var largest int64
	byWorkspace := map[string]int{}
	cutoff := time.Now().AddDate(0, 0, -14)

	for _, s := range sessions {
		if s.Live != nil {
			st.LiveSessions++
		}
		if !s.DirExists() {
			st.DeadDirs++
		}
		switch s.Risk() {
		case core.RiskCritical:
			st.CriticalRisk++
			st.AtRisk++
			if s.Bytes > largest {
				largest = s.Bytes
				st.LargestAtRiskID = shortID(s.ID)
			}
		case core.RiskWarn, core.RiskWatch:
			st.AtRisk++
		}
		if !s.Noise && s.Updated.After(cutoff) && s.Dir != "" {
			byWorkspace[s.Dir]++
		}
	}

	// The busiest recent workspace is the cheapest useful scope to suggest.
	best := 0
	for dir, n := range byWorkspace {
		if n > best {
			best, st.BusiestWorkspace = n, dir
		}
	}

	for _, n := range adapter.Footprints() {
		st.FootprintByte += n
	}

	db, err := index.Open()
	if err != nil {
		return st
	}
	defer db.Close()
	st.HasIndex = true

	if t, err := db.Aggregate(""); err == nil {
		st.Assayed = int(t.Assayed)
		st.ReclaimBytes = t.Reclaimable()
	}
	if counts, err := db.NuggetCounts(); err == nil {
		for _, n := range counts {
			st.Nuggets += int(n)
		}
	}
	if as, err := db.Artifacts(1); err == nil {
		st.Artifacts = len(as)
	}
	return st
}

// suggestNext prints the single most valuable next action.
//
// Every command that reports a finding ends with one of these. The audit found
// `doctor` stopping mid-list after reporting four sessions about to be lost,
// with no instruction — alarm without a path out is a Dead End.
func suggestNext(st guide.State) {
	step, ok := guide.Top(st)
	if !ok {
		return
	}
	fmt.Printf("\n  %s %s\n", render.Bold("NEXT"), render.Dim("· "+step.Why))
	fmt.Printf("  %s   %s\n", costTag(step.Cost), step.Command)
	fmt.Printf("         %s\n\n", render.Dim(step.Value))
}

// costTag renders a cost class so the answer to "will this spend?" is visible
// before the command is run, not after.
func costTag(c guide.Cost) string {
	if c == guide.Spends {
		return render.Warn("SPENDS")
	}
	return render.Dim("FREE  ")
}

// cmdStart is the guided first run.
//
// Typing `midden` used to produce twenty commands in near-alphabetical order
// with no indication of which spent money or where to begin. This is the
// answer to "I have no idea what this does".
func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("looking at your machine..."))
	st := collectState()
	steps := guide.Next(st)

	if *asJSON {
		return emitJSON(map[string]any{"state": st, "steps": steps})
	}

	fmt.Printf("\n  %s\n", render.Bold("MIDDEN"))
	fmt.Printf("  %s\n\n", render.Dim("your AI CLI sessions, measured — then salvaged, then cleaned up"))

	// What is here. Orientation before instruction.
	fmt.Printf("  %s\n", render.Bold("WHAT YOU HAVE"))
	fmt.Printf("    %-22s %s across %d sessions\n", render.Dim("on disk"),
		render.Bytes(st.FootprintByte), st.Sessions)
	if st.LiveSessions > 0 {
		fmt.Printf("    %-22s %d\n", render.Dim("open right now"), st.LiveSessions)
	}
	if st.CriticalRisk > 0 {
		fmt.Printf("    %-22s %s\n", render.Dim("past the resume cliff"),
			render.RiskColour(core.RiskCritical)+fmt.Sprintf(" %d — these will not reload", st.CriticalRisk))
	}
	if st.Assayed > 0 {
		fmt.Printf("    %-22s %s\n", render.Dim("reclaimable"), render.Bytes(st.ReclaimBytes))
	}
	if st.Nuggets > 0 {
		fmt.Printf("    %-22s %d\n", render.Dim("nuggets reclaimed"), st.Nuggets)
	}

	// What it costs. Stated before anything is suggested.
	fmt.Printf("\n  %s\n", render.Bold("WHAT IT COSTS"))
	fmt.Printf("    %s  everything except the two below\n", render.Dim("FREE  "))
	fmt.Printf("    %s  %s\n", costTag(guide.Spends),
		"reclaim, refine — these call a model through your existing CLI seat")
	if unit := unitCostLine(); unit != "" {
		fmt.Printf("    %s\n", render.Dim(unit))
	} else {
		fmt.Printf("    %s\n", render.Dim("no spend recorded yet — both preview with --dry-run before charging anything"))
	}

	// What to do, in consequence order.
	fmt.Printf("\n  %s\n", render.Bold("WHAT TO DO, IN ORDER"))
	for i, s := range steps {
		if i >= 4 {
			break
		}
		fmt.Printf("\n    %d. %s\n", i+1, s.Why)
		fmt.Printf("       %s   %s\n", costTag(s.Cost), s.Command)
		fmt.Printf("              %s\n", render.Dim(s.Value))
	}

	fmt.Printf("\n  %s\n", render.Bold("SPENDING LESS"))
	for _, tip := range guide.Cheapest() {
		fmt.Printf("    %s %s\n", render.Dim("·"), tip)
	}

	fmt.Printf("\n  %s\n\n", render.Dim("midden help — every command, in pipeline order   |   midden ui — the same thing in a browser"))
	return nil
}

// unitCostLine reports observed unit economics, so "what does this cost" is
// answered in real numbers rather than adjectives.
func unitCostLine() string {
	db, err := index.Open()
	if err != nil {
		return ""
	}
	defer db.Close()

	totals, err := db.Costs()
	if err != nil || totals.Runs == 0 {
		return ""
	}

	var parts []string
	ops := make([]string, 0, len(totals.ByOp))
	for k := range totals.ByOp {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	for _, op := range ops {
		o := totals.ByOp[op]
		if o.Items == 0 {
			continue
		}
		noun := "nugget"
		if op == "refine" {
			noun = "artifact"
		}
		if o.AIU > 0 {
			parts = append(parts, fmt.Sprintf("%s ~%.0f AIU per %s", op, o.PerItem(), noun))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "measured on your runs: " + joinAnd(parts)
}

func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	out := ""
	for i, p := range parts {
		if i == len(parts)-1 {
			out += " and " + p
			continue
		}
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
