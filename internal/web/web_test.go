package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
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
