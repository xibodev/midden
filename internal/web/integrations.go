package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/integrations"
	"github.com/mekjr1/midden/internal/opennotebook"
	"github.com/mekjr1/midden/internal/plugins"
)

// setupIntegrationView is the human-facing representation of a known
// integration. Plugins remain the execution contract; this view describes how
// a person can safely get from no setup to a tested local connection.
type setupIntegrationView struct {
	ID                    string                  `json:"id"`
	Name                  string                  `json:"name"`
	Description           string                  `json:"description"`
	Kind                  string                  `json:"kind"`
	Cost                  string                  `json:"cost"`
	GitHubURL             string                  `json:"github_url"`
	InstallSummary        string                  `json:"install_summary"`
	State                 string                  `json:"state"`
	StateDetail           string                  `json:"state_detail"`
	Detail                string                  `json:"detail,omitempty"`
	Enabled               bool                    `json:"enabled"`
	LastVerified          string                  `json:"last_verified,omitempty"`
	MigrationPending      bool                    `json:"migration_pending"`
	ConfigurationConflict bool                    `json:"configuration_conflict"`
	Legacy                bool                    `json:"legacy"`
	Settings              integrationSettingsView `json:"settings"`
}

type integrationSettingsView struct {
	APIURL           string `json:"api_url,omitempty"`
	UIURL            string `json:"ui_url,omitempty"`
	PasswordRequired bool   `json:"password_required"`
	Home             string `json:"home,omitempty"`
	Backend          string `json:"backend,omitempty"`
}

type integrationWriteRequest struct {
	ID               string `json:"id"`
	Enabled          bool   `json:"enabled"`
	APIURL           string `json:"api_url"`
	UIURL            string `json:"ui_url"`
	PasswordRequired bool   `json:"password_required"`
	Home             string `json:"home"`
	Backend          string `json:"backend"`
	ReplaceLegacy    bool   `json:"replace_legacy"`
}

type legacyIntegration struct {
	manifest  plugins.Manifest
	err       error
	duplicate bool
}

type legacyIntegrationTest struct {
	result plugins.Result
	at     time.Time
}

func (s *Server) acquireIntegrationMutation(root string) (func(), error) {
	s.integrationMu.Lock()
	lock, err := integrations.AcquireSettingsLock(root)
	if err != nil {
		s.integrationMu.Unlock()
		return nil, err
	}
	return func() {
		_ = lock.Release()
		s.integrationMu.Unlock()
	}, nil
}

func (s *Server) rememberLegacyTest(id string, result plugins.Result) {
	s.legacyTestMu.Lock()
	defer s.legacyTestMu.Unlock()
	if s.legacyTests == nil {
		s.legacyTests = map[string]legacyIntegrationTest{}
	}
	s.legacyTests[id] = legacyIntegrationTest{result: result, at: time.Now().UTC()}
}

func (s *Server) legacyTest(id string) (legacyIntegrationTest, bool) {
	s.legacyTestMu.RLock()
	defer s.legacyTestMu.RUnlock()
	result, ok := s.legacyTests[id]
	return result, ok
}

// handleIntegrations is deliberately passive. It reads Midden's settings and
// any existing advanced manifests, but never contacts a target.
func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	out, err := s.integrationViews()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

