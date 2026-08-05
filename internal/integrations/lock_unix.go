//go:build !windows

package integrations

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockSettingsFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockSettingsFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
