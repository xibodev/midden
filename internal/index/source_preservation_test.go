package index

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func configureIndexSources(t *testing.T, key, path string) {
	t.Helper()
	for _, name := range []string{"MIDDEN_COPILOT_ROOT", "MIDDEN_CLAUDE_ROOT", "MIDDEN_OPENCODE_DB"} {
		t.Setenv(name, "")
	}
	t.Setenv(key, path)
}

func TestIndexOpenRejectsConfiguredSourceRootsBeforeCreatingState(t *testing.T) {
	for _, key := range []string{"MIDDEN_COPILOT_ROOT", "MIDDEN_CLAUDE_ROOT"} {
		for _, entry := range []string{"OpenAt", "MIDDEN_HOME"} {
			t.Run(key+"/"+entry, func(t *testing.T) {
				source := filepath.Join(t.TempDir(), "source")
				configureIndexSources(t, key, source)
				destination := filepath.Join(source, "new", "index")
				var db *DB
				var err error
				if entry == "OpenAt" {
					db, err = OpenAt(destination)
				} else {
					t.Setenv("MIDDEN_HOME", destination)
					db, err = Open()
				}
				if db != nil {
					db.Close()
				}
				if err == nil {
					t.Error("index opening accepted a configured source-store destination")
				}
				if _, err = os.Stat(source); !os.IsNotExist(err) {
					t.Fatal("index opening created state inside the source store")
				}
			})
		}
	}
}

func TestIndexProtectsDatabaseNamesButAllowsWorkspaceState(t *testing.T) {
	workspace := t.TempDir()
	database := filepath.Join(workspace, "source.db")
	configureIndexSources(t, "MIDDEN_OPENCODE_DB", database)
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		protected := database + suffix
		db, err := OpenAt(filepath.Join(protected, "cache"))
		if db != nil {
			db.Close()
		}
		if err == nil {
			t.Fatalf("index claimed a source database or companion filename: %s", protected)
		}
		if _, err = os.Stat(protected); !os.IsNotExist(err) {
			t.Fatal("rejected index destination created a reserved database path")
		}
	}
	db, err := OpenAt(filepath.Join(workspace, "midden-state"))
	if err != nil {
		t.Fatalf("ordinary workspace state was rejected: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestIndexRejectsDatabaseAliasIntoSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.db")
	configureIndexSources(t, "MIDDEN_OPENCODE_DB", source)
	sourceDB, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sourceDB.Exec(`CREATE TABLE source_records (value TEXT); INSERT INTO source_records VALUES ('synthetic source')`); err != nil {
		sourceDB.Close()
		t.Fatal(err)
	}
	if err = sourceDB.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	if err = os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(source, filepath.Join(state, "core-index.db")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	db, err := OpenAt(state)
	if db != nil {
		db.Close()
	}
	if err == nil {
		t.Error("index opening followed a database alias into the source")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("index opening modified the source database")
	}
}
