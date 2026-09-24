package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xibodev/facet-studio/pkg/auth"
	"github.com/xibodev/facet-studio/pkg/config"
)

func TestModelCredentialUsesOnlyTheIsolatedHostStore(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err = app.SetModel(ModelInput{Provider: "openai", Model: "fixture", Endpoint: "https://example.invalid", APIKey: "synthetic-credential-value"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(opts.State, "kernel", "auth.json")); err != nil {
		t.Fatal("credential did not use isolated host store", err)
	}
	raw, err := os.ReadFile(filepath.Join(opts.State, "model.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-credential-value") {
		t.Fatal("credential leaked into nonsecret model settings")
	}
	credential, err := auth.GetCredential(app.model.CredentialRef)
	if err != nil || credential == nil {
		t.Fatal("stored reference not usable", err)
	}
}
func TestConfiguredSourceStoreCannotBeUsedAsHostWorkspace(t *testing.T) {
	opts := testOptions(t)
	opts.SourceEnv = map[string]string{"MIDDEN_CLAUDE_ROOT": opts.Workspace}
	if _, err := NewApp(opts); err == nil {
		t.Fatal("source store accepted as an editable workspace")
	}
}
