package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetiredManifestIsNotLoadedOrRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old-video.yaml")
	raw := []byte("name: openmontage\nkind: capability\ncost: spends\nenabled: true\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDir(dir)
	if err != nil || len(loaded) != 0 {
		t.Fatalf("retired adapter loaded: %+v %v", loaded, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("user manifest was changed")
	}
}
