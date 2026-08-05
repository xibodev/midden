package plugins

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDirDeterministicallyReportsMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "z-valid.yaml", "name: valid\nkind: capability\ncost: free\n")
	writeManifest(t, dir, "a-broken.yml", "name: first\nname: second\n")

	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded) != 2 {
		t.Fatalf("LoadDir() returned %d entries, want 2", len(loaded))
	}
	if filepath.Base(loaded[0].Manifest.File) != "a-broken.yml" || loaded[0].Error == nil {
		t.Fatalf("first entry = %#v, want malformed a-broken.yml", loaded[0])
	}
	if loaded[1].Error != nil || loaded[1].Manifest.Name != "valid" {
		t.Fatalf("second entry = %#v, want valid manifest", loaded[1])
	}
}

func TestLoadDirRejectsUnknownManifestFields(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "typo.yaml", `
name: typo
kind: service
enable: false
cost: free
`)
	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Error == nil {
		t.Fatalf("unknown enable field was silently accepted: %#v", loaded)
	}
	if loaded[0].Manifest.IsEnabled() == false {
		t.Fatal("test did not prove the typo would otherwise default to enabled")
	}
}

func TestLoadDirRejectsTrailingYAMLDocuments(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "two-docs.yaml", `
name: first
kind: capability
cost: free
---
enable: false
`)
	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Error == nil || !strings.Contains(loaded[0].Error.Error(), "exactly one YAML document") {
		t.Fatalf("trailing document was accepted: %#v", loaded)
	}
}

func TestLoadDirRejectsDuplicateActionNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		writeManifest(t, dir, name, "name: same\nkind: capability\ncost: free\nprobe:\n  kind: directory\n  path: /tmp\n")
	}
	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded=%d, want 2", len(loaded))
	}
	for _, item := range loaded {
		if item.Error == nil || !strings.Contains(item.Error.Error(), "duplicate manifest name") {
			t.Fatalf("duplicate not rejected: %#v", item)
		}
	}
}

func TestLoadDirRejectsMalformedDuplicateActionName(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "a-broken.yaml", `
name: open-notebook
kind: service
cost: free
unknown_field: true
`)
	writeManifest(t, dir, "b-valid.yaml", `
name: open-notebook
kind: service
cost: free
probe:
  kind: http
  url: http://127.0.0.1:5055/openapi.json
`)
	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded=%d, want 2", len(loaded))
	}
	for _, item := range loaded {
		if item.Error == nil || !strings.Contains(item.Error.Error(), "duplicate manifest name") {
			t.Fatalf("malformed duplicate escaped collision detection: %#v", item)
		}
	}
}

func TestLoadDirRejectsLinksAndOversizedManifests(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	writeManifest(t, dir, "target.yaml", "name: target\nkind: capability\ncost: free\n")
	linkCreated := os.Symlink(target, filepath.Join(dir, "linked.yaml")) == nil
	if err := os.WriteFile(filepath.Join(dir, "large.yaml"), make([]byte, maxManifestBody+1), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var linkErr, largeErr bool
	for _, item := range loaded {
		switch filepath.Base(item.Manifest.File) {
		case "linked.yaml":
			linkErr = item.Error != nil && strings.Contains(item.Error.Error(), "regular non-link")
		case "large.yaml":
			largeErr = item.Error != nil && strings.Contains(item.Error.Error(), "exceeds")
		}
	}
	if (linkCreated && !linkErr) || !largeErr {
		t.Fatalf("manifest boundary errors not reported: %#v", loaded)
	}
}

func TestProbeHTTPNeverExpandsAmbientSecrets(t *testing.T) {
	t.Setenv("PLUGIN_TEST_SECRET", "not-for-the-network")
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		t.Errorf("probe sent a request containing an ambient secret candidate: %s", r.URL)
	}))
	defer server.Close()

	result := ProbeManifest(context.Background(), Manifest{
		Probe: &Probe{Kind: "http", URL: server.URL + "?token=${PLUGIN_TEST_SECRET}"},
	}, nil)
	if result.Status != Unavailable || !strings.Contains(result.Detail, "may not interpolate") {
		t.Fatalf("ProbeManifest() = %#v, want environment interpolation refusal", result)
	}
	if hits != 0 {
		t.Fatalf("probe contacted the configured host %d time(s)", hits)
	}
	if strings.Contains(result.Detail, "not-for-the-network") {
		t.Fatalf("probe detail leaked the secret: %q", result.Detail)
	}
}

