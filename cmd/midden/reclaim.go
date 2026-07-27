package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/reclaim"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/render"
)

// cmdReclaim mines sessions for reusable knowledge.
//
// Scoping is mandatory and the cost is shown before anything runs: nobody
// salvages 36 GiB blind, and a surprise bill is the fastest way to make a
// tool untrustworthy.
func cmdReclaim(args []string) error {
	fs := flag.NewFlagSet("reclaim", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	backend := fs.String("backend", "", "AI CLI to use (copilot|claude|opencode); default is the first installed")
	model := fs.String("model", "", "model to pass to the backend (cheap models are fine for extraction)")
	maxRecords := fs.Int("records", 120, "evidence records per session")
	budgetCalls := fs.Int("budget", 20, "maximum model invocations for this run")
	dryRun := fs.Bool("dry-run", false, "show the plan and estimated cost, invoke nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		sc.IDPrefix = fs.Arg(0)
		sc.IncludeNoise = true
	}
	if sc.Days == 0 && sc.Workspace == "" && sc.IDPrefix == "" {
		return fmt.Errorf("reclaim requires a scope: use --days, --workspace, or a session id")
	}

	sessions, errs := adapter.Collect(*sc)
	reportErrs(errs)
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions match")
	}
	if len(sessions) > *budgetCalls {
		sessions = sessions[:*budgetCalls]
	}

	be, err := exec.Detect(*backend)
	if err != nil {
		return err
	}

	// Build every slice first so the true cost is known before anything is
	// invoked.
	type job struct {
		session core.Session
		slice   reclaim.Slice
	}
	var jobs []job
	var estTokens int
	var allFindings []redact.Finding

	fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("assaying scope..."))
	for _, s := range sessions {
		a, ok := adapter.Find(s.Tool).(adapter.Assayer)
		if !ok {
			continue
		}
		m, err := a.Assay(s, *maxRecords)
		if err != nil {
			continue
		}
		sl := reclaim.BuildSlice(s, m, *maxRecords)
		if len(sl.Candidates) == 0 {
			continue
		}
		jobs = append(jobs, job{s, sl})
		estTokens += sl.EstTokens()
		allFindings = append(allFindings, sl.Findings...)
	}
	if len(jobs) == 0 {
		return fmt.Errorf("no usable evidence in scope")
	}

	// Pre-flight: nothing expensive starts without a prediction.
	fmt.Printf("\n  %s  %d session(s) via %s\n", render.Bold("RECLAIM"), len(jobs), be)
	fmt.Printf("  %s\n\n", render.Rule(62))
	fmt.Printf("  %-16s %d\n", render.Dim("model calls"), len(jobs))
	fmt.Printf("  %-16s ~%d\n", render.Dim("input tokens"), estTokens)
	fmt.Printf("  %-16s %s\n", render.Dim("backend"), string(be)+" (uses your existing seat, no API key)")
	if *model != "" {
		fmt.Printf("  %-16s %s\n", render.Dim("model"), *model)
	}
	if s := redact.Summary(mergeFindings(allFindings)); s != "" {
		fmt.Printf("  %-16s %s\n", render.Dim("redaction"), s)
	}
	fmt.Println()

	if *dryRun {
		fmt.Printf("  %s\n\n", render.Dim("dry run — nothing invoked. Drop --dry-run to proceed."))
		return nil
	}
	if !*yes && !confirm(fmt.Sprintf("Run %d extraction(s)?", len(jobs))) {
		fmt.Println("  cancelled")
		return nil
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	runner := &exec.Runner{Backend: be, Model: *model, Pure: true}
	budget := &exec.Budget{MaxCalls: *budgetCalls}
	ctx := context.Background()

	var stored []index.Nugget
	for i, j := range jobs {
		if err := budget.Allow(j.slice.EstTokens()); err != nil {
			fmt.Fprintf(os.Stderr, "  %s %v\n", render.Dim("stopping:"), err)
			break
		}

		fmt.Fprintf(os.Stderr, "\r  mining %d/%d  %-44s", i+1, len(jobs),
			core.Truncate(j.session.Title, 42))

		res, err := runner.Run(ctx, j.slice.Prompt())
		budget.Charge(j.slice.EstTokens())
		if err != nil {
			fmt.Fprintf(os.Stderr, "\r  %s %v\n", render.Dim("failed:"), err)
			continue
		}

		modelName := *model
		if modelName == "" {
			modelName = string(be) + ":default"
		}
		ns, err := reclaim.Parse(res.Output, j.session, modelName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\r  %s %s: %v\n", render.Dim("unparsed:"),
				shortID(j.session.ID), err)
			continue
		}
		if len(ns) == 0 {
			continue
		}
		if err := db.PutNuggets(ns); err != nil {
			return fmt.Errorf("store nuggets: %w", err)
		}
		stored = append(stored, ns...)
	}
	fmt.Fprintf(os.Stderr, "\r%-70s\r", "")

	if *asJSON {
		return emitJSON(stored)
	}

	calls, toks := budget.Spent()
	fmt.Printf("\n  %s %d nugget(s) from %d call(s), ~%d tokens\n",
		render.Bold("stored"), len(stored), calls, toks)

	byKind := map[string]int{}
	for _, n := range stored {
		byKind[n.Kind]++
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Printf("    %-11s %d\n", k, byKind[k])
	}
	fmt.Printf("\n  %s\n\n", render.Dim("midden nuggets   |   midden refine tutorial --workspace <name>"))
	return nil
}

