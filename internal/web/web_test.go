package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/plugins"
)

func TestNonLoopbackIsRejected(t *testing.T) {
	// This process can read every session on the machine. It must never be
	// reachable from the network, even if the port is accidentally exposed.
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret session data"))
	}))

	for _, addr := range []string{"10.0.0.5:5555", "192.168.1.20:80", "203.0.113.7:443"} {
		req := httptest.NewRequest("GET", "/api/health", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s got %d, want 403", addr, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("%s reached the handler", addr)
		}
	}
}

func TestLoopbackIsAllowed(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	for _, addr := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = addr
		req.Host = "127.0.0.1:7777"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s got %d, want 200", addr, rec.Code)
		}
	}
}

func TestLoopbackPeerWithExternalHostIsRejected(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret session data"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "attacker.example:7777"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("external Host reached loopback handler")
	}
}

func TestSniffingIsDisabled(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff header missing")
	}
}

func TestLoopbackResponsesDenyFramesAndInlineInjection(t *testing.T) {
	h := localOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Host = "127.0.0.1:7777"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options=%q", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP=%q missing %q", csp, want)
		}
	}
}

func TestUIAssetsAreEmbedded(t *testing.T) {
	// The UI ships inside the binary; a missing asset would only show up at
	// runtime otherwise.
	for _, name := range []string{"ui/index.html", "ui/app.css", "ui/app.js", "ui/favicon.svg"} {
		b, err := uiFS.ReadFile(name)
		if err != nil {
			t.Errorf("%s not embedded: %v", name, err)
			continue
		}
		if len(b) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestEmbeddedHTMLReferencesItsAssets(t *testing.T) {
	b, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{"app.css", "app.js", "favicon.svg"} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html does not reference %s", want)
		}
	}
}

func TestHumanAge(t *testing.T) {
	cases := map[string]string{}
	_ = cases
	if got := round1(3.14159); got != 3.1 {
		t.Errorf("round1 = %v, want 3.1", got)
	}
	if got := round1(0); got != 0 {
		t.Errorf("round1(0) = %v", got)
	}
}

func TestSnapshotInvalidationDiscardsInFlightBuild(t *testing.T) {
	cache := newSnapshotCache()

	// Model a build that has started reading an old index.
	cache.mu.Lock()
	cache.building = true
	cache.buildGeneration = cache.generation
	cache.done = make(chan struct{})
	generation := cache.buildGeneration
	done := cache.done
	cache.mu.Unlock()

	// An explicit refresh writes the index and invalidates the old result
	// before that build completes.
	cache.invalidate()
	if published := cache.finishBuild(&snapshot{TakenAt: time.Now()}, generation); published {
		t.Fatal("an invalidated build was published as fresh")
	}

	select {
	case <-done:
		// Waiters are released, then get() loops rather than returning nil.
	default:
		t.Fatal("waiters were not released after discarded build")
	}
	cache.mu.Lock()
	cur := cache.cur
	cache.mu.Unlock()
	if cur != nil {
		t.Fatal("discarded build populated cache")
	}
}

