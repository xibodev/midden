package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/dispose"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

// kindOf extracts a record's tool-native type without knowing which tool
// wrote it.
func kindOf(line []byte) string {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &probe) != nil {
		return "unparsed"
	}
	if probe.Type == "" {
		return "unparsed"
	}
	return probe.Type
}

// cmdPrune rewrites transcripts with bulk payloads replaced by markers.
//
// Defaults to a dry run: disposal is the one place where a wrong default is
// unrecoverable.
func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	apply := fs.Bool("apply", false, "actually write pruned copies (default is a dry run)")
	replace := fs.Bool("replace", false, "after verification, swap the pruned copy in and back up the original")
	artifacts := fs.Bool("artifacts", false, "also prune binary assets (screenshots) — harvest them first")
	minBytes := fs.Int("min-bytes", 2048, "leave payloads smaller than this alone")
	minSize := fs.Int64("min-session", 50, "only consider transcripts larger than this many MiB")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		sc.IDPrefix = fs.Arg(0)
	}
	sc.IncludeNoise = true

	sessions, errs := adapter.Collect(*sc)
	reportErrs(errs)

	var targets []core.Session
	for _, s := range sessions {
		if s.TranscriptPath == "" || !fileExists(s.TranscriptPath) {
			continue
		}
		if s.Bytes < *minSize<<20 {
			continue
		}
		targets = append(targets, s)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Bytes > targets[j].Bytes })

	if len(targets) == 0 {
		fmt.Printf("  %s\n", render.Dim(fmt.Sprintf(
			"no transcripts over %d MiB — lower --min-session to consider smaller ones", *minSize)))
		return nil
	}

	opts := dispose.Options{
		Exhaust:     true,
		Bookkeeping: true,
		Artifacts:   *artifacts,
		MinBytes:    *minBytes,
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	workDir := filepath.Join(index.Dir(), "pruned")

	type outcome struct {
		Session  core.Session          `json:"session"`
		Plan     dispose.Plan          `json:"plan"`
		Verified *dispose.Verification `json:"verification,omitempty"`
		Replaced bool                  `json:"replaced"`
		Err      string                `json:"error,omitempty"`
	}
	var results []outcome
	var totalBefore, totalAfter int64

	if !*asJSON {
		mode := render.Dim("dry run — nothing will be written")
		if *apply {
			mode = render.Bold("APPLY")
		}
		fmt.Printf("\n  %s  %d transcript(s)   %s\n", render.Bold("PRUNE"), len(targets), mode)
		fmt.Printf("  %s\n\n", render.Rule(66))
	}

	for _, s := range targets {
		o := outcome{Session: s}

		if !*apply {
			// Estimate from the stored manifest rather than rewriting.
			p, err := estimatePrune(s, opts)
			if err != nil {
				o.Err = err.Error()
			} else {
				o.Plan = p
			}
			results = append(results, o)
			totalBefore += s.Bytes
			totalAfter += s.Bytes - o.Plan.BytesPruned
			if !*asJSON {
				printPruneRow(s, o.Plan, nil, false)
			}
			continue
		}

		target := filepath.Join(workDir, string(s.Tool), s.ID+".jsonl")
		p, err := dispose.PruneJSONL(s.TranscriptPath, target, opts, kindOf)
		if err != nil {
			o.Err = err.Error()
			results = append(results, o)
			db.RecordOp("prune", string(s.Tool), s.ID, s.Bytes, s.Bytes, err.Error(), false)
			continue
		}
		o.Plan = p

		// Nothing is trusted until it is verified.
		v, verr := dispose.Verify(s.TranscriptPath, target, kindOf)
		if verr != nil {
			o.Err = verr.Error()
		} else {
			o.Verified = v
		}

		if o.Verified != nil && o.Verified.OK && *replace {
			if err := swapIn(s, target); err != nil {
				o.Err = err.Error()
			} else {
				o.Replaced = true
			}
		}

		db.RecordOp("prune", string(s.Tool), s.ID, p.BeforeBytes, p.AfterBytes,
			fmt.Sprintf("verified=%v replaced=%v", o.Verified != nil && o.Verified.OK, o.Replaced),
			o.Err == "")

		results = append(results, o)
		totalBefore += p.BeforeBytes
		totalAfter += p.AfterBytes
		if !*asJSON {
			printPruneRow(s, p, o.Verified, o.Replaced)
		}
	}

	if *asJSON {
		return emitJSON(results)
	}

	saved := totalBefore - totalAfter
	pct := 0.0
	if totalBefore > 0 {
		pct = 100 * float64(saved) / float64(totalBefore)
	}
	fmt.Printf("\n  %-12s %s -> %s   %s\n", render.Bold("total"),
		render.Bytes(totalBefore), render.Bytes(totalAfter),
		render.Bold(fmt.Sprintf("%s recovered (%.0f%%)", render.Bytes(saved), pct)))

	if !*apply {
		fmt.Printf("\n  %s\n", render.Dim("this was an estimate; run with --apply to write verified copies"))
		fmt.Printf("  %s\n\n", render.Dim("originals are never modified unless you also pass --replace"))
	} else if !*replace {
		fmt.Printf("\n  %s\n", render.Dim("pruned copies written to "+workDir))
		fmt.Printf("  %s\n\n", render.Dim("originals untouched; add --replace to swap them in after verification"))
	} else {
		fmt.Printf("\n  %s\n\n", render.Dim("originals backed up alongside as .original"))
	}
	return nil
}

