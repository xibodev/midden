package material

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergedVersionsDoNotSilentlyResolveAmbiguousQuotes(t *testing.T) {
	service, source, transcript := fixture(t)
	root := t.TempDir()
	first, err := service.Open(source, ReadOptions{Limit: 12, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(root, "first")
	service.Collect(CollectOptions{Views: []string{first.ID}, Out: firstPath})
	raw, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(transcript, []byte(strings.Replace(string(raw), "preserve zero scores", "preserve ones scores", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := service.Open(source, ReadOptions{Limit: 12, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	if first.Records[11].ID != second.Records[11].ID {
		t.Fatal("test requires same ordinal/window across different revisions")
	}
	secondPath := filepath.Join(root, "second")
	service.Collect(CollectOptions{Views: []string{second.ID}, Out: secondPath})
	merged := filepath.Join(root, "merged")
	if _, err = Merge([]string{firstPath, secondPath}, merged); err != nil {
		t.Fatal(err)
	}
	if _, err = MatchQuote(merged, first.Records[11].ID, "preserve ones scores"); err == nil {
		t.Fatal("ambiguous source version silently chose one record")
	}
	selected, err := Select(merged, []string{first.Records[11].ID}, filepath.Join(root, "selected"))
	if err != nil {
		t.Fatal(err)
	}
	if selected.RecordCount != 2 {
		t.Fatal("selection discarded merged source-version identity")
	}
}
