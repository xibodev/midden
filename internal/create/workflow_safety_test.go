package create

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestWorkflowRefusesRedirectedOutputDirectory(t *testing.T) {
	home := t.TempDir()
	foreign := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = os.MkdirAll(filepath.Join(home, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(foreign, filepath.Join(home, "artifacts", "refinery")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err = os.WriteFile(filepath.Join(foreign, "output.md"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	w := Workflow{DB: db}
	if _, err = w.OwnedFile(filepath.Join(home, "artifacts", "refinery", "output.md")); err == nil {
		t.Fatal("symlink escaped owned state")
	}
	if err = w.checkWritePath(filepath.Join(home, "artifacts", "refinery", "new", "output.md")); err == nil {
		t.Fatal("write allowed through redirected directory")
	}
}
