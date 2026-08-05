package index

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

func TestReconcileRemovesOnlyProvenDerivedDrift(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().Add(-time.Minute).Round(time.Second)
	generation := int64(42)
	stalePath := filepath.Join(t.TempDir(), "stale.jsonl")
	if err := os.WriteFile(stalePath, []byte("the transcript is newer than the assay"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(stalePath)
	if err != nil {
		t.Fatal(err)
	}

	ghost := core.Session{
		Tool: core.ToolClaude, ID: "ghost", Dir: t.TempDir(), Title: "gone",
		Created: now, Updated: now, Bytes: 10, TranscriptPath: filepath.Join(t.TempDir(), "gone.jsonl"),
	}
	live := core.Session{
		Tool: core.ToolClaude, ID: "live", Dir: t.TempDir(), Title: "live",
		Created: now, Updated: now, Bytes: fi.Size(), TranscriptPath: stalePath,
	}
	if err := db.PutSessionsWithGeneration([]core.Session{ghost, live}, generation-1, now); err != nil {
		t.Fatal(err)
	}

	// One old assay for the live source, one for the ghost, and an already
	// orphaned manifest simulate the exact rows found in a pre-migration
	// index. Temporarily remove the new fence to construct historical
	// corruption, then restore it before reconciliation.
	if _, err := db.SQL().Exec(`DROP TRIGGER reject_orphan_manifest_insert`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		tool, id string
		bytes    int64
	}{
		{"claude", "live", fi.Size() - 1},
		{"claude", "ghost", 10},
		{"claude", "already-orphan", 1},
	} {
		if _, err := db.SQL().Exec(`
			INSERT INTO manifests (tool,id,source_bytes,source_mtime,assayed_at)
			VALUES (?,?,?,?,?)`, row.tool, row.id, row.bytes, now.Unix(), now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ensureTombstoneTrigger(); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		uid string
		at  int64
	}{
		{"old", now.Unix()},
		{"new", now.Add(time.Second).Unix()},
	} {
		if _, err := db.SQL().Exec(`
			INSERT INTO artifacts (uid,kind,path,created_at) VALUES (?,?,?,?)`,
			row.uid, "tsg", "C:/same-artifact.md", row.at); err != nil {
			t.Fatal(err)
		}
	}

	// cmdScan puts fresh metadata first, then reconciles it.
	if err := db.PutSessionsWithGeneration([]core.Session{live}, generation, now); err != nil {
		t.Fatal(err)
	}
	report, err := db.Reconcile([]core.Session{live}, []core.Tool{core.ToolClaude}, generation)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(report.GhostSessions), 1; got != want {
		t.Fatalf("ghosts=%d, want %d", got, want)
	}
	if got, want := len(report.StaleManifests), 1; got != want {
		t.Fatalf("stale=%d, want %d", got, want)
	}
	// The ghost manifest is removed alongside its session; the pre-existing
	// orphan is reported independently.
	if got, want := len(report.OrphanManifests), 1; got != want {
		t.Fatalf("orphans=%d, want %d", got, want)
	}
	if got, want := report.DuplicateRows, 1; got != want {
		t.Fatalf("duplicates=%d, want %d", got, want)
	}
	if got, want := report.Total(), 4; got != want {
		t.Fatalf("total=%d, want %d", got, want)
	}

	assertIndexCount(t, db, "sessions", 1)
	assertIndexCount(t, db, "manifests", 0)
	assertIndexCount(t, db, "artifacts", 1)

	again, err := db.Reconcile([]core.Session{live}, []core.Tool{core.ToolClaude}, generation)
	if err != nil {
		t.Fatal(err)
	}
	if again.Total() != 0 {
		t.Fatalf("second reconcile total=%d, want 0", again.Total())
	}
}

func TestFullScopeAndAuthoritativeTools(t *testing.T) {
	full := core.Scope{Tools: []core.Tool{core.ToolClaude}}
	if !FullScope(full) {
		t.Fatal("unfiltered tool scan must be authoritative")
	}

	for name, scope := range map[string]core.Scope{
		"days":      {Days: 7},
		"workspace": {Workspace: "project"},
		"repo":      {Repo: "repo"},
		"id":        {IDPrefix: "abc"},
		"limit":     {Limit: 10},
	} {
		if FullScope(scope) {
			t.Errorf("%s scope was incorrectly authoritative", name)
		}
	}

	tools := AuthoritativeTools(full, []core.Tool{core.ToolClaude})
	if len(tools) != 1 || tools[0] != core.ToolClaude {
		t.Fatalf("tools=%v, want [claude]", tools)
	}
	if tools := AuthoritativeTools(core.Scope{Days: 1}, []core.Tool{core.ToolClaude}); len(tools) != 0 {
		t.Fatalf("narrowed tools=%v, want none", tools)
	}
}

func TestSourceStampDoesNotUseOptionalOpenCodeSizes(t *testing.T) {
	updated := time.Now().Round(time.Second)
	s := core.Session{
		Tool:    core.ToolOpencode,
		Bytes:   99 << 20, // present only after an expensive --sizes scan
		Updated: updated,
	}
	bytes, mtime := SourceStamp(s)
	if bytes != 0 {
		t.Fatalf("opencode stamp bytes=%d, want 0 stable sentinel", bytes)
	}
	if !mtime.Equal(updated) {
		t.Fatalf("opencode stamp mtime=%s, want %s", mtime, updated)
	}
}

func TestOlderScanCannotDeleteNewerSession(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	old := int64(10)
	newer := old + 1
	session := core.Session{
		Tool: core.ToolClaude, ID: "discovered-by-newer-scan",
		Dir: t.TempDir(), Title: "new", Created: time.Now(), Updated: time.Now(),
	}
	if err := db.PutSessionsWithGeneration([]core.Session{session}, newer, time.Now()); err != nil {
		t.Fatal(err)
	}

	// This models a slower scan that started before the session existed. Its
	// source list is empty, but it must not delete a row stamped by the newer
	// scan that completed first.
	report, err := db.Reconcile(nil, []core.Tool{core.ToolClaude}, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.GhostSessions) != 0 {
		t.Fatalf("older scan deleted newer rows: %#v", report.GhostSessions)
	}
	assertIndexCount(t, db, "sessions", 1)
}

func TestOlderScanCannotResurrectNewerDeletion(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	old := int64(10)
	newer := old + 1
	session := core.Session{
		Tool: core.ToolClaude, ID: "deleted-by-newer-scan",
		Dir: t.TempDir(), Title: "old", Created: time.Now(), Updated: time.Now(),
	}
	if err := db.PutSessionsWithGeneration([]core.Session{session}, old, time.Now()); err != nil {
		t.Fatal(err)
	}

	// A newer scan proves the session absent and records a tombstone.
	if _, err := db.Reconcile(nil, []core.Tool{core.ToolClaude}, newer); err != nil {
		t.Fatal(err)
	}
	assertIndexCount(t, db, "sessions", 0)

	// The slower old scan completes after the deletion. There is no existing
	// row for an UPSERT generation guard to compare, so the tombstone is what
	// stops this stale observation from resurrecting the session.
	if err := db.PutSessionsWithGeneration([]core.Session{session}, old, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertIndexCount(t, db, "sessions", 0)

	var generation int64
	if err := db.SQL().QueryRow(`
		SELECT scan_gen FROM session_tombstones
		WHERE tool = ? AND id = ?`, string(core.ToolClaude), session.ID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != newer {
		t.Fatalf("tombstone generation=%d, want %d", generation, newer)
	}
}

func TestOlderScanCannotInsertAfterNewerEmptyScan(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Scan B finished later and found no Claude sessions, so it has no row
	// to tombstone. Its completed-generation watermark still must fence an
	// older scan A that observed a session before it was deleted.
	if _, err := db.ReconcileAndMark(nil, []core.Tool{core.ToolClaude}, 20, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	old := core.Session{
		Tool: core.ToolClaude, ID: "seen-only-by-old-scan",
		Dir: t.TempDir(), Title: "stale", Created: time.Now(), Updated: time.Now(),
	}
	if err := db.PutSessionsWithGeneration([]core.Session{old}, 19, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertIndexCount(t, db, "sessions", 0)
}

func TestLaterObservationWinsRegardlessOfScanStartOrder(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Scan B started later but observed an empty source at generation 200 and
	// completed first. Scan A began earlier, but only observes this newly
	// created session after B's empty observation. Source observation order,
	// not scan-start order, is what must decide whether A can write.
	if _, err := db.ReconcileAndMark(nil, []core.Tool{core.ToolClaude}, 200, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	later := core.Session{
		Tool: core.ToolClaude, ID: "created-after-empty-observation",
		Dir: t.TempDir(), Title: "new", Created: time.Now(), Updated: time.Now(),
	}
	if err := db.PutSessionsWithGeneration([]core.Session{later}, 300, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertIndexCount(t, db, "sessions", 1)
}

func TestNewerScanCanRestoreTombstonedSession(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	session := core.Session{
		Tool: core.ToolClaude, ID: "source-reappeared",
		Dir: t.TempDir(), Title: "back", Created: time.Now(), Updated: time.Now(),
	}
	if _, err := db.SQL().Exec(`
		INSERT INTO session_tombstones (tool,id,scan_gen,deleted_at)
		VALUES (?,?,?,?)`, string(session.Tool), session.ID, 10, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSessionsWithGeneration([]core.Session{session}, 11, time.Now()); err != nil {
		t.Fatal(err)
	}
	assertIndexCount(t, db, "sessions", 1)
	var count int
	if err := db.SQL().QueryRow(`
		SELECT COUNT(*) FROM session_tombstones
		WHERE tool = ? AND id = ?`, string(session.Tool), session.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("newer source did not clear its tombstone")
	}
}

func TestLegacyWriterCannotResurrectTombstonedSession(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// This mimics a pre-upgrade binary: it does not mention scan_gen, so
	// SQLite supplies DEFAULT 0. The trigger is the only fence it knows.
	if _, err := db.SQL().Exec(`
		INSERT INTO session_tombstones (tool,id,scan_gen,deleted_at)
		VALUES (?,?,?,?)`, string(core.ToolClaude), "legacy-resurrection", 10, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`
		INSERT INTO sessions (tool,id,dir,seen_at)
		VALUES (?,?,?,?)`, string(core.ToolClaude), "legacy-resurrection", t.TempDir(), time.Now().Unix()); err == nil {
		t.Fatal("legacy writer resurrected a newer tombstone")
	}
	assertIndexCount(t, db, "sessions", 0)
}

func TestLegacyWriterCannotOverwriteGenerationStampedSession(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	current := core.Session{
		Tool: core.ToolClaude, ID: "generation-stamped",
		Dir: t.TempDir(), Title: "new metadata", Created: time.Now(), Updated: time.Now(),
	}
	if err := db.PutSessionsWithGeneration([]core.Session{current}, 10, time.Now()); err != nil {
		t.Fatal(err)
	}

	// This mirrors the old binary's INSERT/UPSERT: scan_gen is omitted, so
	// SQLite leaves the existing generation untouched while overwriting the
	// other columns. The migration trigger must refuse that stale writer.
	if _, err := db.SQL().Exec(`
		INSERT INTO sessions (tool,id,dir,title,seen_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(tool,id) DO UPDATE SET
		  dir=excluded.dir, title=excluded.title, seen_at=excluded.seen_at`,
		string(current.Tool), current.ID, "C:/old", "old metadata", time.Now().Unix()); err == nil {
		t.Fatal("legacy writer overwrote a generation-stamped row")
	}

	var title string
	if err := db.SQL().QueryRow(`SELECT title FROM sessions WHERE tool=? AND id=?`, string(current.Tool), current.ID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != current.Title {
		t.Fatalf("title=%q, want newer metadata %q", title, current.Title)
	}
}

func TestLegacyWriterCannotInsertAfterCompletedEmptyScan(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// A newer empty scan has no row to tombstone, but it still proves the
	// source was empty. A pre-upgrade writer arriving afterward must not add
	// a stale session with SQLite's DEFAULT scan_gen=0.
	if _, err := db.ReconcileAndMark(nil, []core.Tool{core.ToolClaude}, 10, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`
		INSERT INTO sessions (tool,id,dir,seen_at)
		VALUES (?,?,?,?)`, string(core.ToolClaude), "legacy-after-empty", t.TempDir(), time.Now().Unix()); err == nil {
		t.Fatal("legacy writer inserted after a completed empty scan")
	}
	assertIndexCount(t, db, "sessions", 0)
}

func TestAssayCannotInsertManifestForDeletedSession(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// This models an older --assay goroutine completing after a newer scan
	// reconciled its session. The trigger must refuse derived data with no
	// source row, or Aggregate will silently count it again.
	if _, err := db.SQL().Exec(`
		INSERT INTO manifests (tool,id,source_bytes,source_mtime,assayed_at)
		VALUES (?,?,?,?,?)`,
		string(core.ToolClaude), "deleted-before-assay-finished", 10, time.Now().Unix(), time.Now().Unix()); err == nil {
		t.Fatal("orphan manifest was inserted")
	}
	assertIndexCount(t, db, "manifests", 0)
}

func TestAuthoritativeIndexedAtDoesNotBorrowUnrelatedRowFreshness(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	older := time.Now().Add(-time.Hour).Round(time.Second)
	newer := time.Now().Round(time.Second)
	if err := db.PutSessionsWithGeneration([]core.Session{
		{Tool: core.ToolClaude, ID: "claude-old", Dir: t.TempDir(), Updated: older},
		{Tool: core.ToolCopilot, ID: "copilot-new", Dir: t.TempDir(), Updated: newer},
	}, 1, newer); err != nil {
		t.Fatal(err)
	}
	// MAX(seen_at) would now say the whole index is new. Only an explicit,
	// complete scan may make that claim.
	if got := db.AuthoritativeIndexedAt(nil); !got.IsZero() {
		t.Fatalf("unmarked all-tool view looked fresh at %s", got)
	}

	if err := db.MarkIndexed([]core.Tool{core.ToolCopilot}, 1, newer, false); err != nil {
		t.Fatal(err)
	}
	if got := db.AuthoritativeIndexedAt([]core.Tool{core.ToolCopilot}); !got.Equal(newer) {
		t.Fatalf("copilot freshness=%s, want %s", got, newer)
	}
	if got := db.AuthoritativeIndexedAt(nil); !got.IsZero() {
		t.Fatalf("single-tool scan marked all-tool view fresh at %s", got)
	}

	if err := db.MarkIndexed([]core.Tool{core.ToolClaude, core.ToolCopilot}, 2, newer, true); err != nil {
		t.Fatal(err)
	}
	if got := db.AuthoritativeIndexedAt(nil); !got.Equal(newer) {
		t.Fatalf("all-tool freshness=%s, want %s", got, newer)
	}
}

func TestAllToolMarkerAllowsSuccessfulEmptyTool(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PutSessionsWithGeneration([]core.Session{
		{Tool: core.ToolCopilot, ID: "copilot", Dir: t.TempDir()},
	}, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Claude was successfully scanned but had no eligible sessions, so it
	// is absent from the index. That must not prevent an all-tool scan from
	// marking the view authoritative. The coverage check runs inside the
	// reconcile/mark transaction, not on a stale pre-transaction snapshot.
	now := time.Now().Round(time.Second)
	if _, err := db.ReconcileAndMark(
		[]core.Session{{Tool: core.ToolCopilot, ID: "copilot", Dir: t.TempDir()}},
		[]core.Tool{core.ToolCopilot, core.ToolClaude},
		2, now, true,
	); err != nil {
		t.Fatal(err)
	}
	if got := db.AuthoritativeIndexedAt(nil); !got.Equal(now) {
		t.Fatalf("successful empty tool left all-tool freshness stale: %s", got)
	}
}

func TestScanLockSerializesConnections(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	first, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	lock, err := first.AcquireScanLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.AcquireScanLock(); !errors.Is(err, ErrScanLocked) {
		t.Fatalf("second lock error=%v, want ErrScanLocked", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := second.AcquireScanLock()
	if err != nil {
		t.Fatalf("lock did not release: %v", err)
	}
	defer again.Release()
}

func TestScanGenerationIsDurablyMonotonic(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	lock, err := db.AcquireScanLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	first, err := db.NextScanGeneration()
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.NextScanGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if second != first+1 {
		t.Fatalf("generations %d then %d, want strictly consecutive", first, second)
	}
}

func TestEmptyReconcileReportUsesArraysInJSON(t *testing.T) {
	report := ReconcileReport{
		GhostSessions:   []SessionKey{},
		StaleManifests:  []SessionKey{},
		OrphanManifests: []SessionKey{},
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"ghost_sessions":[]`,
		`"stale_manifests":[]`,
		`"orphan_manifests":[]`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON %s missing %s", data, want)
		}
	}
}

func assertIndexCount(t *testing.T, db *DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s count=%d, want %d", table, got, want)
	}
}
