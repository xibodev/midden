package main

import (
	"strings"
	"testing"
)

func TestEstTokens(t *testing.T) {
	if estTokens("") != 0 {
		t.Error("empty string should cost nothing")
	}
	// ~4 chars per token, and the estimate should never under-report badly.
	if got := estTokens(strings.Repeat("x", 400)); got < 100 || got > 102 {
		t.Errorf("estTokens(400 chars) = %d, want ~100", got)
	}
}

func TestCapTokensLeavesSmallInputAlone(t *testing.T) {
	in := "short output"
	if got := capTokens(in, 100); got != in {
		t.Errorf("capTokens mangled a small input: %q", got)
	}
}

func TestCapTokensTruncatesAndSaysSo(t *testing.T) {
	// Silent truncation is worse than short output: the reader would assume
	// it saw everything.
	in := strings.Repeat("line of text here\n", 500)
	got := capTokens(in, 50)

	if estTokens(got) > 80 {
		t.Errorf("capTokens exceeded its budget: ~%d tokens", estTokens(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncation must be announced")
	}
}

func TestBudgetLinesNeverEmitsPartialRecords(t *testing.T) {
	// A half-written session line is worse than a known-short list, so
	// budgetLines drops whole lines rather than cutting mid-record.
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "copilot abc12345 | E:\\some\\workspace | 40 turns | 3d ago | A session title here"
	}

	got := budgetLines(lines, 300, "sessions")

	for _, ln := range strings.Split(got, "\n") {
		if ln == "" || strings.HasPrefix(ln, "[showing") {
			continue
		}
		if !strings.HasPrefix(ln, "copilot ") {
			t.Fatalf("emitted a partial record: %q", ln)
		}
	}
	if estTokens(got) > 400 {
		t.Errorf("budgetLines overran: ~%d tokens", estTokens(got))
	}
	if !strings.Contains(got, "omitted") {
		t.Error("omitted count must be reported so the reader can narrow scope")
	}
}

func TestBudgetLinesFitsEverythingWhenItCan(t *testing.T) {
	lines := []string{"one", "two", "three"}
	got := budgetLines(lines, 1000, "sessions")

	if strings.Contains(got, "omitted") {
		t.Error("nothing should be reported omitted when everything fits")
	}
	for _, want := range lines {
		if !strings.Contains(got, want) {
			t.Errorf("lost line %q", want)
		}
	}
}

func TestToolBudgetsAreOrdered(t *testing.T) {
	// A brief must never be allowed to cost more than a whole listing, or the
	// cheap-survey-then-drill-down workflow inverts.
	if budgetBrief >= budgetList {
		t.Errorf("brief budget (%d) should be below list budget (%d)", budgetBrief, budgetList)
	}
	if budgetHealth >= budgetBrief {
		t.Errorf("health budget (%d) should be below brief budget (%d)", budgetHealth, budgetBrief)
	}
	if budgetSmall >= budgetHealth {
		t.Errorf("small budget (%d) should be the cheapest", budgetSmall)
	}
}
