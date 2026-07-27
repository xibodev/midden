// Package index is Midden's own store: a cache of what has been measured, so
// that scanning 36 GiB happens once rather than on every invocation.
//
// This database is the only one Midden ever writes to. Every source store
// belongs to another tool and is strictly read-only.
package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

// DB is Midden's index.
type DB struct {
	sql  *sql.DB
	path string
}

// Dir is where Midden keeps its own data.
func Dir() string {
	if v := os.Getenv("MIDDEN_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".midden"
	}
	return filepath.Join(home, ".midden")
}

// Path is the index database location.
func Path() string { return filepath.Join(Dir(), "index.db") }

// Open creates or opens the index, applying the schema.
func Open() (*DB, error) {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", Dir(), err)
	}

	p := filepath.ToSlash(Path())
	// WAL keeps reads working while a scan writes.
	dsn := "file:" + p + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)"

	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := sdb.Ping(); err != nil {
		sdb.Close()
		return nil, err
	}

	db := &DB{sql: sdb, path: Path()}
	if err := db.migrate(); err != nil {
		sdb.Close()
		return nil, err
	}
	return db, nil
}

func (d *DB) Close() error { return d.sql.Close() }
func (d *DB) Path() string { return d.path }
func (d *DB) SQL() *sql.DB { return d.sql }

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  tool           TEXT NOT NULL,
  id             TEXT NOT NULL,
  dir            TEXT NOT NULL,
  title          TEXT,
  repo           TEXT,
  created        INTEGER,
  updated        INTEGER,
  turns          INTEGER,
  bytes          INTEGER,
  noise          INTEGER DEFAULT 0,
  transcript     TEXT,
  seen_at        INTEGER NOT NULL,
  PRIMARY KEY (tool, id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_dir     ON sessions(dir);
CREATE INDEX IF NOT EXISTS idx_sessions_bytes   ON sessions(bytes DESC);

-- One manifest per session. source_bytes and source_mtime let a scan skip
-- sessions whose transcript has not changed since it was last assayed.
CREATE TABLE IF NOT EXISTS manifests (
  tool          TEXT NOT NULL,
  id            TEXT NOT NULL,
  total_records INTEGER,
  total_bytes   INTEGER,
  signal_bytes  INTEGER,
  exhaust_bytes INTEGER,
  artifact_bytes INTEGER,
  book_bytes    INTEGER,
  dup_reads     INTEGER,
  dup_bytes     INTEGER,
  images        INTEGER,
  image_clusters INTEGER,
  by_kind       TEXT,
  source_bytes  INTEGER,
  source_mtime  INTEGER,
  assayed_at    INTEGER NOT NULL,
  PRIMARY KEY (tool, id)
);

-- Nuggets are the reclaimed value. Provenance is mandatory: every nugget
-- names the session and the model that produced it.
CREATE TABLE IF NOT EXISTS nuggets (
  uid         TEXT PRIMARY KEY,
  tool        TEXT NOT NULL,
  session_id  TEXT NOT NULL,
  kind        TEXT NOT NULL,
  title       TEXT,
  body        TEXT NOT NULL,
  tags        TEXT,
  workspace   TEXT,
  repo        TEXT,
  confidence  REAL,
  model       TEXT,
  redacted    INTEGER DEFAULT 0,
  turn_ref    TEXT,
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nuggets_session ON nuggets(session_id);
CREATE INDEX IF NOT EXISTS idx_nuggets_kind    ON nuggets(kind);

CREATE TABLE IF NOT EXISTS artifacts (
  uid        TEXT PRIMARY KEY,
  kind       TEXT NOT NULL,
  title      TEXT,
  path       TEXT,
  scope      TEXT,
  nugget_ids TEXT,
  model      TEXT,
  created_at INTEGER NOT NULL
);

-- Append-only record of every mutating operation, so disposal is auditable.
CREATE TABLE IF NOT EXISTS operations (
  uid        TEXT PRIMARY KEY,
  op         TEXT NOT NULL,
  tool       TEXT,
  session_id TEXT,
  before     INTEGER,
  after      INTEGER,
  detail     TEXT,
  ok         INTEGER,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT
);
`

func (d *DB) migrate() error {
	if _, err := d.sql.Exec(schema); err != nil {
		return err
	}
	return d.migrateRuns()
}

// PutSessions replaces the session index in one transaction.
func (d *DB) PutSessions(sessions []core.Session) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO sessions (tool,id,dir,title,repo,created,updated,turns,bytes,noise,transcript,seen_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tool,id) DO UPDATE SET
		  dir=excluded.dir, title=excluded.title, repo=excluded.repo,
		  created=excluded.created, updated=excluded.updated, turns=excluded.turns,
		  bytes=excluded.bytes, noise=excluded.noise, transcript=excluded.transcript,
		  seen_at=excluded.seen_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, s := range sessions {
		noise := 0
		if s.Noise {
			noise = 1
		}
		if _, err := stmt.Exec(string(s.Tool), s.ID, s.Dir, s.Title, s.Repo,
			unix(s.Created), unix(s.Updated), s.Turns, s.Bytes, noise,
			s.TranscriptPath, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PutManifest stores an assay result along with the source fingerprint that
// produced it, so a later scan can skip unchanged transcripts.
func (d *DB) PutManifest(m *assay.Manifest, srcBytes int64, srcMtime time.Time) error {
	byKind, _ := json.Marshal(m.ByKind)
	_, err := d.sql.Exec(`
		INSERT INTO manifests (tool,id,total_records,total_bytes,signal_bytes,exhaust_bytes,
		  artifact_bytes,book_bytes,dup_reads,dup_bytes,images,image_clusters,by_kind,
		  source_bytes,source_mtime,assayed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tool,id) DO UPDATE SET
		  total_records=excluded.total_records, total_bytes=excluded.total_bytes,
		  signal_bytes=excluded.signal_bytes, exhaust_bytes=excluded.exhaust_bytes,
		  artifact_bytes=excluded.artifact_bytes, book_bytes=excluded.book_bytes,
		  dup_reads=excluded.dup_reads, dup_bytes=excluded.dup_bytes,
		  images=excluded.images, image_clusters=excluded.image_clusters,
		  by_kind=excluded.by_kind, source_bytes=excluded.source_bytes,
		  source_mtime=excluded.source_mtime, assayed_at=excluded.assayed_at`,
		m.Tool, m.SessionID, m.TotalRecords, m.TotalBytes,
		m.Bytes["signal"], m.Bytes["exhaust"], m.Bytes["artifact"], m.Bytes["bookkeeping"],
		m.DuplicateReads, m.DuplicateBytes, m.ImageCount, m.ImageClusters,
		string(byKind), srcBytes, unix(srcMtime), time.Now().Unix())
	return err
}

// ManifestFresh reports whether a stored manifest still matches the source.
func (d *DB) ManifestFresh(tool, id string, srcBytes int64, srcMtime time.Time) bool {
	var b, mt int64
	err := d.sql.QueryRow(
		`SELECT source_bytes, source_mtime FROM manifests WHERE tool=? AND id=?`,
		tool, id).Scan(&b, &mt)
	if err != nil {
		return false
	}
	return b == srcBytes && mt == unix(srcMtime)
}

// Totals is the aggregate view used by reports.
type Totals struct {
	Sessions int64
	Assayed  int64
	Bytes    int64
	Signal   int64
	Exhaust  int64
	Artifact int64
	Book     int64
	DupBytes int64
	Images   int64
	Clusters int64
}

// Reclaimable is exhaust plus bookkeeping: bytes removable without losing
// meaning.
func (t Totals) Reclaimable() int64 { return t.Exhaust + t.Book }

// Compression is total bytes over signal bytes.
func (t Totals) Compression() float64 {
	if t.Signal <= 0 {
		return 0
	}
	return float64(t.Bytes) / float64(t.Signal)
}

// Aggregate sums stored manifests, optionally for one tool.
func (d *DB) Aggregate(tool string) (Totals, error) {
	var t Totals
	where, args := "", []any{}
	if tool != "" {
		where = " WHERE tool = ?"
		args = append(args, tool)
	}

	err := d.sql.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_bytes),0), COALESCE(SUM(signal_bytes),0),
		       COALESCE(SUM(exhaust_bytes),0), COALESCE(SUM(artifact_bytes),0),
		       COALESCE(SUM(book_bytes),0), COALESCE(SUM(dup_bytes),0),
		       COALESCE(SUM(images),0), COALESCE(SUM(image_clusters),0)
		FROM manifests`+where, args...).
		Scan(&t.Assayed, &t.Bytes, &t.Signal, &t.Exhaust, &t.Artifact,
			&t.Book, &t.DupBytes, &t.Images, &t.Clusters)
	if err != nil {
		return t, err
	}

	sw, sargs := "", []any{}
	if tool != "" {
		sw = " WHERE tool = ?"
		sargs = append(sargs, tool)
	}
	d.sql.QueryRow(`SELECT COUNT(*) FROM sessions`+sw, sargs...).Scan(&t.Sessions)
	return t, nil
}

// RecordOp appends to the audit log. Every mutating operation is recorded,
// whether or not it succeeded.
func (d *DB) RecordOp(op, tool, sessionID string, before, after int64, detail string, ok bool) error {
	okv := 0
	if ok {
		okv = 1
	}
	_, err := d.sql.Exec(`
		INSERT INTO operations (uid,op,tool,session_id,before,after,detail,ok,created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		NewUID(), op, tool, sessionID, before, after, detail, okv, time.Now().Unix())
	return err
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
