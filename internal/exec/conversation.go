package exec

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Conversation sends multiple prompts to ONE underlying CLI session.
//
// This exists because of a measured cost structure: in a real 66-turn session,
// cache writes were ~55% of total cost and roughly 3x the cache reads. Loading
// context is expensive; re-reading it is nearly free.
//
// N separate shell-outs are N cold contexts, each paying full cache-write cost
// AND — far worse — each starting with no memory of the evidence. An early
// version of this type tried to recover the session id by pattern-matching CLI
// output; when that failed it silently fell back to fresh invocations, and
// produced artifacts written from no evidence at all. They looked plausible.
//
// The session id is therefore ASSIGNED, never discovered: copilot and claude
// both accept --session-id to fix the UUID of a new session, and opencode
// reports its own id in JSON output.
type Conversation struct {
	runner    *Runner
	sessionID string
	turns     int
	primed    bool
}

// NewConversation starts a batch against a backend.
func (r *Runner) NewConversation() *Conversation {
	return &Conversation{runner: r, sessionID: NewUUID()}
}

// SessionID is the underlying CLI session.
func (c *Conversation) SessionID() string { return c.sessionID }

// Turns is how many prompts have been sent.
func (c *Conversation) Turns() int { return c.turns }

// NewUUID generates an RFC-4122 v4 identifier, the shape copilot and claude
// expect for --session-id.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// primeArgv starts a new session with a known identifier.
func (c *Conversation) primeArgv(prompt string) []string {
	switch c.runner.Backend {
	case Copilot:
		a := []string{"--session-id", c.sessionID, "--prompt", prompt,
			"--allow-all-tools", "--allow-all-paths"}
		if c.runner.Model != "" {
			a = append(a, "--model", c.runner.Model)
		}
		if c.runner.Pure {
			a = append(a, "--additional-mcp-config", emptyMCPConfig)
		}
		return a

	case Claude:
		a := []string{"--session-id", c.sessionID, "--print", prompt}
		if c.runner.Model != "" {
			a = append(a, "--model", c.runner.Model)
		}
		return a

	case Opencode:
		a := []string{"run", "--format", "json"}
		if c.runner.Model != "" {
			a = append(a, "--model", c.runner.Model)
		}
		if c.runner.Pure {
			a = append(a, "--pure")
		}
		if c.runner.Dir != "" {
			a = append(a, "--dir", c.runner.Dir)
		}
		return append(a, prompt)
	}
	return nil
}

// followupArgv continues the same session.
func (c *Conversation) followupArgv(prompt string) []string {
	switch c.runner.Backend {
	case Copilot:
		a := []string{"--resume", c.sessionID, "--prompt", prompt,
			"--allow-all-tools", "--allow-all-paths"}
		if c.runner.Pure {
			a = append(a, "--additional-mcp-config", emptyMCPConfig)
		}
		return a

	case Claude:
		a := []string{"--resume", c.sessionID, "--print", prompt}
		if c.runner.Model != "" {
			a = append(a, "--model", c.runner.Model)
		}
		return a

	case Opencode:
		a := []string{"run", "--session", c.sessionID}
		if c.runner.Pure {
			a = append(a, "--pure")
		}
		return append(a, prompt)
	}
	return nil
}

// Prime sends the stable prefix: instructions and evidence. Everything sent
// afterwards reuses this as cached context.
func (c *Conversation) Prime(ctx context.Context, preamble string) (*Result, error) {
	if c.primed {
		return nil, fmt.Errorf("conversation already primed")
	}

	res, err := c.invoke(ctx, c.primeArgv, preamble)
	if err != nil {
		return res, err
	}
	c.primed = true

	if c.runner.Backend == Opencode {
		id := sessionIDFromJSON(res.Output)
		if id == "" {
			return res, fmt.Errorf("opencode did not report a session id; " +
				"follow-ups would lose the evidence")
		}
		c.sessionID = id
	}
	return res, nil
}

