package legacy

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestExportDestinationCannotAliasTheSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "legacy")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(source, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE editorial_projects(id TEXT,document TEXT)`)
	db.Close()
	alias := filepath.Join(root, "alias")
	if err = os.Symlink(source, alias); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err = Export(source, filepath.Join(alias, "export")); err == nil {
		t.Fatal("export wrote through an alias into the source")
	}
	if _, err = os.Stat(filepath.Join(source, "export")); !os.IsNotExist(err) {
		t.Fatal("source store was mutated")
	}
}
