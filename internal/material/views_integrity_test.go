package material

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCachedViewCannotLaunderEditedTextIntoACollection(t *testing.T) {
	service, source, _ := fixture(t)
	view, err := service.Open(source, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(service.State, "views", view.ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cached cachedView
	if err = json.Unmarshal(raw, &cached); err != nil {
		t.Fatal(err)
	}
	cached.View.Records[0].Text = "fabricated completion"
	raw, _ = json.Marshal(cached)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Read(view.ID, ReadOptions{}); err == nil {
		t.Fatal("edited source view was read without an integrity error")
	}
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Out: filepath.Join(t.TempDir(), "sources")}); err == nil {
		t.Fatal("edited source view was exported")
	}
}
