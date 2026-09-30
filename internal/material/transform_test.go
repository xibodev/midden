package material

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectionSelectionMergeAndExportPreserveEvidence(t *testing.T) {
	service, source, _ := fixture(t)
	view, err := service.Open(source, ReadOptions{Limit: 12, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	full := filepath.Join(root, "full")
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Out: full}); err != nil {
		t.Fatal(err)
	}
	chosen := filepath.Join(root, "chosen")
	if _, err = Select(full, []string{view.Records[11].ID}, chosen); err != nil {
		t.Fatal(err)
	}
	merged := filepath.Join(root, "merged")
	result, err := Merge([]string{full, chosen}, merged)
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordCount != 12 {
		t.Fatal("merge duplicated shared evidence")
	}
	out := filepath.Join(root, "source-notes.md")
	if err = Export(merged, out, "markdown"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "scheduler issue is still unresolved") {
		t.Fatal("export lost the scoped outcome")
	}
	if err = Export(merged, out, "markdown"); err == nil {
		t.Fatal("export overwrote an existing file")
	}
	if _, err = Select(full, []string{"not-a-record"}, filepath.Join(root, "bad")); err == nil {
		t.Fatal("unknown selection silently shrank")
	}
}

func TestCollectionVerificationFindsTampering(t *testing.T) {
	service, source, _ := fixture(t)
	view, err := service.Open(source, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "collection")
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Out: path}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "records.jsonl")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "Recorded", "Altered!", 1))
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Problems) == 0 {
		t.Fatal("tampered data was verified")
	}
	if _, err = Select(path, []string{view.Records[0].ID}, filepath.Join(t.TempDir(), "copied")); err == nil {
		t.Fatal("transform accepted invalid source collection")
	}
}
