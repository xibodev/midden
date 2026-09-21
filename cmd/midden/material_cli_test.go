package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrdinaryCLIReadsAndCollectsWithoutModuleEnvelope(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, ".claude", "projects", "synthetic")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("MIDDEN_HOME", filepath.Join(home, "state"))
	file, err := os.Create(filepath.Join(project, "session-fixture.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		raw, _ := json.Marshal(map[string]any{"type": "assistant", "cwd": home, "sessionId": "session-fixture", "timestamp": "2026-01-01T00:00:00Z", "message": map[string]string{"role": "assistant", "content": strings.Repeat("Use explicit presence when zero is meaningful. ", 8)}})
		if _, err = file.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	var output bytes.Buffer
	if err = runMaterial("read", []string{"--tool", "claude", "--session", "session-fixture", "--limit", "8", "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var view struct {
		ID      string                      `json:"view_id"`
		Records []struct{ ID, Text string } `json:"records"`
	}
	if err = json.Unmarshal(output.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || len(view.Records) == 0 || view.Records[0].Text == "" {
		t.Fatalf("unusable read result: %s", output.String())
	}
	if output.Len() > 16384 {
		t.Fatal("ordinary JSON response exceeded its bound")
	}
	path := filepath.Join(home, "work", "sources")
	output.Reset()
	if err = runMaterial("collect", []string{"--view", view.ID, "--record", view.Records[0].ID, "--out", path, "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(path, "records.jsonl")); err != nil {
		t.Fatal("portable source collection missing", err)
	}
	if strings.Contains(output.String(), `"protocol"`) {
		t.Fatal("retired module envelope leaked into normal CLI")
	}
}

func TestCoreCLIRejectsUnboundedSearchAndImplicitOverwrite(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	var output bytes.Buffer
	if runMaterial("search", []string{"needle", "--json"}, &output) == nil {
		t.Fatal("search without a view was accepted")
	}
}
