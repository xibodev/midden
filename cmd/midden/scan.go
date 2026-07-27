package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

// cmdScan refreshes the index and, optionally, assays transcripts.
//
// Assay is skipped for sessions whose transcript is byte- and mtime-identical
// to the last run, so a second scan over 36 GiB costs seconds.
func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	doAssay := fs.Bool("assay", false, "also classify transcripts (slower, enables compression reporting)")
	force := fs.Bool("force", false, "re-assay even when the transcript is unchanged")
	maxBytes := fs.Int64("max-bytes", 0, "skip assay for transcripts larger than this many MiB (0 = no limit)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
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

	if err := db.PutSessions(sessions); err != nil {
		return fmt.Errorf("index sessions: %w", err)
	}

	type result struct {
		Indexed      int    `json:"indexed"`
		Assayed      int    `json:"assayed"`
		Skipped      int    `json:"skipped_unchanged"`
		TooLarge     int    `json:"skipped_too_large"`
		NoTranscript int    `json:"no_transcript"`
		Failed       int    `json:"failed"`
		Bytes        int64  `json:"bytes_assayed"`
		IndexPath    string `json:"index_path"`
	}
	res := result{Indexed: len(sessions), IndexPath: db.Path()}

	if *doAssay {
		limit := *maxBytes << 20
		start := time.Now()

		for i, s := range sessions {
			// File-backed tools may have an indexed session with no
			// transcript on disk: the CLI recorded metadata but the event log
			// was never written or has been cleaned up. That is normal, not a
			// failure.
			if s.Tool != core.ToolOpencode {
				if s.TranscriptPath == "" || !fileExists(s.TranscriptPath) {
					res.NoTranscript++
					continue
				}
			}
			if limit > 0 && s.Bytes > limit {
				res.TooLarge++
				continue
			}

			srcBytes, srcMtime := transcriptStamp(s)
			if !*force && db.ManifestFresh(string(s.Tool), s.ID, srcBytes, srcMtime) {
				res.Skipped++
				continue
			}

			a, ok := adapter.Find(s.Tool).(adapter.Assayer)
			if !ok {
				continue
			}
			if !*asJSON {
				fmt.Fprintf(os.Stderr, "\r  assaying %d/%d  %-46s",
					i+1, len(sessions), core.Truncate(s.Title, 44))
			}

			m, err := a.Assay(s, 200)
			if err != nil {
				res.Failed++
				continue
			}
			if err := db.PutManifest(m, srcBytes, srcMtime); err != nil {
				res.Failed++
				continue
			}
			res.Assayed++
			res.Bytes += m.TotalBytes
		}

		if !*asJSON {
			fmt.Fprintf(os.Stderr, "\r%-72s\r", "")
			fmt.Printf("  assayed %s in %s\n", render.Bytes(res.Bytes),
				time.Since(start).Round(time.Second))
		}
	}

	if *asJSON {
		return emitJSON(res)
	}

	fmt.Printf("\n  %s\n", render.Bold("SCAN COMPLETE"))
	fmt.Printf("  %-22s %d\n", render.Dim("sessions indexed"), res.Indexed)
	if *doAssay {
		fmt.Printf("  %-22s %d\n", render.Dim("transcripts assayed"), res.Assayed)
		if res.Skipped > 0 {
			fmt.Printf("  %-22s %d %s\n", render.Dim("skipped"), res.Skipped, render.Dim("(unchanged)"))
		}
		if res.TooLarge > 0 {
			fmt.Printf("  %-22s %d %s\n", render.Dim("skipped"), res.TooLarge, render.Dim("(over --max-bytes)"))
		}
		if res.NoTranscript > 0 {
			fmt.Printf("  %-22s %d %s\n", render.Dim("no transcript"), res.NoTranscript,
				render.Dim("(metadata only, nothing on disk to classify)"))
		}
		if res.Failed > 0 {
			fmt.Printf("  %-22s %d\n", render.Dim("failed"), res.Failed)
		}
	}
	fmt.Printf("  %-22s %s\n\n", render.Dim("index"), db.Path())
	if !*doAssay {
		fmt.Printf("  %s\n\n", render.Dim("run `midden scan --assay` to measure what is reclaimable"))
	}
	return nil
}

