package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/find"
	"github.com/mekjr1/midden/internal/render"
)

// cmdFind searches session TRANSCRIPT CONTENT.
//
// `midden ls` filters by tool, repo, workspace and age -- facts about where a
// session lived. None of them answers "find my work on X", and Title is only
// the first prompt. Measured here: "module-v2" matched zero of 400 titles while
// seven of the forty most recent transcripts contained it.
//
// This is the step that was missing before mining: evidence is searchable once
// mined, and choosing what to mine required searching first.
func cmdFind(args []string) error {
	fs := flag.NewFlagSet("find", flag.ExitOnError)
	limit := fs.Int("limit", 15, "maximum sessions to report")
	scanMax := fs.Int("scan", 300, "maximum sessions to open")
	days := fs.Int("days", 0, "only sessions updated in the last N days")
	tool := fs.String("tool", "", "restrict to one tool")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	query := fs.Arg(0)
	if query == "" {
		return fmt.Errorf("usage: midden find <text> [-days N] [-tool claude] [-limit N]")
	}

	sc := core.Scope{Days: *days}
	if *tool != "" {
		sc.Tools = []core.Tool{core.Tool(*tool)}
	}

	sessions, errs := adapter.Collect(sc)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "source store: %v\n", e)
	}

	res := find.Sessions(sessions, query, find.Options{
		MaxSessions: *scanMax,
		MaxHits:     *limit,
	})

	if *asJSON {
		return emitJSON(res)
	}

	if len(res.Hits) == 0 {
		fmt.Printf("\n  no session transcript contains %q\n", query)
		fmt.Printf("  scanned %d of %d sessions\n\n", res.Scanned, len(sessions))
		return nil
	}

	fmt.Printf("\n  SESSIONS MENTIONING %q\n", query)
	fmt.Println("  " + render.Rule(60))
	for _, h := range res.Hits {
		s := h.Session
		fmt.Printf("\n  [%s] %s  %s  %d match(es)\n",
			shortID(s.ID), s.Tool, render.Age(s.Age()), h.Matches)
		// A title is DERIVED FROM THE FIRST PROMPT -- unbounded prose, not a
		// label. Printed whole it buried three results under thousands of
		// characters of instructions.
		if t := clipLine(s.Title, 90); t != "" {
			fmt.Printf("      %s\n", t)
		}
		fmt.Printf("      %s\n", h.Excerpt)
	}

	// The count travels with what it excludes: a bounded scan that reports
	// only its hits reads as a complete answer.
	fmt.Printf("\n  scanned %d session(s)", res.Scanned)
	if res.Skipped > 0 {
		fmt.Printf(", %d not opened (bound reached)", res.Skipped)
	}
	if res.Truncated {
		fmt.Printf("; at least one transcript was read in part")
	}
	fmt.Printf("\n  midden assay <id>  |  midden reclaim <id>\n\n")
	return nil
}

// clipLine renders untrusted prose as one bounded line.
func clipLine(s string, n int) string {
	flat := strings.Join(strings.Fields(s), " ")
	if len(flat) <= n {
		return flat
	}
	return flat[:n] + "…"
}
