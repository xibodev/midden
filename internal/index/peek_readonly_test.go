package index

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
)

func TestWarmPeekCacheDoesNotCreateAbsentState(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	configureIndexSources(t, "MIDDEN_CLAUDE_ROOT", source)
	for _, state := range []string{filepath.Join(root, "absent"), filepath.Join(source, "absent")} {
		t.Setenv("MIDDEN_HOME", state)
		if err := WarmPeekCache(); err != nil {
			t.Fatalf("ordinary absent state was not tolerated: %v", err)
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatal("passive cache warmup created an absent index directory")
		}
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("passive cache warmup created a source store")
	}
}

func TestWarmPeekCacheDoesNotCreateDatabaseInExistingState(t *testing.T) {
	state := t.TempDir()
	configureIndexSources(t, "MIDDEN_CLAUDE_ROOT", filepath.Join(t.TempDir(), "source"))
	t.Setenv("MIDDEN_HOME", state)
	if err := WarmPeekCache(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Fatalf("passive cache warmup created index files: %v %v", entries, err)
	}
}

func TestWarmPeekCacheReadsExistingIndexWithoutMigration(t *testing.T) {
	state := t.TempDir()
	configureIndexSources(t, "MIDDEN_CLAUDE_ROOT", filepath.Join(t.TempDir(), "source"))
	t.Setenv("MIDDEN_HOME", state)
	t.Cleanup(func() { adapter.SetPeekCache(nil) })
	path := filepath.Join(state, "core-index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE sessions (
		tool TEXT, id TEXT, dir TEXT, title TEXT, repo TEXT,
		created INTEGER, updated INTEGER, turns INTEGER,
		bytes INTEGER, noise INTEGER, transcript TEXT, seen_at INTEGER)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO sessions VALUES (
		'claude', 'synthetic-session', 'synthetic-workspace', 'Recorded title', '',
		1767225600, 1767225600, 1, 100, 0, 'synthetic-transcript.jsonl', 1767225600)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = WarmPeekCache(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("passive cache warmup migrated or rewrote the existing index")
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "core-index.db" {
		t.Fatal("passive cache warmup created journal or other state files")
	}
}
