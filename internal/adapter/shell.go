package adapter

import (
	"runtime"
	"strings"
	"time"
)

func isWindows() bool { return runtime.GOOS == "windows" }

// processAlive reports whether a PID belongs to a running process.
//
// Retained for callers with no marker file to date the process against.
// Prefer processAliveSince, which also rejects a recycled PID.
func processAlive(pid int) bool {
	return processAliveSince(pid, time.Time{})
}

// shellQuote quotes a string for the host shell so instructions containing
// spaces or quotes survive being pasted into a terminal.
func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if isWindows() {
		// PowerShell single-quoted string: '' escapes a literal quote.
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CdCmd returns the shell command that walks to a directory, in the host
// shell's dialect.
func CdCmd(dir string) string {
	if isWindows() {
		return "Set-Location " + shellQuote(dir)
	}
	return "cd " + shellQuote(dir)
}

// Joiner returns the separator between the cd and the resume command.
func Joiner() string {
	if isWindows() {
		return "; "
	}
	return " && "
}

// WalkAndResume builds the full one-liner: walk to the workspace, then resume.
// Claude and Copilot key sessions to their original cwd, so the cd is not
// cosmetic — resume fails without it.
func WalkAndResume(dir, resume string) string {
	return CdCmd(dir) + Joiner() + resume
}
