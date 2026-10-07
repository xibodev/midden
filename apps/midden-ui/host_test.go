package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalAPIRejectsForeignOriginsAndRequiresCSRF(t *testing.T) {
	app := newTestApp(t)
	for _, origin := range []string{"https://untrusted.invalid", "null"} {
		request := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/sessions", strings.NewReader(`{}`))
		request.Header.Set("Origin", origin)
		request.Header.Set("X-Midden-CSRF", app.csrf)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, keyed(app, request))
		if response.Code != http.StatusForbidden {
			t.Fatalf("foreign origin status=%d", response.Code)
		}
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/sessions", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, keyed(app, request))
	if response.Code != http.StatusForbidden {
		t.Fatal("mutation without CSRF was accepted")
	}
}

func TestEveryRequestNeedsTheLaunchKey(t *testing.T) {
	app := newTestApp(t)
	get := func(target string, cookie string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", target, nil)
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: launchCookie, Value: cookie})
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		return response
	}
	for _, path := range []string{"/api/status", "/api/sessions", "/api/files", "/api/events", "/app.js", "/preview?path=x"} {
		if response := get("http://127.0.0.1:18890"+path, ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s without the key: %d", path, response.Code)
		}
		if response := get("http://127.0.0.1:18890"+path, "wrong"); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s with a wrong key: %d", path, response.Code)
		}
	}
	page := get("http://127.0.0.1:18890/", "")
	if page.Code != http.StatusUnauthorized || !strings.Contains(page.Body.String(), "Start entry") {
		t.Fatalf("the page without a key: %d %s", page.Code, page.Body)
	}
	if wrong := get("http://127.0.0.1:18890/?key=wrong", ""); wrong.Code != http.StatusUnauthorized || len(wrong.Result().Cookies()) != 0 {
		t.Fatalf("a wrong launch key: %d", wrong.Code)
	}
	launch := get(app.launchAddress("127.0.0.1:18890"), "")
	if launch.Code != http.StatusSeeOther || launch.Header().Get("Location") != "/" {
		t.Fatalf("the launch address: %d %v", launch.Code, launch.Header())
	}
	cookies := launch.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != launchCookie || cookies[0].Value != app.key || !cookies[0].HttpOnly ||
		cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("the launch cookie: %+v", cookies)
	}
	if status := get("http://127.0.0.1:18890/api/status", app.key); status.Code != http.StatusOK || !strings.Contains(status.Body.String(), app.csrf) {
		t.Fatalf("status with the key: %d", status.Code)
	}
}

func TestSessionMetadataSurvivesReopenWithoutAProvider(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.NewSession("Synthetic article")
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	reopened, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	sessions := reopened.Sessions()
	if len(sessions) != 1 || sessions[0].ID != created.ID || sessions[0].Title != "Synthetic article" {
		t.Fatal("saved conversation metadata was lost")
	}
	raw, _ := json.Marshal(reopened.Status())
	if strings.Contains(string(raw), "apiKey") || strings.Contains(string(raw), reopened.key) {
		t.Fatal("status exposes a credential input or the launch key")
	}
}

func TestArtifactReadsStayInThePersonsFiles(t *testing.T) {
	app := newTestApp(t)
	for _, name := range []string{"../private.txt", "../AGENT.md", "../../kernel/config.json"} {
		if _, err := app.ReadFile(name); err == nil {
			t.Fatalf("%s escaped the person's files", name)
		}
	}
	if err := os.MkdirAll(filepath.Join(app.paths.Files, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.paths.Files, ".git", "config"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFile(".git/config"); err == nil {
		t.Fatal("a hidden folder was readable")
	}
	outside := filepath.Join(app.paths.Workspace, "outside.txt")
	if err := os.WriteFile(outside, []byte("synthetic private fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.paths.Files, "linked.txt")); err == nil {
		if _, err := app.ReadFile("linked.txt"); err == nil {
			t.Fatal("a symlink escaped the person's files")
		}
	}
	if err := os.WriteFile(filepath.Join(app.paths.Files, "note.md"), []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if content, err := app.ReadFile("note.md"); err != nil || content.Content != "kept" {
		t.Fatal("a file among the person's files was not readable", err)
	}
	files, err := app.Files()
	if err != nil || len(files) != 1 || files[0].Path != "note.md" {
		t.Fatalf("the files list shows more than the person's files: %+v %v", files, err)
	}
}
