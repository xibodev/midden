package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
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
		printHandoff(s, hv, *clipAt)
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

// printHandoff emits a block designed to be pasted into a NEW session, which
// is the recommended action for a transcript past the resume cliff.
func printHandoff(s core.Session, hv core.Harvest, clipAt int) {
	var b strings.Builder

	b.WriteString("You are picking up work from a previous session that cannot be resumed")
	if s.Bytes > 0 {
		fmt.Fprintf(&b, " (its transcript reached %s, past the point where --resume loads)", render.Bytes(s.Bytes))
	}
	b.WriteString(".\n\n")

	fmt.Fprintf(&b, "WORKSPACE: %s\n", s.Dir)
	if s.Repo != "" {
		fmt.Fprintf(&b, "REPO: %s\n", s.Repo)
	}
	fmt.Fprintf(&b, "PRIOR SESSION: %s (%s), %d user turns between %s and %s\n\n",
		s.ID, s.Tool, hv.UserTurns,
		s.Created.Format("2006-01-02"), s.Updated.Format("2006-01-02"))

	if hv.Goal != nil {
		b.WriteString("ORIGINAL GOAL\n")
		b.WriteString(clip(hv.Goal.Text, clipAt))
		b.WriteString("\n\n")
	}

	if len(hv.Recent) > 0 {
		b.WriteString("MOST RECENT EXCHANGES (oldest first)\n")
		for _, t := range hv.Recent {
			fmt.Fprintf(&b, "\n[%s] %s\n", strings.ToUpper(t.Role), clip(t.Text, clipAt))
		}
		b.WriteString("\n")
	}

	b.WriteString("\nBefore doing anything, confirm the current state of the workspace against " +
		"the claims above — the prior session's last actions may not have completed.\n")

	fmt.Println(b.String())
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n[... truncated]"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
