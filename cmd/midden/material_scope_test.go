package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scopeFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	store := filepath.Join(home, "claude")
	project := filepath.Join(store, "projects", "synthetic")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIDDEN_CLAUDE_ROOT", store)
	t.Setenv("MIDDEN_COPILOT_ROOT", "")
	t.Setenv("MIDDEN_OPENCODE_DB", "")
	t.Setenv("MIDDEN_HOME", filepath.Join(home, "state"))
	file, err := os.Create(filepath.Join(project, "fixture.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i := 0; i < 10; i++ {
		if err = encoder.Encode(map[string]any{"type": "assistant", "cwd": home, "sessionId": "fixture", "message": map[string]string{"content": strings.Repeat("synthetic recorded material ", 15)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = runMaterial("read", []string{"--tool", "claude", "--session", "fixture", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var view struct {
		ID      string                `json:"view_id"`
		Records []struct{ ID string } `json:"records"`
	}
	if err = json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return store, view.ID
}

func TestReadCannotSilentlyIgnoreRequestedRecords(t *testing.T) {
	_, _ = scopeFixture(t)
	var out bytes.Buffer
	if runMaterial("read", []string{"--tool", "claude", "--session", "fixture", "--record", "claude:fixture:999@0-0", "--json"}, &out) == nil {
		t.Fatal("record selector was ignored in favor of an orientation sample")
	}
}

func TestRejectedAssetOutputDoesNotCreateSourceDirectories(t *testing.T) {
	store, view := scopeFixture(t)
	parent := filepath.Join(store, "new-parent")
	var out bytes.Buffer
	if runMaterial("assets", []string{"--view", view, "--record", "claude:fixture:0@0-100", "--out", filepath.Join(parent, "images"), "--json"}, &out) == nil {
		t.Fatal("source destination accepted")
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatal("CLI modified the source before rejecting the destination")
	}
}