// Ask sends a follow-up into the same session, reusing cached context.
//
// A missing session handle is fatal rather than silently downgraded: an
// artifact written without its evidence looks plausible and is worthless.
func (c *Conversation) Ask(ctx context.Context, prompt string) (*Result, error) {
	if !c.primed {
		return nil, fmt.Errorf("conversation must be primed before follow-ups")
	}
	if c.sessionID == "" {
		return nil, fmt.Errorf("no session handle: refusing a follow-up that " +
			"would have no evidence in context")
	}
	return c.invoke(ctx, c.followupArgv, prompt)
}

// invoke runs one turn, staging oversized prompts to a file.
func (c *Conversation) invoke(ctx context.Context, argv func(string) []string, prompt string) (*Result, error) {
	staged, cleanup, err := c.runner.stage(prompt)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := argv(staged)
	if args == nil {
		return nil, fmt.Errorf("unsupported backend %q", c.runner.Backend)
	}

	display := string(c.runner.Backend) + " " + summarise(args)
	if c.runner.DryRun {
		c.turns++
		return &Result{Backend: c.runner.Backend, Command: display,
			Output: "[dry run — nothing was invoked]"}, nil
	}

	timeout := c.runner.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, string(c.runner.Backend), args...)
	if c.runner.Dir != "" && c.runner.Backend != Opencode {
		cmd.Dir = c.runner.Dir
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	start := time.Now()
	out, err := cmd.CombinedOutput()
	c.turns++

	res := &Result{
		Output:  strings.TrimSpace(string(out)),
		Backend: c.runner.Backend,
		Model:   c.runner.Model,
		Command: display,
		Elapsed: time.Since(start),
	}
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("%s timed out after %s", c.runner.Backend, timeout)
		}
		if res.Output == "" {
			return res, fmt.Errorf("%s turn failed: %w", c.runner.Backend, err)
		}
	}
	return res, nil
}

// sessionIDFromJSON pulls a session id out of opencode's JSON event stream.
func sessionIDFromJSON(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var probe map[string]any
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue
		}
		for _, key := range []string{"sessionID", "session_id", "sessionId"} {
			if v, ok := probe[key].(string); ok && v != "" {
				return v
			}
		}
		if info, ok := probe["info"].(map[string]any); ok {
			if v, ok := info["id"].(string); ok && strings.HasPrefix(v, "ses_") {
				return v
			}
		}
	}
	return ""
}

// Budget caps spending for one task.
//
// Metered in the subscription's unit rather than dollars: under a seat the
// scarce resource is premium requests, and dollars are a fiction.
type Budget struct {
	MaxCalls  int
	MaxTokens int
	spent     int
	tokens    int
}

// Estimate is what a task is predicted to cost, shown before it starts.
type Estimate struct {
	Calls      int `json:"calls"`
	InTokens   int `json:"input_tokens"`
	OutTokens  int `json:"output_tokens"`
	CachedRead int `json:"cached_read_tokens"`
}

// Total is the predicted token movement.
func (e Estimate) Total() int { return e.InTokens + e.OutTokens + e.CachedRead }

// Allow reports whether another call fits the budget.
func (b *Budget) Allow(estTokens int) error {
	if b == nil {
		return nil
	}
	if b.MaxCalls > 0 && b.spent >= b.MaxCalls {
		return fmt.Errorf("budget exhausted: %d calls", b.MaxCalls)
	}
	if b.MaxTokens > 0 && b.tokens+estTokens > b.MaxTokens {
		return fmt.Errorf("budget exhausted: ~%d tokens would exceed cap of %d",
			b.tokens+estTokens, b.MaxTokens)
	}
	return nil
}

// Charge records a completed call.
func (b *Budget) Charge(tokens int) {
	if b == nil {
		return
	}
	b.spent++
	b.tokens += tokens
}

// Spent reports usage so far.
func (b *Budget) Spent() (calls, tokens int) {
	if b == nil {
		return 0, 0
	}
	return b.spent, b.tokens
}
