package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/render"
)

// cmdWatch is the prevention daemon.
//
// It is deliberately tiny and LLM-free: poll file sizes and live PIDs, warn
// before the resume cliff, and print the handoff command. An on-demand tool
// cannot warn you about a session that is dying right now, which is how four
// sessions and 2.8 GiB were lost on the machine this was built for.
func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	interval := fs.Duration("interval", 5*time.Minute, "poll interval")
	once := fs.Bool("once", false, "check once and exit (for cron or Task Scheduler)")
	quiet := fs.Bool("quiet", false, "only print when something needs attention")
	minRisk := fs.String("min-risk", "watch", "report at or above this level (watch|warn|critical)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	threshold, err := parseRisk(*minRisk)
	if err != nil {
		return err
	}

	check := func() int {
		return reportRisk(threshold, *quiet)
	}

	if *once {
		// Exit code carries the signal so schedulers and hooks can react.
		if n := check(); n > 0 {
			os.Exit(3)
		}
		return nil
	}

	fmt.Printf("  %s watching every %s (min-risk %s) — Ctrl-C to stop\n\n",
		render.Bold("midden watch"), *interval, threshold)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	check()
	for {
		select {
		case <-ticker.C:
			check()
		case <-stop:
			fmt.Println("\n  stopped")
			return nil
		}
	}
}

// reportRisk prints every session at or above the threshold and returns how
// many were found.
func reportRisk(threshold core.Risk, quiet bool) int {
	sessions, errs := adapter.Collect(core.Scope{IncludeNoise: true})
	reportErrs(errs)

	var at []core.Session
	for _, s := range sessions {
		if s.Risk() >= threshold && s.Risk() != core.RiskNone {
			at = append(at, s)
		}
	}
	sort.Slice(at, func(i, j int) bool { return at[i].Bytes > at[j].Bytes })

	stamp := render.Dim(time.Now().Format("15:04:05"))

	if len(at) == 0 {
		if !quiet {
			fmt.Printf("  %s  %s\n", stamp, render.Dim("all clear"))
		}
		return 0
	}

	fmt.Printf("  %s  %s\n", stamp, render.Bold(fmt.Sprintf("%d session(s) need attention", len(at))))
	for _, s := range at {
		fmt.Printf("\n    %-9s %-11s %s\n",
			render.RiskColour(s.Risk()), render.Bytes(s.Bytes), core.Truncate(s.Title, 52))
		fmt.Printf("    %s\n", render.Dim(s.Dir))

		switch s.Risk() {
		case core.RiskCritical:
			fmt.Printf("    %s\n", render.Dim("past the cliff — do not resume; hand off instead:"))
			fmt.Printf("    midden brief %s --handoff\n", shortID(s.ID))
		case core.RiskWarn:
			fmt.Printf("    %s\n", render.Dim("approaching the cliff — finish at a milestone and hand off soon"))
			fmt.Printf("    midden brief %s --handoff\n", shortID(s.ID))
		default:
			fmt.Printf("    %s\n", render.Dim("growing — worth a milestone handoff before it gets larger"))
		}
	}
	fmt.Println()
	return len(at)
}

func parseRisk(s string) (core.Risk, error) {
	switch s {
	case "watch":
		return core.RiskWatch, nil
	case "warn":
		return core.RiskWarn, nil
	case "critical":
		return core.RiskCritical, nil
	}
	return core.RiskNone, fmt.Errorf("unknown risk level %q (want watch, warn or critical)", s)
}
