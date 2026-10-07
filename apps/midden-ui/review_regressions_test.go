package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreValidatorRejectsSeparatorAndUnknownFlagBypasses(t *testing.T) {
	app := newTestApp(t)
	for _, args := range [][]string{{"collection", "inspect", "--", "../outside"}, {"read", "--", "--state=outside"}, {"read", "--unknown", "--state=outside"}} {
		if app.validateCoreArgs(args) == nil {
			t.Fatalf("unsafe selector accepted: %v", args)
		}
	}
}

func TestKernelSettingsAreNotThePersonsFiles(t *testing.T) {
	app := newTestApp(t)
	storeTestModel(t, app, "https://example.invalid/v1", "synthetic-credential")
	for _, name := range []string{"../../kernel/auth.json", "../../kernel/config.json", "../../app/sessions.json"} {
		if _, err := app.ReadFile(name); err == nil {
			t.Fatalf("%s was readable as a file", name)
		}
		if app.writePath(name) == nil {
			t.Fatalf("%s was accepted as an output", name)
		}
	}
	files, err := app.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f.Path, "auth.json") || strings.Contains(f.Path, "AGENT.md") {
			t.Fatal("the App's own state was listed as a file", f.Path)
		}
	}
}

func TestSessionSaveDoesNotWriteAnUnreadableAggregate(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	s, err := app.NewSession("small")
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.sessions[s.ID].Messages = []Message{{Role: "user", Content: strings.Repeat("x", 9<<20)}}
	err = app.saveSessionsLocked()
	app.mu.Unlock()
	if err == nil {
		t.Fatal("unreadable aggregate saved")
	}
	app.Close()
	reopened, err := NewApp(opts)
	if err != nil {
		t.Fatal("last loadable state was lost", err)
	}
	reopened.Close()
	if _, err = os.Stat(filepath.Join(opts.Data, "app", "sessions.json")); err != nil {
		t.Fatal(err)
	}
}
