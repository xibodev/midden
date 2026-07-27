// Package refine turns nuggets into publishable artifacts.
//
// It never touches raw events. Everything here operates on nuggets, which are
// already extracted, bounded, redacted and provenanced.
//
// The workflow is catalog-then-generate, for a measured reason: cache writes
// were ~55% of cost in a real session and roughly 3x cache reads. Loading the
// evidence is expensive; re-reading it is nearly free. So the evidence is
// loaded ONCE and every artifact is generated from that same warm context.
// Generating a dozen artifacts one invocation at a time would cost close to a
// dozen times more.
package refine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/index"
)

// Template is a kind of artifact that can be produced.
type Template struct {
	Name     string
	Title    string
	Audience string
	Shape    string
	// Wants are the nugget kinds this template draws on.
	Wants []string
}

// Templates are the artifact kinds Midden can produce. Each states its shape
// explicitly, because "write a tutorial" produces mush without one.
var Templates = []Template{
	{
		Name: "tutorial", Title: "Step-by-step tutorial",
		Audience: "someone who has never done this before",
		Shape:    "Prerequisites, then numbered steps each with the exact command and what to expect, then verification, then common failures.",
		Wants:    []string{"command", "decision", "error_fix", "gotcha"},
	},
	{
		Name: "howto", Title: "Focused how-to",
		Audience: "a practitioner who knows the domain and wants one task done",
		Shape:    "One goal stated up front, the minimal path to it, and nothing else. No background.",
		Wants:    []string{"command", "gotcha"},
	},
	{
		Name: "faq", Title: "Frequently asked questions",
		Audience: "someone hitting problems",
		Shape:    "Question as a heading, answer in 2-4 sentences, most common first.",
		Wants:    []string{"gotcha", "error_fix", "decision"},
	},
	{
		Name: "tsg", Title: "Troubleshooting guide",
		Audience: "someone whose system is broken right now",
		Shape:    "Symptom, then how to confirm the diagnosis, then the fix, then how to prevent recurrence. Symptom-first, not cause-first.",
		Wants:    []string{"error_fix", "gotcha", "dead_end"},
	},
	{
		Name: "adr", Title: "Architecture decision record",
		Audience: "an engineer joining the project later",
		Shape:    "Context, Decision, Alternatives considered and why rejected, Consequences. One decision per record.",
		Wants:    []string{"decision", "dead_end"},
	},
	{
		Name: "changelog", Title: "Changelog entry",
		Audience: "users of the software",
		Shape:    "Grouped under Added / Changed / Fixed. One line each, user-visible effect first.",
		Wants:    []string{"decision", "error_fix"},
	},
	{
		Name: "post", Title: "Blog post",
		Audience: "a technical reader who does not know the project",
		Shape:    "A concrete hook, the problem, what was tried, what actually worked, and what transfers. Honest about the failures.",
		Wants:    []string{"decision", "dead_end", "error_fix", "gotcha"},
	},
	{
		Name: "readme", Title: "README section",
		Audience: "someone evaluating the project",
		Shape:    "What it does, why it exists, how to run it. Short.",
		Wants:    []string{"decision", "command"},
	},
	{
		Name: "lessons", Title: "Lessons learned",
		Audience: "the team, retrospectively",
		Shape:    "Each lesson as a claim with the evidence that produced it. No platitudes.",
		Wants:    []string{"dead_end", "gotcha", "decision", "error_fix"},
	},
}

// FindTemplate resolves a template by name.
func FindTemplate(name string) (Template, bool) {
	for _, t := range Templates {
		if t.Name == strings.ToLower(name) {
			return t, true
		}
	}
	return Template{}, false
}

// TemplateNames lists available artifact kinds.
func TemplateNames() []string {
	out := make([]string, len(Templates))
	for i, t := range Templates {
		out[i] = t.Name
	}
	return out
}

// Evidence is the nugget set an artifact draws on.
type Evidence struct {
	Scope   string
	Nuggets []index.Nugget
}

