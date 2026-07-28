package adapter

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestProcessAliveDetectsExit is the regression guard for a liveness check
// that did not check liveness.
//
// os.FindProcess succeeds on Windows for a process that has already exited,
// so every Claude marker file — which outlives the process that wrote it —
// reported its session as open forever. The tool asserted "open now: 2" about
// sessions that had closed days earlier.
func TestProcessAliveDetectsExit(t *testing.T) {
	cmd := sleeper()
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a helper process: %v", err)
	}
	pid := cmd.Process.Pid

	if !processAlive(pid) {
		t.Fatalf("pid %d reported dead while running", pid)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	cmd.Wait()

	// Exit is not instantaneous; poll rather than sleeping a fixed guess.
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still reported alive 10s after being killed", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestProcessAliveRejectsImplausiblePIDs covers the cheap guards.
func TestProcessAliveRejectsImplausiblePIDs(t *testing.T) {
	for _, pid := range []int{0, -1, -999} {
		if processAlive(pid) {
			t.Errorf("pid %d reported alive", pid)
		}
	}
}

// TestProcessAliveSinceRejectsRecycledPID covers the second failure: PIDs are
// recycled, marker files are not. A process that started after the marker was
// written cannot be the one that wrote it, so it must not resurrect a session
// that has already ended.
func TestProcessAliveSinceRejectsRecycledPID(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("start-time bound is only applied on windows")
	}

	cmd := sleeper()
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a helper process: %v", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	pid := cmd.Process.Pid

	// A marker supposedly written well before this process existed: that is
	// what a recycled PID looks like.
	stale := time.Now().Add(-24 * time.Hour)
	if processAliveSince(pid, stale) {
		t.Errorf("pid %d accepted against a marker written 24h before it started", pid)
	}

	// The same process against a marker written now must still be accepted,
	// or the guard would reject every genuinely live session.
	if !processAliveSince(pid, time.Now()) {
		t.Errorf("pid %d rejected against a current marker", pid)
	}
}

// sleeper returns a command that stays alive long enough to be probed and
// killed, without depending on a shell being present.
func sleeper() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >nul")
	}
	return exec.Command("sleep", "60")
}
