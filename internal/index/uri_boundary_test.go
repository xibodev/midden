package index

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexURICannotRedirectAnAllowedDestinationIntoSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	configureIndexSources(t, "MIDDEN_OPENCODE_DB", source)
	original, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = original.Exec(`CREATE TABLE source_records (value TEXT)`); err != nil {
		original.Close()
		t.Fatal(err)
	}
	if err = original.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	state := source + "#separate-state"
	db, err := OpenAt(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("SQLite URI parsing redirected index writes into the source database")
	}
	if _, err = os.Stat(filepath.Join(state, "core-index.db")); err != nil {
		t.Fatal("index was not written at its checked filesystem destination")
	}
	readOnly, err := OpenReadOnly(state)
	if err != nil {
		t.Fatalf("read-only index path was not URI-escaped: %v", err)
	}
	if err = readOnly.Close(); err != nil {
		t.Fatal(err)
	}
}