// EstTokens is the cost of loading this evidence once.
func (e Evidence) EstTokens() int {
	n := 0
	for _, g := range e.Nuggets {
		n += len(g.Title) + len(g.Body)
	}
	return n/4 + 400
}

// Preamble is the stable prefix loaded once per conversation.
//
// It carries the evidence and the rules. Everything after this is a short
// per-artifact instruction that reuses this as cached context.
func (e Evidence) Preamble() string {
	var b strings.Builder

	b.WriteString(`You are writing documentation from evidence mined out of real engineering
sessions. The evidence below is the ONLY source you may use.

Rules that apply to everything you will be asked to write:
- Use only what the evidence supports. Never invent commands, versions,
  file paths, or results.
- Where the evidence is thin, say so plainly rather than padding.
- Keep placeholders like <API_KEY — ask operator> exactly as they appear.
- Prefer concrete specifics over general advice.
- Write in Markdown. No preamble, no meta-commentary about the task.

Acknowledge with the single word READY and wait for the writing request.

`)

	fmt.Fprintf(&b, "SCOPE: %s\nEVIDENCE: %d items\n\n", e.Scope, len(e.Nuggets))

	byKind := map[string][]index.Nugget{}
	for _, g := range e.Nuggets {
		byKind[g.Kind] = append(byKind[g.Kind], g)
	}
	for _, kind := range index.NuggetKinds {
		items := byKind[kind]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n", strings.ToUpper(kind))
		for _, g := range items {
			fmt.Fprintf(&b, "\n- **%s** (confidence %.0f%%, source %s)\n  %s\n",
				g.Title, g.Confidence*100, g.SessionID[:min(8, len(g.SessionID))],
				strings.Join(strings.Fields(g.Body), " "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Request is the short per-artifact instruction sent after the preamble.
func (t Template) Request(topic string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Write a %s.\n\n", t.Title)
	fmt.Fprintf(&b, "Audience: %s\n", t.Audience)
	fmt.Fprintf(&b, "Shape: %s\n", t.Shape)
	if topic != "" {
		fmt.Fprintf(&b, "Focus: %s\n", topic)
	}
	b.WriteString("\nDraw only on the evidence already provided. " +
		"Output the Markdown document and nothing else.\n")
	return b.String()
}

// CatalogItem is one proposed artifact.
type CatalogItem struct {
	Template string   `json:"template"`
	Title    string   `json:"title"`
	Why      string   `json:"why"`
	Nuggets  int      `json:"nuggets"`
	Kinds    []string `json:"kinds"`
}

// Catalog proposes what could be written from the available evidence.
//
// Deterministic: a template is proposed when the evidence contains enough of
// the nugget kinds it draws on. Proposing the full set up front is what makes
// one-warm-context generation possible.
func Catalog(ns []index.Nugget, minNuggets int) []CatalogItem {
	if minNuggets <= 0 {
		minNuggets = 2
	}
	counts := map[string]int{}
	for _, n := range ns {
		counts[n.Kind]++
	}

	var out []CatalogItem
	for _, t := range Templates {
		total := 0
		var kinds []string
		for _, want := range t.Wants {
			if counts[want] > 0 {
				total += counts[want]
				kinds = append(kinds, want)
			}
		}
		if total < minNuggets {
			continue
		}
		out = append(out, CatalogItem{
			Template: t.Name,
			Title:    t.Title,
			Why:      fmt.Sprintf("%d supporting nugget(s) across %s", total, strings.Join(kinds, ", ")),
			Nuggets:  total,
			Kinds:    kinds,
		})
	}
	return out
}

// Slug turns a title into a filename-safe stem.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "artifact"
	}
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
}

// CleanOutput strips code fences a model may have wrapped the document in.
func CleanOutput(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// NuggetIDs collects provenance ids for an artifact record.
func NuggetIDs(ns []index.Nugget) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.UID)
	}
	return out
}

// MarshalCatalog renders a catalog as JSON.
func MarshalCatalog(items []CatalogItem) string {
	b, _ := json.MarshalIndent(items, "", "  ")
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
