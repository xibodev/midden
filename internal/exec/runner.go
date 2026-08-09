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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

	// AllowedDirs bounds Copilot's filesystem access for tool-capable runs.
	// An empty list preserves the legacy all-paths behavior used by existing
	// non-workspace callers.
	AllowedDirs []string

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
		a := r.copilotRuntimeArgs()
		if r.Model != "" {
			a = append(a, "--model", r.Model)
		}
		if r.Pure {
			// An empty MCP config means no servers, so no tool definitions
			// are loaded into context.
			a = append(a, "--additional-mcp-config", emptyMCPConfig)
		}
		return append(a, "--prompt", prompt)

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

func (r *Runner) copilotRuntimeArgs() []string {
	args := []string{
		"--allow-all-tools", "--no-ask-user", "--silent", "--no-color",
		"--output-format", "json", "--no-custom-instructions",
	}
	if r.Pure {
		args = append(args, "--disable-builtin-mcps")
		for _, name := range configuredCopilotMCPServers() {
			args = append(args, "--disable-mcp-server", name)
		}
	}
	if r.Dir != "" {
		args = append(args, "-C", r.Dir)
	}
	if len(r.AllowedDirs) == 0 {
		return append(args, "--allow-all-paths")
	}
	seen := map[string]bool{}
	for _, dir := range r.AllowedDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		args = append(args, "--add-dir", dir)
	}
	return args
}

func configuredCopilotMCPServers() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(home, ".copilot", "mcp-config.json"))
	if err != nil || len(body) > 1<<20 {
		return nil
	}
	var config struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(body, &config) != nil {
		return nil
	}
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// maxInlinePrompt bounds how much prompt text is passed as a command-line
// argument.
//
// Windows caps a command line at 8191 characters, and a salvage prompt
// carrying evidence routinely exceeds that. Beyond this threshold the prompt
// is written to a file and referenced instead, which every backend can read.
const maxInlinePrompt = 5000

// stage writes an oversized or multiline prompt to a file and returns a short
// instruction that points at it, plus a cleanup function. Windows command
// wrappers can truncate an argv value at the first newline even when the total
// prompt is small.
func (r *Runner) stage(prompt string) (string, func(), error) {
	noop := func() {}
	if len(prompt) <= maxInlinePrompt && !strings.ContainsAny(prompt, "\r\n") {
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
	if r.Backend == Copilot {
		res.Output = copilotFinalAnswer(res.Output)
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

func copilotFinalAnswer(output string) string {
	final := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Data struct {
				Content string `json:"content"`
				Phase   string `json:"phase"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.Type == "assistant.message" &&
			(event.Data.Phase == "" || event.Data.Phase == "final_answer") &&
			strings.TrimSpace(event.Data.Content) != "" {
			final = strings.TrimSpace(event.Data.Content)
		}
	}
	if final != "" {
		return final
	}
	return strings.TrimSpace(output)
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

// cliChrome matches the framing an AI CLI prints around its answer: tool-call
// echoes from reading a staged prompt, and the usage footer.
//
// Without this, a summary or answer arrives wrapped in "I'll read the file
// first", a Read tool echo, and a credits table — none of which the operator
// asked for, and all of which would be written into saved artifacts.
var cliChrome = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^\s*[●○*]\s*Read\s+midden-prompt-[^\n]*\n(?:\s*[│└|].*\n)*`),
	regexp.MustCompile(`(?mi)^\s*(?:I'll|I will|Let me|Reading|First,? I'll)\s+(?:now\s+)?read\s+[^\n]*\n+`),
	regexp.MustCompile(`(?ms)^\s*Changes\s+\+\d+\s+-\d+\s*\n.*?^\s*Resume\s+.*$`),
	regexp.MustCompile(`(?m)^\s*(?:AI Credits|Tokens|Resume)\s{2,}[^\n]*\n`),
	regexp.MustCompile(`(?m)^\s*Changes\s{2,}[^\n]*\n`),
}

// CleanOutput removes CLI framing from a model response.
func CleanOutput(s string) string {
	for _, re := range cliChrome {
		s = re.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
}
