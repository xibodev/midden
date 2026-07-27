// Package summary produces session summaries at three depths.
//
// The depths exist because understanding has three different prices, and
// collapsing them into one "summarise" button hides that from the operator:
//
//	shallow  free         what was asked, what happened last
//	deep     one call     what was decided and why, synthesised across turns
//	xray     one call+    the session read against the workspace it changed
//
// Shallow is deterministic extraction and always runs first — it is often
// enough, and running it first means the paid tiers start from evidence
// rather than from nothing.
package summary

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/redact"
)

// Depth is how hard to look.
type Depth string

const (
	Shallow Depth = "shallow"
	Deep    Depth = "deep"
	XRay    Depth = "xray"
)

// Depths in order, for menus and validation.
var Depths = []Depth{Shallow, Deep, XRay}

// ParseDepth validates a depth name.
func ParseDepth(s string) (Depth, error) {
	switch Depth(strings.ToLower(strings.TrimSpace(s))) {
	case Shallow, "":
		return Shallow, nil
	case Deep:
		return Deep, nil
	case XRay, "x-ray":
		return XRay, nil
	}
	return "", fmt.Errorf("unknown depth %q (want shallow, deep or xray)", s)
}

// Spends reports whether a depth calls a model.
func (d Depth) Spends() bool { return d == Deep || d == XRay }

// Describe explains what a depth buys, so the choice is informed.
func (d Depth) Describe() string {
	switch d {
	case Deep:
		return "synthesised across the whole session: decisions, what was tried, where it ended"
	case XRay:
		return "the session read against the workspace it changed — claims checked against git"
	default:
		return "the original ask and the last exchanges, extracted directly"
	}
}

// Context is everything gathered for a summary, before any model sees it.
type Context struct {
	Session   core.Session
	Depth     Depth
	Harvest   core.Harvest
	Manifest  *assay.Manifest
	Workspace *WorkspaceState
	Findings  []redact.Finding
}

// WorkspaceState is the deterministic ground truth an x-ray checks against.
//
// A dying session's final claims routinely describe work that never completed.
// Reading the repository is how those claims get checked rather than repeated.
type WorkspaceState struct {
	Dir           string    `json:"dir"`
	IsGit         bool      `json:"is_git"`
	Branch        string    `json:"branch,omitempty"`
	Dirty         bool      `json:"dirty"`
	DirtyFiles    int       `json:"dirty_files"`
	RecentCommits []string  `json:"recent_commits,omitempty"`
	LastCommitAt  time.Time `json:"last_commit_at,omitempty"`
	TopLevel      []string  `json:"top_level,omitempty"`
	Unpushed      int       `json:"unpushed"`
}

// InspectWorkspace reads repository state. Read-only: nothing here mutates a
// working tree.
func InspectWorkspace(dir string) *WorkspaceState {
	if dir == "" {
		return nil
	}
	ws := &WorkspaceState{Dir: dir}

	if out, err := git(dir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		ws.TopLevel = topLevel(dir)
		return ws
	}
	ws.IsGit = true

	if out, err := git(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		ws.Branch = strings.TrimSpace(out)
	}
	if out, err := git(dir, "status", "--porcelain"); err == nil {
		lines := nonEmptyLines(out)
		ws.DirtyFiles = len(lines)
		ws.Dirty = len(lines) > 0
	}
	if out, err := git(dir, "log", "-12", "--pretty=format:%h %ad %s", "--date=short"); err == nil {
		ws.RecentCommits = nonEmptyLines(out)
	}
	if out, err := git(dir, "log", "-1", "--pretty=format:%at"); err == nil {
		var secs int64
		fmt.Sscanf(strings.TrimSpace(out), "%d", &secs)
		if secs > 0 {
			ws.LastCommitAt = time.Unix(secs, 0)
		}
	}
	if out, err := git(dir, "log", "--oneline", "@{upstream}..HEAD"); err == nil {
		ws.Unpushed = len(nonEmptyLines(out))
	}
	ws.TopLevel = topLevel(dir)
	return ws
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "--no-pager"}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func topLevel(dir string) []string {
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := filepath.Base(e)
		if strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, name)
		if len(out) >= 24 {
			break
		}
	}
	return out
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

// RawTokens estimates the prompt weight before calibration.
func (c Context) RawTokens() int { return len(c.Prompt())/4 + 1 }

