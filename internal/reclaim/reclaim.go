// Package reclaim turns an assay-filtered slice of a session into nuggets:
// small, provenanced units of reusable value.
//
// The LLM never sees raw events. It sees candidates that ASSAY already
// selected and bounded, which is what keeps the cost of mining a 681 MiB
// session in the low thousands of tokens rather than the tens of millions.
package reclaim

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
)

// Slice is the evidence handed to a model: bounded, redacted, provenanced.
type Slice struct {
	Session    core.Session
	Candidates []assay.Record
	Findings   []redact.Finding
}

// BuildSlice assembles evidence from a manifest, redacting as it goes.
//
// Redaction happens here rather than at publication because nuggets are
// written to a store that may be synced or committed. A secret that reaches
// the store has already escaped.
func BuildSlice(s core.Session, m *assay.Manifest, maxRecords int) Slice {
	sl := Slice{Session: s}
	if maxRecords <= 0 {
		maxRecords = 120
	}

	seenFindings := map[string]int{}
	for _, c := range m.Candidates {
		if len(sl.Candidates) >= maxRecords {
			break
		}
		if strings.TrimSpace(c.Preview) == "" {
			continue
		}
		r := redact.Text(c.Preview)
		for _, f := range r.Findings {
			seenFindings[f.Rule] += f.Count
		}
		c.Preview = r.Text
		sl.Candidates = append(sl.Candidates, c)
	}

	for rule, n := range seenFindings {
		sl.Findings = append(sl.Findings, redact.Finding{Rule: rule, Count: n})
	}
	return sl
}

// EstTokens is the predicted input cost of this slice.
func (s Slice) EstTokens() int {
	n := 0
	for _, c := range s.Candidates {
		n += len(c.Preview)
	}
	return n/4 + promptOverheadTokens
}

// promptOverheadTokens covers instructions and schema.
const promptOverheadTokens = 700

// Prompt renders the extraction request.
//
// The stable prefix (instructions, schema, kinds) comes first so it forms a
// reusable cache prefix; the variable evidence follows.
func (s Slice) Prompt() string {
	var b strings.Builder

	b.WriteString(`You are mining a finished AI coding session for reusable knowledge.

Extract only what would be expensive to rediscover and is supported by the
evidence below. Do not invent, infer beyond the text, or pad. Fewer, better
nuggets are strictly preferable to more.

Return ONLY a JSON array, no prose, no code fences. Each element:

{"kind":"...","title":"short imperative title","body":"2-6 sentences, concrete and self-contained","tags":["..."],"confidence":0.0-1.0}

Valid kinds:
  decision   a choice made, and what was rejected
  error_fix  a specific failure and what actually resolved it
  command    an invocation that worked, with the context it needed
  gotcha     surprising behaviour worth remembering
  dead_end   an approach tried and abandoned, and why
  artifact   a produced thing and what it demonstrates

Rules:
- If the evidence does not support a nugget, return [].
- Never include secrets, tokens, account ids or customer data. Placeholders
  like <API_KEY — ask operator> are already redacted; keep them as-is.
- confidence reflects how well the evidence supports the claim.

`)

	fmt.Fprintf(&b, "SESSION: %s\nWORKSPACE: %s\n", s.Session.Title, s.Session.Dir)
	if s.Session.Repo != "" {
		fmt.Fprintf(&b, "REPO: %s\n", s.Session.Repo)
	}
	b.WriteString("\nEVIDENCE (excerpts, oldest first):\n")

	for i, c := range s.Candidates {
		role := c.Role
		if role == "" {
			role = c.Kind
		}
		fmt.Fprintf(&b, "\n[%d|%s] %s\n", i+1, role, c.Preview)
	}
	return b.String()
}

// rawNugget is the model's output shape.
type rawNugget struct {
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags"`
	Confidence float64  `json:"confidence"`
}

// Parse converts a model response into storable nuggets.
//
// Models wrap JSON in prose and code fences no matter how firmly they are
// asked not to, so the array is located rather than assumed.
func Parse(out string, s core.Session, model string) ([]index.Nugget, error) {
	body := extractJSONArray(out)
	if body == "" {
		return nil, fmt.Errorf("no JSON array in model output")
	}

	var raws []rawNugget
	if err := json.Unmarshal([]byte(body), &raws); err != nil {
		return nil, fmt.Errorf("parse nuggets: %w", err)
	}

	var out2 []index.Nugget
	for _, r := range raws {
		if strings.TrimSpace(r.Body) == "" {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(r.Kind))
		if !index.ValidKind(kind) {
			kind = "gotcha"
		}

		// Belt and braces: redact again on the way in. The model may have
		// echoed something the preview redaction missed.
		rb := redact.Text(r.Body)
		rt := redact.Text(r.Title)

		out2 = append(out2, index.Nugget{
			UID:        index.NewUID(),
			Tool:       string(s.Tool),
			SessionID:  s.ID,
			Kind:       kind,
			Title:      strings.TrimSpace(rt.Text),
			Body:       strings.TrimSpace(rb.Text),
			Tags:       cleanTags(r.Tags),
			Workspace:  s.Dir,
			Repo:       s.Repo,
			Confidence: clamp01(r.Confidence),
			Model:      model,
			Redacted:   rb.Redacted || rt.Redacted,
			TurnRef:    fmt.Sprintf("%s:%s", s.Tool, s.ID),
			CreatedAt:  time.Now(),
		})
	}
	return out2, nil
}

// extractJSONArray finds the outermost JSON array in a response.
func extractJSONArray(s string) string {
	s = strings.TrimSpace(s)

	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		s = strings.TrimSpace(rest)
	}

	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

func cleanTags(in []string) []string {
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(strings.ToLower(t))
		// Commas would corrupt the comma-joined storage format.
		t = strings.ReplaceAll(t, ",", " ")
		if t != "" && len(out) < 8 {
			out = append(out, t)
		}
	}
	return out
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}
