package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/integrations"
	"github.com/mekjr1/midden/internal/plugins"
)

func TestIntegrationsListsHumanSetupCatalogWithoutProbing(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	rec := httptest.NewRecorder()
	server.handleIntegrations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []setupIntegrationView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("integrations=%#v, want two built-ins", got)
	}
	for _, item := range got {
		expected := "not_set_up"
		if item.ID == integrations.FacetID {
			expected = "separate_project"
		}
		if item.State != expected || item.GitHubURL == "" || item.InstallSummary == "" {
			t.Fatalf("first-run integration=%#v", item)
		}
	}
	if _, err := os.Stat(integrations.ConfigPath(index.Dir())); !os.IsNotExist(err) {
		t.Fatalf("passive catalog created settings or returned unexpected error: %v", err)
	}
}

func TestIntegrationConfigureIsExplicitAndDoesNotProbe(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	hits := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}},"/api/sources/{source_id}/status":{"get":{}}}}`))
	}))
	defer api.Close()

	body := `{"id":"open-notebook","enabled":true,"api_url":"` + api.URL + `","ui_url":"` + api.URL + `","password_required":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/configure", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "127.0.0.1:7777"
	rec := httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("headerless configure=%d, want 403", rec.Code)
	}
	req.Header.Set("X-Midden-Request", "1")
	req.Header.Set("Origin", "http://attacker.example")
	rec = httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin configure=%d, want 403", rec.Code)
	}

	req = explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", body)
	rec = httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("configure=%d body=%s", rec.Code, rec.Body.String())
	}

	if hits != 0 {
		t.Fatalf("saving integration contacted target %d time(s)", hits)
	}
	config, err := integrations.Load(index.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if config.OpenNotebook == nil || config.OpenNotebook.APIURL != api.URL {
		t.Fatalf("saved config=%#v", config)
	}

	req = explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/test", `{"id":"open-notebook"}`)
	rec = httptest.NewRecorder()
	server.handleIntegrationTest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test=%d body=%s", rec.Code, rec.Body.String())
	}
	if hits == 0 {
		t.Fatal("explicit test did not contact the configured target")
	}
	var views []setupIntegrationView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	for _, item := range views {
		if item.ID == integrations.OpenNotebookID {
			if item.State != "connected" || item.LastVerified == "" {
				t.Fatalf("tested integration=%#v", item)
			}
			return
		}
	}
	t.Fatal("Open Notebook missing from test response")
}

func TestIntegrationConfigureRejectsOversizedRequest(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	req := explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", strings.Repeat("x", (64<<10)+1))
	rec := httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "exceeds") {
		t.Fatalf("oversized configure=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(integrations.ConfigPath(index.Dir())); !os.IsNotExist(err) {
		t.Fatalf("oversized request wrote settings or returned unexpected error: %v", err)
	}
}

func TestIntegrationConfigureRejectsUnsafeURLAndPreservesLegacyManifest(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	unsafe := explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure",
		`{"id":"open-notebook","enabled":true,"api_url":"https://example.com","ui_url":"http://127.0.0.1:8502"}`)
	rec := httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, unsafe)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe URL configure=%d body=%s", rec.Code, rec.Body.String())
	}

	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "open-notebook.yaml")
	legacy := "name: open-notebook\n" +
		"kind: service\n" +
		"enabled: true\n" +
		"cost: free\n" +
		"probe:\n" +
		"  kind: http\n" +
		"  url: http://127.0.0.1:5055/openapi.json\n" +
		"api:\n" +
		"  base: http://127.0.0.1:5055/api\n" +
		"uses:\n" +
		"  - method: POST\n" +
		"    path: /sources\n" +
		"  - method: GET\n" +
		"    path: /sources/{source_id}/status\n" +
		"push:\n" +
		"  - endpoint: /sources\n" +
		"    encoding: multipart\n" +
		"    fields:\n" +
		"      type: text\n" +
		"      content: \"{{body}}\"\n" +
		"      notebooks: \"[\\\"{{notebook_id}}\\\"]\"\n" +
		"link: http://127.0.0.1:8502/notebooks/{{notebook_id|urlencode}}\n"
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure",
		`{"id":"open-notebook","enabled":true,"api_url":"http://127.0.0.1:5055","ui_url":"http://127.0.0.1:8502"}`)
	rec = httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, conflict)
	if rec.Code != http.StatusConflict {
		t.Fatalf("legacy configure=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("configure changed legacy manifest: %v", err)
	}

	adopt := explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/adopt", `{"id":"open-notebook"}`)
	rec = httptest.NewRecorder()
	server.handleIntegrationAdopt(rec, adopt)
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy manifest remained active or returned unexpected error: %v", err)
	}
	matches, err := filepath.Glob(legacyPath + ".midden-legacy-*.bak")
	if err != nil || len(matches) != 1 {
		t.Fatalf("legacy backup=%v err=%v", matches, err)
	}
	config, err := integrations.Load(index.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if config.OpenNotebook == nil || config.OpenNotebook.APIURL != "http://127.0.0.1:5055" {
		t.Fatalf("adopted config=%#v", config)
	}
}

