package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/integrations"
)

func TestRetiredVideoCannotBeConfiguredOrExecuted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs(db)}
	defer s.Close()
	for _, id := range []string{"openmontage", "facet"} {
		w := httptest.NewRecorder()
		s.handleIntegrationConfigure(w, explicitIntegrationRequest(t, http.MethodPost, "/api/integrations/configure", `{"id":"`+id+`","enabled":true}`))
		if w.Code == http.StatusOK {
			t.Fatalf("%s configured as executable adapter", id)
		}
		if _, _, err := s.effectiveIntegration(id); err == nil {
			t.Fatalf("%s resolved an executable manifest", id)
		}
	}
	if _, err := os.Stat(integrations.ConfigPath(home)); !os.IsNotExist(err) {
		t.Fatal("unsupported setup wrote configuration")
	}
	views, err := s.integrationViews()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.ID == "openmontage" {
			t.Fatal("retired adapter advertised")
		}
		if v.ID == "facet" && (v.State != "separate_project" || v.Enabled || v.Kind != "related_project") {
			t.Fatalf("Facet incorrectly claims readiness: %+v", v)
		}
	}
	for _, endpoint := range []string{"browse", "capabilities", "open"} {
		r := httptest.NewRequest(http.MethodPost, "/api/integrations/"+endpoint, strings.NewReader(`{"id":"openmontage"}`))
		r.Host = "localhost"
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("removed endpoint %s returned %d", endpoint, w.Code)
		}
	}
}

func TestRetiredSettingsDoNotGrantWorkspaceAccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	if err := os.WriteFile(integrations.ConfigPath(home), []byte(`{"version":1,"open_montage":{"enabled":true,"home":"obsolete","backend":"copilot"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	delivery := filepath.Join(home, "deliverables")
	if dirs := studioAgentAllowedDirs(work, delivery); len(dirs) != 2 {
		t.Fatalf("retired settings granted paths: %v", dirs)
	}
}
