package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/refine"
	"github.com/mekjr1/midden/internal/render"
)

// cmdCatalog proposes what could be written from the reclaimed evidence,
// without invoking anything.
//
// Deciding the whole artifact set up front is what makes single-warm-context
// generation possible, which is where the cost saving lives.
func cmdCatalog(args []string) error {
	fs := flag.NewFlagSet("catalog", flag.ExitOnError)
	workspace := fs.String("workspace", "", "limit evidence to one workspace")
	session := fs.String("session", "", "limit evidence to one session")
	minNuggets := fs.Int("min", 2, "minimum supporting nuggets for a proposal")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	ns, err := db.Nuggets(index.NuggetQuery{Workspace: *workspace, SessionID: *session})
	if err != nil {
		return err
	}
	if len(ns) == 0 {
		return fmt.Errorf("no nuggets in scope — run `midden reclaim` first")
	}

	items := refine.Catalog(ns, *minNuggets)
	if *asJSON {
		return emitJSON(items)
	}

	ev := refine.Evidence{Nuggets: ns}
	fmt.Printf("\n  %s  %d nugget(s) available\n", render.Bold("CATALOG"), len(ns))
	fmt.Printf("  %s\n\n", render.Rule(64))

	if len(items) == 0 {
		fmt.Printf("  %s\n\n", render.Dim("not enough evidence for any artifact — reclaim more sessions"))
		return nil
	}
	for _, it := range items {
		fmt.Printf("  %-11s %-30s %s\n", render.Bold(it.Template),
			it.Title, render.Dim(it.Why))
	}

	fmt.Printf("\n  %s ~%d tokens loaded once, then ~%d per artifact\n",
		render.Dim("cost:"), ev.EstTokens(), 250)
	fmt.Printf("  %s\n\n", render.Dim(fmt.Sprintf(
		"midden refine %s --workspace <name>   |   midden refine --all", items[0].Template)))
	return nil
}

