package create

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/mekjr1/midden/internal/index"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRenderEditableSlidesAndStaleSource(t *testing.T) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		t.Skip("Pandoc not installed")
	}
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := index.Recipe{Title: "Slides"}
	db.PutRecipe(&r)
	dir := filepath.Join(home, "artifacts", "refinery", "slides")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "slides.md")
	os.WriteFile(path, []byte("---\nmarp: true\n---\n# First\n\n- One\n\n---\n\n# Second\n\n- Two\n"), 0600)
	o := index.RefineryOutput{RecipeID: r.UID, Kind: "slides", Title: "Test", Format: "marp", Path: path, Status: "draft"}
	db.PutRefineryOutput(&o)
	w := Workflow{DB: db}
	value, err := w.Render(context.Background(), o.UID, "pptx")
	if err != nil {
		t.Fatal(err)
	}
	if value.(map[string]any)["slides"] != 2 {
		t.Fatal(value)
	}
	if _, err = w.DeliveryPath(o.UID, "pptx"); err != nil {
		t.Fatal(err)
	}
	provenance := path + ".provenance.json"
	if err = os.WriteFile(provenance, []byte(`{"status":"draft"}`), 0600); err != nil {
		t.Fatal(err)
	}
	o.ProvenancePath = provenance
	if err = db.PutRefineryOutput(&o); err != nil {
		t.Fatal(err)
	}
	bundle, err := w.Bundle(o.UID)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 5 {
		t.Fatalf("bundle files=%d", len(archive.File))
	}
	os.WriteFile(path, []byte("# Changed"), 0600)
	if _, err = w.DeliveryPath(o.UID, "pptx"); err == nil {
		t.Fatal("stale rendering downloaded")
	}
}
