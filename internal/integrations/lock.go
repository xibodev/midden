package integrations

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrSettingsLocked means another Midden process is changing integration
// settings. A read-modify-write transaction must not overlap, or one process
// can silently discard the other process's update.
var ErrSettingsLocked = errors.New("another Midden process is changing integration settings")

// SettingsLock is a kernel-held lock, released automatically if its process
// exits. It protects the complete settings read-modify-write transaction.
type SettingsLock struct {
	file *os.File
}

// AcquireSettingsLock serializes settings changes across UI processes. It
// creates only the settings root and lock file; it never probes a target.
func AcquireSettingsLock(root string) (*SettingsLock, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create integrations root: %w", err)
	}
	path := filepath.Join(root, "integrations.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open integrations lock: %w", err)
	}
	if err := lockSettingsFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("%w: %v", ErrSettingsLocked, err)
	}
	return &SettingsLock{file: file}, nil
}

// Release releases the kernel lock and closes its lock file.
func (l *SettingsLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockSettingsFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
