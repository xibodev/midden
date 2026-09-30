package adapter

import (
	"syscall"
	"time"
)

// stillActive is the exit code Windows reports for a process that has not
// exited yet.
const stillActive = 259

// processAliveSince reports whether pid belongs to a process that is running
// now and started no later than notAfter.
//
// os.FindProcess is not a liveness check on Windows. It succeeds for a process
// that has already exited — verified by spawning one, killing it, and watching
// FindProcess keep returning success — so the marker files Claude leaves
// behind made closed sessions look open indefinitely.
//
// The start-time bound catches the second failure: PIDs are recycled, marker
// files are not. Without it, an unrelated process that happens to inherit an
// old PID resurrects a session that ended days ago. A process that started
// after the marker was written cannot be the one that wrote it.
func processAliveSince(pid int, notAfter time.Time) bool {
	if pid <= 0 {
		return false
	}

	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means the process exists but belongs to someone else,
		// which is not a session of ours to report.
		return false
	}
	defer syscall.CloseHandle(h)

	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	if code != stillActive {
		return false
	}

	if notAfter.IsZero() {
		return true
	}

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		// Liveness is established; only recycling is unproven.
		return true
	}
	// A small grace window absorbs clock granularity between the process
	// starting and the marker file being written.
	return time.Unix(0, creation.Nanoseconds()).Before(notAfter.Add(2 * time.Second))
}
