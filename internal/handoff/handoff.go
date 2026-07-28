// Package handoff builds the text that carries a session's intent into a new
// one.
//
// This is the deterministic floor of the product: no model call, no cost, and
// the only remedy for a transcript past the resume cliff. It lives in its own
// package because both the CLI and the web UI must produce byte-identical
// output — a rescue that differs by surface is a rescue you cannot trust.
package handoff

import (
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/render"
)

// DefaultClip is the per-turn character budget. Long enough to carry intent,
// short enough that the result stays paste-able.
const DefaultClip = 1200

// Text renders a paste-able prompt for a fresh session.
func Text(s core.Session, hv core.Harvest, clipAt int) string {
	if clipAt <= 0 {
		clipAt = DefaultClip
	}

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
		b.WriteString(Clip(hv.Goal.Text, clipAt))
		b.WriteString("\n\n")
	}

	if len(hv.Recent) > 0 {
		b.WriteString("MOST RECENT EXCHANGES (oldest first)\n")
		for _, t := range hv.Recent {
			fmt.Fprintf(&b, "\n[%s] %s\n", strings.ToUpper(t.Role), Clip(t.Text, clipAt))
		}
		b.WriteString("\n")
	}

	// The prior session may have died mid-action, so its last claims are the
	// least trustworthy part of the brief.
	b.WriteString("\nBefore doing anything, confirm the current state of the workspace against " +
		"the claims above — the prior session's last actions may not have completed.\n")

	return b.String()
}

// Clip truncates to a rune budget, marking where content was dropped so the
// reader knows the record is partial.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n[... truncated]"
}
