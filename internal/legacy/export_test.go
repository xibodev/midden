package legacy

import (
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestExportPreservesOldStoreAndCopiesActualArtifacts(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE refinery_outputs(uid TEXT,path TEXT,provenance_path TEXT,status TEXT);CREATE TABLE editorial_projects(id TEXT,document TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(source, "artifacts", "draft.md")
	if err = os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(artifact, []byte("# Synthetic draft\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO refinery_outputs VALUES('one',?,'','draft'); INSERT INTO editorial_projects VALUES('p','{"goal":"synthetic"}')`, artifact); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "recovered")
	report, err := Export(source, out)
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != 1 || report.Rows != 2 {
		t.Fatalf("legacy work lost: %+v", report)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("legacy database was modified")
	}
	copied, err := os.ReadFile(filepath.Join(out, "files", "artifacts", "draft.md"))
	if err != nil || !strings.Contains(string(copied), "Synthetic") {
		t.Fatal("artifact was not copied", err)
	}
	if _, err = Export(source, out); err == nil {
		t.Fatal("existing output overwritten")
	}
}

func TestLegacyExportDoesNotFollowOutsideArtifactPaths(t *testing.T) {
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.txt")
	os.WriteFile(outside, []byte("not part of legacy state"), 0600)
	db, err := sql.Open("sqlite", filepath.Join(source, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE refinery_outputs(uid TEXT,path TEXT,provenance_path TEXT,status TEXT)`)
	db.Exec(`INSERT INTO refinery_outputs VALUES('outside',?,'','draft')`, outside)
	db.Close()
	report, err := Export(source, filepath.Join(t.TempDir(), "export"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != 0 || len(report.Warnings) == 0 {
		t.Fatal("outside data was copied or omission was hidden")
	}
}
