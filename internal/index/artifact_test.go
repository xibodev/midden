package index

import (
	"path/filepath"
	"testing"
)

// TestPutArtifactReplacesSamePath guards a duplicate found by rescuing the
// same session twice: the file on disk is overwritten, so two rows would list
// a version that no longer exists beside the one that does.
func TestPutArtifactReplacesSamePath(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())

	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	path := filepath.Join("artifacts", "handoff-copilot-9544176f.md")

	if err := db.PutArtifact(Artifact{Kind: "handoff", Title: "first", Path: path}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutArtifact(Artifact{Kind: "handoff", Title: "second", Path: path}); err != nil {
		t.Fatal(err)
	}

	got, err := db.Artifacts(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d rows for one file, want 1", len(got))
	}
	if got[0].Title != "second" {
		t.Errorf("kept %q, want the latest write", got[0].Title)
	}
}

// TestPutArtifactKeepsDistinctPaths confirms replacement is scoped to the
// path and does not collapse unrelated artifacts.
func TestPutArtifactKeepsDistinctPaths(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())

	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, p := range []string{"a.md", "b.md", "c.md"} {
		if err := db.PutArtifact(Artifact{Kind: "handoff", Path: p}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.Artifacts(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("%d rows, want 3", len(got))
	}
}