func printPruneRow(s core.Session, p dispose.Plan, v *dispose.Verification, replaced bool) {
	status := render.Dim("estimate")
	switch {
	case replaced:
		status = render.Bold("replaced")
	case v != nil && v.OK:
		status = "verified"
	case v != nil && !v.OK:
		status = "FAILED"
	}

	after := p.AfterBytes
	if after == 0 {
		after = s.Bytes - p.BytesPruned
	}
	saved := s.Bytes - after
	pct := 0.0
	if s.Bytes > 0 {
		pct = 100 * float64(saved) / float64(s.Bytes)
	}

	fmt.Printf("  %-9s %10s -> %-10s %5.0f%%  %-9s %s\n",
		s.Tool, render.Bytes(s.Bytes), render.Bytes(after), pct, status,
		core.Truncate(s.Title, 34))

	if v != nil && !v.OK {
		for _, f := range v.Failures {
			fmt.Printf("            %s\n", render.Dim("! "+f))
		}
	}
}

// estimatePrune predicts recovery from the stored manifest, without rewriting.
func estimatePrune(s core.Session, opts dispose.Options) (dispose.Plan, error) {
	a, ok := adapter.Find(s.Tool).(adapter.Assayer)
	if !ok {
		return dispose.Plan{}, fmt.Errorf("no assayer for %s", s.Tool)
	}
	m, err := a.Assay(s, 0)
	if err != nil {
		return dispose.Plan{}, err
	}

	var pruned int64
	if opts.Exhaust {
		pruned += m.Bytes["exhaust"]
	}
	if opts.Bookkeeping {
		pruned += m.Bytes["bookkeeping"]
	}
	if opts.Artifacts {
		pruned += m.Bytes["artifact"]
	}

	// Markers and preserved record structure mean recovery is never total.
	// 90% of the classified bulk is a realistic, deliberately conservative
	// estimate.
	pruned = pruned * 9 / 10

	return dispose.Plan{
		SessionID:    s.ID,
		Tool:         string(s.Tool),
		Source:       s.TranscriptPath,
		BeforeBytes:  m.TotalBytes,
		AfterBytes:   m.TotalBytes - pruned,
		RecordsTotal: m.TotalRecords,
		BytesPruned:  pruned,
	}, nil
}

// swapIn replaces the original transcript with the pruned copy, keeping the
// original alongside as a backup rather than deleting it.
func swapIn(s core.Session, pruned string) error {
	backup := s.TranscriptPath + ".original"
	if fileExists(backup) {
		return fmt.Errorf("backup already exists: %s", backup)
	}
	if err := os.Rename(s.TranscriptPath, backup); err != nil {
		return fmt.Errorf("back up original: %w", err)
	}
	if err := copyOver(pruned, s.TranscriptPath); err != nil {
		// Restore rather than leave the session without a transcript.
		os.Rename(backup, s.TranscriptPath)
		return fmt.Errorf("install pruned copy: %w", err)
	}
	return nil
}

