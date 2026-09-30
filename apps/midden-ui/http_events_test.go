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

func TestLocalSessionAPIAndSandboxedArtifactPreview(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	request := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/sessions", strings.NewReader(`{"title":"Test session"}`))
	request.Header.Set("X-Midden-CSRF", app.csrf)
	request.Header.Set("Origin", "http://127.0.0.1:18890")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("session creation failed: %s", response.Body.String())
	}
	var created Session
	if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatal("missing session", err)
	}
	if err = os.WriteFile(filepath.Join(app.opts.Workspace, "deck.html"), []byte("<h1>Local artifact</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:18890/preview?path=deck.html", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") {
		t.Fatal("preview is not sandboxed")
	}
	if strings.Contains(response.Header().Get("Content-Security-Policy"), "allow-same-origin") {
		t.Fatal("artifact has application-origin privileges")
	}
}
