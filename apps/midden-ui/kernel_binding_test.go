package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xibodev/compa/pkg/auth"
	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
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

func TestStoredKeyBelongsToItsConnection(t *testing.T) {
	const key = "synthetic-bound-key"
	service := fakeModelService(t, key, true, "bound-model")
	app := newTestApp(t)
	if reply := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": service.URL + "/v1", "apiKey": key}); reply.code != 200 {
		t.Fatalf("create: %d %s", reply.code, reply.raw)
	}
	cfg, err := app.loadModelConfig()
	if err != nil {
		t.Fatal(err)
	}
	instance := findInstance(cfg, "custom_openai")
	if instance == nil || instance.AuthConnectionRef != "credential:midden-custom_openai" {
		t.Fatal("the connection does not reference its own stored key")
	}
	if secret, err := modelservice.ResolveCredentialReference(instance.AuthConnectionRef); err != nil || secret != key {
		t.Fatal("stored key reference was not usable", err)
	}
	if reply := callModels(t, app, "POST", "/api/models/instances/custom_openai/check", map[string]string{"model": "bound-model"}); reply.Status != "tested" {
		t.Fatalf("check: %s", reply.raw)
	}
	if reply := callModels(t, app, "DELETE", "/api/models/instances/custom_openai", nil); reply.code != 200 {
		t.Fatalf("delete: %d %s", reply.code, reply.raw)
	}
	if stored, err := auth.GetCredential("midden-custom_openai"); err != nil || stored != nil {
		t.Fatal("deleting the connection kept its key", err)
	}
	catalogs, err := modelservice.LoadCatalogs()
	if err != nil || catalogs.Entries["custom_openai"] != nil {
		t.Fatal("deleting the connection kept its model list", err)
	}
	if checks := app.loadModelChecks(); checks["custom_openai"] != nil {
		t.Fatal("deleting the connection kept its check results")
	}
}

func TestFailedModelSaveDoesNotLeaveKeysOrModelLists(t *testing.T) {
	service := fakeModelService(t, "synthetic-unsaved-key", true, "unsaved-model")
	app := newTestApp(t)
	// The configuration cannot be saved once the key and model list are written.
	if err := os.MkdirAll(filepath.Join(app.opts.State, "kernel", config.SecurityConfigFile), 0700); err != nil {
		t.Fatal(err)
	}
	reply := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": service.URL + "/v1", "apiKey": "synthetic-unsaved-key"})
	if reply.code == 200 || !strings.Contains(reply.Error, "save model configuration") || strings.Contains(reply.raw, "synthetic-unsaved-key") {
		t.Fatalf("expected an explicit save failure: %d %s", reply.code, reply.raw)
	}
	store, err := auth.LoadStore()
	if err != nil || len(store.Credentials) != 0 {
		t.Fatal("a failed save kept the new key", err)
	}
	catalogs, err := modelservice.LoadCatalogs()
	if err != nil || len(catalogs.Entries) != 0 {
		t.Fatal("a failed save kept the new model list", err)
	}
	if _, err := os.Stat(app.modelConfigPath()); !os.IsNotExist(err) {
		t.Fatal("a failed save wrote a configuration")
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
	pinned, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(pinned), "---\nname: Midden\n") || !strings.Contains(string(pinned), "\nmemory: false\nprivateWorkspace: true\nrequireTools: true\n---\n") {
		t.Fatalf("unexpected pinned agent policy: %q %v", pinned, err)
	}
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