// transcriptStamp fingerprints a session's source so an unchanged transcript
// can be skipped on the next scan.
func transcriptStamp(s core.Session) (int64, time.Time) {
	if s.TranscriptPath != "" {
		if fi, err := os.Stat(s.TranscriptPath); err == nil {
			return fi.Size(), fi.ModTime()
		}
	}
	// DB-backed tools have no file; the update time is the best available
	// change signal.
	return s.Bytes, s.Updated
}

// cmdAssay reports what a scope is made of and what could be reclaimed.
func cmdAssay(args []string) error {
	fs := flag.NewFlagSet("assay", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	live := fs.Bool("live", false, "classify now instead of reading stored manifests")
	top := fs.Int("top", 10, "how many record kinds to show")
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

	if *live || sc.IDPrefix != "" {
		return assayLive(*sc, *asJSON, *top)
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	toolFilter := ""
	if len(sc.Tools) == 1 {
		toolFilter = string(sc.Tools[0])
	}
	t, err := db.Aggregate(toolFilter)
	if err != nil {
		return err
	}
	if t.Assayed == 0 {
		return fmt.Errorf("nothing assayed yet — run `midden scan --assay` first")
	}

	if *asJSON {
		return emitJSON(t)
	}
	printTotals(t)
	return nil
}

// assayLive classifies without touching the index, for a single session or an
// ad-hoc scope.
func assayLive(sc core.Scope, asJSON bool, top int) error {
	sessions, errs := adapter.Collect(sc)
	reportErrs(errs)
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions match")
	}

	agg := assay.NewManifest("(scope)", "mixed")
	var manifests []*assay.Manifest

	for _, s := range sessions {
		a, ok := adapter.Find(s.Tool).(adapter.Assayer)
		if !ok {
			continue
		}
		m, err := a.Assay(s, 40)
		if err != nil {
			fmt.Fprintln(os.Stderr, render.Dim("warning: "+err.Error()))
			continue
		}
		manifests = append(manifests, m)

		agg.TotalRecords += m.TotalRecords
		agg.TotalBytes += m.TotalBytes
		for k, v := range m.Counts {
			agg.Counts[k] += v
		}
		for k, v := range m.Bytes {
			agg.Bytes[k] += v
		}
		for k, v := range m.ByKind {
			agg.ByKind[k] += v
		}
		agg.DuplicateReads += m.DuplicateReads
		agg.DuplicateBytes += m.DuplicateBytes
		agg.ImageCount += m.ImageCount
		agg.ImageClusters += m.ImageClusters
		agg.Candidates = append(agg.Candidates, m.Candidates...)
	}

	if asJSON {
		return emitJSON(agg)
	}

	if len(manifests) == 1 {
		m := manifests[0]
		fmt.Printf("\n  %s  %s\n", render.Bold(core.Truncate(m.Title, 60)), render.Dim(m.SessionID))
	} else {
		fmt.Printf("\n  %s across %d sessions\n", render.Bold("ASSAY"), len(manifests))
	}
	printManifest(agg, top)
	return nil
}

func printManifest(m *assay.Manifest, top int) {
	fmt.Printf("  %s\n\n", render.Rule(64))

	classes := []struct {
		name string
		desc string
	}{
		{"signal", "intent, decisions, reasoning"},
		{"exhaust", "tool payloads, file dumps"},
		{"artifact", "screenshots, diffs, files"},
		{"bookkeeping", "protocol overhead"},
	}
	for _, c := range classes {
		b := m.Bytes[c.name]
		if b == 0 && m.Counts[c.name] == 0 {
			continue
		}
		pct := 0.0
		if m.TotalBytes > 0 {
			pct = 100 * float64(b) / float64(m.TotalBytes)
		}
		fmt.Printf("  %-13s %10s  %5.1f%%  %8d records  %s\n",
			c.name, render.Bytes(b), pct, m.Counts[c.name], render.Dim(c.desc))
	}

	fmt.Printf("\n  %-13s %10s\n", "total", render.Bytes(m.TotalBytes))
	fmt.Printf("  %-13s %10s  %s\n", "reclaimable", render.Bytes(m.ReclaimableBytes()),
		render.Dim("removable without losing meaning"))
	fmt.Printf("  %-13s %9.1fx  %s\n", "compression", m.Compression(),
		render.Dim(fmt.Sprintf("signal is %.1f%% of bytes", 100*m.SignalShare())))

	// The slice is what RECLAIM would actually send. Signal at record level
	// still includes megabytes of assistant output; the slice is bounded
	// previews of the most relevant records, and it is the number that
	// decides whether salvage is affordable.
	if sl := m.EstSliceTokens(); sl > 0 {
		fmt.Printf("  %-13s %8s  %s\n", "salvage slice",
			fmt.Sprintf("~%d tok", sl),
			render.Dim(fmt.Sprintf("%d candidates, %.0f:1 vs source — this is what a model sees",
				len(m.Candidates), m.SliceCompression())))
	}

	if m.DuplicateReads > 0 {
		fmt.Printf("\n  %s %d repeated tool payloads, %s\n",
			render.Dim("duplicates:"), m.DuplicateReads, render.Bytes(m.DuplicateBytes))
	}
	if m.ImageCount > 0 {
		fmt.Printf("  %s %d images in %d time clusters %s\n",
			render.Dim("images:    "), m.ImageCount, m.ImageClusters,
			render.Dim("(one representative per cluster is enough for vision)"))
	}

	if top > 0 && len(m.ByKind) > 0 {
		type kv struct {
			k string
			v int64
		}
		var kinds []kv
		for k, v := range m.ByKind {
			kinds = append(kinds, kv{k, v})
		}
		sort.Slice(kinds, func(i, j int) bool { return kinds[i].v > kinds[j].v })

		fmt.Printf("\n  %s\n", render.Dim("largest record kinds"))
		for i, k := range kinds {
			if i >= top {
				break
			}
			pct := 0.0
			if m.TotalBytes > 0 {
				pct = 100 * float64(k.v) / float64(m.TotalBytes)
			}
			fmt.Printf("    %-26s %10s  %5.1f%%  %s\n",
				core.Truncate(k.k, 24), render.Bytes(k.v), pct,
				render.Dim(assay.Classify(k.k).String()))
		}
	}
	fmt.Println()
}

func printTotals(t index.Totals) {
	fmt.Printf("\n  %s  %d sessions, %d assayed\n", render.Bold("ASSAY"), t.Sessions, t.Assayed)
	fmt.Printf("  %s\n\n", render.Rule(64))

	row := func(name string, b int64, desc string) {
		pct := 0.0
		if t.Bytes > 0 {
			pct = 100 * float64(b) / float64(t.Bytes)
		}
		fmt.Printf("  %-13s %10s  %5.1f%%  %s\n", name, render.Bytes(b), pct, render.Dim(desc))
	}
	row("signal", t.Signal, "intent, decisions, reasoning")
	row("exhaust", t.Exhaust, "tool payloads, file dumps")
	row("artifact", t.Artifact, "screenshots, diffs, files")
	row("bookkeeping", t.Book, "protocol overhead")

	fmt.Printf("\n  %-13s %10s\n", "total", render.Bytes(t.Bytes))
	fmt.Printf("  %-13s %10s  %s\n", "reclaimable", render.Bytes(t.Reclaimable()),
		render.Dim("removable without losing meaning"))
	if c := t.Compression(); c > 0 {
		fmt.Printf("  %-13s %9.1fx\n", "compression", c)
	}
	if t.DupBytes > 0 {
		fmt.Printf("  %-13s %10s  %s\n", "duplicates", render.Bytes(t.DupBytes),
			render.Dim("repeated tool payloads"))
	}
	if t.Images > 0 {
		fmt.Printf("  %-13s %10d  %s\n", "images", t.Images,
			render.Dim(fmt.Sprintf("in %d clusters", t.Clusters)))
	}
	fmt.Printf("\n  %s\n\n", render.Dim("midden prune --dry-run   # preview what disposal would recover"))
}

var _ = strings.TrimSpace

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
