package index

// Index reconciliation.
//
// PutSessions is an upsert: it adds and updates what an adapter returns, but
// cannot by itself remove records whose source transcript was deleted. That
// left stale rows in the UI with working-looking resume commands. On one
// machine, 55 Claude rows survived after their transcripts were gone; copying
// one started a new session rather than resuming the old work.
//
// Reconcile is called only after an unrestricted scan of a successfully read
// source store. It never deletes on an adapter error or a narrowed scope:
// absence is proof only when the complete source was read.

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// ReconcileReport describes derived rows removed during an authoritative scan.
// All source stores remain read-only; this reports mutations only in Midden's
// own index.
type ReconcileReport struct {
	GhostSessions    []SessionKey `json:"ghost_sessions"`
	DuplicateRows    int          `json:"duplicate_artifact_rows"`
	StaleManifests   []SessionKey `json:"stale_manifests"`
	OrphanManifests  []SessionKey `json:"orphan_manifests"`
	AuthoritativeAll bool         `json:"authoritative_all"`
}

// SessionKey identifies a session within a source tool.
type SessionKey struct {
	Tool core.Tool `json:"tool"`
	ID   string    `json:"id"`
}

// Total reports how many derived rows reconciliation removed.
func (r ReconcileReport) Total() int {
	return len(r.GhostSessions) + r.DuplicateRows + len(r.StaleManifests) + len(r.OrphanManifests)
}

// SourceStamp fingerprints the source that produced a session. File-backed
// tools use the transcript's size and mtime; database-backed tools use the
// metadata available from their store.
func SourceStamp(s core.Session) (int64, time.Time) {
	if s.TranscriptPath != "" {
		if fi, err := os.Stat(s.TranscriptPath); err == nil {
			return fi.Size(), fi.ModTime()
		}
	}
	// OpenCode stores its session content inside SQLite. Its Bytes field is
	// populated only for the opt-in --sizes path (a full part-table scan that
	// takes minutes), so using it as a source stamp makes a normal refresh
	// turn an assayed nonzero size into zero and falsely invalidate every
	// manifest. Updated is the stable revision signal available without that
	// expensive scan.
	if s.Tool == core.ToolOpencode {
		return 0, s.Updated
	}
	return s.Bytes, s.Updated
}

// Reconcile synchronizes index rows for tools whose complete, unrestricted
// session lists were just read at generation.
//
// tools is deliberately explicit. Passing only Claude after `midden scan
// --tool claude` reconciles Claude and preserves the Copilot/OpenCode cache.
// Passing no tools still cleans globally derived artifact duplicates and
// already orphaned manifests, but never removes a session.
//
// Only rows whose scan_gen is no newer than generation can be removed. This
// prevents an older, slower scan from deleting a session that a newer scan
// discovered and indexed while the older one was still reading stores.
func (d *DB) Reconcile(sessions []core.Session, tools []core.Tool, generation int64) (ReconcileReport, error) {
	return d.reconcile(sessions, tools, generation, time.Time{}, false)
}

// ReconcileAndMark atomically removes stale derived rows and records that the
// same generation completely scanned tools. The atomic boundary matters:
// writing a completion marker after deletion leaves a window where an older
// scan can insert a session the newer scan never saw.
func (d *DB) ReconcileAndMark(sessions []core.Session, tools []core.Tool, generation int64, indexedAt time.Time, allRequested bool) (ReconcileReport, error) {
	return d.reconcile(sessions, tools, generation, indexedAt, allRequested)
}

