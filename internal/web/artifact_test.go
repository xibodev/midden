package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

// TestArtifactBodyConfinedToArtifactsDir is the check that matters on this
// endpoint. It serves file contents by path, and binding to loopback is not a
// reason to serve arbitrary files: anything running locally can reach it.
func TestArtifactBodyConfinedToArtifactsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)

	arts := filepath.Join(home, "artifacts")
	if err := os.MkdirAll(arts, 0o755); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(arts, "handoff-copilot-abc.md")
	if err := os.WriteFile(good, []byte("rescued brief"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A secret that lives outside the artifacts directory.
	secret := filepath.Join(home, "credentials.txt")
	if err := os.WriteFile(secret, []byte("SENSITIVE"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv := &Server{db: db}

	call := func(p string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet,
			"/api/artifact?path="+url.QueryEscape(p), nil)
		rec := httptest.NewRecorder()
		srv.handleArtifactBody(rec, req)
		return rec
	}

	t.Run("serves an artifact", func(t *testing.T) {
		rec := call(good)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
		if body := rec.Body.String(); !contains(body, "rescued brief") {
			t.Errorf("body did not contain the artifact: %s", body)
		}
	})

	for name, p := range map[string]string{
		"sibling file":     secret,
		"traversal":        filepath.Join(arts, "..", "credentials.txt"),
		"deep traversal":   filepath.Join(arts, "..", "..", "..", "windows", "win.ini"),
		"empty":            "",
		"missing artifact": filepath.Join(arts, "nope.md"),
	} {
		t.Run(name, func(t *testing.T) {
			rec := call(p)
			if rec.Code == http.StatusOK {
				t.Fatalf("served %q with 200; body=%s", p, rec.Body.String())
			}
			if contains(rec.Body.String(), "SENSITIVE") {
				t.Fatalf("leaked file contents for %q", p)
			}
		})
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