func TestAuthoritativeEmptyIndexIsNotCold(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snap, err := db.ReadSessionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if hasIndexSnapshot(snap) {
		t.Fatal("new empty index looked initialized")
	}
	if err := db.MarkIndexed([]core.Tool{core.ToolClaude}, 1, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	snap, err = db.ReadSessionSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !hasIndexSnapshot(snap) {
		t.Fatal("authoritative empty index was treated as cold")
	}
}

func TestSnapshotHeaderCarriesCacheGeneration(t *testing.T) {
	rec := httptest.NewRecorder()
	setSnapshotHeader(rec, &snapshot{Version: 42})
	if got := rec.Header().Get("X-Midden-Snapshot"); got != "42" {
		t.Fatalf("snapshot header=%q, want 42", got)
	}
}

func TestSnapshotVersionAdvancesForEveryPublishedBuild(t *testing.T) {
	cache := newSnapshotCache()

	publish := func() *snapshot {
		cache.mu.Lock()
		cache.building = true
		cache.buildGeneration = cache.generation
		cache.done = make(chan struct{})
		generation := cache.buildGeneration
		cache.mu.Unlock()

		snap := &snapshot{TakenAt: time.Now()}
		if !cache.finishBuild(snap, generation) {
			t.Fatal("test build was unexpectedly discarded")
		}
		return snap
	}

	first := publish()
	second := publish()
	if first.Version == 0 || second.Version != first.Version+1 {
		t.Fatalf("versions %d then %d, want a new version per published build", first.Version, second.Version)
	}
}

func TestPluginsHandlerReportsDisabledManifestWithoutProbing(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disabled.yaml"), []byte(`
name: no-network
kind: service
enabled: false
cost: free
probe:
  kind: http
  url: http://127.0.0.1:1/openapi.json
`), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rec := httptest.NewRecorder()
	server.handlePlugins(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Enabled bool   `json:"enabled"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "no-network" || got[0].Status != "disabled" || got[0].Enabled {
		t.Fatalf("plugin response=%#v", got)
	}
	if got[0].Detail != "plugin is disabled" {
		t.Fatalf("detail=%q, want disabled reason", got[0].Detail)
	}
}

func TestPluginProbeRequiresExplicitSameOriginPost(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	get := httptest.NewRequest(http.MethodGet, "/api/plugins/probe", nil)
	get.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	server.handlePluginProbe(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET probe status=%d, want 405", rec.Code)
	}

	post := httptest.NewRequest(http.MethodPost, "/api/plugins/probe", nil)
	post.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	server.handlePluginProbe(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("headerless probe status=%d, want 403", rec.Code)
	}

	post = httptest.NewRequest(http.MethodPost, "/api/plugins/probe", nil)
	post.RemoteAddr = "127.0.0.1:1"
	post.Header.Set("X-Midden-Request", "1")
	post.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	server.handlePluginProbe(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin probe status=%d, want 403", rec.Code)
	}
}

func TestOpenNotebookPushSendsNuggetsNotRawSessions(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PutNuggets([]index.Nugget{{
		Tool:      "claude",
		SessionID: "session-123",
		Kind:      "decision",
		Title:     "Use explicit refresh",
		Body:      "The index must be reconciled before it is called fresh.",
		Workspace: "project",
	}}); err != nil {
		t.Fatal(err)
	}

	var uploaded string
	serverHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/openapi.json":
			_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}},"/api/sources/{source_id}/status":{"get":{}}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/sources":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if got, want := r.FormValue("type"), "text"; got != want {
				t.Fatalf("type=%q, want %q", got, want)
			}
			if got, want := r.FormValue("notebooks"), `["notebook:abc123"]`; got != want {
				t.Fatalf("notebooks=%q, want %q", got, want)
			}
			uploaded = r.FormValue("content")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"source:xyz","status":"new"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverHTTP.Close()

	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `
name: open-notebook
kind: service
enabled: true
cost: free
probe:
  kind: http
  url: ` + serverHTTP.URL + `/openapi.json
api:
  base: ` + serverHTTP.URL + `/api
uses:
  - method: POST
    path: /sources
  - method: GET
    path: /sources/{source_id}/status
push:
  - endpoint: /sources
    encoding: multipart
    fields:
      type: text
      content: "{{body}}"
      title: "{{title}}"
      notebooks: "[\"{{notebook_id}}\"]"
      embed: "true"
      async_processing: "true"
    expect_status: [201]
    poll:
      url: /sources/{{source_id}}
      until: completed
link: ` + serverHTTP.URL + `/notebooks/{{notebook_id|urlencode}}
`
	if err := os.WriteFile(filepath.Join(dir, "open-notebook.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	result, err := server.doOpenNotebookPush("job-test", actionRequest{
		Plugin:     "open-notebook",
		NotebookID: "notebook:abc123",
		Workspace:  "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uploaded, "Use explicit refresh") || strings.Contains(uploaded, "raw transcript") {
		t.Fatalf("uploaded content=%q", uploaded)
	}
	got := result.(map[string]any)
	if got["source_id"] != "source:xyz" || got["source_status"] != "new" {
		t.Fatalf("result=%#v", got)
	}
	if link, _ := got["notebook_url"].(string); !strings.Contains(link, "notebook%3Aabc123") {
		t.Fatalf("deep link=%q", link)
	}
}

func TestOpenNotebookPushRequiresExplicitScope(t *testing.T) {
	server := &Server{}
	_, err := server.doOpenNotebookPush("job-test", actionRequest{NotebookID: "notebook:abc123"})
	if err == nil || !strings.Contains(err.Error(), "explicitly include all") {
		t.Fatalf("unscoped Open Notebook export error=%v", err)
	}
	_, err = server.doOpenNotebookPush("job-test", actionRequest{
		NotebookID: "notebook:abc123", Workspace: "project", AllWorkspaces: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("ambiguous Open Notebook export error=%v", err)
	}
}

func TestOpenNotebookStatusRequiresActionReadyManifest(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"paths":{"/api/sources":{"post":{}}}}`))
	}))
	defer api.Close()
	dir := filepath.Join(index.Dir(), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Reachable service + declared route, but no source push contract.
	if err := os.WriteFile(filepath.Join(dir, "open-notebook.yaml"), []byte(`
name: open-notebook
kind: service
enabled: true
cost: free
probe:
  kind: http
  url: `+api.URL+`/openapi.json
api:
  base: `+api.URL+`/api
uses:
  - method: POST
    path: /sources
link: http://127.0.0.1:8502/notebooks/{{notebook_id|urlencode}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	status, err := server.pluginStatus(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].Status != plugins.Unavailable || !strings.Contains(status[0].Detail, "no source push") {
		t.Fatalf("status=%#v, want unavailable action contract", status)
	}
}

