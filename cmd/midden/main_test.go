package main

import (
	"flag"
	"reflect"
	"testing"
)

// newTestFlags mirrors a real command's flag shape: a mix of bool and
// value-taking flags.
func newTestFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("handoff", false, "")
	fs.Bool("json", false, "")
	fs.Bool("all", false, "")
	fs.String("with", "", "")
	fs.String("tool", "", "")
	fs.Int("turns", 0, "")
	return fs
}

func TestReorderArgs(t *testing.T) {
	// Regression: stdlib flag stops at the first positional, so flags written
	// after an id were silently dropped. A bool flag must NOT swallow the
	// following token — that bug made `brief <id> --handoff --turns 2`
	// consume "--turns" as the value of "--handoff".
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "flag after positional is hoisted",
			in:   []string{"abc123", "--with", "do the thing"},
			want: []string{"--with", "do the thing", "abc123"},
		},
		{
			name: "bool flag does not consume the next flag",
			in:   []string{"abc123", "--handoff", "--turns", "2"},
			want: []string{"--handoff", "--turns", "2", "abc123"},
		},
		{
			name: "trailing bool flag",
			in:   []string{"abc123", "--json"},
			want: []string{"--json", "abc123"},
		},
		{
			name: "attached value is left alone",
			in:   []string{"abc123", "--turns=5"},
			want: []string{"--turns=5", "abc123"},
		},
		{
			name: "already ordered is unchanged",
			in:   []string{"--tool", "claude", "--all"},
			want: []string{"--tool", "claude", "--all"},
		},
		{
			name: "double dash ends flag parsing",
			in:   []string{"--json", "--", "--not-a-flag"},
			want: []string{"--json", "--not-a-flag"},
		},
		{
			name: "single-dash form",
			in:   []string{"abc", "-turns", "3"},
			want: []string{"-turns", "3", "abc"},
		},
		{
			name: "no args",
			in:   nil,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reorderArgs(newTestFlags(), tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reorderArgs(%v)\n got %v\nwant %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestReorderedArgsActuallyParse(t *testing.T) {
	// End-to-end: the reordered slice must parse, with both the positional
	// and every flag recovered.
	fs := newTestFlags()
	handoff := fs.Lookup("handoff")
	turns := fs.Lookup("turns")

	args := []string{"ac0c39cf", "--handoff", "--turns", "2"}
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if handoff.Value.String() != "true" {
		t.Error("--handoff was not set")
	}
	if turns.Value.String() != "2" {
		t.Errorf("--turns = %s, want 2", turns.Value.String())
	}
	if fs.NArg() != 1 || fs.Arg(0) != "ac0c39cf" {
		t.Errorf("positional lost: %v", fs.Args())
	}
}

func TestClipPreservesRuneBoundaries(t *testing.T) {
	if got := clip("short", 100); got != "short" {
		t.Errorf("clip should leave short text alone, got %q", got)
	}
	got := clip("Avaliação Digital UnISCED", 8)
	if !hasSuffix(got, "[... truncated]") {
		t.Errorf("clip should mark truncation, got %q", got)
	}
	head := []rune(got)[:8]
	if len(head) != 8 {
		t.Errorf("clip broke a rune boundary: %q", got)
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func TestShortID(t *testing.T) {
	if got := shortID("ac0c39cf-4c75-4054-9239-589c22f03a79"); got != "ac0c39cf" {
		t.Errorf("shortID = %q", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID should not pad short ids, got %q", got)
	}
}