// Prompt renders the request for the chosen depth.
//
// Instructions come first so they form a reusable cache prefix; the variable
// evidence follows.
func (c Context) Prompt() string {
	var b strings.Builder

	switch c.Depth {
	case XRay:
		b.WriteString(`You are examining a finished AI coding session against the workspace it
changed. Your job is to establish what actually happened, not to repeat what
the session claimed.

Produce, in Markdown:

## What this session was for
## What was actually decided
## What the evidence shows was completed
## What was claimed but is NOT confirmed by the workspace
## Where it left off, and the obvious next step
## Risks or loose ends

Rules:
- Repository state is ground truth. Where the transcript and the repository
  disagree, say so plainly and trust the repository.
- A dying session's final messages routinely describe work that never
  completed. Check claims; do not echo them.
- If the evidence does not support a section, write "not established by the evidence" rather than inventing content.
- Keep placeholders like <API_KEY - ask operator> exactly as they appear.

`)
	case Deep:
		b.WriteString(`You are summarising a finished AI coding session for someone who needs to
pick the work up without reading it.

Produce, in Markdown:

## What this was for
## What was decided, and what was rejected
## What was tried that did not work
## Where it ended
## What to do next

Rules:
- Use only the evidence below. Do not invent commands, results or file paths.
- Prefer specifics over summary-of-a-summary. Name the actual decision.
- If a section has no support in the evidence, say so in one line.
- Keep placeholders like <API_KEY - ask operator> exactly as they appear.

`)
	}

	s := c.Session
	fmt.Fprintf(&b, "SESSION: %s\nTOOL: %s\nWORKSPACE: %s\n", s.Title, s.Tool, s.Dir)
	if s.Repo != "" {
		fmt.Fprintf(&b, "REPO: %s\n", s.Repo)
	}
	fmt.Fprintf(&b, "ACTIVE: %s to %s\n", s.Created.Format("2006-01-02"), s.Updated.Format("2006-01-02"))
	if c.Manifest != nil {
		fmt.Fprintf(&b, "SIZE: %d records, %.0f%% of bytes are actual conversation\n",
			c.Manifest.TotalRecords, 100*c.Manifest.SignalShare())
	}

	if c.Harvest.Goal != nil {
		fmt.Fprintf(&b, "\nORIGINAL ASK:\n%s\n", clip(c.Harvest.Goal.Text, 1600))
	}

	if len(c.Harvest.Recent) > 0 {
		b.WriteString("\nMOST RECENT EXCHANGES (oldest first):\n")
		for _, t := range c.Harvest.Recent {
			fmt.Fprintf(&b, "\n[%s] %s\n", strings.ToUpper(t.Role), clip(t.Text, 900))
		}
	}

	if c.Manifest != nil && len(c.Manifest.Candidates) > 0 {
		b.WriteString("\nEVIDENCE SAMPLED ACROSS THE SESSION:\n")
		for i, r := range c.Manifest.Candidates {
			if i >= 60 {
				break
			}
			role := r.Role
			if role == "" {
				role = r.Kind
			}
			fmt.Fprintf(&b, "- [%s] %s\n", role, r.Preview)
		}
	}

	if c.Depth == XRay && c.Workspace != nil {
		w := c.Workspace
		b.WriteString("\nWORKSPACE STATE (ground truth, read from disk just now):\n")
		if !w.IsGit {
			fmt.Fprintf(&b, "Not a git repository. Top level: %s\n", strings.Join(w.TopLevel, ", "))
		} else {
			fmt.Fprintf(&b, "branch: %s\n", w.Branch)
			state := "clean"
			if w.Dirty {
				state = "dirty"
			}
			fmt.Fprintf(&b, "working tree: %s (%d file(s) changed)\n", state, w.DirtyFiles)
			if w.Unpushed > 0 {
				fmt.Fprintf(&b, "unpushed commits: %d\n", w.Unpushed)
			}
			if !w.LastCommitAt.IsZero() {
				fmt.Fprintf(&b, "last commit: %s\n", w.LastCommitAt.Format("2006-01-02 15:04"))
			}
			if len(w.RecentCommits) > 0 {
				b.WriteString("recent commits:\n")
				for _, cm := range w.RecentCommits {
					fmt.Fprintf(&b, "  %s\n", cm)
				}
			}
			if len(w.TopLevel) > 0 {
				fmt.Fprintf(&b, "top level: %s\n", strings.Join(w.TopLevel, ", "))
			}
		}
	}

	b.WriteString("\nOutput the Markdown document and nothing else.\n")
	return b.String()
}

// Redact scrubs the assembled context before it can reach a model or a file.
func (c *Context) Redact() {
	if c.Harvest.Goal != nil {
		r := redact.Text(c.Harvest.Goal.Text)
		c.Harvest.Goal.Text = r.Text
		c.Findings = append(c.Findings, r.Findings...)
	}
	for i := range c.Harvest.Recent {
		r := redact.Text(c.Harvest.Recent[i].Text)
		c.Harvest.Recent[i].Text = r.Text
		c.Findings = append(c.Findings, r.Findings...)
	}
	if c.Manifest != nil {
		for i := range c.Manifest.Candidates {
			r := redact.Text(c.Manifest.Candidates[i].Preview)
			c.Manifest.Candidates[i].Preview = r.Text
			c.Findings = append(c.Findings, r.Findings...)
		}
	}
}

// RenderShallow produces the free summary with no model involved.
func RenderShallow(c Context) string {
	var b strings.Builder
	s := c.Session

	fmt.Fprintf(&b, "# %s\n\n", s.Title)
	fmt.Fprintf(&b, "- **Tool:** %s\n- **Workspace:** %s\n", s.Tool, s.Dir)
	if s.Repo != "" {
		fmt.Fprintf(&b, "- **Repo:** %s\n", s.Repo)
	}
	fmt.Fprintf(&b, "- **Active:** %s to %s (%.0f days)\n",
		s.Created.Format("2006-01-02"), s.Updated.Format("2006-01-02"), s.SpanDays())
	fmt.Fprintf(&b, "- **User turns:** %d\n", c.Harvest.UserTurns)
	if c.Manifest != nil {
		fmt.Fprintf(&b, "- **Composition:** %.0f%% conversation, %.0f%% tool output\n",
			100*c.Manifest.SignalShare(),
			100*float64(c.Manifest.Bytes["exhaust"])/float64(max64(c.Manifest.TotalBytes, 1)))
	}

	if c.Harvest.Goal != nil {
		fmt.Fprintf(&b, "\n## Original ask\n\n%s\n", clip(c.Harvest.Goal.Text, 1200))
	}
	if c.Harvest.LastAssistant != nil {
		fmt.Fprintf(&b, "\n## Where it left off\n\n%s\n", clip(c.Harvest.LastAssistant.Text, 1200))
	}

	b.WriteString("\n---\n\n*Extracted directly from the transcript. No model was called, " +
		"so nothing here is interpreted — only quoted.*\n")
	return b.String()
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n[... truncated]"
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
