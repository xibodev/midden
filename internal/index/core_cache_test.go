package index

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

func TestIndexedSessionLookupKeepsExactIDs(t *testing.T) {
	db, err := OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.PutSessions([]core.Session{{Tool: core.ToolClaude, ID: "chosen", Dir: "work"}, {Tool: core.ToolClaude, ID: "chosen-other", Dir: "work"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Sessions(core.Scope{IDs: []string{"chosen"}, IncludeNoise: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "chosen" {
		t.Fatal("exact ids widened into other indexed sessions")
	}
}

func TestCoreCacheNeverMigratesTheLegacyEditorialStore(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "index.db")
	original := []byte("synthetic legacy data; must not be opened or rewritten")
	if err := os.WriteFile(legacy, original, 0600); err != nil {
		t.Fatal(err)
	}

	db, err := OpenAt(dir)
	if err != nil {
		t.Fatalf("core tried to open the legacy store: %v", err)
	}
	defer db.Close()
	raw, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(raw, original) {
		t.Fatal("legacy store changed")
	}
	if db.Path() != filepath.Join(dir, "core-index.db") {
		t.Fatal("core cache is not independently located")
	}
	for _, table := range []string{"nuggets", "refinery_recipes", "refinery_outputs", "editorial_projects", "host_reviews", "agent_views", "reading_packets", "runs"} {
		var count int
		if err := db.SQL().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("core opened editorial/model table %s", table)
		}
	}
}
