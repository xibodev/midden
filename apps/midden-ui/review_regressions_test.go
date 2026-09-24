package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreWrapperRejectsSeparatorAndUnknownFlagBypasses(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	tool := coreTool{app}
	for _, args := range [][]string{{"collection", "inspect", "--", "../outside"}, {"read", "--", "--state=outside"}, {"read", "--unknown", "--state=outside"}} {
		if tool.validate(args) == nil {
			t.Fatalf("unsafe selector accepted: %v", args)
		}
	}
}
func TestCustomStateDirectoryCannotBeReadOrOverwrittenAsAnArtifact(t *testing.T) {
	opts := testOptions(t)
	opts.State = filepath.Join(opts.Workspace, "host-data")
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err = app.SetModel(ModelInput{Provider: "openai", Model: "fixture", Endpoint: "https://example.invalid", APIKey: "synthetic-credential"}); err != nil {
		t.Fatal(err)
	}
	if _, err = app.ReadFile("host-data/kernel/auth.json"); err == nil {
		t.Fatal("custom state credential exposed")
	}
	files, err := app.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "host-data/") {
			t.Fatal("custom state listed as a deliverable")
		}
	}
	if app.toolPath("host-data/model.json", true) == nil {
		t.Fatal("generic tool allowed overwriting model state")
	}
}
func TestNativeCodexRejectsAnIgnoredCustomEndpoint(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if app.SetModel(ModelInput{Provider: "openai-codex", Model: "fixture", Endpoint: "http://127.0.0.1:12345", CredentialRef: "synthetic"}) == nil {
		t.Fatal("native provider accepted an endpoint it ignores")
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
	if _, err = os.Stat(filepath.Join(opts.State, "sessions.json")); err != nil {
		t.Fatal(err)
	}
}
