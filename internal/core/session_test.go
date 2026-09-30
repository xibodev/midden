package core

import (
	"testing"
	"time"
)

func TestRiskThresholds(t *testing.T) {
	// The thresholds encode an observed failure: Copilot --resume times out
	// and silently starts a NEW session above ~680 MiB. Four sessions
	// (774/740/687/681 MiB) were lost this way, so every one of those sizes
	// must score critical.
	cases := []struct {
		name  string
		bytes int64
		want  Risk
	}{
		{"empty", 0, RiskNone},
		{"small", 10 << 20, RiskNone},
		{"just below watch", RiskWatchBytes - 1, RiskNone},
		{"watch", RiskWatchBytes, RiskWatch},
		{"warn", RiskWarnBytes, RiskWarn},
		{"critical", RiskCriticalBytes, RiskCritical},
		{"observed loss 681MiB", 681 << 20, RiskCritical},
		{"observed loss 687MiB", 687 << 20, RiskCritical},
		{"observed loss 740MiB", 740 << 20, RiskCritical},
		{"observed loss 774MiB", 774 << 20, RiskCritical},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Session{Bytes: c.bytes}).Risk(); got != c.want {
				t.Errorf("Risk(%d) = %v, want %v", c.bytes, got, c.want)
			}
		})
	}
}

func TestSpanDetectsIdleButOpen(t *testing.T) {
	// A session created 12 days ago but touched an hour ago is the shape that
	// age alone hides: it looks "recent" yet has been open for days.
	now := time.Now()
	s := Session{Created: now.AddDate(0, 0, -12), Updated: now.Add(-time.Hour)}

	if days := s.SpanDays(); days < 11.9 || days > 12.1 {
		t.Errorf("SpanDays() = %.2f, want ~12", days)
	}
	if s.Age() > 2*time.Hour {
		t.Errorf("Age() = %v, want ~1h", s.Age())
	}
}

func TestSpanNeverNegative(t *testing.T) {
	now := time.Now()
	s := Session{Created: now, Updated: now.Add(-time.Hour)} // clock skew
	if s.Span() != 0 {
		t.Errorf("Span() = %v, want 0 for reversed timestamps", s.Span())
	}
	if (Session{}).Span() != 0 {
		t.Error("zero-value Session should have zero span")
	}
}

func TestScopeSinceIsCalendarDay(t *testing.T) {
	// Calendar-day granularity keeps adapters consistent. Mixing exact-hour
	// and calendar-day cutoffs makes windows disagree between tools.
	cutoff := Scope{Days: 5}.Since()
	if cutoff.Hour() != 0 || cutoff.Minute() != 0 || cutoff.Second() != 0 {
		t.Errorf("Since() = %v, want midnight-aligned", cutoff)
	}
	if !(Scope{}).Since().IsZero() {
		t.Error("Days=0 should be unbounded")
	}
}

func TestScopeMatch(t *testing.T) {
	now := time.Now()
	base := Session{
		Tool:    ToolClaude,
		ID:      "abc123",
		Dir:     `E:\startup projects\orvantix`,
		Repo:    "mekjr1/orvantix",
		Updated: now.Add(-time.Hour),
	}

	tests := []struct {
		name  string
		scope Scope
		sess  Session
		want  bool
	}{
		{"zero scope matches", Scope{}, base, true},
		{"tool match", Scope{Tools: []Tool{ToolClaude}}, base, true},
		{"tool mismatch", Scope{Tools: []Tool{ToolCopilot}}, base, false},
		{"workspace substring", Scope{Workspace: "orvantix"}, base, true},
		{"workspace case-insensitive", Scope{Workspace: "ORVANTIX"}, base, true},
		{"workspace mismatch", Scope{Workspace: "employed"}, base, false},
		{"repo match", Scope{Repo: "orvantix"}, base, true},
		{"id prefix", Scope{IDPrefix: "abc"}, base, true},
		{"id prefix mismatch", Scope{IDPrefix: "xyz"}, base, false},
		{"within window", Scope{Days: 2}, base, true},
		{"outside window", Scope{Days: 1}, Session{Tool: ToolClaude, Updated: now.AddDate(0, 0, -9)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.Match(tc.sess); got != tc.want {
				t.Errorf("Match() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNoiseIsHiddenButNotLost(t *testing.T) {
	noisy := Session{Tool: ToolCopilot, Noise: true, Updated: time.Now()}

	if (Scope{}).Match(noisy) {
		t.Error("noise should be hidden by default")
	}
	if !(Scope{IncludeNoise: true}).Match(noisy) {
		t.Error("--all should reveal noise")
	}
}

func TestIsNoise(t *testing.T) {
	tests := []struct {
		title, dir string
		turns      int
		want       bool
	}{
		{"Review Repository Code", `E:\startup projects\orvantix`, 39, false},
		{"<GARRISON_GUIDE>\n# Adjutant", `C:\x\garrison-work\g\assistant\copilot\ses_1`, 1, true},
		{"Reply briefly after using the shell tool to run: x", `C:\x`, 1, true},
		{"hi", `E:\startup projects\open-tools`, 4, true},
		{"anything", `C:\Users\g\AppData\Local\Temp\pytest-of-g\x`, 20, true},
		{"real work in a normal dir", `E:\repo`, 12, false},
		{"", `E:\repo`, 1, true},
	}
	for _, tc := range tests {
		if got := IsNoise(tc.title, tc.dir, tc.turns); got != tc.want {
			t.Errorf("IsNoise(%q, %q, %d) = %v, want %v", tc.title, tc.dir, tc.turns, got, tc.want)
		}
	}
}

func TestCleanTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "(untitled)"},
		{"  spaced   out\n\ttitle ", "spaced out title"},
		{"<local-command-caveat>Caveat: ...", "(slash command session)"},
		{"<command-name>foo", "(slash command session)"},
		{"normal title", "normal title"},
	}
	for _, tc := range tests {
		if got := CleanTitle(tc.in); got != tc.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdefghij", 5); got != "ab..." {
		t.Errorf("Truncate = %q, want %q", got, "ab...")
	}
	if got := Truncate("short", 20); got != "short" {
		t.Errorf("Truncate should leave short strings alone, got %q", got)
	}
	// Multi-byte input must not be cut mid-rune.
	if got := Truncate("Avaliação_Digital", 8); len([]rune(got)) != 8 {
		t.Errorf("Truncate broke rune boundaries: %q", got)
	}
}
