package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s got %d, want 200", addr, rec.Code)
		}
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
