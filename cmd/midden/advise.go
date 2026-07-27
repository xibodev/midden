package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/advise"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

// cmdAdvise reports how to produce less exhaust next time.
//
// Every finding cites its evidence. "An oracle that finds patterns" is
// unfalsifiable; "these four sessions hold half your bytes" is checkable.
func cmdAdvise(args []string) error {
	fs := flag.NewFlagSet("advise", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	minSeverity := fs.String("min", "info", "minimum severity (info|low|medium|high)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}

	threshold, err := parseSeverity(*minSeverity)
	if err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	sc.IncludeNoise = true
	sessions, errs := adapter.Collect(*sc)
	reportErrs(errs)

	toolFilter := ""
	if len(sc.Tools) == 1 {
		toolFilter = string(sc.Tools[0])
	}
	totals, _ := db.Aggregate(toolFilter)
	nuggets, _ := db.NuggetCounts()

	findings := advise.Analyse(advise.Input{
		Sessions:   sessions,
		Footprints: adapter.Footprints(),
		Assayed:    totals.Assayed,
		Signal:     totals.Signal,
		Exhaust:    totals.Exhaust,
		Artifact:   totals.Artifact,
		Book:       totals.Book,
		DupBytes:   totals.DupBytes,
		Images:     totals.Images,
		Clusters:   totals.Clusters,
		Nuggets:    nuggets,
	})

	var kept []advise.Finding
	for _, f := range findings {
		if f.Severity >= threshold {
			kept = append(kept, f)
		}
	}

	if *asJSON {
		return emitJSON(kept)
	}

	fmt.Printf("\n  %s  %d finding(s) across %d session(s)\n",
		render.Bold("ADVISE"), len(kept), len(sessions))
	fmt.Printf("  %s\n", render.Rule(66))

	if len(kept) == 0 {
		fmt.Printf("\n  %s\n\n", render.Dim("nothing to flag — run `midden scan --assay` for deeper analysis"))
		return nil
	}

	var recoverable int64
	for _, f := range kept {
		fmt.Printf("\n  %-8s %-11s %s\n", severityColour(f.Severity),
			render.Dim(f.Category), render.Bold(f.Title))
		fmt.Printf("           %s\n", render.Dim(f.Evidence))
		for _, line := range wrap(f.Action, 74) {
			fmt.Printf("           %s\n", line)
		}
		if f.Bytes > 0 {
			recoverable += f.Bytes
		}
	}

	if totals.Assayed == 0 {
		fmt.Printf("\n  %s\n", render.Dim("run `midden scan --assay` to enable exhaust and duplication analysis"))
	}
	fmt.Println()
	return nil
}

func severityColour(s advise.Severity) string {
	switch s {
	case advise.High:
		return render.RiskColour(core.RiskCritical)
	case advise.Medium:
		return render.RiskColour(core.RiskWarn)
	case advise.Low:
		return render.RiskColour(core.RiskWatch)
	}
	return render.Dim("info")
}

func parseSeverity(s string) (advise.Severity, error) {
	switch strings.ToLower(s) {
	case "info":
		return advise.Info, nil
	case "low":
		return advise.Low, nil
	case "medium":
		return advise.Medium, nil
	case "high":
		return advise.High, nil
	}
	return advise.Info, fmt.Errorf("unknown severity %q (want info, low, medium or high)", s)
}

// wrap breaks text to a width so multi-line advice stays aligned.
func wrap(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}