func TestSessionsHandlerPaginatesSearchesAndSortsByConsequence(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now()
	var sessions []core.Session
	for i := 0; i < 25; i++ {
		sessions = append(sessions, core.Session{
			Tool: core.ToolClaude, ID: fmt.Sprintf("session-%02d", i),
			Dir: `E:\projects\ordinary`, Title: fmt.Sprintf("Routine work %02d", i),
			Updated: now.Add(-time.Duration(i) * time.Minute), Bytes: int64(i + 1),
		})
	}
	sessions = append(sessions, core.Session{
		Tool: core.ToolCopilot, ID: "critical-session", Dir: `E:\projects\payments`,
		Title: "Payments rescue", Updated: now.Add(-time.Hour), Bytes: core.RiskCriticalBytes,
	})
	if err := db.PutSessions(sessions); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	req := httptest.NewRequest(http.MethodGet,
		"/api/sessions?limit=5&offset=0&sort=consequence", nil)
	rec := httptest.NewRecorder()
	server.handleSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page []sessionView
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 5 || page[0].ID != "critical-session" {
		t.Fatalf("first page=%#v", page)
	}

	req = httptest.NewRequest(http.MethodGet,
		"/api/sessions?limit=5&offset=0&search=payments", nil)
	rec = httptest.NewRecorder()
	server.handleSessions(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != "critical-session" {
		t.Fatalf("search page=%#v", page)
	}

	req = httptest.NewRequest(http.MethodGet,
		"/api/session-stats?limit=5&offset=0&search=payments", nil)
	rec = httptest.NewRecorder()
	server.handleSessionStats(rec, req)
	var stats map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats["target"].(float64) != 1 {
		t.Fatalf("stats=%#v", stats)
	}
}

func TestCompactDesktopKeepsPersistentNavigation(t *testing.T) {
	css, err := uiFS.ReadFile("ui/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"min-width: 821px", "max-width: 1180px", "max-width: 820px"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("compact desktop CSS missing %q", want)
		}
	}
}

func TestEmbeddedUIUsesPagedLiveActivityJobs(t *testing.T) {
	js, err := uiFS.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/api/jobs?limit=40", "/api/recovery-runs?limit=${state.activityRecoveryPageSize}", "/api/ops?limit=${state.activityAuditPageSize}", "activityJobs", "jobsTotal", "recoveryRunsTotal", "activityOperationsTotal"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("Activity UI missing %q", want)
		}
	}
}

func TestExplicitMiddenRequestRequiresHeaderHostAndSameOrigin(t *testing.T) {
	newRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/action", nil)
		req.RemoteAddr = "127.0.0.1:1"
		req.Host = "127.0.0.1:7777"
		return req
	}
	rec := httptest.NewRecorder()
	if requireExplicitMiddenRequest(rec, newRequest()) || rec.Code != http.StatusForbidden {
		t.Fatalf("missing X-Midden-Request status=%d", rec.Code)
	}
	req := newRequest()
	req.Header.Set("X-Midden-Request", "1")
	rec = httptest.NewRecorder()
	if requireExplicitMiddenRequest(rec, req) || rec.Code != http.StatusForbidden {
		t.Fatalf("missing Origin status=%d", rec.Code)
	}
	req = newRequest()
	req.Header.Set("X-Midden-Request", "1")
	req.Header.Set("Origin", "http://attacker.example")
	rec = httptest.NewRecorder()
	if requireExplicitMiddenRequest(rec, req) || rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", rec.Code)
	}
	req = newRequest()
	req.Header.Set("X-Midden-Request", "1")
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec = httptest.NewRecorder()
	if !requireExplicitMiddenRequest(rec, req) || rec.Code != http.StatusOK {
		t.Fatalf("same-origin status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBrandContractAndKnownGoodCanaryArePresent(t *testing.T) {
	contract, err := os.ReadFile("../../docs/product/brand-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"brand": "Midden"`, `"required_wordmark": "midden"`, `"Recover"`, `"Studio"`, `"Library"`, `"Cleanup"`, `"Activity"`, `"Tools"`, `"Retired vertical tabs as primary navigation."`, `"Mobile or phone presentation at 1024x768."`} {
		if !strings.Contains(string(contract), want) {
			t.Errorf("brand contract missing %q", want)
		}
	}
	canary, err := os.ReadFile("../../docs/product/brand/canary-known-good.svg")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"midden", "#10161c", "KNOWN-GOOD CANARY"} {
		if !strings.Contains(string(canary), want) {
			t.Errorf("canary missing %q", want)
		}
	}
}
