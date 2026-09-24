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
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, origin := range []string{"https://untrusted.invalid", "null"} {
		request := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/sessions", strings.NewReader(`{}`))
		request.Header.Set("Origin", origin)
		request.Header.Set("X-Midden-CSRF", app.csrf)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("foreign origin status=%d", response.Code)
		}
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/sessions", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal("mutation without CSRF was accepted")
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
	if strings.Contains(string(raw), "apiKey") {
		t.Fatal("status exposes a credential input")
	}
}

func TestArtifactReadsDoNotEscapeWorkspace(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err = app.ReadFile("../private.txt"); err == nil {
		t.Fatal("workspace traversal accepted")
	}
	outside := filepath.Join(filepath.Dir(opts.Workspace), "outside.txt")
	if err = os.WriteFile(outside, []byte("synthetic private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(opts.Workspace, "linked.txt")); err == nil {
		if _, err = app.ReadFile("linked.txt"); err == nil {
			t.Fatal("symlink escaped workspace")
		}
	}
}
