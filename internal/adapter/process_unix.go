//go:build !windows

package adapter

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// processAliveSince reports whether pid belongs to a running process.
//
// os.FindProcess always succeeds on Unix, so liveness needs an explicit
// signal-0 probe. The start-time bound that guards against PID recycling on
// Windows has no cheap portable equivalent here, so it is not applied: a
// recycled PID is a much narrower window on systems with a large PID space.
func processAliveSince(pid int, _ time.Time) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer p.Release()
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
