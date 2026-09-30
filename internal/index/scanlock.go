package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrScanLocked means another CLI or UI process is already collecting source
// stores and applying its result. A second scan must not overlap it: source
// observation order is otherwise ambiguous, and an older list can resurrect
// or delete a newer result.
var ErrScanLocked = errors.New("another index refresh is already running")

// ScanLock is an OS-held advisory lock. Unlike a create-only lock file, the
// kernel releases it when a process crashes, so a dead scan never permanently
// blocks future refreshes.
type ScanLock struct {
	file *os.File
}

// AcquireScanLock serializes the complete scan lifecycle: source collection,
// upsert, reconciliation, and completion marker. It intentionally uses one
// global lock rather than per-tool locks: scans read all adapters serially
// today, and a global lock makes the cross-process invariant obvious.
func (d *DB) AcquireScanLock() (*ScanLock, error) {
	path := filepath.Join(Dir(), "scan.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open scan lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("%w: %v", ErrScanLocked, err)
	}
	return &ScanLock{file: file}, nil
}

// Release must be deferred by every successful AcquireScanLock caller.
func (l *ScanLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