// cmdRefine generates artifacts from nuggets.
//
// When several artifacts are requested they share one conversation: the
// evidence is loaded once and every subsequent artifact reuses it as cached
// context. Generating them separately would repay the dominant cost each time.
func cmdRefine(args []string) error {
	fs := flag.NewFlagSet("refine", flag.ExitOnError)
	workspace := fs.String("workspace", "", "limit evidence to one workspace")
	session := fs.String("session", "", "limit evidence to one session")
	topic := fs.String("topic", "", "focus the artifact on a particular angle")
	outDir := fs.String("out", "", "output directory (default ~/.midden/artifacts)")
	backend := fs.String("backend", "", "AI CLI to use")
	model := fs.String("model", "", "model to pass to the backend")
	all := fs.Bool("all", false, "generate every artifact the catalog proposes")
	dryRun := fs.Bool("dry-run", false, "show the plan and estimated cost, invoke nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	ns, err := db.Nuggets(index.NuggetQuery{Workspace: *workspace, SessionID: *session})
	if err != nil {
		return err
	}
	if len(ns) == 0 {
		return fmt.Errorf("no nuggets in scope — run `midden reclaim` first")
	}

	// Decide the full artifact set before loading anything.
	var wanted []refine.Template
	if *all {
		for _, it := range refine.Catalog(ns, 2) {
			if t, ok := refine.FindTemplate(it.Template); ok {
				wanted = append(wanted, t)
			}
		}
	} else {
		for _, name := range fs.Args() {
			t, ok := refine.FindTemplate(name)
			if !ok {
				return fmt.Errorf("unknown artifact %q (have: %s)",
					name, strings.Join(refine.TemplateNames(), ", "))
			}
			wanted = append(wanted, t)
		}
	}
	if len(wanted) == 0 {
		return fmt.Errorf("name an artifact (%s) or pass --all",
			strings.Join(refine.TemplateNames(), ", "))
	}

	scope := *workspace
	if scope == "" {
		scope = "all reclaimed evidence"
	}
	ev := refine.Evidence{Scope: scope, Nuggets: ns}

	be, err := exec.Detect(*backend)
	if err != nil {
		return err
	}

	dir := *outDir
	if dir == "" {
		dir = filepath.Join(index.Dir(), "artifacts")
	}

	// Pre-flight. The saving from one warm context is the headline number, so
	// it is stated explicitly.
	loadOnce := ev.EstTokens()
	perArtifact := 300
	batched := loadOnce + len(wanted)*perArtifact
	separate := len(wanted) * (loadOnce + perArtifact)

	fmt.Printf("\n  %s  %d artifact(s) from %d nugget(s) via %s\n",
		render.Bold("REFINE"), len(wanted), len(ns), be)
	fmt.Printf("  %s\n\n", render.Rule(64))
	for _, t := range wanted {
		fmt.Printf("  %-11s %s\n", t.Name, render.Dim(t.Title))
	}
	fmt.Printf("\n  %-18s ~%d tokens %s\n", render.Dim("evidence"), loadOnce,
		render.Dim("loaded once, reused by every artifact"))
	fmt.Printf("  %-18s ~%d tokens\n", render.Dim("estimated total"), batched)
	if len(wanted) > 1 {
		fmt.Printf("  %-18s ~%d tokens %s\n", render.Dim("if run separately"), separate,
			render.Dim(fmt.Sprintf("(%.1fx more — cache writes dominate)",
				float64(separate)/float64(batched))))
	}
	fmt.Printf("  %-18s %s\n\n", render.Dim("output"), dir)

	if *dryRun {
		fmt.Printf("  %s\n\n", render.Dim("dry run — nothing invoked."))
		return nil
	}
	if !*yes && !confirm(fmt.Sprintf("Generate %d artifact(s)?", len(wanted))) {
		fmt.Println("  cancelled")
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	runner := &exec.Runner{Backend: be, Model: *model, Pure: true, Timeout: 15 * time.Minute}
	conv := runner.NewConversation()
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("loading evidence into one session..."))
	if _, err := conv.Prime(ctx, ev.Preamble()); err != nil {
		return fmt.Errorf("prime conversation: %w", err)
	}

	modelName := *model
	if modelName == "" {
		modelName = string(be) + ":default"
	}

	type produced struct {
		Template string `json:"template"`
		Path     string `json:"path"`
		Bytes    int    `json:"bytes"`
		Warning  string `json:"warning,omitempty"`
		Err      string `json:"error,omitempty"`
	}
	var out []produced

	for i, t := range wanted {
		fmt.Fprintf(os.Stderr, "\r  writing %d/%d  %-34s", i+1, len(wanted), t.Name)

		res, err := conv.Ask(ctx, t.Request(*topic))
		if err != nil {
			out = append(out, produced{Template: t.Name, Err: err.Error()})
			continue
		}

		body := refine.CleanOutput(res.Output)
		if strings.TrimSpace(body) == "" {
			out = append(out, produced{Template: t.Name, Err: "empty output"})
			continue
		}

		// Last line of defence before anything reaches disk in a form a human
		// might publish.
		scan := redact.Scan(body)
		p := produced{Template: t.Name}
		if len(scan) > 0 {
			r := redact.Text(body)
			body = r.Text
			p.Warning = redact.Summary(scan)
		}

		header := fmt.Sprintf("<!-- generated by midden from %d nuggets · scope: %s · model: %s · %s -->\n\n",
			len(ns), scope, modelName, time.Now().Format("2006-01-02"))
		path := filepath.Join(dir, refine.Slug(scope+"-"+t.Name)+".md")
		if err := os.WriteFile(path, []byte(header+body), 0o644); err != nil {
			out = append(out, produced{Template: t.Name, Err: err.Error()})
			continue
		}

		p.Path = path
		p.Bytes = len(body)
		out = append(out, p)

		db.PutArtifact(index.Artifact{
			Kind: t.Name, Title: t.Title, Path: path, Scope: scope,
			NuggetIDs: refine.NuggetIDs(ns), Model: modelName,
		})
	}
	fmt.Fprintf(os.Stderr, "\r%-60s\r", "")

	if *asJSON {
		return emitJSON(out)
	}

	fmt.Printf("\n  %s\n", render.Bold("written"))
	for _, p := range out {
		if p.Err != "" {
			fmt.Printf("    %-11s %s\n", p.Template, render.Dim("failed: "+p.Err))
			continue
		}
		fmt.Printf("    %-11s %-8s %s\n", p.Template, render.Bytes(int64(p.Bytes)), p.Path)
		if p.Warning != "" {
			fmt.Printf("                %s\n", render.Dim("! "+p.Warning))
		}
	}
	fmt.Printf("\n  %s\n\n", render.Dim(
		"review before publishing — generated from your own sessions, which may contain private detail"))
	return nil
}

// cmdArtifacts lists what has been generated.
func cmdArtifacts(args []string) error {
	fs := flag.NewFlagSet("artifacts", flag.ExitOnError)
	limit := fs.Int("limit", 25, "maximum entries")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	as, err := db.Artifacts(*limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(as)
	}
	if len(as) == 0 {
		fmt.Printf("  %s\n", render.Dim("nothing generated yet — try `midden catalog`"))
		return nil
	}

	fmt.Printf("\n  %s\n  %s\n\n", render.Bold("ARTIFACTS"), render.Rule(64))
	for _, a := range as {
		fmt.Printf("  %-11s %-30s %s\n", a.Kind, core.Truncate(a.Title, 28),
			render.Dim(a.CreatedAt.Format("01-02 15:04")))
		fmt.Printf("    %s\n", render.Dim(a.Path))
		fmt.Printf("    %s\n\n", render.Dim(fmt.Sprintf("%d nuggets · %s", len(a.NuggetIDs), a.Model)))
	}
	return nil
}
