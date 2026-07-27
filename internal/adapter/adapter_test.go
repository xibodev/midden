package adapter

import (
	"runtime"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

func TestWalkAndResumeIncludesCd(t *testing.T) {
	// Claude and Copilot key sessions to their original cwd, so the walk is
	// required for resume to work at all — not decoration.
	got := WalkAndResume(`E:\startup projects\orvantix`, "copilot --resume abc")

	if !strings.Contains(got, "orvantix") {
		t.Errorf("one-liner lost the workspace: %q", got)
	}
	if !strings.Contains(got, "copilot --resume abc") {
		t.Errorf("one-liner lost the resume command: %q", got)
	}
	if runtime.GOOS == "windows" && !strings.HasPrefix(got, "Set-Location") {
		t.Errorf("want PowerShell dialect on Windows, got %q", got)
	}
}

func TestShellQuoteEscapes(t *testing.T) {
	got := shellQuote(`it's fine`)
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("shellQuote should quote: %q", got)
	}
	if strings.Contains(got, `s' fine`) {
		t.Errorf("shellQuote left an unescaped quote: %q", got)
	}
	if shellQuote("") != `""` {
		t.Errorf("empty string should quote to \"\", got %q", shellQuote(""))
	}
}

func TestResumeCmdDialects(t *testing.T) {
	s := core.Session{ID: "sess-1"}

	tests := []struct {
		name        string
		adapter     core.Adapter
		instruction string
		wantSubstr  []string
	}{
		{"copilot plain", NewCopilot(), "", []string{"copilot --resume sess-1"}},
		{"copilot with instruction", NewCopilot(), "do the thing", []string{"--resume sess-1", "--prompt", "do the thing"}},
		{"claude plain", NewClaude(), "", []string{"claude --resume sess-1"}},
		// opencode resumes with --session, NOT --resume, and needs `run` to
		// deliver a message non-interactively.
		{"opencode plain", NewOpencode(), "", []string{"opencode --session sess-1"}},
		{"opencode with instruction", NewOpencode(), "summarise", []string{"opencode run --session sess-1", "summarise"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.adapter.ResumeCmd(s, tc.instruction)
			for _, want := range tc.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("ResumeCmd = %q, want substring %q", got, want)
				}
			}
		})
	}
}

func TestOpencodeNeverUsesResumeFlag(t *testing.T) {
	// Regression guard: opencode has no --resume flag. Emitting one produces
	// a command that fails at the terminal.
	got := NewOpencode().ResumeCmd(core.Session{ID: "ses_x"}, "")
	if strings.Contains(got, "--resume") {
		t.Errorf("opencode must use --session, got %q", got)
	}
}

func TestAdaptersDeclareDistinctTools(t *testing.T) {
	seen := map[core.Tool]bool{}
	for _, a := range All() {
		if seen[a.Tool()] {
			t.Errorf("duplicate adapter for tool %s", a.Tool())
		}
		seen[a.Tool()] = true

		if Find(a.Tool()) == nil {
			t.Errorf("Find(%s) returned nil", a.Tool())
		}
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 adapters, got %d", len(seen))
	}
}

func TestRoDSNIsReadOnly(t *testing.T) {
	// Source stores belong to tools that may be running; a writable handle
	// risks corrupting them.
	dsn := roDSN(`C:\Users\g\.copilot\session-store.db`)

	if !strings.Contains(dsn, "mode=ro") {
		t.Errorf("DSN must be read-only: %q", dsn)
	}
	if !strings.Contains(dsn, "query_only") {
		t.Errorf("DSN should set query_only: %q", dsn)
	}
	if strings.Contains(dsn, `\`) {
		t.Errorf("DSN must use forward slashes: %q", dsn)
	}
	if strings.Contains(roDSN("/home/u/my db/x.db"), " ") {
		t.Error("DSN must escape spaces")
	}
}
