package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	coreStdoutLimit = 64 << 10
	coreStderrLimit = 8 << 10
	coreMaxArgs     = 100
	coreMaxArgBytes = 32768
)

// coreSlots bounds concurrent core processes started through either door.
var coreSlots = make(chan struct{}, 8)

// coreOutput is one finished core call. ExitCode is -1 when the process did
// not start or did not exit normally.
type coreOutput struct {
	Stdout        string
	Stderr        string
	ExitCode      int
	StdoutClipped bool
	StderrClipped bool
}

// runCore runs the core in the workspace with the host's core state and the
// launch-time source roots. The error is the process error, if any.
func (a *App) runCore(ctx context.Context, args []string) (coreOutput, error) {
	result := coreOutput{ExitCode: -1}
	select {
	case coreSlots <- struct{}{}:
		defer func() { <-coreSlots }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	cmd := exec.CommandContext(ctx, a.opts.Core, args...)
	cmd.Dir = a.opts.Workspace
	cmd.Env = a.coreEnv()
	cmd.WaitDelay = 5 * time.Second
	stdout := boundedOutput{max: coreStdoutLimit}
	stderr := boundedOutput{max: coreStderrLimit}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	result.StdoutClipped, result.StderrClipped = stdout.clipped, stderr.clipped
	return result, err
}

// coreEnv drops inherited MIDDEN_* settings; the host supplies its own.
func (a *App) coreEnv() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "MIDDEN_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "MIDDEN_HOME="+filepath.Join(a.opts.State, "core"))
	for key, value := range a.opts.SourceEnv {
		env = append(env, key+"="+value)
	}
	return env
}

var (
	coreCommands = map[string]bool{"ls": true, "list": true, "find": true, "show": true, "read": true, "search": true, "collect": true,
		"collection": true, "assets": true, "brief": true, "assay": true, "usage": true, "resume": true, "help": true, "--help": true, "version": true}
	coreOperations = map[string]bool{"inspect": true, "read": true, "search": true, "select": true, "merge": true, "verify": true, "export": true}
	coreSwitches   = map[string]bool{"--json": true, "--all": true, "--live": true, "--assets": true, "--include-tools": true, "--help": true,
		"--h": true, "--group": true, "--sizes": true, "--handoff": true}
	coreValueFlags = map[string]bool{"--tool": true, "--session": true, "--query": true, "--record": true, "--view": true, "--limit": true,
		"--offset": true, "--chars": true, "--before": true, "--after": true, "--format": true, "--out": true, "--days": true,
		"--workspace": true, "--repo": true, "--top": true, "--turns": true, "--clip": true, "--scan": true, "--quote": true}
	coreHostFlags = map[string]bool{"--state": true, "--copilot-root": true, "--claude-root": true, "--opencode-db": true, "--sources-only": true}
)

// coreCall is a parsed core argument list.
type coreCall struct {
	command    string
	operation  string              // collection operation
	positional []string            // after the command and operation
	flags      map[string][]string // canonical --name to values; switches record their attached value
}

func (c coreCall) has(name string) bool {
	_, ok := c.flags[name]
	return ok
}

// parseCoreArgs reads arguments as the core's flag parsing does: a value flag
// consumes the next token, which is then never read as a flag.
func parseCoreArgs(args []string) (coreCall, error) {
	if len(args) == 0 || len(args) > coreMaxArgs {
		return coreCall{}, fmt.Errorf("provide 1..100 CLI arguments")
	}
	total := 0
	for _, arg := range args {
		total += len(arg)
	}
	if total > coreMaxArgBytes {
		return coreCall{}, fmt.Errorf("arguments exceed size limit")
	}
	call := coreCall{command: args[0], flags: map[string][]string{}}
	if !coreCommands[call.command] {
		return coreCall{}, fmt.Errorf("command is not available through the data-only host tool")
	}
	rest := args[1:]
	if call.command == "collection" {
		if len(rest) == 0 {
			return coreCall{}, fmt.Errorf("collection requires an operation: inspect, read, search, select, merge, verify or export")
		}
		call.operation, rest = rest[0], rest[1:]
		if !coreOperations[call.operation] && call.operation != "--help" && call.operation != "-h" {
			return coreCall{}, fmt.Errorf("collection operation must be inspect, read, search, select, merge, verify or export")
		}
	}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if !strings.HasPrefix(arg, "-") {
			call.positional = append(call.positional, arg)
			continue
		}
		if arg == "--" {
			return coreCall{}, fmt.Errorf("argument separator is not supported by the restricted core tool")
		}
		name, value, attached := strings.Cut(arg, "=")
		name = "--" + strings.TrimLeft(name, "-")
		switch {
		case coreHostFlags[name]:
			return coreCall{}, fmt.Errorf("source/state bindings are controlled by the host")
		case coreSwitches[name] && !(attached && name == "--h"):
		case coreValueFlags[name]:
			if !attached {
				if i+1 >= len(rest) {
					return coreCall{}, fmt.Errorf("flag %s requires a value", arg)
				}
				i++
				value = rest[i]
			}
		default:
			return coreCall{}, fmt.Errorf("unsupported core flag %s; inspect the command help", name)
		}
		call.flags[name] = append(call.flags[name], value)
	}
	return call, nil
}

// validateCoreArgs is the one validator for both doors: allowlisted commands
// and flags, host-owned bindings, collection paths confined to the workspace
// and new output destinations inside it.
func (a *App) validateCoreArgs(args []string) error {
	call, err := parseCoreArgs(args)
	if err != nil {
		return err
	}
	for _, out := range call.flags["--out"] {
		if err := a.newDestination(out); err != nil {
			return err
		}
	}
	if call.command == "collection" {
		for _, path := range call.positional {
			if err := a.writePath(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// newDestination accepts only a path inside the workspace that does not exist yet.
func (a *App) newDestination(name string) error {
	if err := a.writePath(name); err != nil {
		return err
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.opts.Workspace, path)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("output destination already exists; choose a new path")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("output destination cannot be created")
	}
	return nil
}

type boundedOutput struct {
	bytes.Buffer
	max     int
	clipped bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.max - b.Len()
	if remaining < n {
		b.clipped = true
		p = p[:max(remaining, 0)]
	}
	_, err := b.Buffer.Write(p)
	return n, err
}
