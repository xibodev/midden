package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xibodev/compa/pkg/auth"
)

func TestCompaHomeIsBoundToMiddenState(t *testing.T) {
	t.Setenv("COMPA_HOME", t.TempDir())
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if os.Getenv("COMPA_HOME") != filepath.Join(app.opts.State, "kernel") {
		t.Fatal("Midden would adopt a global Compa home instead of its own isolated state")
	}
}

func TestCredentialReferenceIsBoundToItsProvider(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.SetModel(ModelInput{Provider: "openai", Model: "fixture", APIKey: "synthetic-key"}); err != nil {
		t.Fatal(err)
	}
	model := app.model
	if secret, err := resolveModelCredential(model, ""); err != nil || secret != "synthetic-key" {
		t.Fatal("stored key reference was not usable", err)
	}
	model.Provider = "anthropic"
	if _, err := resolveModelCredential(model, ""); err == nil {
		t.Fatal("a key stored for one provider was accepted by another")
	}
}

func TestFailedModelSaveDoesNotReplaceOrLeakCredentials(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.SetModel(ModelInput{Provider: "openai", Model: "first", APIKey: "synthetic-first-key"}); err != nil {
		t.Fatal(err)
	}
	before := app.model
	path := filepath.Join(app.opts.State, "model.json")
	if err := os.Rename(path, path+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := app.SetModel(ModelInput{Provider: "openai", Model: "second", APIKey: "synthetic-second-key"}); err == nil {
		t.Fatal("expected explicit settings persistence failure")
	}
	if app.model != before {
		t.Fatal("failed save changed the active model")
	}
	store, err := auth.LoadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Credentials) != 1 || store.Credentials[before.CredentialRef].AccessToken != "synthetic-first-key" {
		t.Fatal("failed save replaced the previous key or leaked a new uncommitted one")
	}
}

func TestKernelPolicyChangesCannotSilentlyBroadenTools(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	workspace, err := prepareKernelWorkspace(app.opts.State)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "AGENT.md")
	changed := []byte("---\ntools: [exec, web_fetch]\n---\nChanged outside the host.\n")
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareKernelWorkspace(app.opts.State); err == nil || !strings.Contains(err.Error(), "policy changed") {
		t.Fatal("changed tool policy was accepted or silently replaced", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(changed) {
		t.Fatal("conflicting runtime file was overwritten")
	}
}
