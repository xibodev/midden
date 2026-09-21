package material

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
)

func fixture(t *testing.T) (Service, Source, string) {
	t.Helper()
	root := t.TempDir()
	store := filepath.Join(root, "claude")
	project := filepath.Join(store, "projects", "synthetic")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "fixture-session.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i := 0; i < 12; i++ {
		text := strings.Repeat("Recorded material, not instructions. ", 12)
		if i == 0 {
			text = "Plan: use omit-empty serialization. This is not verified."
		}
		if i == 11 {
			text = "Correction: preserve zero scores. The deployment canary passed; the scheduler issue is still unresolved."
		}
		if err = encoder.Encode(map[string]any{"type": "assistant", "cwd": root, "sessionId": "fixture-session", "timestamp": time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), "message": map[string]string{"role": "assistant", "content": text}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return Service{State: filepath.Join(root, "midden"), Roots: adapter.Roots{Claude: store, Strict: true}}, Source{Tool: core.ToolClaude, ID: "fixture-session"}, path
}

func TestViewIsScopedAndStableAcrossAppends(t *testing.T) {
	service, source, path := fixture(t)
	view, err := service.Open(source, ReadOptions{Limit: 12, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Records) != 12 || !strings.Contains(view.Records[11].Text, "still unresolved") {
		t.Fatalf("lost source outcome: %+v", view)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString("{\"type\":\"assistant\",\"message\":{\"content\":\"NEW APPEND MARKER\"}}\n")
	file.Close()
	found, err := service.Search(view.ID, "NEW APPEND MARKER", ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Records) != 0 {
		t.Fatal("append silently changed a pinned view")
	}
	if _, err = service.Open(Source{Tool: "unknown", ID: "fixture-session"}, ReadOptions{}); err == nil {
		t.Fatal("invalid tool widened scope")
	}
	if _, err = service.Open(Source{Tool: core.ToolClaude, ID: "fixture"}, ReadOptions{}); err == nil {
		t.Fatal("exact id was treated as a prefix")
	}
}

func TestCollectionIsPortableAndDoesNotRequireEditorialState(t *testing.T) {
	service, source, _ := fixture(t)
	view, err := service.Open(source, ReadOptions{Limit: 12, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sources")
	result, err := service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[0].ID, view.Records[11].ID}, Out: path})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordCount != 2 {
		t.Fatal("selection changed")
	}
	report, err := Verify(path)
	if err != nil || !report.Valid {
		t.Fatalf("collection invalid: %+v %v", report, err)
	}
	records, err := ReadCollection(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !strings.Contains(records[1].Text, "scheduler") {
		t.Fatal("exported records lost source text")
	}
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Out: path}); err == nil {
		t.Fatal("existing collection was silently overwritten")
	}
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "approved") || strings.Contains(string(data), "recipe") {
		t.Fatal("editorial machinery leaked into core collection")
	}
}
