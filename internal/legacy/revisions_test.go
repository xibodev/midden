package legacy

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestExportIncludesActualLegacyRevisionHistory(t *testing.T) {
	source := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(source, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE editorial_projects(id TEXT);CREATE TABLE editorial_revisions(project_id TEXT,revision INTEGER,document TEXT);
INSERT INTO editorial_projects VALUES('synthetic');
INSERT INTO editorial_revisions VALUES('synthetic',1,'{"stage":"first"}'),('synthetic',2,'{"stage":"second"}')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	result, err := Export(source, filepath.Join(t.TempDir(), "export"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 {
		t.Fatalf("revision history omitted: %+v", result)
	}
}
