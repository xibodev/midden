package index

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
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
		where = append(where, `LOWER(dir) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(strings.ToLower(sc.Workspace))+"%")
	}
	if sc.Repo != "" {
		where = append(where, `LOWER(COALESCE(repo,'')) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(strings.ToLower(sc.Repo))+"%")
	}
	if sc.IDPrefix != "" {
		where = append(where, `id LIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(sc.IDPrefix)+"%")
	}
	if len(sc.IDs) > 0 {
		holes := make([]string, len(sc.IDs))
		for i, id := range sc.IDs {
			holes[i] = "?"
			args = append(args, id)
		}
		where = append(where, "id IN ("+strings.Join(holes, ",")+")")
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

// SessionSnapshot is one consistent read of indexed sessions and their
// authoritative freshness markers. The UI must render both from the same
// SQLite read transaction: fetching sessions first and a marker later can
// label old rows "indexed just now" when another process scans in between.
type SessionSnapshot struct {
	Sessions      []core.Session
	IndexedAt     time.Time
	ToolIndexedAt map[core.Tool]time.Time
}

// ReadSessionSnapshot returns every indexed session (including automated
// ones) and its freshness markers from one read-only transaction.
func (d *DB) ReadSessionSnapshot() (SessionSnapshot, error) {
	var snap SessionSnapshot
	tx, err := d.sql.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return snap, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT tool,id,dir,COALESCE(title,''),COALESCE(repo,''),
		       COALESCE(created,0),COALESCE(updated,0),COALESCE(turns,0),
		       COALESCE(bytes,0),COALESCE(noise,0),COALESCE(transcript,'')
		FROM sessions ORDER BY updated DESC`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var (
			s                core.Session
			tool             string
			created, updated int64
			noise            int
		)
		if err := rows.Scan(&tool, &s.ID, &s.Dir, &s.Title, &s.Repo,
			&created, &updated, &s.Turns, &s.Bytes, &noise, &s.TranscriptPath); err != nil {
			rows.Close()
			return snap, err
		}
		s.Tool = core.Tool(tool)
		s.Noise = noise == 1
		if created > 0 {
			s.Created = time.Unix(created, 0)
		}
		if updated > 0 {
			s.Updated = time.Unix(updated, 0)
		}
		snap.Sessions = append(snap.Sessions, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return snap, err
	}
	rows.Close()

	snap.ToolIndexedAt = map[core.Tool]time.Time{}
	markers, err := tx.Query(`
		SELECT key,value FROM meta
		WHERE key = ? OR key LIKE ?`, indexedAllKey, indexedToolPrefix+"%")
	if err != nil {
		return snap, err
	}
	for markers.Next() {
		var key, raw string
		if err := markers.Scan(&key, &raw); err != nil {
			markers.Close()
			return snap, err
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		at := time.Unix(n, 0)
		if key == indexedAllKey {
			snap.IndexedAt = at
		} else if tool := strings.TrimPrefix(key, indexedToolPrefix); tool != "" {
			snap.ToolIndexedAt[core.Tool(tool)] = at
		}
	}
	if err := markers.Err(); err != nil {
		markers.Close()
		return snap, err
	}
	markers.Close()
	if err := tx.Commit(); err != nil {
		return snap, err
	}
	return snap, nil
}

// SessionCount reports how many sessions are indexed, which is how a caller
// decides whether the index is usable or needs a first scan.
func (d *DB) SessionCount() int {
	var n int
	d.sql.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	return n
}

// IndexedTools returns the source tools represented by the current index.
// Scan uses it to avoid calling an all-tool view fresh when an unavailable
// source still has rows in the cache.
func (d *DB) IndexedTools() []core.Tool {
	rows, err := d.sql.Query(`SELECT DISTINCT tool FROM sessions`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []core.Tool
	for rows.Next() {
		var tool string
		if rows.Scan(&tool) == nil {
			out = append(out, core.Tool(tool))
		}
	}
	return out
}

// IndexedAt is the newest row timestamp in the session cache.
//
// It is useful for diagnostics but not an authoritative freshness claim: a
// scoped or partial scan can update one row "now" while other tools are days
// stale. UI freshness must use AuthoritativeIndexedAt instead.
func (d *DB) IndexedAt() time.Time {
	var n int64
	if err := d.sql.QueryRow(`SELECT MAX(seen_at) FROM sessions`).Scan(&n); err != nil || n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

const (
	indexedAllKey          = "indexed_at:all"
	indexedToolPrefix      = "indexed_at:"
	completedGenToolPrefix = "completed_generation:"
	completedGenAllKey     = "completed_generation:all"
)

// MarkIndexed records that a complete, error-free scan finished for tools at
// generation. A full all-tool scan also marks the aggregate key.
func (d *DB) MarkIndexed(tools []core.Tool, scanGeneration int64, indexedAt time.Time, all bool) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := d.markIndexedTx(tx, tools, scanGeneration, indexedAt, all); err != nil {
		return err
	}
	return tx.Commit()
}

// markIndexedTx records completion only when this generation is not older
// than the marker it would replace. It is shared by ReconcileAndMark so the
// completion fence commits atomically with deletion.
func (d *DB) markIndexedTx(tx *sql.Tx, tools []core.Tool, scanGeneration int64, indexedAt time.Time, all bool) error {
	put := func(key string, value string) error {
		_, err := tx.Exec(`
			INSERT INTO meta (key,value) VALUES (?,?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		return err
	}
	mark := func(genKey, timeKey string) error {
		current, err := completedGenerationKey(tx, genKey)
		if err != nil {
			return err
		}
		if scanGeneration < current {
			return nil
		}
		if err := put(genKey, fmt.Sprintf("%d", scanGeneration)); err != nil {
			return err
		}
		return put(timeKey, fmt.Sprintf("%d", indexedAt.Unix()))
	}
	for _, tool := range uniqueTools(tools) {
		if err := mark(completedGenToolPrefix+string(tool), indexedToolPrefix+string(tool)); err != nil {
			return err
		}
	}
	if all {
		if err := mark(completedGenAllKey, indexedAllKey); err != nil {
			return err
		}
	}
	return nil
}

// AuthoritativeIndexedAt returns the timestamp of the last complete,
// error-free scan that covered this view. An empty tool list means the all-tool
// view; a scoped view must have its own per-tool marker rather than borrowing
// a newer row from an unrelated source.
func (d *DB) AuthoritativeIndexedAt(tools []core.Tool) time.Time {
	key := indexedAllKey
	if len(tools) == 1 {
		key = indexedToolPrefix + string(tools[0])
	}
	var raw string
	if err := d.sql.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&raw); err != nil {
		return time.Time{}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

// CompletedGeneration returns the newest complete scan generation for one
// tool. It is the fence that stops a slower older scan from writing sessions
// after a newer complete scan has already proved them absent.
func completedGeneration(tx *sql.Tx, tool core.Tool) (int64, error) {
	return completedGenerationKey(tx, completedGenToolPrefix+string(tool))
}

func completedGenerationKey(tx *sql.Tx, key string) (int64, error) {
	var raw string
	err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse completed generation %s: %w", key, err)
	}
	return n, nil
}
