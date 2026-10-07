package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xibodev/llm-provider-auth/tokenstore"
)

func decodedJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var value any
	if err := decodeJSON(raw, &value); err != nil {
		t.Fatalf("invalid JSON %s: %v", raw, err)
	}
	return value
}

func TestKernelConfigKeepsWhatMiddenDoesNotChange(t *testing.T) {
	home := t.TempDir()
	original := `{
  "agents": {"defaults": {"workspace": "/w", "max_tokens": 32768, "model_name": "old/model", "routing": {"light_model": "old/model"}},
             "list": [{"id": "main", "model": "old/model", "skills": ["a"]}]},
  "channel_list": {"telegram": {"enabled": false, "type": "telegram", "settings": {"token": "[NOT_HERE]"}}},
  "tools": {"approval": {"rules": [{"tool": "exec", "action": "ask"}]}, "exec": {"timeout_seconds": 60}},
  "provider_instances": [{"id": "old", "provider_kind": "openai", "adapter": "openai-compatible", "protocol": "openai",
    "settings": {"display_name": "Old", "big": 12345678901234567890}, "runtime": {"proxy": "", "rpm": 7}, "state": "enabled"}],
  "future_setting": {"nested": [1, 2.5, "three"]}
}`
	if err := os.WriteFile(kernelConfigPath(home), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadKernelConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel() != "old/model" || len(cfg.Instances) != 1 || cfg.instance("old") == nil {
		t.Fatalf("model settings were not read: %q %+v", cfg.DefaultModel(), cfg.Instances)
	}
	cfg.Instances = append(cfg.Instances, &providerInstance{ID: "new", ProviderKind: "custom_openai", Adapter: adapterOpenAI,
		Protocol: "openai", Endpoint: "http://127.0.0.1:1/v1", State: instanceEnabled})
	cfg.eachSelection(func(value string) string {
		if value == "old/model" {
			return "new/model"
		}
		return value
	})
	if err := saveKernelConfig(home, cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(kernelConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	got, want := decodedJSON(t, saved).(map[string]any), decodedJSON(t, []byte(original)).(map[string]any)
	for _, key := range []string{"channel_list", "tools", "future_setting"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Fatalf("%s changed: %v", key, got[key])
		}
	}
	defaults := got["agents"].(map[string]any)["defaults"].(map[string]any)
	if defaults["max_tokens"] != json.Number("32768") || defaults["workspace"] != "/w" || defaults["model_name"] != "new/model" ||
		defaults["routing"].(map[string]any)["light_model"] != "new/model" {
		t.Fatalf("agent defaults = %v", defaults)
	}
	agent := got["agents"].(map[string]any)["list"].([]any)[0].(map[string]any)
	if agent["model"] != "new/model" || !reflect.DeepEqual(agent["skills"], []any{"a"}) {
		t.Fatalf("agent = %v", agent)
	}
	instances := got["provider_instances"].([]any)
	old := instances[0].(map[string]any)
	if old["settings"].(map[string]any)["big"] != json.Number("12345678901234567890") ||
		!reflect.DeepEqual(old["runtime"], map[string]any{"proxy": "", "rpm": json.Number("7")}) {
		t.Fatalf("an instance lost settings it does not change: %v", old)
	}
	if len(instances) != 2 || instances[1].(map[string]any)["id"] != "new" {
		t.Fatalf("instances = %v", instances)
	}
}

func TestKernelConfigSaveRefusesAChangeMadeMeanwhile(t *testing.T) {
	home := t.TempDir()
	cfg, err := loadKernelConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetDefaultModel("a/b")
	other := []byte(`{"agents": {"defaults": {"model_name": "x/y"}}}`)
	if err := os.WriteFile(kernelConfigPath(home), other, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveKernelConfig(home, cfg); !errors.Is(err, errSettingsChanged) {
		t.Fatalf("save over another writer's change = %v", err)
	}
	if data, _ := os.ReadFile(kernelConfigPath(home)); string(data) != string(other) {
		t.Fatal("the other writer's change was replaced")
	}
	reloaded, err := loadKernelConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.SetDefaultModel("a/b")
	if err := saveKernelConfig(home, reloaded); err != nil {
		t.Fatal(err)
	}
	if again, _ := loadKernelConfig(home); again.DefaultModel() != "a/b" {
		t.Fatal("a fresh change was not saved")
	}
	if err := saveKernelConfig(home, reloaded); err != nil {
		t.Fatalf("saving the same config twice failed: %v", err)
	}
}

func TestKernelConfigRejectsWhatIsNotAnObject(t *testing.T) {
	home := t.TempDir()
	for _, content := range []string{`[]`, `{"provider_instances": {"id": 1}}`, `{`} {
		if err := os.WriteFile(kernelConfigPath(home), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadKernelConfig(home); err == nil {
			t.Fatalf("%s was read as settings", content)
		}
	}
}

func TestExactTargets(t *testing.T) {
	for raw, want := range map[string]exactTarget{"a/b": {"a", "b"}, " a/b/c ": {"a", "b/c"}} {
		if got, err := parseExactTarget(raw); err != nil || got != want {
			t.Fatalf("parse %q = %+v %v", raw, got, err)
		}
	}
	for _, raw := range []string{"route", "A/b", "a/", "a/b c", "a/b//c", "/b"} {
		if _, err := parseExactTarget(raw); err == nil {
			t.Fatalf("%q was accepted", raw)
		}
	}
}

func TestAuthStoreKeepsOtherCredentialsExactly(t *testing.T) {
	home := t.TempDir()
	other := `{"access_token":"other-secret","provider":"openai","auth_method":"api_key","unknown_field":[1,2]}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"credentials":{"openai":`+other+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := kernelAuth(home)
	if err := store.set("Midden-Fixture", &authCredential{AccessToken: "fixture-key", AuthMethod: apiKeyAuthMethod}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.get("midden-fixture")
	if err != nil || stored == nil || stored.AccessToken != "fixture-key" || stored.Provider != "midden-fixture" {
		t.Fatalf("stored = %+v %v", stored, err)
	}
	credentials, err := store.read()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodedJSON(t, credentials["openai"]), decodedJSON(t, []byte(other))) {
		t.Fatalf("another credential changed: %s", credentials["openai"])
	}
	if err := store.remove("MIDDEN-FIXTURE", "absent"); err != nil {
		t.Fatal(err)
	}
	if gone, _ := store.get("midden-fixture"); gone != nil {
		t.Fatal("the credential was not removed")
	}
	if kept, _ := store.get("openai"); kept == nil || kept.AccessToken != "other-secret" {
		t.Fatal("removing one credential removed another")
	}
	if info, err := os.Stat(filepath.Join(home, "auth.json")); err != nil || (info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("auth.json is not private: %v %v", info, err)
	}
}

func TestCredentialReferencesResolveOnlyUsableTokens(t *testing.T) {
	home := t.TempDir()
	store := kernelAuth(home)
	_ = store.set("live", &authCredential{AccessToken: " secret "})
	_ = store.set("expired", &authCredential{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Minute)})
	_ = store.set("empty", &authCredential{})
	if secret, err := resolveCredentialRef(home, "credential:live"); err != nil || secret != "secret" {
		t.Fatalf("live = %q %v", secret, err)
	}
	for _, ref := range []string{"credential:expired", "credential:empty", "credential:absent", "token:live", "credential:"} {
		if _, err := resolveCredentialRef(home, ref); err == nil {
			t.Fatalf("%s resolved", ref)
		}
	}
}

func TestTokenStoreFencesStaleWriters(t *testing.T) {
	ctx := context.Background()
	store := authTokenStore{kernelAuth(t.TempDir())}
	saved, err := store.Save(ctx, "ext-signin-a", tokenstore.Record{AccessToken: "first", RefreshToken: "r1", Metadata: map[string]string{"k": "v"}})
	if err != nil || saved.Revision == "" {
		t.Fatalf("save = %+v %v", saved, err)
	}
	loaded, err := store.Load(ctx, "EXT-SIGNIN-A")
	if err != nil || loaded.AccessToken != "first" || loaded.Revision != saved.Revision || loaded.Metadata["k"] != "v" {
		t.Fatalf("load = %+v %v", loaded, err)
	}
	if _, err := store.ReplaceIfCurrent(ctx, "ext-signin-a", "stale", tokenstore.Record{AccessToken: "x"}); !errors.Is(err, tokenstore.ErrConflict) {
		t.Fatalf("stale replace = %v", err)
	}
	replaced, err := store.ReplaceIfCurrent(ctx, "ext-signin-a", saved.Revision, tokenstore.Record{AccessToken: "second"})
	if err != nil || replaced.Revision == saved.Revision {
		t.Fatalf("replace = %+v %v", replaced, err)
	}
	if err := store.RevokeIfCurrent(ctx, "ext-signin-a", saved.Revision); !errors.Is(err, tokenstore.ErrConflict) {
		t.Fatalf("stale revoke = %v", err)
	}
	if err := store.RevokeIfCurrent(ctx, "ext-signin-a", replaced.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, "ext-signin-a"); !errors.Is(err, tokenstore.ErrNotFound) {
		t.Fatalf("revoked load = %v", err)
	}
	if err := store.RevokeIfCurrent(ctx, "ext-signin-a", replaced.Revision); !errors.Is(err, tokenstore.ErrNotFound) {
		t.Fatalf("absent revoke = %v", err)
	}
}

func TestTokenStoreLeaseIsExclusive(t *testing.T) {
	store := authTokenStore{kernelAuth(t.TempDir())}
	release, err := store.Lease(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := store.Lease(ctx, "key"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second lease = %v", err)
	}
	other, err := store.Lease(context.Background(), "other-key")
	if err != nil {
		t.Fatalf("another key's lease waited: %v", err)
	}
	other()
	release()
	release()
	again, err := store.Lease(context.Background(), "key")
	if err != nil {
		t.Fatalf("the lease was not released: %v", err)
	}
	again()
}

func TestCatalogsKeepOtherInstancesLists(t *testing.T) {
	home := t.TempDir()
	foreign := `{"id":"theirs","instance_id":"theirs","provider":"openai","api_base":"x","models":[{"id":"m","extra":true}],"fetched_at":"t"}`
	if err := os.WriteFile(catalogsPath(home), []byte(`{"entries":{"theirs":`+foreign+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	instance := &providerInstance{ID: "mine", ProviderKind: "custom_openai", Endpoint: "http://127.0.0.1:1/v1/"}
	if err := saveInstanceCatalog(home, instance, []catalogModel{{ID: "a", Surfaces: []string{"chat_completions"}}}); err != nil {
		t.Fatal(err)
	}
	entries, err := loadCatalogs(home)
	if err != nil {
		t.Fatal(err)
	}
	if mine := entries["mine"]; !validCatalog("mine", mine, instance) || mine.APIBase != "http://127.0.0.1:1/v1" || len(mine.Models) != 1 {
		t.Fatalf("saved list = %+v", mine)
	}
	if validCatalog("mine", entries["mine"], &providerInstance{ID: "mine", ProviderKind: "ollama"}) {
		t.Fatal("a list from another kind of provider counted as the instance's")
	}
	if err := deleteInstanceCatalog(home, "mine"); err != nil {
		t.Fatal(err)
	}
	raw, err := readCatalogEntries(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["mine"]; ok || !reflect.DeepEqual(decodedJSON(t, raw["theirs"]), decodedJSON(t, []byte(foreign))) {
		t.Fatalf("lists after delete = %v", raw)
	}
	if err := os.WriteFile(catalogsPath(home), []byte(`{"entries":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCatalogs(home); err == nil || !strings.Contains(err.Error(), "model_catalogs.json") {
		t.Fatalf("a corrupt file read as %v", err)
	}
	if err := saveInstanceCatalog(home, instance, nil); err == nil {
		t.Fatal("a corrupt file was replaced instead of reported")
	}
}

func TestFileLockSerializesWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := withFileLock(path, func() error {
				data, _ := os.ReadFile(path)
				n, _ := strconv.Atoi(string(data))
				return replaceFile(path, []byte(strconv.Itoa(n+1)))
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if data, _ := os.ReadFile(path); string(data) != "20" {
		t.Fatalf("counter = %s", data)
	}
	if _, err := os.Stat(path + ".flock"); err != nil {
		t.Fatal("the lock does not sit beside the file as Compa's does")
	}
}
