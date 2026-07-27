// Package oracle answers questions about your own AI CLI history.
//
// It is deliberately not a chatbot over 40 GiB of transcripts. That would be
// unaffordable and unnecessary. Everything expensive has already been reduced:
// the index knows the shape of every session, ASSAY knows what they are made
// of, RECLAIM has extracted the reusable knowledge, and ADVISE has computed
// the findings.
//
// The oracle assembles that compressed picture — a few thousand tokens for a
// corpus of hundreds of sessions — and reasons over it. The cost of a question
// is therefore roughly constant regardless of how much history you have.
package oracle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/advise"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
)

// Brief is the compressed picture of a machine, assembled deterministically.
type Brief struct {
	State     guide.State
	Findings  []advise.Finding
	Nuggets   []index.Nugget
	Costs     index.CostTotals
	TopDirs   []DirStat
	Redaction []redact.Finding
}

// DirStat is per-workspace activity, which is usually what a question is
// really about.
type DirStat struct {
	Dir      string
	Sessions int
	Bytes    int64
}

// MaxNuggets bounds how much reclaimed knowledge is included.
//
// Nuggets are the densest evidence available — each is a few sentences that
// cost real money to extract — so a generous cap here is still cheap.
const MaxNuggets = 60

// Budget is the ceiling on assembled context, in estimated tokens. A question
// should cost about the same whether you have 50 sessions or 5,000.
const Budget = 6000

// Prompt renders the question with its evidence.
//
// Stable instructions first so they cache; the variable brief follows.
func (b Brief) Prompt(question string) string {
	var s strings.Builder

	s.WriteString(`You are answering a question about someone's own AI coding history, using
only the evidence below. You are talking to the person whose history this is.

Rules:
- Answer the question directly in the first sentence. No preamble.
- Ground every claim in the evidence. Cite session ids or numbers where you
  have them.
- If the evidence cannot answer the question, say exactly what is missing and
  which command would produce it. Do not speculate to fill the gap.
- Be specific and brief. This person knows their own work; skip the exposition.
- Where you recommend an action, give the exact command.
- Keep placeholders like <API_KEY - ask operator> exactly as they appear.
- Never invent session ids, file paths, numbers or commands.

Commands you may recommend, and whether they cost anything:
`)
	for _, c := range guide.Commands {
		tag := "free  "
		if c.Cost == guide.Spends {
			tag = "SPENDS"
		}
		fmt.Fprintf(&s, "  %s midden %-9s %s\n", tag, c.Name, c.Blurb)
	}

	s.WriteString("\n--- EVIDENCE ---\n\n")
	s.WriteString(b.Evidence())

	fmt.Fprintf(&s, "\n--- QUESTION ---\n\n%s\n", question)
	return s.String()
}

