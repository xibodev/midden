// Package exec runs prompts through the AI CLIs already installed and
// authenticated on this machine.
//
// Midden never calls a model API directly. It shells out to copilot, claude or
// opencode, which means:
//
//   - no API keys to manage; it uses seats already paid for
//   - subscription economics rather than per-token list pricing
//   - rate-limit fallback is handled natively by those tools
//
// Two consequences shape this package:
//
//  1. Separate invocations do not share a prompt cache. Cache writes dominate
//     cost, so batching must happen INSIDE one session (Conversation), never
//     across N shell-outs.
//  2. Salvage only reads text, so MCP servers and skills are stripped from
//     these invocations. Loading tool definitions on every launch is pure
//     waste and, on a heavily configured machine, the largest single cost.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Backend is one AI CLI Midden can drive.
type Backend string

const (
	Copilot  Backend = "copilot"
	Claude   Backend = "claude"
	Opencode Backend = "opencode"
)

// Runner executes prompts against a backend.
type Runner struct {
	Backend Backend
	Model   string
	Dir     string
	Timeout time.Duration

	// Pure strips MCP servers and plugins. On by default for salvage.
	Pure bool

	// DryRun prints what would run without invoking anything.
	DryRun bool
}

// Available reports which backends are installed.
func Available() []Backend {
	var out []Backend
	for _, b := range []Backend{Copilot, Claude, Opencode} {
		if _, err := exec.LookPath(string(b)); err == nil {
			out = append(out, b)
		}
	}
	return out
}

// Detect picks a backend, preferring an explicit choice, then the first
// installed CLI.
func Detect(preferred string) (Backend, error) {
	avail := Available()
	if len(avail) == 0 {
		return "", fmt.Errorf("no AI CLI found on PATH (looked for copilot, claude, opencode)")
	}
	if preferred != "" {
		for _, b := range avail {
			if string(b) == preferred {
				return b, nil
			}
		}
		return "", fmt.Errorf("%q is not installed (available: %s)", preferred, join(avail))
	}
	return avail[0], nil
}

func join(bs []Backend) string {
	s := make([]string, len(bs))
	for i, b := range bs {
		s[i] = string(b)
	}
	return strings.Join(s, ", ")
}

// Result is one completed invocation.
type Result struct {
	Output   string        `json:"output"`
	Backend  Backend       `json:"backend"`
	Model    string        `json:"model"`
	Command  string        `json:"command"`
	Elapsed  time.Duration `json:"elapsed"`
	ExitCode int           `json:"exit_code"`
}

// argv builds the non-interactive invocation for a backend.
func (r *Runner) argv(prompt string) []string {
	switch r.Backend {
	case Copilot:
		a := []string{"--prompt", prompt, "--allow-all-tools", "--allow-all-paths"}
		if r.Model != "" {
			a = append(a, "--model", r.Model)
		}
		if r.Pure {
			// An empty MCP config means no servers, so no tool definitions
			// are loaded into context.
			a = append(a, "--additional-mcp-config", emptyMCPConfig)
		}
		return a

	case Claude:
		a := []string{"--print", prompt}
		if r.Model != "" {
			a = append(a, "--model", r.Model)
		}
		return a

	case Opencode:
		a := []string{"run"}
		if r.Model != "" {
			a = append(a, "--model", r.Model)
		}
		if r.Pure {
			a = append(a, "--pure")
		}
		if r.Dir != "" {
			a = append(a, "--dir", r.Dir)
		}
		return append(a, prompt)
	}
	return nil
}

// maxInlinePrompt bounds how much prompt text is passed as a command-line
// argument.
//
// Windows caps a command line at 8191 characters, and a salvage prompt
// carrying evidence routinely exceeds that. Beyond this threshold the prompt
// is written to a file and referenced instead, which every backend can read.
const maxInlinePrompt = 5000

// stage writes an oversized prompt to a file and returns a short instruction
// that points at it, plus a cleanup function.
func (r *Runner) stage(prompt string) (string, func(), error) {
	noop := func() {}
	if len(prompt) <= maxInlinePrompt {
		return prompt, noop, nil
	}

	f, err := os.CreateTemp("", "midden-prompt-*.md")
	if err != nil {
		return "", noop, fmt.Errorf("stage prompt: %w", err)
	}
	path := f.Name()
	if _, err := f.WriteString(prompt); err != nil {
		f.Close()
		os.Remove(path)
		return "", noop, fmt.Errorf("write prompt: %w", err)
	}
	f.Close()

	ref := fmt.Sprintf(
		"Read the file %s and follow the instructions it contains exactly. "+
			"Output only what those instructions ask for: no preamble, no commentary, "+
			"no code fences.", path)

	return ref, func() { os.Remove(path) }, nil
}

// Run executes a single prompt.
//
// Prefer Conversation for multi-step work: each Run is a fresh process with a
// cold cache, and paying the cache-write cost repeatedly is the single easiest
// way to make salvage expensive.
func (r *Runner) Run(ctx context.Context, prompt string) (*Result, error) {
	staged, cleanup, err := r.stage(prompt)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := r.argv(staged)
	if args == nil {
		return nil, fmt.Errorf("unsupported backend %q", r.Backend)
	}

	display := string(r.Backend) + " " + summarise(args)
	if r.DryRun {
		return &Result{Backend: r.Backend, Model: r.Model, Command: display,
			Output: "[dry run — nothing was invoked]"}, nil
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, string(r.Backend), args...)
	if r.Dir != "" && r.Backend != Opencode {
		cmd.Dir = r.Dir
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	res := &Result{
		Output:  strings.TrimSpace(stdout.String()),
		Backend: r.Backend,
		Model:   r.Model,
		Command: display,
		Elapsed: time.Since(start),
	}

	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("%s timed out after %s", r.Backend, timeout)
		}
		if res.Output == "" {
			return res, fmt.Errorf("%s failed: %w: %s", r.Backend, err,
				truncate(strings.TrimSpace(stderr.String()), 300))
		}
		// Non-zero exit with output usually means a soft failure; keep what
		// was produced rather than discarding useful work.
	}
	return res, nil
}

// summarise renders an argv for display without dumping an entire prompt.
func summarise(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if len(a) > 60 {
			out = append(out, fmt.Sprintf("<prompt %d chars>", len(a)))
			continue
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// emptyMCPConfig disables every MCP server for an invocation.
//
// Salvage only reads text, so loading tool definitions is pure waste. On a
// heavily configured machine this is the largest single per-invocation saving
// available. Copilot requires the mcpServers key to be present even when it is
// empty.
const emptyMCPConfig = `{"mcpServers":{}}`
