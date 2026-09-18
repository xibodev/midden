package adapter

import (
	"errors"
	"os"
	"path/filepath"
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

func TestCollectDetailedKeepsPartialSessionsButMarksToolIncomplete(t *testing.T) {
	partial := fakeAdapter{
		tool: core.ToolClaude,
		sessions: []core.Session{
			{Tool: core.ToolClaude, ID: "readable"},
		},
		err: errors.New("one unreadable subtree"),
	}
	complete := fakeAdapter{
		tool:     core.ToolCopilot,
		sessions: []core.Session{{Tool: core.ToolCopilot, ID: "complete"}},
	}

	sessions, result := collectFrom(core.Scope{IncludeNoise: true}, []core.Adapter{partial, complete})
	if got, want := len(sessions), 2; got != want {
		t.Fatalf("sessions=%d, want %d", got, want)
	}
	if got, want := len(result.Errors), 1; got != want {
		t.Fatalf("errors=%d, want %d", got, want)
	}
	if got, want := result.Complete, []core.Tool{core.ToolCopilot}; !sameTools(got, want) {
		t.Fatalf("complete=%v, want %v", got, want)
	}
}

func TestCollectionDoesNotMarkAllFreshWhenToolSetChanges(t *testing.T) {
	result := CollectionResult{
		Attempted: []core.Tool{core.ToolCopilot, core.ToolClaude},
		Complete:  []core.Tool{core.ToolCopilot, core.ToolClaude},
	}
	if !result.isAllSourcesComplete(core.Scope{}, []core.Tool{core.ToolCopilot, core.ToolClaude}) {
		t.Fatal("stable complete collection was not all-source complete")
	}
	if result.isAllSourcesComplete(core.Scope{}, []core.Tool{core.ToolCopilot, core.ToolClaude, core.ToolOpencode}) {
		t.Fatal("newly appeared source store was incorrectly called fresh")
	}
	if result.isAllSourcesComplete(core.Scope{Tools: []core.Tool{core.ToolClaude}}, []core.Tool{core.ToolClaude}) {
		t.Fatal("selected-tool scan was incorrectly called all-source fresh")
	}
	result.Errors = []error{errors.New("partial")}
	if result.isAllSourcesComplete(core.Scope{}, []core.Tool{core.ToolCopilot, core.ToolClaude}) {
		t.Fatal("partial collection was incorrectly called all-source fresh")
	}
}

func TestClaudeReportsErrorWhenProjectsRootIsUnreadable(t *testing.T) {
	// A full scan may reconcile-delete absent rows. If Claude cannot read its
	// projects root, an empty list is not proof that every indexed session was
	// deleted. The adapter must surface partiality so callers can preserve the
	// existing index.
	c := &Claude{Root: t.TempDir()}
	sessions, err := c.Sessions(core.Scope{IncludeNoise: true})
	if err == nil {
		t.Fatal("unreadable/missing projects root returned a complete result")
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions=%d, want 0 from missing root", len(sessions))
	}
	if !strings.Contains(err.Error(), "claude projects") {
		t.Fatalf("error=%q, want source-root context", err)
	}
}

func TestClaudeRejectsProjectsRootThatIsAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "projects"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Claude{Root: root}
	if c.Available() {
		t.Fatal("regular projects file reported as an available source store")
	}
	if _, err := c.Sessions(core.Scope{IncludeNoise: true}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error=%v, want non-directory source-root error", err)
	}
}

type fakeAdapter struct {
	tool     core.Tool
	sessions []core.Session
	err      error
}

func (a fakeAdapter) Tool() core.Tool                             { return a.tool }
func (fakeAdapter) Available() bool                               { return true }
func (a fakeAdapter) Sessions(core.Scope) ([]core.Session, error) { return a.sessions, a.err }
func (fakeAdapter) ResumeCmd(core.Session, string) string         { return "" }
func (fakeAdapter) Footprint() int64                              { return 0 }

func sameTools(got, want []core.Tool) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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
	dsn := roDSN(filepath.Join(t.TempDir(), "session-store.db"))

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

type exactSpyAdapter struct {
	tool     core.Tool
	sessions []core.Session
	err      error
	calls    int
}

func (a *exactSpyAdapter) Tool() core.Tool { return a.tool }
func (*exactSpyAdapter) Available() bool   { return true }
func (a *exactSpyAdapter) Sessions(scope core.Scope) ([]core.Session, error) {
	a.calls++
	var out []core.Session
	for _, session := range a.sessions {
		if scope.Match(session) {
			out = append(out, session)
		}
	}
	return out, a.err
}
func (*exactSpyAdapter) ResumeCmd(core.Session, string) string { return "" }
func (*exactSpyAdapter) Footprint() int64                      { return 0 }

func TestCollectExactOnlyReadsSelectedToolAndIDs(t *testing.T) {
	claude := &exactSpyAdapter{tool: core.ToolClaude, sessions: []core.Session{
		{Tool: core.ToolClaude, ID: "selected"}, {Tool: core.ToolClaude, ID: "other"},
	}}
	copilot := &exactSpyAdapter{tool: core.ToolCopilot, sessions: []core.Session{{Tool: core.ToolCopilot, ID: "unselected"}}}
	got, errs := collectExactFrom([]string{"claude:selected"}, []core.Adapter{copilot, claude})
	if len(errs) != 0 || len(got) != 1 || got[0].ID != "selected" {
		t.Fatalf("sessions=%#v errors=%v", got, errs)
	}
	if claude.calls != 1 || copilot.calls != 0 {
		t.Fatalf("selected calls=%d unselected calls=%d", claude.calls, copilot.calls)
	}
}

func TestCollectExactIgnoresUnselectedAdapterFailure(t *testing.T) {
	claude := &exactSpyAdapter{tool: core.ToolClaude, sessions: []core.Session{
		{Tool: core.ToolClaude, ID: "selected"},
	}}
	copilot := &exactSpyAdapter{tool: core.ToolCopilot, err: errors.New("copilot store is unreadable")}
	got, errs := collectExactFrom([]string{"claude:selected"}, []core.Adapter{copilot, claude})
	if len(errs) != 0 || len(got) != 1 || got[0].Tool != core.ToolClaude || got[0].ID != "selected" {
		t.Fatalf("sessions=%#v errors=%v", got, errs)
	}
	if claude.calls != 1 || copilot.calls != 0 {
		t.Fatalf("selected calls=%d unselected calls=%d", claude.calls, copilot.calls)
	}
}
