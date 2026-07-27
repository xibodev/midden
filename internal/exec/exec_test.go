package exec

import (
	"strings"
	"testing"
)

func TestConversationAssignsSessionIDUpFront(t *testing.T) {
	// Regression: an earlier version pattern-matched the session id out of CLI
	// output. When that failed it silently fell back to fresh invocations,
	// producing artifacts written from no evidence. The id must be known
	// before the first call.
	r := &Runner{Backend: Copilot}
	c := r.NewConversation()

	if c.SessionID() == "" {
		t.Fatal("session id must be assigned at construction")
	}
	if len(c.SessionID()) != 36 {
		t.Errorf("expected a UUID, got %q", c.SessionID())
	}
}

func TestPrimeAndFollowupUseTheSameSession(t *testing.T) {
	for _, backend := range []Backend{Copilot, Claude} {
		t.Run(string(backend), func(t *testing.T) {
			c := (&Runner{Backend: backend}).NewConversation()
			id := c.SessionID()

			prime := strings.Join(c.primeArgv("hello"), " ")
			if !strings.Contains(prime, "--session-id") || !strings.Contains(prime, id) {
				t.Errorf("prime must fix the session id: %s", prime)
			}

			follow := strings.Join(c.followupArgv("more"), " ")
			if !strings.Contains(follow, "--resume") || !strings.Contains(follow, id) {
				t.Errorf("follow-up must resume the same session: %s", follow)
			}
		})
	}
}

func TestAskRefusesWithoutASessionHandle(t *testing.T) {
	// Silently downgrading to a fresh call produces a plausible, worthless
	// artifact. Failing loudly is correct.
	c := (&Runner{Backend: Opencode}).NewConversation()
	c.primed = true
	c.sessionID = ""

	if _, err := c.Ask(nil, "write something"); err == nil {
		t.Fatal("Ask should refuse when the evidence would be missing")
	}
}

func TestAskRequiresPriming(t *testing.T) {
	c := (&Runner{Backend: Copilot}).NewConversation()
	if _, err := c.Ask(nil, "x"); err == nil {
		t.Error("Ask before Prime should fail")
	}
}

func TestSalvageInvocationsStripMCP(t *testing.T) {
	// Salvage only reads text. Loading tool definitions on every launch is
	// pure waste, and on a heavily configured machine it is the largest
	// per-invocation cost.
	c := (&Runner{Backend: Copilot, Pure: true}).NewConversation()
	got := strings.Join(c.primeArgv("x"), " ")

	if !strings.Contains(got, "--additional-mcp-config") {
		t.Error("pure invocations should disable MCP servers")
	}
	if !strings.Contains(got, "mcpServers") {
		t.Error("copilot rejects an MCP config without the mcpServers key")
	}

	oc := (&Runner{Backend: Opencode, Pure: true}).NewConversation()
	if !strings.Contains(strings.Join(oc.primeArgv("x"), " "), "--pure") {
		t.Error("opencode pure invocations should pass --pure")
	}
}

func TestOversizedPromptsAreStagedToAFile(t *testing.T) {
	// Windows caps a command line at 8191 characters and salvage prompts
	// routinely exceed it. The failure mode was an opaque OS error.
	r := &Runner{Backend: Copilot}
	huge := strings.Repeat("evidence ", 5000)

	staged, cleanup, err := r.stage(huge)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) >= len(huge) {
		t.Error("oversized prompt was not staged")
	}
	if len(staged) > 500 {
		t.Errorf("staged reference should be short, got %d bytes", len(staged))
	}
	if !strings.Contains(staged, "midden-prompt") {
		t.Errorf("staged reference should point at the prompt file: %q", staged)
	}
}

func TestSmallPromptsArePassedInline(t *testing.T) {
	r := &Runner{Backend: Copilot}
	staged, cleanup, err := r.stage("short prompt")
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if staged != "short prompt" {
		t.Errorf("small prompts should not be staged, got %q", staged)
	}
}

func TestOpencodeUsesRunNotResume(t *testing.T) {
	// opencode has no --resume flag; emitting one produces a broken command.
	c := (&Runner{Backend: Opencode}).NewConversation()
	c.sessionID = "ses_abc"

	got := strings.Join(c.followupArgv("x"), " ")
	if strings.Contains(got, "--resume") {
		t.Errorf("opencode must use --session, got %q", got)
	}
	if !strings.Contains(got, "run --session ses_abc") {
		t.Errorf("unexpected opencode follow-up: %q", got)
	}
}

func TestSessionIDFromJSON(t *testing.T) {
	cases := []string{
		`{"sessionID":"ses_abc123"}`,
		`noise` + "\n" + `{"info":{"id":"ses_abc123"}}`,
		`{"type":"start","session_id":"ses_abc123"}`,
	}
	for _, in := range cases {
		if got := sessionIDFromJSON(in); got != "ses_abc123" {
			t.Errorf("sessionIDFromJSON(%q) = %q", in, got)
		}
	}
	if got := sessionIDFromJSON("no json here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestBudgetStopsSpending(t *testing.T) {
	b := &Budget{MaxCalls: 2, MaxTokens: 1000}

	if err := b.Allow(100); err != nil {
		t.Fatal(err)
	}
	b.Charge(100)
	b.Charge(100)

	if err := b.Allow(100); err == nil {
		t.Error("budget should refuse after MaxCalls")
	}

	b2 := &Budget{MaxTokens: 500}
	if err := b2.Allow(600); err == nil {
		t.Error("budget should refuse a call that would exceed MaxTokens")
	}

	var nilBudget *Budget
	if err := nilBudget.Allow(1e9); err != nil {
		t.Error("a nil budget should be unlimited, not an error")
	}
}

func TestSummariseDoesNotDumpWholePrompts(t *testing.T) {
	got := summarise([]string{"--prompt", strings.Repeat("x", 5000)})
	if len(got) > 100 {
		t.Errorf("summary should elide long prompts, got %d bytes", len(got))
	}
	if !strings.Contains(got, "5000 chars") {
		t.Errorf("summary should report the elided size: %q", got)
	}
}

func TestNewUUIDIsWellFormedAndUnique(t *testing.T) {
	a, b := NewUUID(), NewUUID()
	if a == b {
		t.Error("uuids should be unique")
	}
	if len(a) != 36 || strings.Count(a, "-") != 4 {
		t.Errorf("malformed uuid: %q", a)
	}
	if a[14] != '4' {
		t.Errorf("expected a v4 uuid, got %q", a)
	}
}
