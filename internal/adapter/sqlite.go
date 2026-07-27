package adapter

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openRO opens a SQLite database read-only.
//
// Every source store belongs to a tool that may be running right now, so
// Midden never opens one writable. When a live WAL prevents a read-only open
// (SQLite needs to build the -shm index), fall back to a temp-copy of the
// db/-wal/-shm triple rather than degrading to a writable handle.
func openRO(path string) (*sql.DB, func(), error) {
	noop := func() {}

	if _, err := os.Stat(path); err != nil {
		return nil, noop, fmt.Errorf("store not found: %w", err)
	}

	db, err := sql.Open("sqlite", roDSN(path))
	if err == nil {
		if err = db.Ping(); err == nil {
			if _, err = db.Exec("SELECT 1"); err == nil {
				return db, func() { db.Close() }, nil
			}
		}
		db.Close()
	}

	// Fall back to a private snapshot.
	tmpDir, terr := os.MkdirTemp("", "midden-ro-")
	if terr != nil {
		return nil, noop, fmt.Errorf("read-only open failed (%v) and no temp dir: %w", err, terr)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	tmp := filepath.Join(tmpDir, filepath.Base(path))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if cerr := copyFile(path+suffix, tmp+suffix); cerr != nil && suffix == "" {
			cleanup()
			return nil, noop, fmt.Errorf("snapshot failed: %w", cerr)
		}
	}

	db, err = sql.Open("sqlite", roDSN(tmp))
	if err != nil {
		cleanup()
		return nil, noop, err
	}
	if err = db.Ping(); err != nil {
		db.Close()
		cleanup()
		return nil, noop, err
	}
	return db, func() { db.Close(); cleanup() }, nil
}

// roDSN builds a read-only SQLite URI. Windows paths need forward slashes and
// escaped spaces to survive URI parsing.
func roDSN(path string) string {
	p := filepath.ToSlash(path)
	p = strings.ReplaceAll(p, " ", "%20")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
