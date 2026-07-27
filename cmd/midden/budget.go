package main

// Token budgeting.
//
// The whole point of the MCP surface is that an agent can survey hundreds of
// sessions without drowning in context. Every tool therefore declares a hard
// ceiling and truncates to it, rather than hoping output stays small.

import (
	"fmt"
	"strings"
)

// Budgets in estimated tokens. These are ceilings, not targets.
const (
	budgetList   = 4000 // a whole session list
	budgetBrief  = 1500 // one session's recoverable context
	budgetHealth = 400  // footprint + at-risk summary
	budgetSearch = 1500
	budgetSmall  = 200
)

// estTokens approximates token count from character length.
//
// Deliberately crude and slightly pessimistic: this exists to enforce a
// ceiling, not to do accounting. Four characters per token is the usual
// English-text approximation; code and paths run denser, so real counts tend
// to land under the estimate.
func estTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(s)/4 + 1
}

// capTokens truncates text to a token budget, appending an explicit marker so
// the reader knows output was cut rather than assuming it saw everything.
func capTokens(s string, maxTokens int) string {
	if estTokens(s) <= maxTokens {
		return s
	}
	maxChars := maxTokens * 4

	// Prefer cutting at a line boundary so the tail is not a fragment.
	cut := s[:maxChars]
	if i := strings.LastIndexByte(cut, '\n'); i > maxChars/2 {
		cut = cut[:i]
	}
	return cut + fmt.Sprintf("\n[truncated at ~%d token budget — narrow the scope for more]", maxTokens)
}

// budgetLines emits as many lines as fit, reporting how many were dropped.
// Preferred over capTokens for lists, because a half-written record is worse
// than a known-short list.
func budgetLines(lines []string, maxTokens int, noun string) string {
	var b strings.Builder
	used, shown := 0, 0

	for _, ln := range lines {
		cost := estTokens(ln) + 1
		if used+cost > maxTokens {
			break
		}
		b.WriteString(ln)
		b.WriteByte('\n')
		used += cost
		shown++
	}

	if shown < len(lines) {
		fmt.Fprintf(&b, "\n[showing %d of %d %s — %d omitted to stay within a ~%d token budget; narrow with tool/days/workspace filters]",
			shown, len(lines), noun, len(lines)-shown, maxTokens)
	}
	return b.String()
}