func TestProbeHTTPRequiresExplicitNetworkAuthorization(t *testing.T) {
	result := ProbeManifest(context.Background(), Manifest{
		Probe: &Probe{Kind: "http", URL: "https://example.com/openapi.json"},
	}, nil)
	if result.Status != Unavailable || !strings.Contains(result.Detail, "requires explicit authorization") {
		t.Fatalf("network probe = %#v, want explicit authorization refusal", result)
	}
}

func TestManifestStringFieldsAndRequiredValues(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "service.yaml", `
name: notebook
kind: service
enabled: true
cost: free
probe:
  kind: directory
  path: .
api:
  base: /api
uses:
  - method: POST
    path: /sources
push:
  - endpoint: /sources
    encoding: multipart
    fields:
      embed: "false"
      notebooks: "[\"{{notebook_id}}\"]"
    poll:
      url: /sources/{{source_id}}
      until: status
      states: [completed, failed]
`)
	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded[0].Manifest
	if loaded[0].Error != nil {
		t.Fatal(loaded[0].Error)
	}
	if !m.IsEnabled() || m.Probe == nil || m.Probe.Kind != "directory" ||
		m.API.Base != "/api" || len(m.Uses) != 1 || len(m.Push) != 1 ||
		m.Push[0].Poll.URL != "/sources/{{source_id}}" {
		t.Fatalf("manifest fields parsed incorrectly: %#v", m)
	}
	if got := m.Push[0].Fields["notebooks"]; got != `["{{notebook_id}}"]` {
		t.Fatalf("notebooks = %q, want JSON string unchanged", got)
	}
	if got := m.Push[0].Fields["embed"]; got != "false" {
		t.Fatalf("embed = %q, want string false", got)
	}
	if got := Validate(m); len(got) != 0 {
		t.Fatalf("Validate() = %v, want no errors", got)
	}

	for _, manifest := range []Manifest{
		{Name: "missing-cost", Kind: "capability"},
		{Name: "bad-push", Kind: "service", Cost: "free", Push: []Push{{Endpoint: "sources", Encoding: "xml"}}},
		{Name: "network-path", Kind: "service", Cost: "free", Push: []Push{{Endpoint: "//attacker.test/push", Encoding: "json", Fields: map[string]string{"body": "x"}}}},
	} {
		if errs := Validate(manifest); !contains(errs, "cost") && manifest.Name == "missing-cost" {
			t.Fatalf("Validate(%#v) = %v, want loud missing-cost error", manifest, errs)
		} else if manifest.Name == "bad-push" && (!contains(errs, "absolute API path") || !contains(errs, "json|multipart")) {
			t.Fatalf("Validate(%#v) = %v, want endpoint and encoding errors", manifest, errs)
		} else if manifest.Name == "network-path" && !contains(errs, "absolute API path") {
			t.Fatalf("Validate(%#v) = %v, want network-path rejection", manifest, errs)
		}
	}
}

func TestProbeDisabledAndUnavailable(t *testing.T) {
	disabled := false
	if result := ProbeManifest(context.Background(), Manifest{Enabled: &disabled}, nil); result.Status != Disabled {
		t.Fatalf("disabled probe = %#v, want disabled", result)
	}
	if result := ProbeManifest(context.Background(), Manifest{Probe: &Probe{Kind: "unknown"}}, nil); result.Status != Unavailable {
		t.Fatalf("unknown probe = %#v, want unavailable", result)
	}
}

