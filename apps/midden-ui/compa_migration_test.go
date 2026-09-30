package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xibodev/compa/pkg/auth"
)

func TestRetiredNativeSettingsRemainVisibleButRequireReconnection(t *testing.T) {
	for _, provider := range []string{"github-copilot", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			opts := testOptions(t)
			if err := os.MkdirAll(opts.State, 0700); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(Model{Provider: provider, Model: "previous-model", CredentialRef: "old-native-reference"})
			path := filepath.Join(opts.State, "model.json")
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			app, err := NewApp(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			model := app.Status()["model"].(map[string]any)
			if model["configured"] != false || !strings.Contains(fmtModelError(model), "Reconnect") {
				t.Fatalf("retired connection was reported ready: %v", model)
			}
			if model["provider"] != provider || model["credentialRef"] != "old-native-reference" {
				t.Fatal("old selection was silently discarded or converted")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("merely opening the host rewrote old model settings")
			}
			if err := app.SetModel(ModelInput{Provider: provider, Model: "previous-model"}); err == nil {
				t.Fatal("removed native adapter was still selectable")
			}
		})
	}
}

func fmtModelError(model map[string]any) string {
	value, _ := model["setupError"].(string)
	return value
}

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

func TestExistingAPIKeyReferenceRemainsUsableWithoutRewritingIt(t *testing.T) {
	opts := testOptions(t)
	home := filepath.Join(opts.State, "kernel")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"credentials":{"midden-ui-openai":{"access_token":"synthetic-existing-key","provider":"openai","auth_method":"token"}}}`)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	model := Model{Provider: "openai", Model: "synthetic", CredentialRef: "midden-ui-openai"}
	secret, err := resolveModelCredential(model, "")
	if err != nil || secret != "synthetic-existing-key" {
		t.Fatal("existing API-key reference was lost", err)
	}
	model.Provider = "anthropic"
	if _, err := resolveModelCredential(model, ""); err == nil {
		t.Fatal("existing key was accepted by a different provider")
	}
	after, _ := os.ReadFile(filepath.Join(home, "auth.json"))
	if string(after) != string(legacy) {
		t.Fatal("opening or resolving the old key rewrote its backup")
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

func TestRetiredSignInRouteReturnsExplicitMigrationGuidance(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	r := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/auth/copilot/start", strings.NewReader(`{"model":"old-model"}`))
	r.Header.Set("X-Midden-CSRF", app.csrf)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "Reconnect") {
		t.Fatal("stale clients were not given explicit native-adapter migration guidance")
	}
	if app.model.Provider != "" || app.model.CredentialRef != "" {
		t.Fatal("retired sign-in altered model or credential state")
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
