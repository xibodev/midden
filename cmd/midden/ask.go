package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/advise"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/oracle"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/render"
	"github.com/mekjr1/midden/internal/summary"
)

// cmdSummarize produces a session summary at a chosen depth.
//
// Depth is explicit rather than automatic because the three levels have three
// different prices. Shallow always runs first: it is free, it is often enough,
// and it gives the paid tiers evidence to start from.
func cmdSummarize(args []string) error {
	fs := flag.NewFlagSet("summarize", flag.ExitOnError)
	depthFlag := fs.String("depth", "shallow", "shallow (free) | deep (one call) | xray (one call + workspace)")
	backend := fs.String("backend", "", "AI CLI to use")
	model := fs.String("model", "", "model to pass to the backend")
	out := fs.String("out", "", "write to a file instead of stdout")
	dryRun := fs.Bool("dry-run", false, "show the plan and estimated cost, invoke nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: midden summarize <id> [--depth deep|xray]")
	}

	depth, err := summary.ParseDepth(*depthFlag)
	if err != nil {
		return err
	}

	s, err := findOne(fs.Arg(0))
	if err != nil {
		return err
	}

	ctx := summary.Context{Session: s, Depth: depth}

	// Shallow evidence is gathered for every depth. It is free, and the paid
	// tiers are only worth running on top of it.
	if h, ok := adapter.Find(s.Tool).(core.Harvester); ok {
		if hv, err := h.Harvest(s, 12); err == nil {
			ctx.Harvest = hv
		}
	}
	if a, ok := adapter.Find(s.Tool).(adapter.Assayer); ok {
		if m, err := a.Assay(s, 80); err == nil {
			ctx.Manifest = m
		}
	}
	if depth == summary.XRay {
		fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("reading workspace state..."))
		ctx.Workspace = summary.InspectWorkspace(s.Dir)
	}
	ctx.Redact()

	// Free path: no model, no ledger entry, no confirmation.
	if depth == summary.Shallow {
		body := summary.RenderShallow(ctx)
		return emitSummary(body, *out, *asJSON, s, depth, nil)
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	raw := ctx.RawTokens()
	est := estimateFor(db, "summarize", raw, 1)

	fmt.Printf("\n  %s  %s\n", render.Bold("SUMMARIZE"), core.Truncate(s.Title, 52))
	fmt.Printf("  %s\n\n", render.Rule(62))
	fmt.Printf("  %-16s %s  %s\n", render.Dim("depth"), depth, render.Dim(depth.Describe()))
	fmt.Printf("  %-16s %s\n", render.Dim("estimated cost"), est.String())
	if ctx.Workspace != nil && ctx.Workspace.IsGit {
		state := "clean"
		if ctx.Workspace.Dirty {
			state = fmt.Sprintf("dirty, %d file(s)", ctx.Workspace.DirtyFiles)
		}
		fmt.Printf("  %-16s %s on %s\n", render.Dim("workspace"), state, ctx.Workspace.Branch)
	}
	if sm := redact.Summary(mergeFindings(ctx.Findings)); sm != "" {
		fmt.Printf("  %-16s %s\n", render.Dim("redaction"), sm)
	}
	fmt.Printf("\n  %s\n\n", render.Dim("free alternative: --depth shallow extracts the same session with no model call"))

	if *dryRun {
		fmt.Printf("  %s\n\n", render.Dim("dry run — nothing invoked."))
		return nil
	}
	if !*yes && !confirm("Run the "+string(depth)+" summary?") {
		fmt.Println("  cancelled")
		return nil
	}

	be, err := exec.Detect(*backend)
	if err != nil {
		return err
	}
	runner := &exec.Runner{Backend: be, Model: *model, Pure: true, Timeout: 12 * time.Minute}
	conv := runner.NewConversation()

	run := recordRun(db, "summarize", string(depth)+" "+shortID(s.ID), string(be), raw)
	run.CLISessions = append(run.CLISessions, conv.SessionID())

	fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("reading the session..."))
	res, err := conv.Prime(context.Background(), ctx.Prompt())

	run.Items = 1
	run.EndedAt = time.Now()
	run.OK = err == nil
	db.PutRun(run)

	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}

	body := exec.CleanOutput(res.Output)
	if r := redact.Text(body); r.Redacted {
		body = r.Text
	}

	time.Sleep(1500 * time.Millisecond)
	reconcile(db)

	var spent *cost.Run
	if runs, err := db.Runs(1, "summarize"); err == nil && len(runs) > 0 {
		spent = &runs[0]
	}
	return emitSummary(body, *out, *asJSON, s, depth, spent)
}

func emitSummary(body, outPath string, asJSON bool, s core.Session, depth summary.Depth, spent *cost.Run) error {
	if asJSON {
		return emitJSON(map[string]any{
			"session": s.ID, "depth": depth, "body": body, "cost": spent,
		})
	}
	if outPath != "" {
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Printf("\n  %s %s\n", render.Bold("written"), outPath)
	} else {
		fmt.Println()
		fmt.Println(body)
	}

	if spent != nil && !spent.Usage.Empty() {
		fmt.Printf("\n  %s %s  %s\n", render.Dim("cost"), spent.Usage.Unit(),
			render.Dim(fmt.Sprintf("%s tokens · %.0fs",
				cost.Compact(spent.Usage.Billable()), spent.Duration().Seconds())))
	} else if depth == summary.Shallow {
		fmt.Printf("\n  %s\n", render.Dim("free — no model was called. --depth deep synthesises across the whole session."))
	}
	fmt.Println()
	return nil
}

