package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarmPeekCacheDoesNotIgnoreAnActiveJournal(t *testing.T) {
	state := t.TempDir()
	configureIndexSources(t, "MIDDEN_CLAUDE_ROOT", filepath.Join(t.TempDir(), "source"))
	t.Setenv("MIDDEN_HOME", state)
	writer, err := OpenAt(state)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = os.Stat(filepath.Join(state, "core-index.db-wal")); err != nil {
		t.Fatal(err)
	}
	if err = WarmPeekCache(); err == nil || !strings.Contains(err.Error(), "journal") {
		t.Fatalf("live journal was read or ignored without an explicit cache error: %v", err)
	}
}
