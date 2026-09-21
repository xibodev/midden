package index

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
)

func TestWarmPeekCacheInstallsMetadataAndClearsItForMissingState(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "claude")
	project := filepath.Join(store, "projects", "synthetic")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	configureIndexSources(t, "MIDDEN_CLAUDE_ROOT", store)
	state := filepath.Join(root, "state")
	t.Setenv("MIDDEN_HOME", state)
	t.Cleanup(func() { adapter.SetPeekCache(nil) })
	transcript := filepath.Join(project, "synthetic-session.jsonl")
	raw, err := json.Marshal(map[string]any{
		"type": "user", "cwd": root,
		"message": map[string]string{"role": "user", "content": "Uncached source title."},
		"padding": strings.Repeat("x", 3000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(transcript, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(transcript)
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenAt(state)
	if err != nil {
		t.Fatal(err)
	}
	err = db.PutSessions([]core.Session{{
		Tool: core.ToolClaude, ID: "synthetic-session", Dir: root,
		Title: "Recorded cached title", Bytes: info.Size(), Updated: info.ModTime(),
		TranscriptPath: transcript,
	}})
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("create synthetic cache: %v %v", err, closeErr)
	}
	indexPath := filepath.Join(state, "core-index.db")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = WarmPeekCache(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("passive warmup changed the existing WAL-mode index")
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "core-index.db" {
		t.Fatalf("passive warmup created state beside the WAL-mode index: %v %v", entries, err)
	}
	source := &adapter.Claude{Root: store}
	sessions, err := source.Sessions(core.Scope{IDs: []string{"synthetic-session"}, IncludeNoise: true})
	if err != nil || len(sessions) != 1 || sessions[0].Title != "Recorded cached title" {
		t.Fatalf("passive warmup did not install existing metadata: %+v %v", sessions, err)
	}
	missing := filepath.Join(root, "missing-state")
	t.Setenv("MIDDEN_HOME", missing)
	if err = WarmPeekCache(); err != nil {
		t.Fatal(err)
	}
	sessions, err = source.Sessions(core.Scope{IDs: []string{"synthetic-session"}, IncludeNoise: true})
	if err != nil || len(sessions) != 1 || sessions[0].Title != "Uncached source title." {
		t.Fatalf("absent state retained metadata from a different cache: %+v %v", sessions, err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("passive warmup created missing state")
	}
}

func TestWarmPeekCacheReportsCorruptStateWithoutRewritingIt(t *testing.T) {
	state := t.TempDir()
	configureIndexSources(t, "MIDDEN_CLAUDE_ROOT", filepath.Join(t.TempDir(), "source"))
	t.Setenv("MIDDEN_HOME", state)
	path := filepath.Join(state, "core-index.db")
	raw := []byte("synthetic invalid cache")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WarmPeekCache(); err == nil {
		t.Fatal("unexpected cache corruption was silently treated as ordinary absence")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("passive warmup rewrote corrupt state")
	}
}