func (s *Server) handleIntegrationConfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	root := index.Dir()
	release, err := s.acquireIntegrationMutation(root)
	if err != nil {
		http.Error(w, "integration settings are busy; try again shortly", http.StatusConflict)
		return
	}
	defer release()
	config, err := integrations.Load(root)
	if err != nil {
		http.Error(w, "read integration settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if config.PendingLegacy != nil {
		http.Error(w, "finish the pending legacy migration before changing integration settings", http.StatusConflict)
		return
	}
	legacy, err := loadLegacyIntegrations(root)
	if err != nil {
		http.Error(w, "read advanced integrations: "+err.Error(), http.StatusInternalServerError)
		return
	}
	legacyItem, hasLegacy := legacy[req.ID]
	managedBefore := integrationConfigured(config, req.ID)
	if managedBefore && hasLegacy {
		http.Error(w, "Settings and an advanced manifest both exist; remove Settings to return to the advanced setup before editing it", http.StatusConflict)
		return
	}
	if !managedBefore && hasLegacy && !req.ReplaceLegacy {
		http.Error(w, "an advanced configuration already exists; adopt it or keep using it before saving Settings", http.StatusConflict)
		return
	}
	if err := applyIntegrationSettings(&config, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if hasLegacy && !managedBefore && req.ReplaceLegacy {
		if legacyItem.duplicate {
			http.Error(w, "multiple advanced manifests use this integration name; remove duplicates before replacing it", http.StatusConflict)
			return
		}
		if err := validateIntegrationSettings(config, req.ID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		migration, err := newLegacyMigration(root, req.ID, legacyItem.manifest.File)
		if err != nil {
			http.Error(w, "prepare legacy migration: "+err.Error(), http.StatusBadRequest)
			return
		}
		config.PendingLegacy = migration
		if err := integrations.Save(root, config); err != nil {
			http.Error(w, "save integration settings: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := finishLegacyMigration(root, &config); err != nil {
			http.Error(w, "settings saved, but legacy migration is pending: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.writeIntegrationViews(w)
		return
	}
	if err := integrations.Save(root, config); err != nil {
		http.Error(w, "save integration settings: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.writeIntegrationViews(w)
}

func (s *Server) handleIntegrationAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	root := index.Dir()
	release, err := s.acquireIntegrationMutation(root)
	if err != nil {
		http.Error(w, "integration settings are busy; try again shortly", http.StatusConflict)
		return
	}
	defer release()
	config, err := integrations.Load(root)
	if err != nil {
		http.Error(w, "read integration settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if integrationConfigured(config, req.ID) {
		http.Error(w, "this integration is already managed in Settings", http.StatusConflict)
		return
	}
	if config.PendingLegacy != nil {
		http.Error(w, "finish the pending legacy migration before adopting another integration", http.StatusConflict)
		return
	}
	legacy, err := loadLegacyIntegrations(root)
	if err != nil {
		http.Error(w, "read advanced integrations: "+err.Error(), http.StatusInternalServerError)
		return
	}
	current, ok := legacy[req.ID]
	if !ok {
		http.Error(w, "no advanced configuration was found to adopt", http.StatusNotFound)
		return
	}
	if current.err != nil {
		http.Error(w, "advanced configuration cannot be adopted: "+current.err.Error(), http.StatusBadRequest)
		return
	}
	if err := adoptLegacyIntegration(&config, req.ID, current.manifest); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	migration, err := newLegacyMigration(root, req.ID, current.manifest.File)
	if err != nil {
		http.Error(w, "prepare legacy migration: "+err.Error(), http.StatusBadRequest)
		return
	}
	config.PendingLegacy = migration
	if err := integrations.Save(root, config); err != nil {
		http.Error(w, "save integration settings: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := finishLegacyMigration(root, &config); err != nil {
		http.Error(w, "settings saved, but legacy migration is pending: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeIntegrationViews(w)
}

func (s *Server) handleIntegrationFinishMigration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	root := index.Dir()
	release, err := s.acquireIntegrationMutation(root)
	if err != nil {
		http.Error(w, "integration settings are busy; try again shortly", http.StatusConflict)
		return
	}
	defer release()
	config, err := integrations.Load(root)
	if err != nil {
		http.Error(w, "read integration settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if config.PendingLegacy == nil || config.PendingLegacy.ID != req.ID {
		http.Error(w, "no matching legacy migration is pending", http.StatusNotFound)
		return
	}
	if err := finishLegacyMigration(root, &config); err != nil {
		http.Error(w, "legacy migration is still pending: "+err.Error(), http.StatusConflict)
		return
	}
	s.writeIntegrationViews(w)
}

func (s *Server) handleIntegrationRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	root := index.Dir()
	release, err := s.acquireIntegrationMutation(root)
	if err != nil {
		http.Error(w, "integration settings are busy; try again shortly", http.StatusConflict)
		return
	}
	defer release()
	config, err := integrations.Load(root)
	if err != nil {
		http.Error(w, "read integration settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if config.PendingLegacy != nil {
		http.Error(w, "finish the pending legacy migration before removing integration settings", http.StatusConflict)
		return
	}
	switch req.ID {
	case integrations.OpenNotebookID:
		if config.OpenNotebook == nil {
			http.Error(w, "Open Notebook is not managed in Settings", http.StatusNotFound)
			return
		}
		config.OpenNotebook = nil
	case integrations.OpenMontageID:
		if config.OpenMontage == nil {
			http.Error(w, "OpenMontage is not managed in Settings", http.StatusNotFound)
			return
		}
		config.OpenMontage = nil
	default:
		http.Error(w, "unknown integration", http.StatusBadRequest)
		return
	}
	if err := integrations.Save(root, config); err != nil {
		http.Error(w, "save integration settings: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.writeIntegrationViews(w)
}

// handleIntegrationTest is the only setup endpoint that may contact a local
// target. The caller has made an explicit same-origin POST; enabling or saving
// settings alone never performs I/O beyond Midden's own settings file.
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	req, err := decodeIntegrationRequest(r)
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	root := index.Dir()
	release, err := s.acquireIntegrationMutation(root)
	if err != nil {
		http.Error(w, "integration settings are busy; try again shortly", http.StatusConflict)
		return
	}
	defer release()
	manifest, managed, err := s.effectiveIntegration(req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !manifest.IsEnabled() {
		http.Error(w, "this integration is turned off", http.StatusConflict)
		return
	}

	result := testIntegration(r.Context(), req.ID, manifest)
	if managed {
		config, err := integrations.Load(root)
		if err != nil {
			http.Error(w, "read integration settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now().UTC()
		switch req.ID {
		case integrations.OpenNotebookID:
			config.OpenNotebook.LastVerified = now
			config.OpenNotebook.LastStatus = result.Status
			config.OpenNotebook.LastDetail = result.Detail
		case integrations.OpenMontageID:
			config.OpenMontage.LastVerified = now
			config.OpenMontage.LastStatus = result.Status
			config.OpenMontage.LastDetail = result.Detail
		}
		if err := integrations.Save(root, config); err != nil {
			http.Error(w, "save test result: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		s.rememberLegacyTest(req.ID, result)
	}
	s.writeIntegrationViews(w)
}

func testIntegration(ctx context.Context, id string, manifest plugins.Manifest) plugins.Result {
	switch id {
	case integrations.OpenNotebookID:
		verified := plugins.VerifyService(ctx, manifest, nil)
		if verified.Result.Status != plugins.Available {
			return verified.Result
		}
		if _, err := opennotebook.Prepare(manifest); err != nil {
			return plugins.Result{Status: plugins.Unavailable, Detail: err.Error()}
		}
		return verified.Result
	case integrations.OpenMontageID:
		return testOpenMontage(ctx, manifest)
	default:
		return plugins.Result{Status: plugins.Unavailable, Detail: "unknown integration"}
	}
}

func (s *Server) writeIntegrationViews(w http.ResponseWriter) {
	out, err := s.integrationViews()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

func (s *Server) integrationViews() ([]setupIntegrationView, error) {
	root := index.Dir()
	config, err := integrations.Load(root)
	if err != nil {
		return nil, fmt.Errorf("read integration settings: %w", err)
	}
	legacy, err := loadLegacyIntegrations(root)
	if err != nil {
		return nil, fmt.Errorf("read advanced integrations: %w", err)
	}

	out := make([]setupIntegrationView, 0, len(integrations.Catalog()))
	for _, entry := range integrations.Catalog() {
		view := setupIntegrationView{
			ID:             entry.ID,
			Name:           entry.Name,
			Description:    entry.Description,
			Kind:           entry.Kind,
			Cost:           entry.Cost,
			GitHubURL:      entry.GitHubURL,
			InstallSummary: entry.InstallSummary,
			State:          "not_set_up",
			StateDetail:    "No setup is saved yet.",
		}
		switch entry.ID {
		case integrations.OpenNotebookID:
			if config.OpenNotebook != nil {
				view.Enabled = config.OpenNotebook.Enabled
				view.Settings = integrationSettingsView{
					APIURL:           config.OpenNotebook.APIURL,
					UIURL:            config.OpenNotebook.UIURL,
					PasswordRequired: config.OpenNotebook.PasswordRequired,
				}
				setManagedIntegrationState(&view, config.OpenNotebook.Enabled, config.OpenNotebook.LastVerified, config.OpenNotebook.LastStatus, config.OpenNotebook.LastDetail)
			} else if item, ok := legacy[entry.ID]; ok {
				setLegacyIntegrationState(&view, item)
			}
		case integrations.OpenMontageID:
			if config.OpenMontage != nil {
				view.Enabled = config.OpenMontage.Enabled
				view.Settings = integrationSettingsView{
					Home:    config.OpenMontage.Home,
					Backend: config.OpenMontage.Backend,
				}
				setManagedIntegrationState(&view, config.OpenMontage.Enabled, config.OpenMontage.LastVerified, config.OpenMontage.LastStatus, config.OpenMontage.LastDetail)
			} else if item, ok := legacy[entry.ID]; ok {
				setLegacyIntegrationState(&view, item)
			}
		}
		if view.Legacy {
			if checked, ok := s.legacyTest(entry.ID); ok {
				setLegacyTestState(&view, checked)
			}
		}
		if config.PendingLegacy != nil && config.PendingLegacy.ID == entry.ID {
			view.State = "needs_attention"
			view.StateDetail = "Settings were saved, but preserving the prior advanced configuration is unfinished. Finish the migration before using this integration."
			view.Detail = "Legacy migration pending"
			view.MigrationPending = true
		} else if integrationConfigured(config, entry.ID) {
			if _, exists := legacy[entry.ID]; exists {
				view.State = "needs_attention"
				view.StateDetail = "Settings and an advanced manifest both exist. Midden will not silently choose or retire the advanced file."
				view.Detail = "Resolve the advanced configuration before using this integration."
				view.ConfigurationConflict = true
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func setManagedIntegrationState(view *setupIntegrationView, enabled bool, verified time.Time, status, detail string) {
	if !enabled {
		view.State = "turned_off"
		view.StateDetail = "Saved locally and turned off. Midden will not contact it."
		return
	}
	if verified.IsZero() {
		view.State = "ready_to_test"
		view.StateDetail = "Setup is saved. Test it when the local tool is ready."
		return
	}
	view.LastVerified = verified.Format(time.RFC3339)
	view.Detail = detail
	if status == plugins.Available {
		view.State = "connected"
		view.StateDetail = "The last explicit test succeeded."
		return
	}
	view.State = "needs_attention"
	view.StateDetail = "The last explicit test needs attention. Edit the setup or start the local tool, then test again."
}

func setLegacyIntegrationState(view *setupIntegrationView, item legacyIntegration) {
	view.State = "legacy"
	view.Legacy = true
	view.Enabled = item.manifest.IsEnabled()
	if item.manifest.Name == integrations.OpenNotebookID {
		view.Settings.PasswordRequired = item.manifest.API.Auth.Header != "" && !item.manifest.API.Auth.Optional
	}
	if item.err != nil {
		view.StateDetail = "An advanced manifest was found, but it is not usable."
		view.Detail = item.err.Error()
		return
	}
	view.StateDetail = "An existing advanced manifest still works. Adopt it to edit the supported settings here; Midden will keep a backup."
}

func setLegacyTestState(view *setupIntegrationView, checked legacyIntegrationTest) {
	view.LastVerified = checked.at.Format(time.RFC3339)
	view.Detail = checked.result.Detail
	if checked.result.Status == plugins.Available {
		view.State = "connected"
		view.StateDetail = "The current browser session explicitly tested this advanced setup successfully."
		return
	}
	view.State = "needs_attention"
	view.StateDetail = "The last explicit test of this advanced setup needs attention."
}

func (s *Server) effectiveIntegration(id string) (plugins.Manifest, bool, error) {
	config, err := integrations.Load(index.Dir())
	if err != nil {
		return plugins.Manifest{}, false, fmt.Errorf("read integration settings: %w", err)
	}
	if config.PendingLegacy != nil && config.PendingLegacy.ID == id {
		return plugins.Manifest{}, false, fmt.Errorf("finish the pending legacy migration before using %s", integrationName(id))
	}
	legacy, err := loadLegacyIntegrations(index.Dir())
	if err != nil {
		return plugins.Manifest{}, false, fmt.Errorf("read advanced integrations: %w", err)
	}
	if integrationConfigured(config, id) {
		if _, exists := legacy[id]; exists {
			return plugins.Manifest{}, false, fmt.Errorf("Settings and an advanced manifest both exist for %s; resolve the configuration conflict first", integrationName(id))
		}
	}
	switch id {
	case integrations.OpenNotebookID:
		if config.OpenNotebook != nil {
			manifest, err := integrations.OpenNotebookManifest(*config.OpenNotebook)
			return manifest, true, err
		}
	case integrations.OpenMontageID:
		if config.OpenMontage != nil {
			manifest, err := integrations.OpenMontageManifest(*config.OpenMontage)
			return manifest, true, err
		}
	default:
		return plugins.Manifest{}, false, fmt.Errorf("unknown integration %q", id)
	}
	item, ok := legacy[id]
	if !ok {
		return plugins.Manifest{}, false, fmt.Errorf("%s is not set up", integrationName(id))
	}
	if item.err != nil {
		return plugins.Manifest{}, false, fmt.Errorf("advanced %s configuration is invalid: %w", integrationName(id), item.err)
	}
	return item.manifest, false, nil
}

func loadLegacyIntegrations(root string) (map[string]legacyIntegration, error) {
	loaded, err := plugins.LoadDir(filepath.Join(root, "plugins"))
	if err != nil {
		return nil, err
	}
	out := map[string]legacyIntegration{}
	for _, item := range loaded {
		name := item.Manifest.Name
		if name != integrations.OpenNotebookID && name != integrations.OpenMontageID {
			continue
		}
		if existing, exists := out[name]; exists {
			existing.duplicate = true
			existing.err = fmt.Errorf("multiple advanced manifests use the name %q", name)
			out[name] = existing
			continue
		}
		if item.Error != nil {
			out[name] = legacyIntegration{manifest: item.Manifest, err: item.Error}
			continue
		}
		if errs := plugins.Validate(item.Manifest); len(errs) > 0 {
			out[name] = legacyIntegration{manifest: item.Manifest, err: fmt.Errorf("%s", strings.Join(errs, "; "))}
			continue
		}
		out[name] = legacyIntegration{manifest: item.Manifest}
	}
	return out, nil
}

func integrationConfigured(config integrations.Config, id string) bool {
	switch id {
	case integrations.OpenNotebookID:
		return config.OpenNotebook != nil
	case integrations.OpenMontageID:
		return config.OpenMontage != nil
	default:
		return false
	}
}

func applyIntegrationSettings(config *integrations.Config, req integrationWriteRequest) error {
	switch req.ID {
	case integrations.OpenNotebookID:
		config.OpenNotebook = &integrations.OpenNotebookSettings{
			Enabled:          req.Enabled,
			APIURL:           req.APIURL,
			UIURL:            req.UIURL,
			PasswordRequired: req.PasswordRequired,
		}

	case integrations.OpenMontageID:
		config.OpenMontage = &integrations.OpenMontageSettings{
			Enabled: req.Enabled,
			Home:    req.Home,
			Backend: req.Backend,
		}
	default:
		return fmt.Errorf("unknown integration %q", req.ID)
	}
	return nil
}

func validateIntegrationSettings(config integrations.Config, id string) error {
	switch id {
	case integrations.OpenNotebookID:
		if config.OpenNotebook == nil {
			return fmt.Errorf("Open Notebook settings are required")
		}
		_, err := integrations.OpenNotebookManifest(*config.OpenNotebook)
		return err
	case integrations.OpenMontageID:
		if config.OpenMontage == nil {
			return fmt.Errorf("OpenMontage settings are required")
		}
		_, err := integrations.OpenMontageManifest(*config.OpenMontage)
		return err
	default:
		return fmt.Errorf("unknown integration %q", id)
	}
}

func adoptLegacyIntegration(config *integrations.Config, id string, manifest plugins.Manifest) error {
	switch id {
	case integrations.OpenNotebookID:
		if manifest.API.Auth.Header != "" && manifest.API.Auth.Optional {
			return fmt.Errorf("this advanced Open Notebook setup uses optional password authentication; use Set up in Settings and choose its password requirement explicitly")
		}
		apiURL := strings.TrimSuffix(strings.TrimRight(manifest.API.Base, "/"), "/api")
		uiURL := strings.TrimSuffix(manifest.Link, "/notebooks/{{notebook_id|urlencode}}")
		settings := integrations.OpenNotebookSettings{
			Enabled:          manifest.IsEnabled(),
			APIURL:           apiURL,
			UIURL:            uiURL,
			PasswordRequired: manifest.API.Auth.Header != "" && !manifest.API.Auth.Optional,
		}
		if err := integrations.ValidateOpenNotebook(settings); err != nil {
			return fmt.Errorf("cannot adopt Open Notebook settings: %w", err)
		}
		config.OpenNotebook = &settings
	case integrations.OpenMontageID:
		settings := integrations.OpenMontageSettings{
			Enabled: manifest.IsEnabled(),
			Home:    manifest.Runs.Cwd,
			Backend: manifest.Runs.Backend,
		}
		if strings.Contains(settings.Home, "${") {
			return fmt.Errorf("the advanced OpenMontage setup uses an environment variable; enter a concrete folder in Settings instead")
		}
		if err := integrations.ValidateOpenMontage(settings); err != nil {
			return fmt.Errorf("cannot adopt OpenMontage settings: %w", err)
		}
		config.OpenMontage = &settings
	default:
		return fmt.Errorf("unknown integration %q", id)
	}
	return nil
}

func newLegacyMigration(root, id, source string) (*integrations.LegacyMigration, error) {
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	pluginRoot, err := filepath.Abs(filepath.Join(root, "plugins"))
	if err != nil {
		return nil, err
	}
	if !pathWithin(pluginRoot, sourcePath) {
		return nil, fmt.Errorf("advanced manifest is outside Midden's plugin directory")
	}
	return &integrations.LegacyMigration{
		ID:         id,
		SourcePath: sourcePath,
		BackupPath: fmt.Sprintf("%s.midden-legacy-%s.bak", sourcePath, time.Now().UTC().Format("20060102T150405.000000000Z")),
	}, nil
}

// finishLegacyMigration is recoverable across a process crash. Settings first
// record the pending migration; after the legacy file moves, a second atomic
// settings write clears it. If a process stops between those writes, the next
// explicit Finish action either moves the intact source or observes the
// existing backup and finalizes the pending record.
func finishLegacyMigration(root string, config *integrations.Config) error {
	migration := config.PendingLegacy
	if migration == nil {
		return fmt.Errorf("no legacy migration is pending")
	}
	if err := validateLegacyMigrationPaths(root, migration); err != nil {
		return err
	}

	sourceInfo, sourceErr := os.Lstat(migration.SourcePath)
	if sourceErr != nil && !os.IsNotExist(sourceErr) {
		return sourceErr
	}
	if os.IsNotExist(sourceErr) {
		backupInfo, err := os.Lstat(migration.BackupPath)
		if err != nil {
			return fmt.Errorf("legacy source is missing and backup is unavailable: %w", err)
		}
		if backupInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.Mode().IsRegular() {
			return fmt.Errorf("legacy backup must be a regular non-link file")
		}
		config.PendingLegacy = nil
		if err := integrations.Save(root, *config); err != nil {
			config.PendingLegacy = migration
			return err
		}
		return nil
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("advanced manifest must be a regular non-link file")
	}
	if _, err := os.Lstat(migration.BackupPath); err == nil {
		return fmt.Errorf("legacy backup already exists; resolve it before finishing migration")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(migration.SourcePath, migration.BackupPath); err != nil {
		return err
	}
	config.PendingLegacy = nil
	if err := integrations.Save(root, *config); err != nil {
		config.PendingLegacy = migration
		return fmt.Errorf("legacy manifest was preserved but migration state could not be finalized: %w", err)
	}
	return nil
}

func validateLegacyMigrationPaths(root string, migration *integrations.LegacyMigration) error {
	if migration == nil || migration.ID == "" {
		return fmt.Errorf("invalid pending legacy migration")
	}
	pluginRoot, err := filepath.Abs(filepath.Join(root, "plugins"))
	if err != nil {
		return err
	}
	sourcePath, err := filepath.Abs(migration.SourcePath)
	if err != nil {
		return err
	}
	backupPath, err := filepath.Abs(migration.BackupPath)
	if err != nil {
		return err
	}
	if !pathWithin(pluginRoot, sourcePath) || !pathWithin(pluginRoot, backupPath) {
		return fmt.Errorf("pending legacy migration is outside Midden's plugin directory")
	}
	if !strings.HasPrefix(backupPath, sourcePath+".midden-legacy-") || !strings.HasSuffix(backupPath, ".bak") {
		return fmt.Errorf("pending legacy backup path is invalid")
	}
	return nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func decodeIntegrationRequest(r *http.Request) (integrationWriteRequest, error) {
	var req integrationWriteRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil {
		return req, err
	}
	if len(body) > 64<<10 {
		return req, fmt.Errorf("request exceeds 65536 byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return req, fmt.Errorf("request must contain exactly one JSON value")
		}
		return req, err
	}
	if strings.TrimSpace(req.ID) == "" {
		return req, fmt.Errorf("integration id is required")
	}
	return req, nil
}

func integrationName(id string) string {
	switch id {
	case integrations.OpenNotebookID:
		return "Open Notebook"
	case integrations.OpenMontageID:
		return "OpenMontage"
	default:
		return id
	}
}
