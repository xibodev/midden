package index

import (
	"fmt"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// Sessions reads the indexed session list.
//
// This exists because re-deriving the list from the source stores is
// expensive: it means opening a 200 MB+ SQLite file and peeking inside every
// transcript on disk to recover its working directory and title. That is
// acceptable once, during a scan. Doing it on every request made the web UI
// appear to hang — which is precisely the failure the index was built to
// prevent, and which went unnoticed because the CLI always passed a narrow
// scope while the UI did not.
func (d *DB) Sessions(sc core.Scope) ([]core.Session, error) {
	var (
		where []string
		args  []any
	)

	if len(sc.Tools) > 0 {
		var holes []string
		for _, t := range sc.Tools {
			holes = append(holes, "?")
			args = append(args, string(t))
		}
		where = append(where, "tool IN ("+strings.Join(holes, ",")+")")
	}
	if !sc.IncludeNoise {
		where = append(where, "noise = 0")
	}
	if cutoff := sc.Since(); !cutoff.IsZero() {
		where = append(where, "updated >= ?")
		args = append(args, cutoff.Unix())
	}
	if sc.Workspace != "" {
		where = append(where, "LOWER(dir) LIKE ?")
		args = append(args, "%"+strings.ToLower(sc.Workspace)+"%")
	}
	if sc.Repo != "" {
		where = append(where, "LOWER(COALESCE(repo,'')) LIKE ?")
		args = append(args, "%"+strings.ToLower(sc.Repo)+"%")
	}
	if sc.IDPrefix != "" {
		where = append(where, "id LIKE ?")
		args = append(args, sc.IDPrefix+"%")
	}

	q := `SELECT tool,id,dir,COALESCE(title,''),COALESCE(repo,''),
	      COALESCE(created,0),COALESCE(updated,0),COALESCE(turns,0),
	      COALESCE(bytes,0),COALESCE(noise,0),COALESCE(transcript,'')
	      FROM sessions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY updated DESC"
	if sc.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", sc.Limit)
	}

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []core.Session
	for rows.Next() {
		var (
			s                core.Session
			tool             string
			created, updated int64
			noise            int
		)
		if err := rows.Scan(&tool, &s.ID, &s.Dir, &s.Title, &s.Repo,
			&created, &updated, &s.Turns, &s.Bytes, &noise, &s.TranscriptPath); err != nil {
			return nil, err
		}
		s.Tool = core.Tool(tool)
		s.Noise = noise == 1
		if created > 0 {
			s.Created = time.Unix(created, 0)
		}
		if updated > 0 {
			s.Updated = time.Unix(updated, 0)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SessionCount reports how many sessions are indexed, which is how a caller
// decides whether the index is usable or needs a first scan.
func (d *DB) SessionCount() int {
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	return n
}

// IndexedAt is when the index was last refreshed, so staleness can be shown
// rather than hidden.
func (d *DB) IndexedAt() time.Time {
	var n int64
	if err := d.sql.QueryRow(`SELECT MAX(seen_at) FROM sessions`).Scan(&n); err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