// Evidence renders the compressed picture.
func (b Brief) Evidence() string {
	var s strings.Builder
	st := b.State

	s.WriteString("## Machine state\n")
	fmt.Fprintf(&s, "- %d sessions, %s on disk\n", st.Sessions, human(st.FootprintByte))
	fmt.Fprintf(&s, "- %d assayed; %s classified as removable without losing meaning\n",
		st.Assayed, human(st.ReclaimBytes))
	fmt.Fprintf(&s, "- %d at risk of failing to resume (%d already past the cliff)\n",
		st.AtRisk, st.CriticalRisk)
	fmt.Fprintf(&s, "- %d open right now, %d pointing at deleted workspaces\n",
		st.LiveSessions, st.DeadDirs)
	fmt.Fprintf(&s, "- %d nuggets reclaimed, %d artifacts written\n", st.Nuggets, st.Artifacts)
	if st.BusiestWorkspace != "" {
		fmt.Fprintf(&s, "- most active workspace recently: %s\n", st.BusiestWorkspace)
	}

	if len(b.TopDirs) > 0 {
		s.WriteString("\n## Busiest workspaces\n")
		for _, d := range b.TopDirs {
			fmt.Fprintf(&s, "- %s — %d session(s), %s\n", d.Dir, d.Sessions, human(d.Bytes))
		}
	}

	if b.Costs.Runs > 0 {
		s.WriteString("\n## Spending so far\n")
		ops := make([]string, 0, len(b.Costs.ByOp))
		for k := range b.Costs.ByOp {
			ops = append(ops, k)
		}
		sort.Strings(ops)
		for _, op := range ops {
			o := b.Costs.ByOp[op]
			unit := ""
			if o.AIU > 0 && o.Items > 0 {
				unit = fmt.Sprintf(", ~%.0f AIU per item", o.PerItem())
			}
			fmt.Fprintf(&s, "- %s: %d run(s), %d item(s)%s\n", op, o.Runs, o.Items, unit)
		}
	}

	if len(b.Findings) > 0 {
		s.WriteString("\n## Findings from analysis\n")
		for i, f := range b.Findings {
			if i >= 12 {
				break
			}
			fmt.Fprintf(&s, "- [%s/%s] %s — %s\n", f.Severity, f.Category, f.Title, f.Evidence)
		}
	}

	if len(b.Nuggets) > 0 {
		s.WriteString("\n## Reclaimed knowledge\n")
		byKind := map[string][]index.Nugget{}
		for _, n := range b.Nuggets {
			byKind[n.Kind] = append(byKind[n.Kind], n)
		}
		for _, kind := range index.NuggetKinds {
			items := byKind[kind]
			if len(items) == 0 {
				continue
			}
			fmt.Fprintf(&s, "\n### %s\n", kind)
			for _, n := range items {
				fmt.Fprintf(&s, "- **%s** (%s, %s) %s\n", n.Title, shortID(n.SessionID),
					baseDir(n.Workspace), oneLine(n.Body, 320))
			}
		}
	}

	return s.String()
}

// EstTokens is the assembled cost before a model is called.
func (b Brief) EstTokens(question string) int {
	return len(b.Prompt(question))/4 + 1
}

// Trim reduces the brief until it fits the budget, dropping the least dense
// evidence first. Nuggets are the last thing to go: they are the only part
// that cost money to produce.
func (b *Brief) Trim(question string) {
	for b.EstTokens(question) > Budget && len(b.Findings) > 4 {
		b.Findings = b.Findings[:len(b.Findings)-1]
	}
	for b.EstTokens(question) > Budget && len(b.TopDirs) > 3 {
		b.TopDirs = b.TopDirs[:len(b.TopDirs)-1]
	}
	for b.EstTokens(question) > Budget && len(b.Nuggets) > 8 {
		b.Nuggets = b.Nuggets[:len(b.Nuggets)-1]
	}
}

// RedactAll scrubs the brief before it can reach a model.
func (b *Brief) RedactAll() {
	for i := range b.Nuggets {
		rb := redact.Text(b.Nuggets[i].Body)
		rt := redact.Text(b.Nuggets[i].Title)
		b.Nuggets[i].Body = rb.Text
		b.Nuggets[i].Title = rt.Text
		b.Redaction = append(b.Redaction, rb.Findings...)
		b.Redaction = append(b.Redaction, rt.Findings...)
	}
	for i := range b.Findings {
		r := redact.Text(b.Findings[i].Evidence)
		b.Findings[i].Evidence = r.Text
		b.Redaction = append(b.Redaction, r.Findings...)
	}
}

// Suggestions are starter questions, so the operator is not staring at an
// empty prompt wondering what this can answer.
func Suggestions(st guide.State) []string {
	var out []string

	if st.CriticalRisk > 0 {
		out = append(out, "Which sessions am I about to lose, and what were they working on?")
	}
	if st.ReclaimBytes > 1<<30 {
		out = append(out, "What is taking up the most space, and is any of it worth keeping?")
	}
	if st.Nuggets > 0 {
		out = append(out, "What have I learned recently that I keep forgetting?")
		out = append(out, "What should I write up first from what I already know?")
	}
	out = append(out,
		"What am I doing that wastes the most tokens?",
		"Which workspace is costing me the most effort?",
		"What is the cheapest thing I could do right now that would help?",
	)
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit && exp < 4; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func baseDir(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