func TestProbeHTTPRefusesRedirectAndSendsNoAuthorization(t *testing.T) {
	secretHits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("probe unexpectedly sent Authorization")
		}
		if r.URL.Path == "/secret" {
			secretHits++
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/secret", http.StatusFound)
	}))
	defer server.Close()

	result := ProbeManifest(context.Background(), Manifest{
		Probe: &Probe{Kind: "http", URL: server.URL},
	}, &http.Client{})
	if result.Status != Unavailable || !strings.Contains(result.Detail, "returned 302") {
		t.Fatalf("ProbeManifest() = %#v, want refused redirect", result)
	}
	if secretHits != 0 {
		t.Fatalf("probe followed redirect %d time(s)", secretHits)
	}
}

func TestProbeDirectoryExpandsEnvironmentAndGlob(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.yaml"), []byte("name: pipeline"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGIN_FIXTURE_DIR", dir)

	result := ProbeManifestWithOptions(context.Background(), Manifest{
		Probe: &Probe{Kind: "directory", Path: "${PLUGIN_FIXTURE_DIR}", ExpectGlob: "*.yaml"},
	}, nil, ProbeOptions{AllowNetwork: true})
	if result.Status != Available {
		t.Fatalf("directory probe = %#v, want available", result)
	}
	result = ProbeManifest(context.Background(), Manifest{
		Probe: &Probe{Kind: "directory", Path: "${PLUGIN_MISSING_DIR}"},
	}, nil)
	if result.Status != Unavailable || !strings.Contains(result.Detail, "PLUGIN_MISSING_DIR is not set") {
		t.Fatalf("missing env probe = %#v, want explicit unset-variable error", result)
	}
}

func TestDirectoryProbeDoesNotExposeExpandedEnvironmentValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLUGIN_SECRET_PATH", dir)
	result := ProbeManifestWithOptions(context.Background(), Manifest{
		Probe: &Probe{Kind: "directory", Path: "${PLUGIN_SECRET_PATH}"},
	}, nil, ProbeOptions{AllowNetwork: true})
	if result.Status != Available {
		t.Fatalf("directory probe = %#v, want available", result)
	}
	if strings.Contains(result.Detail, dir) {
		t.Fatalf("probe detail leaked expanded path %q", result.Detail)
	}
	if result.Detail != "${PLUGIN_SECRET_PATH}" {
		t.Fatalf("detail=%q, want unexpanded variable name", result.Detail)
	}
}

func TestDirectoryProbeRequiresNetworkAuthorizationForUNC(t *testing.T) {
	result := ProbeManifest(context.Background(), Manifest{
		Probe: &Probe{Kind: "directory", Path: `\\host\share`},
	}, nil)
	if result.Status != Unavailable || !strings.Contains(result.Detail, "directory probe requires explicit authorization") {
		t.Fatalf("UNC probe=%#v, want authorization refusal", result)
	}
}

func TestVerifyServiceChecksAPIPrefixPathsAndMethods(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Errorf("verification unexpectedly sent Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"paths": {
				"/api/sources": {"post": {}},
				"/api/no-verb": {"get": {}}
			}
		}`))
	}))
	defer server.Close()

	got := VerifyService(context.Background(), Manifest{
		Probe: &Probe{Kind: "http", URL: server.URL},
		API:   API{Base: server.URL + "/api"},
		Uses: []Use{
			{Method: "POST", Path: "/sources"},
			{Method: "DELETE", Path: "/no-verb"},
			{Method: "GET", Path: "/missing"},
		},
	}, nil)
	if got.Result.Status != Unavailable || requests != 2 {
		t.Fatalf("VerifyService() = %#v, requests = %d; want incompatible/unavailable and two reads", got, requests)
	}
	if got.Operations[0].Status != "ok" {
		t.Errorf("operation 0 = %#v, want ok", got.Operations[0])
	}
	if got.Operations[1].Status != "missing" || !strings.Contains(got.Operations[1].Detail, "GET") {
		t.Errorf("operation 1 = %#v, want missing verb", got.Operations[1])
	}
	if got.Operations[2].Status != "missing" || !strings.Contains(got.Operations[2].Detail, "path") {
		t.Errorf("operation 2 = %#v, want missing path", got.Operations[2])
	}
}

func TestVerifyServiceRejectsDifferentAPIOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}}}}`))
	}))
	defer server.Close()
	got := VerifyService(context.Background(), Manifest{
		Probe: &Probe{Kind: "http", URL: server.URL},
		API:   API{Base: "http://127.0.0.1:9/api"},
		Uses:  []Use{{Method: "POST", Path: "/sources"}},
	}, nil)
	if got.Result.Status != Unavailable || !strings.Contains(got.Result.Detail, "share the probe origin") {
		t.Fatalf("verification = %#v, want origin refusal", got)
	}
}

