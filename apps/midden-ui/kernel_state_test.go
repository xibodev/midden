package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKernelBookkeepingDoesNotOccupyTheArtifactWorkspace(t *testing.T) {
	app, _, _ := kernelApp(t)
	state := filepath.Join(app.opts.Workspace, "state.json")
	original := []byte(`{"user_document":"keep this"} `)
	if err := os.WriteFile(state, original, 0600); err != nil {
		t.Fatal(err)
	}
	s, _ := app.NewSession("state isolation")
	if _, err := app.StartTurn(s.ID, "Write the article."); err != nil {
		t.Fatal(err)
	}
	p := awaitPermission(t, app)
	app.Decide(p.ID, true)
	awaitIdle(t, app)
	for _, name := range []string{"state", "sessions"} {
		if _, err := os.Stat(filepath.Join(app.opts.Workspace, name)); !os.IsNotExist(err) {
			t.Fatalf("kernel bookkeeping appeared in user artifact directory: %s", name)
		}
	}
	raw, err := os.ReadFile(state)
	if err != nil || string(raw) != string(original) {
		t.Fatal("user state.json was modified")
	}
}