func (d *DB) reconcile(sessions []core.Session, tools []core.Tool, generation int64, indexedAt time.Time, allRequested bool) (ReconcileReport, error) {
	var report ReconcileReport

	present := map[core.Tool]map[string]bool{}
	for _, s := range sessions {
		if present[s.Tool] == nil {
			present[s.Tool] = map[string]bool{}
		}
		present[s.Tool][s.ID] = true
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	for _, tool := range uniqueTools(tools) {
		rows, err := tx.Query(`SELECT id FROM sessions WHERE tool = ? AND scan_gen <= ?`,
			string(tool), generation)
		if err != nil {
			return report, fmt.Errorf("list indexed %s sessions: %w", tool, err)
		}
		var missing []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return report, err
			}
			if !present[tool][id] {
				missing = append(missing, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return report, err
		}
		rows.Close()

		for _, id := range missing {
			if _, err := tx.Exec(`
				INSERT INTO session_tombstones (tool,id,scan_gen,deleted_at)
				VALUES (?,?,?,?)
				ON CONFLICT(tool,id) DO UPDATE SET
				  scan_gen = CASE
				    WHEN excluded.scan_gen > session_tombstones.scan_gen THEN excluded.scan_gen
				    ELSE session_tombstones.scan_gen
				  END,
				  deleted_at = CASE
				    WHEN excluded.scan_gen > session_tombstones.scan_gen THEN excluded.deleted_at
				    ELSE session_tombstones.deleted_at
				  END`,
				string(tool), id, generation, time.Now().Unix()); err != nil {
				return report, fmt.Errorf("tombstone ghost %s/%s: %w", tool, id, err)
			}
			// Remove the manifest first. SQLite foreign keys are intentionally
			// not assumed here because older index files predate them.
			if _, err := tx.Exec(`DELETE FROM manifests WHERE tool = ? AND id = ?`, string(tool), id); err != nil {
				return report, fmt.Errorf("delete manifest %s/%s: %w", tool, id, err)
			}
			if _, err := tx.Exec(`DELETE FROM sessions WHERE tool = ? AND id = ?`, string(tool), id); err != nil {
				return report, fmt.Errorf("delete ghost %s/%s: %w", tool, id, err)
			}
			report.GhostSessions = append(report.GhostSessions, SessionKey{Tool: tool, ID: id})
		}
	}

	// A changed transcript must not keep contributing its old assay to
	// reclaimable-byte totals. A later `scan --assay` will measure the new one.
	for _, s := range sessions {
		bytes, mtime := SourceStamp(s)
		res, err := tx.Exec(`
			DELETE FROM manifests
			WHERE tool = ? AND id = ?
			  AND (source_bytes != ? OR source_mtime != ?)
			  AND EXISTS (
			    SELECT 1 FROM sessions
			    WHERE tool = ? AND id = ? AND scan_gen <= ?
			  )`,
			string(s.Tool), s.ID, bytes, unixSeconds(mtime),
			string(s.Tool), s.ID, generation)
		if err != nil {
			return report, fmt.Errorf("invalidate manifest %s/%s: %w", s.Tool, s.ID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			report.StaleManifests = append(report.StaleManifests, SessionKey{Tool: s.Tool, ID: s.ID})
		}
	}

	// Older versions deleted a ghost session without its manifest. Remove
	// those leftovers too, keyed by tool as well as id to avoid collisions.
	rows, err := tx.Query(`
		SELECT m.tool, m.id
		FROM manifests m
		LEFT JOIN sessions s ON s.tool = m.tool AND s.id = m.id
		WHERE s.id IS NULL`)
	if err != nil {
		return report, fmt.Errorf("list orphan manifests: %w", err)
	}
	var orphans []SessionKey
	for rows.Next() {
		var tool string
		var id string
		if err := rows.Scan(&tool, &id); err != nil {
			rows.Close()
			return report, err
		}
		orphans = append(orphans, SessionKey{Tool: core.Tool(tool), ID: id})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	for _, orphan := range orphans {
		if _, err := tx.Exec(`DELETE FROM manifests WHERE tool = ? AND id = ?`, string(orphan.Tool), orphan.ID); err != nil {
			return report, fmt.Errorf("delete orphan manifest %s/%s: %w", orphan.Tool, orphan.ID, err)
		}
		report.OrphanManifests = append(report.OrphanManifests, orphan)
	}

	// PutArtifact now replaces by path, but indexes created before that fix
	// can retain duplicate rows pointing at a single overwritten file.
	dupes, err := duplicateArtifactUIDs(tx)
	if err != nil {
		return report, err
	}
	for _, uid := range dupes {
		if _, err := tx.Exec(`DELETE FROM artifacts WHERE uid = ?`, uid); err != nil {
			return report, fmt.Errorf("delete duplicate artifact %s: %w", uid, err)
		}
		report.DuplicateRows++
	}

	if !indexedAt.IsZero() {
		// Coverage is checked inside this transaction. Checking it before
		// reconciliation leaves a cross-process window where another scan can
		// add an unscanned tool row and make an aggregate "fresh" marker lie.
		all := false
		if allRequested {
			var err error
			all, err = coversIndexedToolsTx(tx, tools)
			if err != nil {
				return report, fmt.Errorf("check indexed tool coverage: %w", err)
			}
		}
		report.AuthoritativeAll = all
		if err := d.markIndexedTx(tx, tools, generation, indexedAt, all); err != nil {
			return report, fmt.Errorf("mark indexed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

func duplicateArtifactUIDs(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`
		SELECT a.uid
		FROM artifacts a
		WHERE COALESCE(a.path, '') != ''
		  AND EXISTS (
		    SELECT 1 FROM artifacts b
		    WHERE b.path = a.path
		      AND (b.created_at > a.created_at
		           OR (b.created_at = a.created_at AND b.uid > a.uid))
		  )`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func uniqueTools(in []core.Tool) []core.Tool {
	seen := map[core.Tool]bool{}
	var out []core.Tool
	for _, tool := range in {
		if tool == "" || seen[tool] {
			continue
		}
		seen[tool] = true
		out = append(out, tool)
	}
	return out
}

// FullScope reports whether a scope enumerates every session for each selected
// tool. Only this scope gives absence enough meaning to delete an index row.
func FullScope(sc core.Scope) bool {
	return sc.Days == 0 &&
		sc.Workspace == "" &&
		sc.Repo == "" &&
		sc.IDPrefix == "" &&
		sc.Limit == 0
}

// AuthoritativeTools derives the source tools that were completely enumerated
// by one collection snapshot. Callers pass adapter.CollectionResult.Complete;
// recomputing availability after a scan can turn a newly appeared store into a
// destructive empty reconciliation.
func AuthoritativeTools(sc core.Scope, complete []core.Tool) []core.Tool {
	if !FullScope(sc) {
		return nil
	}
	return uniqueTools(complete)
}

// SessionsForTools returns only the sessions whose adapters completed the
// collection. A partial adapter can still contribute fresh rows via upsert,
// but its sessions must not drive manifest invalidation or completion markers.
func SessionsForTools(sessions []core.Session, tools []core.Tool) []core.Session {
	allowed := map[core.Tool]bool{}
	for _, tool := range uniqueTools(tools) {
		allowed[tool] = true
	}
	out := make([]core.Session, 0, len(sessions))
	for _, session := range sessions {
		if allowed[session.Tool] {
			out = append(out, session)
		}
	}
	return out
}

func coversIndexedToolsTx(tx *sql.Tx, tools []core.Tool) (bool, error) {
	seen := map[string]bool{}
	for _, tool := range uniqueTools(tools) {
		seen[string(tool)] = true
	}
	rows, err := tx.Query(`SELECT DISTINCT tool FROM sessions`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var tool string
		if err := rows.Scan(&tool); err != nil {
			return false, err
		}
		if !seen[tool] {
			return false, nil
		}
	}
	return true, rows.Err()
}
