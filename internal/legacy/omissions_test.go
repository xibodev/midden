package legacy

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsupportedLegacyTablesAreDisclosed(t *testing.T) {
	source := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(source, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE custom_work(id TEXT);INSERT INTO custom_work VALUES('synthetic')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	result, err := Export(source, filepath.Join(t.TempDir(), "export"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Warnings, " "), "custom_work") {
		t.Fatal("unsupported legacy work was silently omitted")
	}
}
