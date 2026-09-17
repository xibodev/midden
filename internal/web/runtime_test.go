package web

import (
	"context"
	kernelconfig "github.com/xibodev/facet-studio/pkg/config"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestRuntimeRefusesForeignKernelHome(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	foreign := t.TempDir()
	t.Setenv(kernelconfig.EnvHome, foreign)
	s := &Server{db: db}
	if err := s.configureRuntime(context.Background(), "", false); err == nil || !strings.Contains(err.Error(), filepath.Join(filepath.Dir(db.Path()), "kernel")) {
		t.Fatalf("isolation error=%v", err)
	}
}

func TestUnavailableChatIsAnErrorNotAnAssistantReply(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{db: db, agentError: "no verified catalog"}
	w := httptest.NewRecorder()
	s.handleChat(w, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"message":"hello"}`)))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "Runtime") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	recipes, err := db.Recipes(10)
	if err != nil || len(recipes) != 0 {
		t.Fatalf("unavailable chat created recipes: %v %v", recipes, err)
	}
}

func TestRuntimeDoesNotClaimVerifiedConnectionFromLoopPresence(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{db: db}
	status := s.runtimeSnapshot()
	if status["connection_verified"] != false || status["status"] != "not_started" {
		t.Fatal(status)
	}
}
