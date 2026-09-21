package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
)

// PeekCache returns transcript metadata recorded by earlier scans, keyed by
// transcript path, so adapters can avoid re-opening files that have not
// changed.
//
// Only rows that carry a transcript path are useful here; stores that keep
// their metadata in a database describe themselves cheaply already.
func (d *DB) PeekCache() (map[string]adapter.PeekEntry, error) {
	rows, err := d.sql.Query(`
		SELECT transcript, COALESCE(dir,''), COALESCE(title,''),
		       COALESCE(created,0), COALESCE(bytes,0),
		       COALESCE(updated,0), COALESCE(noise,0)
		FROM sessions
		WHERE COALESCE(transcript,'') != '' AND COALESCE(dir,'') != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]adapter.PeekEntry{}
	for rows.Next() {
		var (
			path, dir, title string
			created, bytes   int64
			updated          int64
			noise            int
		)
		if err := rows.Scan(&path, &dir, &title, &created, &bytes, &updated, &noise); err != nil {
			return nil, err
		}
		e := adapter.PeekEntry{
			Cwd:     dir,
			Title:   title,
			Noise:   noise == 1,
			Size:    bytes,
			ModUnix: updated,
		}
		if created > 0 {
			e.Created = time.Unix(created, 0)
		}
		out[path] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// WarmPeekCache installs the index's transcript metadata into the adapter
// layer without creating or migrating state. Only a checkpointed, unchanged
// cache is used; an active journal or other read failure is returned. Missing
// state only costs speed.
func WarmPeekCache() error {
	adapter.SetPeekCache(nil)
	dir, err := filepath.Abs(Dir())
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "core-index.db")
	resolved, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read peek cache: %w", err)
	}
	before, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat peek cache: %w", err)
	}
	if err = checkPeekJournals(path, resolved); err != nil {
		return err
	}
	// Ordinary SQLite mode=ro may create WAL/SHM files. Immutable mode avoids
	// those writes, but must never silently ignore a live journal.
	db, err := openReadOnlyFile(resolved, true)
	if err != nil {
		return fmt.Errorf("read peek cache: %w", err)
	}
	cache, readErr := db.PeekCache()
	if err = errors.Join(readErr, db.Close()); err != nil {
		return fmt.Errorf("read peek cache: %w", err)
	}
	after, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat peek cache: %w", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("peek cache changed during passive warmup; cached hints were not installed")
	}
	if err = checkPeekJournals(path, resolved); err != nil {
		return err
	}
	adapter.SetPeekCache(cache)
	return nil
}

func checkPeekJournals(paths ...string) error {
	for _, path := range paths {
		for _, suffix := range []string{"-wal", "-journal"} {
			if _, err := os.Lstat(path + suffix); err == nil {
				return fmt.Errorf("peek cache has an existing journal; passive warmup requires a checkpointed cache")
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("inspect peek cache journal: %w", err)
			}
		}
	}
	return nil
}