func copyOver(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// cmdArchive moves a session's transcript out of the tool's active path,
// keeping a manifest so it stays understandable.
func cmdArchive(args []string) error {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	apply := fs.Bool("apply", false, "actually move files (default is a dry run)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		sc.IDPrefix = fs.Arg(0)
	}
	sc.IncludeNoise = true

	sessions, errs := adapter.Collect(*sc)
	reportErrs(errs)
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions match")
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	root := index.Dir()
	var moved, total int64
	var rows []map[string]any

	for _, s := range sessions {
		if s.TranscriptPath == "" || !fileExists(s.TranscriptPath) {
			continue
		}
		if s.Live != nil {
			fmt.Fprintf(os.Stderr, "  %s %s is open right now — skipping\n",
				render.Dim("skip:"), shortID(s.ID))
			continue
		}
		total++

		dir := dispose.ArchivePath(root, string(s.Tool), s.ID)
		rows = append(rows, map[string]any{
			"session": s.ID, "tool": s.Tool, "bytes": s.Bytes, "target": dir,
		})

		if !*apply {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}

		man := dispose.ArchiveManifest{
			Tool: string(s.Tool), SessionID: s.ID, Title: s.Title,
			Workspace: s.Dir, SourcePath: s.TranscriptPath, Bytes: s.Bytes,
			ArchivedAt: time.Now(),
			Note:       "Archived by midden. Move the transcript back to source_path to restore.",
		}
		mb, _ := json.MarshalIndent(man, "", "  ")
		os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o644)

		dst := filepath.Join(dir, filepath.Base(s.TranscriptPath))
		if err := os.Rename(s.TranscriptPath, dst); err != nil {
			if cerr := copyOver(s.TranscriptPath, dst); cerr != nil {
				db.RecordOp("archive", string(s.Tool), s.ID, s.Bytes, s.Bytes, cerr.Error(), false)
				continue
			}
			os.Remove(s.TranscriptPath)
		}
		moved += s.Bytes
		db.RecordOp("archive", string(s.Tool), s.ID, s.Bytes, 0, dir, true)
	}

	if *asJSON {
		return emitJSON(rows)
	}
	if !*apply {
		fmt.Printf("\n  %s would archive %d transcript(s)\n", render.Bold("ARCHIVE"), total)
		fmt.Printf("  %s\n\n", render.Dim("run with --apply to move them; a manifest is written alongside each"))
		return nil
	}
	fmt.Printf("\n  %s archived %s\n  %s\n\n", render.Bold("ARCHIVE"), render.Bytes(moved),
		render.Dim("under "+filepath.Join(root, "archive")))
	return nil
}

// cmdOps prints the audit log of every mutating operation.
func cmdOps(args []string) error {
	fs := flag.NewFlagSet("ops", flag.ExitOnError)
	limit := fs.Int("limit", 30, "how many entries to show")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	ops, err := db.Operations(*limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return emitJSON(ops)
	}
	if len(ops) == 0 {
		fmt.Printf("  %s\n", render.Dim("no operations recorded — nothing has been modified"))
		return nil
	}

	fmt.Printf("\n  %s\n  %s\n\n", render.Bold("OPERATION LOG"), render.Rule(66))
	for _, o := range ops {
		status := "ok"
		if !o.OK {
			status = "FAILED"
		}
		fmt.Printf("  %s  %-8s %-9s %-9s %10s -> %-10s %s\n",
			render.Dim(o.CreatedAt.Format("01-02 15:04")), o.Op, o.Tool,
			shortID(o.SessionID), render.Bytes(o.Before), render.Bytes(o.After), status)
		if o.Detail != "" {
			fmt.Printf("               %s\n", render.Dim(core.Truncate(o.Detail, 70)))
		}
	}
	fmt.Println()
	return nil
}

var _ = strings.TrimSpace
