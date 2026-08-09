package adapter

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// Opencode reads opencode sessions.
//
// IMPORTANT: `opencode session list` only returns sessions for the CURRENT
// project and silently hides every other project's work — on one machine it
// returned 19 of 704 sessions, concealing an entire active project. The
// database is therefore the only honest source.
type Opencode struct {
	DB string
}

func NewOpencode() *Opencode {
	return &Opencode{DB: homeJoin(".local", "share", "opencode", "opencode.db")}
}

func (o *Opencode) Tool() core.Tool { return core.ToolOpencode }

func (o *Opencode) Available() bool { return exists(o.DB) }

// Footprint covers the opencode data directory: database, snapshots, logs and
// tool output.
func (o *Opencode) Footprint() int64 { return dirSize(filepath.Dir(o.DB)) }

// sizes returns per-session transcript bytes.
//
// opencode stores content in the `part` table rather than as files, so this
// requires a full aggregate scan — measured at ~190s over 341k rows. It is
// opt-in (Scope.WithSizes) and must never run on a default path.
func (o *Opencode) sizes(db *sql.DB) map[string]int64 {
	out := map[string]int64{}
	rows, err := db.Query(`
		SELECT session_id, SUM(LENGTH(COALESCE(data, '')))
		FROM part GROUP BY session_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int64
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out
}

func (o *Opencode) Sessions(sc core.Scope) ([]core.Session, error) {
	db, closeDB, err := openRO(o.DB)
	if err != nil {
		return nil, fmt.Errorf("opencode: %w", err)
	}
	defer closeDB()

	// parent_id IS NULL drops sub-agent/child sessions, which outnumber real
	// top-level work roughly 30:1 and are not independently resumable.
	const q = `
		SELECT s.id,
		       COALESCE(s.directory, ''),
		       COALESCE(s.title, ''),
		       s.time_created,
		       s.time_updated,
		       COALESCE(s.time_archived, 0),
		       (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id) AS msgs
		FROM session s
		WHERE s.parent_id IS NULL
		  AND s.directory IS NOT NULL AND TRIM(s.directory) != ''
		ORDER BY s.time_updated DESC`

	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("opencode query: %w", err)
	}
	defer rows.Close()

	var sizes map[string]int64
	if sc.WithSizes {
		sizes = o.sizes(db)
	}

	var out []core.Session
	for rows.Next() {
		var (
			id, dir, title       string
			createdMS, updatedMS int64
			archivedMS           int64
			msgs                 int
		)
		if err := rows.Scan(&id, &dir, &title, &createdMS, &updatedMS, &archivedMS, &msgs); err != nil {
			return nil, err
		}
		if !sc.WantsID(id) {
			continue
		}

		display := core.CleanTitle(title)
		if archivedMS > 0 {
			display = "[archived] " + display
		}

		s := core.Session{
			Tool:    core.ToolOpencode,
			ID:      id,
			Dir:     normaliseDir(dir),
			Title:   display,
			Created: fromUnixMS(createdMS),
			Updated: fromUnixMS(updatedMS),
			Turns:   msgs,
		}
		s.Noise = core.IsNoise(title, s.Dir, msgs)
		if sizes != nil {
			s.Bytes = sizes[id]
		}

		if sc.Match(s) {
			out = append(out, s)
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	// Child/sub-agent sessions are intentionally excluded: they are not
	// independently resumable and were never indexed as top-level work. A
	// top-level session with no directory is different: it is a real source
	// row omitted by this adapter's resumability filter, so a reconciler must
	// not mistake it for deletion.
	var skipped int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM session
		WHERE parent_id IS NULL
		  AND (directory IS NULL OR TRIM(directory) = '')`).Scan(&skipped); err != nil {
		return out, fmt.Errorf("opencode completeness: %w", err)
	}
	if skipped > 0 {
		return out, fmt.Errorf("opencode: skipped %d top-level session(s) without a directory; result is partial", skipped)
	}
	return out, nil
}

func (o *Opencode) ResumeCmd(s core.Session, instruction string) string {
	if instruction != "" {
		// `run` delivers a message to an existing session non-interactively.
		return fmt.Sprintf("opencode run --session %s %s", s.ID, shellQuote(instruction))
	}
	return "opencode --session " + s.ID
}

func fromUnixMS(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).Local()
}

// normaliseDir converts the forward-slash paths opencode stores into the
// host's native separator so directory checks and cd commands work.
func normaliseDir(dir string) string {
	d := strings.TrimSpace(dir)
	if d == "" {
		return ""
	}
	if isWindows() {
		d = strings.ReplaceAll(d, "/", `\`)
	}
	return d
}
