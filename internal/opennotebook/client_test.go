package opennotebook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/plugins"
)

func testManifest(base string) plugins.Manifest {
	return plugins.Manifest{
		Kind: "service",
		API: plugins.API{
			Base: base + "/api",
			Auth: plugins.Auth{Header: "Authorization", Env: "OPEN_NOTEBOOK_PASSWORD", Optional: true},
		},
		Link: "http://127.0.0.1:8502/notebooks/{{notebook_id|urlencode}}",
	}
}

func testPush() plugins.Push {
	return plugins.Push{
		Endpoint:     "/sources",
		Encoding:     "multipart",
		ExpectStatus: []int{http.StatusCreated},
		Fields: map[string]string{
			"type":             "text",
			"content":          "{{body}}",
			"title":            "{{title}}",
			"notebooks":        `["{{notebook_id}}"]`,
			"embed":            "true",
			"async_processing": "true",
		},
		Poll: plugins.Poll{URL: "/sources/{{source_id}}"},
	}
}

func TestCreateTextSourceUsesMultipartStringsAndOptionalAuth(t *testing.T) {
	t.Setenv("OPEN_NOTEBOOK_PASSWORD", "local-password")
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sources" || r.Method != http.MethodPost {
			t.Fatalf("request %s %s, want POST /api/sources", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got, want := r.FormValue("type"), "text"; got != want {
			t.Fatalf("type=%q, want %q", got, want)
		}
		if got, want := r.FormValue("notebooks"), `["notebook:abc123"]`; got != want {
			t.Fatalf("notebooks=%q, want %q", got, want)
		}
		if got, want := r.FormValue("embed"), "true"; got != want {
			t.Fatalf("embed=%q, want string true", got)
		}
		if !strings.Contains(r.FormValue("content"), "\"quoted\"") {
			t.Fatalf("content lost quotes: %q", r.FormValue("content"))
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"source:xyz","status":"new"}`))
	}))
	defer server.Close()

	client, err := New(testManifest(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	source, err := client.CreateTextSource(context.Background(), testPush(), "notebook:abc123", "title", "body with \"quoted\" text")
	if err != nil {
		t.Fatal(err)
	}
	if source.ID != "source:xyz" || source.Status != "new" {
		t.Fatalf("source=%#v", source)
	}
	if gotAuth != "Bearer local-password" {
		t.Fatalf("authorization=%q, want Bearer password", gotAuth)
	}
}

func TestCreateTextSourceRejectsMalformedManifestFieldsBeforeNetwork(t *testing.T) {
	client, err := New(testManifest("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	push := testPush()
	push.Fields["notebooks"] = `[{{notebook_id}}]`
	if _, err := client.CreateTextSource(context.Background(), push, "notebook:abc", "title", "body"); err == nil || !strings.Contains(err.Error(), "notebooks") {
		t.Fatalf("error=%v, want local notebooks validation", err)
	}
}

func TestCreateTextSourceBindsExactlyOneRequestedNotebook(t *testing.T) {
	client, err := New(testManifest("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	push := testPush()
	push.Fields["notebooks"] = `["notebook:other","{{notebook_id}}"]`
	if _, err := client.CreateTextSource(context.Background(), push, "notebook:abc", "title", "body"); err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("error=%v, want singular notebook binding", err)
	}
}

func TestWaitSourceAndNotebookLink(t *testing.T) {
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls++
		if r.URL.Path != "/api/sources/source:xyz/status" && r.URL.Path != "/api/sources/source%3Axyz/status" {
			t.Fatalf("poll path=%q", r.URL.Path)
		}
		status := "running"
		if polls >= 2 {
			status = "completed"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
	}))
	defer server.Close()

	client, err := New(testManifest(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	source, err := client.WaitSource(context.Background(), plugins.Poll{URL: "/sources/{{source_id}}/status"}, "source:xyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if source.Status != "completed" || polls < 2 {
		t.Fatalf("source=%#v polls=%d", source, polls)
	}
	link, err := client.NotebookLink("notebook:abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(link, "notebook%3Aabc123") {
		t.Fatalf("link=%q, want encoded colon", link)
	}
}

func TestOptionalAuthDoesNotSendEmptyHeader(t *testing.T) {
	t.Setenv("OPEN_NOTEBOOK_PASSWORD", "")
	client, err := New(testManifest("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.applyAuth(req); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("empty optional auth header=%q", got)
	}
}

func TestNewRequiresConfiguredRequiredPassword(t *testing.T) {
	t.Setenv("OPEN_NOTEBOOK_PASSWORD", "")
	manifest := testManifest("http://127.0.0.1:5055")
	manifest.API.Auth.Optional = false
	if _, err := New(manifest); err == nil || !strings.Contains(err.Error(), "OPEN_NOTEBOOK_PASSWORD") {
		t.Fatalf("required password error=%v", err)
	}
}

func TestNewDisablesProxyForEvidenceExport(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:8080")
	client, err := New(testManifest("http://LOCALHOST:5055"))
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport=%T, want *http.Transport", client.http.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("Open Notebook export inherited proxy configuration")
	}
}

func TestNewRejectsManifestControlledAuthAndLinkEscapes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*plugins.Manifest)
		want   string
	}{
		{
			name: "ambient credential",
			mutate: func(m *plugins.Manifest) {
				m.API.Auth.Env = "AWS_SECRET_ACCESS_KEY"
			},
			want: "auth env",
		},
		{
			name: "arbitrary header",
			mutate: func(m *plugins.Manifest) {
				m.API.Auth.Header = "X-Export-Token"
			},
			want: "auth header",
		},
		{
			name: "incomplete auth",
			mutate: func(m *plugins.Manifest) {
				m.API.Auth.Header = "Authorization"
				m.API.Auth.Env = ""
			},
			want: "together",
		},
		{
			name: "script link",
			mutate: func(m *plugins.Manifest) {
				m.Link = "javascript:alert(1)"
			},
			want: "http(s)",
		},
		{
			name: "remote link",
			mutate: func(m *plugins.Manifest) {
				m.Link = "https://attacker.example/notebooks/{{notebook_id|urlencode}}"
			},
			want: "loopback",
		},
		{
			name: "remote api",
			mutate: func(m *plugins.Manifest) {
				m.API.Base = "https://attacker.example/api"
			},
			want: "API base",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			m := testManifest("http://127.0.0.1:5055")
			test.mutate(&m)
			if _, err := New(m); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestPrepareRejectsMissingOrInvalidPush(t *testing.T) {
	m := testManifest("http://127.0.0.1:5055")
	if _, err := Prepare(m); err == nil || !strings.Contains(err.Error(), "no source push") {
		t.Fatalf("missing push error=%v", err)
	}
	m.Push = []plugins.Push{{
		Endpoint: "/sources",
		Encoding: "multipart",
		Fields: map[string]string{
			"type":      "text",
			"content":   "{{body}}",
			"notebooks": `["notebook:other","{{notebook_id}}"]`,
		},
	}}
	if _, err := Prepare(m); err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("invalid push error=%v", err)
	}
}

func TestPrepareRequiresEvidenceAndNotebookBindings(t *testing.T) {
	m := testManifest("http://127.0.0.1:5055")
	m.Push = []plugins.Push{testPush()}
	m.Push[0].Fields["content"] = "static text"
	if _, err := Prepare(m); err == nil || !strings.Contains(err.Error(), "{{body}}") {
		t.Fatalf("static content error=%v", err)
	}
	m = testManifest("http://127.0.0.1:5055")
	m.Push = []plugins.Push{testPush()}
	m.Link = "http://127.0.0.1:8502/notebooks"
	if _, err := Prepare(m); err == nil || !strings.Contains(err.Error(), "urlencode") {
		t.Fatalf("static link error=%v", err)
	}
}

func TestHTTPErrorBoundsBody(t *testing.T) {
	body := io.NopCloser(strings.NewReader(strings.Repeat("x", 10000)))
	err := httpError(&http.Response{StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Body: body})
	if err == nil || len(err.Error()) > 8300 {
		t.Fatalf("bounded error=%v", err)
	}
}
