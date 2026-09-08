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
	"strings"
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
func Open() (*DB, error) { return OpenAt(Dir()) }

// OpenAt opens the index under an EXPLICIT directory rather than resolving one
// from the environment.
//
// The module protocol requires this: a host runs modules with an empty
// environment and supplies every path in the request, so Dir() would resolve
// to a relative ".midden" and quietly read an index that is not the user's.
// Open() is OpenAt(Dir()), so every existing caller is unchanged.
func OpenAt(dir string) (*DB, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("index directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	dbPath := filepath.Join(dir, "index.db")
	p := filepath.ToSlash(dbPath)
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

	db := &DB{sql: sdb, path: dbPath}
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
  scan_gen       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (tool, id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_dir     ON sessions(dir);
CREATE INDEX IF NOT EXISTS idx_sessions_bytes   ON sessions(bytes DESC);

-- A durable deletion generation prevents an older overlapping scan from
-- resurrecting a session that a newer scan proved absent and removed.
CREATE TABLE IF NOT EXISTS session_tombstones (
  tool       TEXT NOT NULL,
  id         TEXT NOT NULL,
  scan_gen   INTEGER NOT NULL,
  deleted_at INTEGER NOT NULL,
  PRIMARY KEY (tool, id)
);

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
	if err := d.ensureSessionGeneration(); err != nil {
		return err
	}
	if err := d.migrateRuns(); err != nil {
		return err
	}
	if err := d.migrateRefinery(); err != nil {
		return err
	}
	return d.migrateWorkbench()
}

// ensureSessionGeneration upgrades indexes created before scan_gen existed.
// SQLite has no ADD COLUMN IF NOT EXISTS, so inspect first.
func (d *DB) ensureSessionGeneration() error {
	rows, err := d.sql.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	hasGeneration := false
	for rows.Next() {
		var (
			cid         int
			name, typ   string
			notNull, pk int
			defaultVal  any
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			return err
		}
		if name == "scan_gen" {
			hasGeneration = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !hasGeneration {
		if _, err := d.sql.Exec(`ALTER TABLE sessions ADD COLUMN scan_gen INTEGER NOT NULL DEFAULT 0`); err != nil {
			// Another process can win the check/ALTER race between our PRAGMA and
			// this statement. The desired column now exists, so duplicate-column
			// is success rather than a failed startup.
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				return fmt.Errorf("add sessions.scan_gen: %w", err)
			}
		}
	}
	return d.ensureTombstoneTrigger()
}

// ensureTombstoneTrigger prevents a pre-upgrade process from resurrecting a
// row a newer scan deleted.
//
// Old Midden binaries omit scan_gen, so SQLite supplies DEFAULT 0. If such a
// process remains open while a new binary reconciles a ghost, its delayed
// upsert would otherwise insert the tombstoned id again. The trigger makes
// the database itself enforce the deletion fence for every writer, including
// binaries that do not know this migration exists.
func (d *DB) ensureTombstoneTrigger() error {
	if _, err := d.sql.Exec(`
		CREATE TRIGGER IF NOT EXISTS reject_tombstoned_session_insert
		BEFORE INSERT ON sessions
		WHEN EXISTS (
		  SELECT 1 FROM session_tombstones t
		  WHERE t.tool = NEW.tool
		    AND t.id = NEW.id
		    AND t.scan_gen > NEW.scan_gen
		)
		BEGIN
		  SELECT RAISE(ABORT, 'session tombstoned by newer scan');
		END`); err != nil {
		return fmt.Errorf("create tombstone insert trigger: %w", err)
	}
	_, err := d.sql.Exec(`
		CREATE TRIGGER IF NOT EXISTS reject_legacy_session_update
		BEFORE UPDATE ON sessions
		WHEN OLD.scan_gen > 0 AND NEW.scan_gen <= OLD.scan_gen
		BEGIN
		  SELECT RAISE(ABORT, 'legacy or stale session update rejected');
		END`)
	if err != nil {
		return fmt.Errorf("create legacy update trigger: %w", err)
	}
	_, err = d.sql.Exec(`
		CREATE TRIGGER IF NOT EXISTS reject_legacy_session_insert_after_completion
		BEFORE INSERT ON sessions
		WHEN NEW.scan_gen = 0
		  AND EXISTS (
		    SELECT 1 FROM meta
		    WHERE key = 'completed_generation:' || NEW.tool
		      AND CAST(value AS INTEGER) > 0
		  )
		BEGIN
		  SELECT RAISE(ABORT, 'legacy session insert rejected after completed scan');
		END`)
	if err != nil {
		return fmt.Errorf("create legacy insert trigger: %w", err)
	}
	_, err = d.sql.Exec(`
		CREATE TRIGGER IF NOT EXISTS reject_orphan_manifest_insert
		BEFORE INSERT ON manifests
		WHEN NOT EXISTS (
		  SELECT 1 FROM sessions s
		  WHERE s.tool = NEW.tool AND s.id = NEW.id
		)
		BEGIN
		  SELECT RAISE(ABORT, 'manifest session no longer exists');
		END`)
	if err != nil {
		return fmt.Errorf("create manifest insert trigger: %w", err)
	}
	return nil
}

// PutSessions upserts sessions in one transaction using the current time as
// its scan generation.
//
// It intentionally does not delete rows absent from its argument: narrowed
// scans must never erase unrelated tools or time ranges. An unrestricted,
// successfully read scan follows this with Reconcile, which is the one place
// absence is proof enough to remove a row.
func (d *DB) PutSessions(sessions []core.Session) error {
	generation, err := d.NextScanGeneration()
	if err != nil {
		return err
	}
	return d.PutSessionsWithGeneration(sessions, generation, time.Now())
}

// NextScanGeneration reserves a durable, monotonically increasing generation.
//
// Callers acquire ScanLock before calling this. The lock serializes source
// collection and application; this counter then remains monotonic even if the
// wall clock moves backward, so a later scan cannot silently become "older".
func (d *DB) NextScanGeneration() (int64, error) {
	tx, err := d.sql.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO meta (key,value) VALUES ('scan_generation','0')
		ON CONFLICT(key) DO NOTHING`); err != nil {
		return 0, err
	}
	var current int64
	if err := tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'scan_generation'`).Scan(&current); err != nil {
		return 0, err
	}
	next := current + 1
	if _, err := tx.Exec(`UPDATE meta SET value = ? WHERE key = 'scan_generation'`, fmt.Sprintf("%d", next)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

// PutSessionsWithGeneration upserts sessions from one scan generation.
//
// The generation makes overlapping scans safe. If scan A collected an older
// source list, then scan B discovers a new session and writes first, A must
// not overwrite B's metadata or delete B's row during reconciliation.
func (d *DB) PutSessionsWithGeneration(sessions []core.Session, generation int64, seenAt time.Time) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	clearTombstone, err := tx.Prepare(`
		DELETE FROM session_tombstones
		WHERE tool = ? AND id = ? AND scan_gen <= ?`)
	if err != nil {
		return err
	}
	defer clearTombstone.Close()

	stmt, err := tx.Prepare(`
		INSERT INTO sessions (tool,id,dir,title,repo,created,updated,turns,bytes,noise,transcript,seen_at,scan_gen)
		SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?
		WHERE NOT EXISTS (
		  SELECT 1 FROM session_tombstones
		  WHERE tool = ? AND id = ? AND scan_gen > ?
		)
		ON CONFLICT(tool,id) DO UPDATE SET
		  dir=excluded.dir, title=excluded.title, repo=excluded.repo,
		  created=excluded.created, updated=excluded.updated, turns=excluded.turns,
		  bytes=excluded.bytes, noise=excluded.noise, transcript=excluded.transcript,
		  seen_at=excluded.seen_at, scan_gen=excluded.scan_gen
		WHERE excluded.scan_gen >= sessions.scan_gen`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := seenAt.Unix()
	completed := map[core.Tool]int64{}
	for _, s := range sessions {
		if _, known := completed[s.Tool]; !known {
			n, err := completedGeneration(tx, s.Tool)
			if err != nil {
				return err
			}
			completed[s.Tool] = n
		}
		// A newer complete scan has already established the source truth
		// for this tool. Do not let this older scan resurrect a row that
		// scan proved absent before this one finished reading.
		if generation < completed[s.Tool] {
			continue
		}
		noise := 0
		if s.Noise {
			noise = 1
		}
		if _, err := clearTombstone.Exec(string(s.Tool), s.ID, generation); err != nil {
			return err
		}
		if _, err := stmt.Exec(string(s.Tool), s.ID, s.Dir, s.Title, s.Repo,
			unixSeconds(s.Created), unixSeconds(s.Updated), s.Turns, s.Bytes, noise,
			s.TranscriptPath, now, generation,
			string(s.Tool), s.ID, generation); err != nil {
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
		string(byKind), srcBytes, unixSeconds(srcMtime), time.Now().Unix())
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
	return b == srcBytes && mt == unixSeconds(srcMtime)
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

func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
