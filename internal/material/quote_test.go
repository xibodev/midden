package material

import (
	"path/filepath"
	"testing"
)

func TestQuoteCheckOnlyChecksTheExplicitRequestedWords(t *testing.T) {
	service, source, _ := fixture(t)
	view, err := service.Open(source, ReadOptions{Limit: 12, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sources")
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Out: path}); err != nil {
		t.Fatal(err)
	}
	result, err := MatchQuote(path, view.Records[11].ID, "the scheduler issue is still unresolved")
	if err != nil || !result.Matched {
		t.Fatal("source words did not match", err)
	}
	result, err = MatchQuote(path, view.Records[11].ID, "the scheduler issue is resolved")
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched {
		t.Fatal("changed outcome accepted")
	}
}
