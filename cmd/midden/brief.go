package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	handoffpkg "github.com/mekjr1/midden/internal/handoff"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

// cmdBrief produces a handoff brief: enough context to continue the work in a
// fresh session, extracted deterministically at zero token cost.
//
// This is the deterministic floor of RECLAIM, and the thing that makes the
// resume cliff survivable — you cannot resume a 774 MiB session, but you can
// carry its intent forward.
func cmdBrief(args []string) error {
	fs := flag.NewFlagSet("brief", flag.ExitOnError)
	turns := fs.Int("turns", 10, "how many recent turns to include")
	clipAt := fs.Int("clip", 1200, "max characters per turn")
	asJSON := fs.Bool("json", false, "machine-readable output")
	handoff := fs.Bool("handoff", false, "format as a paste-able prompt for a fresh session")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: midden brief <id-or-prefix> [--turns n] [--handoff]")
	}

	s, err := findOne(fs.Arg(0))
	if err != nil {
		return err
	}

	a := adapter.Find(s.Tool)
	h, ok := a.(core.Harvester)
	if !ok {
		return fmt.Errorf("%s sessions cannot be harvested yet", s.Tool)
	}

	if s.Bytes > 100<<20 {
		fmt.Fprintf(os.Stderr, "%s reading %s transcript, this may take a moment...\n",
			render.Dim("note:"), render.Bytes(s.Bytes))
	}

	start := time.Now()
	hv, err := h.Harvest(s, *turns)
	if err != nil {
		return fmt.Errorf("harvest: %w", err)
	}

	if *asJSON {
		return emitJSON(struct {
			Session core.Session `json:"session"`
			core.Harvest
		}{s, hv})
	}

	if *handoff {
		text := handoffpkg.Text(s, hv, *clipAt)
		fmt.Println(text)
		// Keep it, so a rescue survives closing the terminal. Saving is
		// best-effort: the brief is already on stdout either way.
		if path, err := saveHandoff(s, text); err == nil {
			fmt.Fprintf(os.Stderr, "  %s\n", render.Dim("saved "+path))
		}
		return nil
	}

	fmt.Printf("\n  %s  %s\n", render.Bold(core.Truncate(s.Title, 66)), render.ToolColour(s.Tool))
	fmt.Printf("  %s\n\n", render.Dim(fmt.Sprintf(
		"%s · %d user turns · %d records scanned · harvested in %s",
		s.ID, hv.UserTurns, hv.TotalRecords, time.Since(start).Round(time.Millisecond))))

	if hv.Goal != nil {
		fmt.Printf("  %s\n", render.Bold("ORIGINAL GOAL"))
		fmt.Printf("%s\n\n", indent(clip(hv.Goal.Text, *clipAt), "    "))
	}

	if len(hv.Recent) > 0 {
		label := fmt.Sprintf("RECENT (%d turns)", len(hv.Recent))
		if hv.Truncated {
			label += " — earlier turns omitted"
		}
		fmt.Printf("  %s\n", render.Bold(label))
		for _, t := range hv.Recent {
			who := render.Dim("  assistant")
			if t.Role == "user" {
				who = render.Bold("  user")
			}
			fmt.Printf("\n  %s %s\n", who, render.Dim(t.Time.Format("01-02 15:04")))
			fmt.Println(indent(clip(t.Text, *clipAt), "    "))
		}
	}

	fmt.Printf("\n  %s\n", render.Dim("midden brief "+shortID(s.ID)+" --handoff   # paste-able for a fresh session"))
	return nil
}

// printHandoff moved to internal/handoff so the CLI and the web UI cannot
// drift: a rescue brief that differs by surface is one you cannot trust.

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func clip(s string, n int) string {
	return handoffpkg.Clip(s, n)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// saveHandoff writes a rescued brief alongside the other artifacts and
// records it in the index.
//
// A rescue that exists only in a terminal scrollback is one you have to redo.
// This is the same path and the same artifact kind the web UI writes, so a
// brief taken from either surface shows up in `midden artifacts`.
func saveHandoff(s core.Session, text string) (string, error) {
	dir := filepath.Join(index.Dir(), "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, fmt.Sprintf("handoff-%s-%s.md", s.Tool, shortID(s.ID)))
	header := fmt.Sprintf("<!-- midden handoff · %s %s · %s · rescued %s -->\n\n",
		s.Tool, shortID(s.ID), s.Dir, time.Now().Format("2006-01-02 15:04"))
	if err := os.WriteFile(path, []byte(header+text), 0o644); err != nil {
		return "", err
	}

	if db, err := index.Open(); err == nil {
		defer db.Close()
		db.PutArtifact(index.Artifact{
			Kind:  "handoff",
			Title: core.Truncate(s.Title, 80),
			Path:  path,
			Scope: s.Dir,
		})
	}
	return path, nil
}
