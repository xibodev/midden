//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile blocks until it holds an exclusive advisory lock on f, as Compa's
// processes take it. The system releases it when f closes or its holder
// exits.
func lockFile(f *os.File) error {
	for {
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != unix.EINTR {
			return err
		}
	}
}

// tryLockFile takes that lock if nobody holds it, and reports whether it did.
func tryLockFile(f *os.File) (bool, error) {
	for {
		switch err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err {
		case nil:
			return true, nil
		case unix.EWOULDBLOCK:
			return false, nil
		case unix.EINTR:
			continue
		default:
			return false, err
		}
	}
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
