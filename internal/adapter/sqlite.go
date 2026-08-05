package adapter

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openRO opens a SQLite database read-only.
//
// Every source store belongs to a tool that may be running right now, so
// Midden never opens one writable. When a live WAL prevents a read-only open
// (SQLite needs to build the -shm index), report an incomplete read rather
// than making a raw db/WAL copy. Copying the main DB before a concurrent
// checkpoint and then failing to copy WAL can produce a coherent *old*
// snapshot that silently omits sessions. That is unsafe once a complete scan
// can reconcile-delete rows absent from the result.
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

	return nil, noop, fmt.Errorf("read-only open failed: %w; retry after the source CLI is idle", err)
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