func TestVerifyServiceDoesNotExposeQuerySecretsOrKeepIdleSockets(t *testing.T) {
	secret := "not-for-output"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}}}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	m := Manifest{
		Probe: &Probe{Kind: "http", URL: server.URL + "?token=" + secret},
		API:   API{Base: server.URL + "/api"},
		Uses:  []Use{{Method: "POST", Path: "/sources"}},
	}
	verified := VerifyService(context.Background(), m, nil)
	if verified.Result.Status != Unavailable {
		t.Fatalf("verification=%#v, want unavailable after second request 500", verified)
	}
	if strings.Contains(verified.Result.Detail, secret) {
		t.Fatalf("verification detail leaked query secret: %q", verified.Result.Detail)
	}
	client := probeHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || !transport.DisableKeepAlives {
		t.Fatal("probe client retains idle keep-alive connections")
	}
}

func TestVerifyServiceChecksPushOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"paths":{}}`))
	}))
	defer server.Close()
	got := VerifyService(context.Background(), Manifest{
		Probe: &Probe{Kind: "http", URL: server.URL},
		API:   API{Base: server.URL + "/api"},
		Push:  []Push{{Endpoint: "/sources", Encoding: "multipart", Fields: map[string]string{"type": "text"}}},
	}, nil)
	if got.Result.Status != Unavailable || len(got.Operations) != 1 || got.Operations[0].Path != "/api/sources" || got.Operations[0].Status != "missing" {
		t.Fatalf("push verification=%#v, want missing /api/sources", got)
	}
}

func TestPluginOperationsDoNotModifyManifestOrIndex(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "readonly.yaml")
	index := filepath.Join(dir, "index.db")
	writeManifest(t, dir, "readonly.yaml", `
name: readonly
kind: capability
cost: free
probe:
  kind: directory
  path: `+dir+`
`)
	if err := os.WriteFile(index, []byte("derived index fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeManifest, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	beforeIndex, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result := ProbeManifestWithOptions(context.Background(), loaded[0].Manifest, nil, ProbeOptions{AllowNetwork: true}); result.Status != Available {
		t.Fatalf("ProbeManifest() = %#v, want available", result)
	}
	afterManifest, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	afterIndex, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterManifest) != string(beforeManifest) || string(afterIndex) != string(beforeIndex) {
		t.Fatal("plugin loading or probing modified a fixture file")
	}
}

func TestRenderFieldsPreservesJSONStringAndRejectsMissingValues(t *testing.T) {
	fields := map[string]string{
		"notebooks": `["{{notebook_id}}"]`,
		"embed":     "false",
	}
	rendered, err := RenderFields(fields, map[string]string{"notebook_id": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered["notebooks"]; got != `["abc"]` {
		t.Fatalf("rendered notebooks = %q, want JSON string", got)
	}
	if got := rendered["embed"]; got != "false" {
		t.Fatalf("rendered embed = %q, want false unchanged", got)
	}
	if _, err := RenderFields(fields, nil); err == nil || !strings.Contains(err.Error(), "notebook_id") {
		t.Fatalf("RenderFields missing value error = %v, want notebook_id", err)
	}
}

func writeManifest(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
