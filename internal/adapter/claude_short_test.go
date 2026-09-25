package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

func TestClaudeDiscoversShortSubstantiveTranscripts(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "sample")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	content := []byte("{\"type\":\"user\",\"cwd\":\"C:\\\\sample\",\"timestamp\":\"2026-01-01T12:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"Preserve explicit zero values instead of treating them as absent observations.\"}}\n" +
		"{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":\"Use an explicit presence check; zero and a missing value carry different information.\"}}\n")
	if len(content) >= 2048 {
		t.Fatal("regression fixture must stay below the former size cutoff")
	}
	path := filepath.Join(project, "short-lesson.jsonl")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "empty.jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, all := range []bool{false, true} {
		sessions, err := (&Claude{Root: root}).Sessions(core.Scope{IncludeNoise: all})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 || sessions[0].ID != "short-lesson" || sessions[0].Bytes != int64(len(content)) {
			t.Fatalf("all=%v: short session was omitted or an empty transcript was included: %+v", all, sessions)
		}
	}
}

func TestClaudeReportsUnidentifiableShortTranscript(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "sample")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "broken.jsonl"), []byte("not a session\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Claude{Root: root}).Sessions(core.Scope{IncludeNoise: true}); err == nil {
		t.Fatal("a nonempty unidentifiable transcript must not make an inventory falsely complete")
	}
}
