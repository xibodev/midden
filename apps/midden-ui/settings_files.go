package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The kernel's settings files are Compa's own: config.json, auth.json and
// model_catalogs.json in the kernel's home. Every process that changes one,
// the kernel and its CLI included, holds the OS lock on <file>.flock beside
// it and replaces the file by renaming a complete copy over it. Midden keeps
// to the same protocol, so it never interleaves with the kernel.

// fileLocks serializes this process first: some platforms grant an OS lock
// per process rather than per open file.
var fileLocks sync.Map // cleaned path -> *sync.Mutex

// withFileLock runs fn while holding the lock of path.
func withFileLock(path string, fn func() error) error {
	mu, _ := fileLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".flock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open the lock of %s: %w", filepath.Base(path), err)
	}
	defer lock.Close()
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("lock %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = unlockFile(lock) }()
	return fn()
}

// replaceFile writes data to a private file beside path and renames it over
// path, so a reader sees the old content or the new, never a part.
func replaceFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := renameWithRetry(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

// renameFile is os.Rename; tests replace it.
var renameFile = os.Rename

// renameWithRetry renames from onto to. On Windows a reader that holds to
// open makes the rename fail for a few milliseconds.
func renameWithRetry(from, to string) error {
	var err error
	for attempt := 0; attempt < 40; attempt++ {
		if err = renameFile(from, to); err == nil || errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}
