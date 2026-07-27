package adapter

import (
	"os"
	"runtime"
	"strings"
)

func isWindows() bool { return runtime.GOOS == "windows" }

// processAlive reports whether a PID belongs to a running process.
//
// os.FindProcess always succeeds on Unix, so liveness needs an explicit
// signal-0 probe there. On Windows a failed lookup is conclusive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if isWindows() {
		return true
	}
	return p.Signal(nil) == nil
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