func TestIntegrationConfigureExplicitlyReplacesLegacyWithBackup(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "open-notebook.yaml")
	legacy := "name: open-notebook\nkind: service\nenabled: true\ncost: free\nprobe:\n  kind: http\n  url: http://localhost:5055/openapi.json\n"
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"open-notebook","enabled":true,"api_url":"http://localhost:5055","ui_url":"http://localhost:8502","replace_legacy":true}`
	req := explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", body)
	rec := httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace legacy=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy manifest remained active or returned unexpected error: %v", err)
	}
	matches, err := filepath.Glob(legacyPath + ".midden-legacy-*.bak")
	if err != nil || len(matches) != 1 {
		t.Fatalf("legacy backup=%v err=%v", matches, err)
	}
	config, err := integrations.Load(index.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if config.OpenNotebook == nil || config.OpenNotebook.APIURL != "http://localhost:5055" {
		t.Fatalf("managed config=%#v", config)
	}
}

func TestLegacyOpenNotebookTestMakesSendPathAvailable(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}},"/api/sources/{source_id}/status":{"get":{}}}}`))
	}))
	defer api.Close()
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: open-notebook\n" +
		"kind: service\n" +
		"enabled: true\n" +
		"cost: free\n" +
		"probe:\n  kind: http\n  url: " + api.URL + "/openapi.json\n" +
		"api:\n  base: " + api.URL + "/api\n" +
		"uses:\n  - method: POST\n    path: /sources\n  - method: GET\n    path: /sources/{source_id}/status\n" +
		"push:\n  - endpoint: /sources\n    encoding: multipart\n    fields:\n      type: text\n      content: \"{{body}}\"\n      notebooks: \"[\\\"{{notebook_id}}\\\"]\"\n      embed: \"true\"\n      async_processing: \"true\"\n" +
		"link: " + api.URL + "/notebooks/{{notebook_id|urlencode}}\n"
	if err := os.WriteFile(filepath.Join(dir, "open-notebook.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	server.handleIntegrationTest(rec, explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/test", `{"id":"open-notebook"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy test=%d body=%s", rec.Code, rec.Body.String())
	}
	var views []setupIntegrationView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.ID == integrations.OpenNotebookID {
			if !view.Legacy || view.State != "connected" || view.LastVerified == "" {
				t.Fatalf("tested legacy view=%#v", view)
			}
			return
		}
	}
	t.Fatal("Open Notebook missing from legacy test response")
}

func TestOptionalAuthLegacySetupRequiresExplicitSettingsChoice(t *testing.T) {
	config := integrations.Config{}
	manifest := plugins.Manifest{
		Name: integrations.OpenNotebookID,
		Kind: "service",
		API: plugins.API{
			Base: "http://127.0.0.1:5055/api",
			Auth: plugins.Auth{
				Header: "Authorization", Scheme: "Bearer", Env: "OPEN_NOTEBOOK_PASSWORD", Optional: true,
			},
		},
		Link: "http://127.0.0.1:8502/notebooks/{{notebook_id|urlencode}}",
	}
	err := adoptLegacyIntegration(&config, integrations.OpenNotebookID, manifest)
	if err == nil || !strings.Contains(err.Error(), "optional password") {
		t.Fatalf("optional auth adoption error=%v", err)
	}
	if config.OpenNotebook != nil {
		t.Fatalf("optional auth adoption silently wrote settings: %#v", config)
	}
}

func TestManagedLegacyConflictBlocksUseAndEdits(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	if err := integrations.Save(index.Dir(), integrations.Config{
		OpenNotebook: &integrations.OpenNotebookSettings{
			Enabled: true, APIURL: "http://127.0.0.1:5055", UIURL: "http://127.0.0.1:8502",
		},
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "open-notebook.yaml"), []byte(
		"name: open-notebook\nkind: service\ncost: free\nprobe:\n  kind: http\n  url: http://127.0.0.1:5055/openapi.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.effectiveIntegration(integrations.OpenNotebookID); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("managed/legacy conflict error=%v", err)
	}
	body := `{"id":"open-notebook","enabled":true,"api_url":"http://127.0.0.1:5055","ui_url":"http://127.0.0.1:8502"}`
	rec := httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("managed/legacy configure=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPendingLegacyMigrationIsVisibleAndRecoverable(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "open-notebook.yaml")
	backup := source + ".midden-legacy-20260805T120000.000000000Z.bak"
	if err := os.WriteFile(backup, []byte("legacy preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := integrations.Save(index.Dir(), integrations.Config{
		OpenNotebook: &integrations.OpenNotebookSettings{
			Enabled: true, APIURL: "http://127.0.0.1:5055", UIURL: "http://127.0.0.1:8502",
		},
		PendingLegacy: &integrations.LegacyMigration{
			ID: integrations.OpenNotebookID, SourcePath: source, BackupPath: backup,
		},
	}); err != nil {
		t.Fatal(err)
	}
	views, err := server.integrationViews()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, view := range views {
		if view.ID == integrations.OpenNotebookID {
			found = true
			if !view.MigrationPending || view.State != "needs_attention" {
				t.Fatalf("pending migration view=%#v", view)
			}
			break
		}
	}
	if !found {
		t.Fatal("Open Notebook missing from pending migration view")
	}
	req := explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/finish-migration", `{"id":"open-notebook"}`)
	rec := httptest.NewRecorder()
	server.handleIntegrationFinishMigration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("finish migration=%d body=%s", rec.Code, rec.Body.String())
	}
	config, err := integrations.Load(index.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if config.PendingLegacy != nil {
		t.Fatalf("migration remained pending: %#v", config.PendingLegacy)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup disappeared: %v", err)
	}
}

func TestIntegrationReplacementRejectsDuplicateLegacyManifests(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: open-notebook\nkind: service\nenabled: true\ncost: free\nprobe:\n  kind: http\n  url: http://localhost:5055/openapi.json\n"
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"id":"open-notebook","enabled":true,"api_url":"http://localhost:5055","ui_url":"http://localhost:8502","replace_legacy":true}`
	rec := httptest.NewRecorder()
	server.handleIntegrationConfigure(rec, explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate legacy replacement=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("duplicate legacy manifest %s changed: %v", name, err)
		}
	}
	config, err := integrations.Load(index.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if config.OpenNotebook != nil {
		t.Fatalf("duplicate replacement wrote config: %#v", config)
	}
}

func TestConcurrentIntegrationSettingsRemainConsistent(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	requests := []string{
		`{"id":"open-notebook","enabled":true,"api_url":"http://127.0.0.1:5055","ui_url":"http://127.0.0.1:8502"}`,
		`{"id":"open-notebook","enabled":false,"api_url":"http://127.0.0.1:5056","ui_url":"http://127.0.0.1:8503"}`,
	}

	start := make(chan struct{})
	codes := make(chan int, len(requests))
	var wg sync.WaitGroup
	for _, body := range requests {
		body := body
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			server.handleIntegrationConfigure(rec, explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", body))
			codes <- rec.Code
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("concurrent configure status=%d", code)
		}
	}
	config, err := integrations.Load(index.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if config.OpenNotebook == nil || (config.OpenNotebook.Enabled && config.OpenNotebook.APIURL != "http://127.0.0.1:5055") || (!config.OpenNotebook.Enabled && config.OpenNotebook.APIURL != "http://127.0.0.1:5056") {
		t.Fatalf("concurrent updates lost settings: %#v", config)
	}
}

func TestManagedOpenNotebookExportUsesActionScopedPassword(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	t.Setenv("OPEN_NOTEBOOK_PASSWORD", "ambient-password-must-not-leak")
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PutNuggets([]index.Nugget{{
		Tool: "claude", SessionID: "session-123", Kind: "decision",
		Title: "Keep secrets out of settings", Body: "Passwords stay action-scoped.",
		Workspace: "project",
	}}); err != nil {
		t.Fatal(err)
	}

	var authorization string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/openapi.json":
			_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}},"/api/sources/{source_id}/status":{"get":{}}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/sources":
			authorization = r.Header.Get("Authorization")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"source:xyz","status":"new"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	if err := integrations.Save(index.Dir(), integrations.Config{
		OpenNotebook: &integrations.OpenNotebookSettings{
			Enabled:          true,
			APIURL:           api.URL,
			UIURL:            api.URL,
			PasswordRequired: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	if _, err := server.doOpenNotebookPush("job-test-missing-password", actionRequest{
		Plugin: "open-notebook", NotebookID: "notebook:abc123", Workspace: "project",
	}); err == nil || !strings.Contains(err.Error(), "action-scoped") {
		t.Fatalf("managed required password error=%v", err)
	}
	result, err := server.doOpenNotebookPush("job-test", actionRequest{
		Plugin:               "open-notebook",
		NotebookID:           "notebook:abc123",
		Workspace:            "project",
		OpenNotebookPassword: "action-only-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer action-only-password" {
		t.Fatalf("authorization=%q, want action-scoped password", authorization)
	}
	settings, err := os.ReadFile(integrations.ConfigPath(index.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), "action-only-password") {
		t.Fatalf("settings persisted action password: %s", settings)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "action-only-password") {
		t.Fatalf("export result leaked action password: %s", data)
	}

	if err := integrations.Save(index.Dir(), integrations.Config{
		OpenNotebook: &integrations.OpenNotebookSettings{
			Enabled: true,
			APIURL:  api.URL,
			UIURL:   api.URL,
		},
	}); err != nil {
		t.Fatal(err)
	}
	authorization = ""
	if _, err := server.doOpenNotebookPush("job-test-no-password", actionRequest{
		Plugin: "open-notebook", NotebookID: "notebook:abc123", Workspace: "project",
	}); err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		t.Fatalf("managed password-free setup sent ambient authorization %q", authorization)
	}
}

func explicitIntegrationRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "127.0.0.1:7777"
	req.Header.Set("X-Midden-Request", "1")
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	req.Header.Set("Content-Type", "application/json")
	return req
}