// cmdAsk answers questions about your own history.
//
// Cheap by construction: it reasons over the compressed picture the rest of
// the tool has already produced, not over the raw corpus. A question costs
// roughly the same whether you have 50 sessions or 5,000.
func cmdAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	backend := fs.String("backend", "", "AI CLI to use")
	model := fs.String("model", "", "model to pass to the backend")
	dryRun := fs.Bool("dry-run", false, "show the assembled evidence and cost, invoke nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	showEvidence := fs.Bool("evidence", false, "print the evidence that would be sent")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	st := collectState()
	question := strings.TrimSpace(strings.Join(fs.Args(), " "))

	// An empty prompt is a dead end. Offer questions the evidence can answer.
	if question == "" {
		fmt.Printf("\n  %s\n", render.Bold("ASK"))
		fmt.Printf("  %s\n\n", render.Dim("questions about your own history, answered from what midden already knows"))
		for _, q := range oracle.Suggestions(st) {
			fmt.Printf("    %s midden ask %s\n", render.Dim("·"), quoteArg(q))
		}
		fmt.Printf("\n  %s\n\n", render.Dim("--dry-run shows the evidence and cost without asking"))
		return nil
	}

	brief := buildBrief(db, st)
	brief.RedactAll()
	brief.Trim(question)

	if *showEvidence {
		fmt.Println(brief.Evidence())
		return nil
	}

	raw := brief.EstTokens(question)
	est := estimateFor(db, "ask", raw, 1)

	fmt.Printf("\n  %s  %s\n", render.Bold("ASK"), render.Dim(core.Truncate(question, 58)))
	fmt.Printf("  %s\n\n", render.Rule(62))
	fmt.Printf("  %-16s %d session(s), %d nugget(s), %d finding(s)\n", render.Dim("evidence"),
		st.Sessions, len(brief.Nuggets), len(brief.Findings))
	fmt.Printf("  %-16s %s\n", render.Dim("estimated cost"), est.String())
	if sm := redact.Summary(mergeFindings(brief.Redaction)); sm != "" {
		fmt.Printf("  %-16s %s\n", render.Dim("redaction"), sm)
	}
	fmt.Println()

	if *dryRun {
		fmt.Printf("  %s\n\n", render.Dim("dry run — nothing invoked. --evidence prints what would be sent."))
		return nil
	}
	if !*yes && !confirm("Ask?") {
		fmt.Println("  cancelled")
		return nil
	}

	be, err := exec.Detect(*backend)
	if err != nil {
		return err
	}
	runner := &exec.Runner{Backend: be, Model: *model, Pure: true, Timeout: 10 * time.Minute}
	conv := runner.NewConversation()

	run := recordRun(db, "ask", core.Truncate(question, 40), string(be), raw)
	run.CLISessions = append(run.CLISessions, conv.SessionID())

	fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("thinking..."))
	res, err := conv.Prime(context.Background(), brief.Prompt(question))

	run.Items = 1
	run.EndedAt = time.Now()
	run.OK = err == nil
	db.PutRun(run)

	if err != nil {
		return fmt.Errorf("ask: %w", err)
	}

	answer := exec.CleanOutput(res.Output)
	if r := redact.Text(answer); r.Redacted {
		answer = r.Text
	}

	time.Sleep(1500 * time.Millisecond)
	reconcile(db)

	if *asJSON {
		return emitJSON(map[string]any{"question": question, "answer": answer})
	}

	fmt.Println()
	fmt.Println(answer)

	if runs, err := db.Runs(1, "ask"); err == nil && len(runs) > 0 && !runs[0].Usage.Empty() {
		a := runs[0]
		fmt.Printf("\n  %s %s  %s\n", render.Dim("cost"), a.Usage.Unit(),
			render.Dim(fmt.Sprintf("%s tokens · %.0fs",
				cost.Compact(a.Usage.Billable()), a.Duration().Seconds())))
	}
	fmt.Println()
	return nil
}

// buildBrief assembles the compressed picture the oracle reasons over.
func buildBrief(db *index.DB, st guide.State) oracle.Brief {
	b := oracle.Brief{State: st}

	if ns, err := db.Nuggets(index.NuggetQuery{Limit: oracle.MaxNuggets}); err == nil {
		b.Nuggets = ns
	}
	if t, err := db.Costs(); err == nil {
		b.Costs = t
	}

	sessions, _ := adapter.Collect(core.Scope{IncludeNoise: true})
	totals, _ := db.Aggregate("")
	counts, _ := db.NuggetCounts()

	b.Findings = advise.Analyse(advise.Input{
		Sessions: sessions, Footprints: adapter.Footprints(),
		Assayed: totals.Assayed, Signal: totals.Signal, Exhaust: totals.Exhaust,
		Artifact: totals.Artifact, Book: totals.Book, DupBytes: totals.DupBytes,
		Images: totals.Images, Clusters: totals.Clusters, Nuggets: counts,
	})

	// Workspace activity is usually what a question is really about.
	agg := map[string]*oracle.DirStat{}
	for _, s := range sessions {
		if s.Noise || s.Dir == "" {
			continue
		}
		d, ok := agg[s.Dir]
		if !ok {
			d = &oracle.DirStat{Dir: s.Dir}
			agg[s.Dir] = d
		}
		d.Sessions++
		d.Bytes += s.Bytes
	}
	for _, d := range agg {
		b.TopDirs = append(b.TopDirs, *d)
	}
	sort.Slice(b.TopDirs, func(i, j int) bool { return b.TopDirs[i].Bytes > b.TopDirs[j].Bytes })
	if len(b.TopDirs) > 8 {
		b.TopDirs = b.TopDirs[:8]
	}
	return b
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, ` "'`) {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