func mergeFindings(in []redact.Finding) []redact.Finding {
	m := map[string]int{}
	for _, f := range in {
		m[f.Rule] += f.Count
	}
	var out []redact.Finding
	for k, v := range m {
		out = append(out, redact.Finding{Rule: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rule < out[j].Rule })
	return out
}

// confirm asks before spending. Non-interactive callers should pass --yes.
func confirm(question string) bool {
	fmt.Printf("  %s [y/N] ", question)
	var answer string
	fmt.Scanln(&answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// cmdNuggets browses what has been reclaimed.
func cmdNuggets(args []string) error {
	fs := flag.NewFlagSet("nuggets", flag.ExitOnError)
	kind := fs.String("kind", "", "filter by kind (decision|error_fix|command|gotcha|dead_end|artifact)")
	workspace := fs.String("workspace", "", "filter by workspace")
	search := fs.String("search", "", "substring match on title or body")
	limit := fs.Int("limit", 25, "maximum nuggets to show")
	full := fs.Bool("full", false, "show full bodies")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	ns, err := db.Nuggets(index.NuggetQuery{
		Kind: *kind, Workspace: *workspace, Search: *search, Limit: *limit,
	})
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(ns)
	}
	if len(ns) == 0 {
		fmt.Printf("  %s\n", render.Dim("no nuggets yet — run `midden reclaim --days 7`"))
		return nil
	}

	counts, _ := db.NuggetCounts()
	var parts []string
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
	}

	fmt.Printf("\n  %s  %s\n  %s\n\n", render.Bold("NUGGETS"),
		render.Dim(strings.Join(parts, " · ")), render.Rule(66))

	for _, n := range ns {
		flags := ""
		if n.Redacted {
			flags = render.Dim("  [redacted]")
		}
		fmt.Printf("  %-11s %s%s\n", render.ToolColour(core.Tool(n.Tool)),
			render.Bold(core.Truncate(n.Title, 58)), flags)

		body := n.Body
		if !*full {
			body = core.Truncate(strings.Join(strings.Fields(body), " "), 150)
		}
		fmt.Printf("    %s\n", body)
		fmt.Printf("    %s\n\n", render.Dim(fmt.Sprintf("%s · conf %.0f%% · %s · %s",
			n.Kind, n.Confidence*100, shortID(n.SessionID), core.Truncate(n.Workspace, 34))))
	}
	return nil
}
