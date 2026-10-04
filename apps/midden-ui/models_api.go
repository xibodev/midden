package main

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/xibodev/compa/pkg/auth"
	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
)

// modelState is what the Models page shows. Secrets are never part of it.
type modelState struct {
	Roster       []rosterEntry  `json:"roster"`
	Instances    []instanceView `json:"instances"`
	Routes       []routeView    `json:"routes"`
	DefaultModel string         `json:"defaultModel"`
	ActiveModels []string       `json:"activeModels"`
	Extension    extensionView  `json:"extension"`
	Configured   bool           `json:"configured"`
	SetupError   string         `json:"setupError"`
}

type rosterEntry struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Adapter         string   `json:"adapter"`
	Protocol        string   `json:"protocol"`
	AuthMethods     []string `json:"authMethods"`
	DefaultEndpoint string   `json:"defaultEndpoint"`
	RequiresAPIKey  bool     `json:"requiresApiKey"`
	RequiresBaseURL bool     `json:"requiresBaseUrl"`
	Keyless         bool     `json:"keyless"`
}

type instanceView struct {
	ID              string                `json:"id"`
	Label           string                `json:"label"`
	ProviderKind    string                `json:"providerKind"`
	Adapter         string                `json:"adapter"`
	Protocol        string                `json:"protocol"`
	Endpoint        string                `json:"endpoint"`
	State           string                `json:"state"`
	CredentialReady bool                  `json:"credentialReady"`
	Source          string                `json:"source"`
	Models          []catalogModelView    `json:"models"`
	Checks          map[string]modelCheck `json:"checks"`
}

type catalogModelView struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

type routeView struct {
	Name    string   `json:"name"`
	Targets []string `json:"targets"`
}

type extensionView struct {
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
}

// serveModels exposes model connections managed through Compa's runtime format
// under STATE/kernel: providers, free models, local servers, extension services,
// routes and the default selection.
func (a *App) serveModels(w http.ResponseWriter, r *http.Request) {
	if a.serveModelExtras(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/models/")
	switch {
	case rest == "state":
		if r.Method != "GET" {
			methodNotAllowed(w, "GET")
			return
		}
		a.respondModelState(w, map[string]any{})
	case rest == "free":
		if r.Method != "POST" {
			methodNotAllowed(w, "POST")
			return
		}
		if !decode(w, r, &struct{}{}) {
			return
		}
		outcomes, err := a.connectFree(r.Context())
		if err != nil {
			modelError(w, err)
			return
		}
		a.respondModelState(w, map[string]any{"outcomes": outcomes})
	case rest == "instances":
		if r.Method != "POST" {
			methodNotAllowed(w, "POST")
			return
		}
		var input instanceInput
		if !decode(w, r, &input) {
			return
		}
		id, err := a.createInstance(r.Context(), input)
		if err != nil {
			modelError(w, err)
			return
		}
		a.respondModelInstance(w, id)
	case strings.HasPrefix(rest, "instances/"):
		a.serveModelInstance(w, r, strings.TrimPrefix(rest, "instances/"))
	case rest == "default":
		if r.Method != "PUT" {
			methodNotAllowed(w, "PUT")
			return
		}
		var input struct {
			Selection *string `json:"selection"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Selection == nil || len(*input.Selection) > 300 {
			apiError(w, http.StatusBadRequest, `selection must be a model or route name, or "" to clear it`)
			return
		}
		if err := a.setDefaultModel(*input.Selection); err != nil {
			modelError(w, err)
			return
		}
		a.respondModelState(w, map[string]any{})
	case strings.HasPrefix(rest, "routes/"):
		a.serveModelRoute(w, r, strings.TrimPrefix(rest, "routes/"))
	default:
		apiError(w, http.StatusNotFound, "route not found")
	}
}

func (a *App) serveModelInstance(w http.ResponseWriter, r *http.Request, rest string) {
	id, action, _ := strings.Cut(rest, "/")
	if !modelName.MatchString(id) {
		apiError(w, http.StatusNotFound, "model connection not found")
		return
	}
	switch action {
	case "":
		if r.Method != "DELETE" {
			methodNotAllowed(w, "DELETE")
			return
		}
		if err := a.deleteInstance(id); err != nil {
			modelError(w, err)
			return
		}
		a.respondModelState(w, map[string]any{})
	case "sync":
		if r.Method != "POST" {
			methodNotAllowed(w, "POST")
			return
		}
		if !decode(w, r, &struct{}{}) {
			return
		}
		if err := a.syncInstance(r.Context(), id); err != nil {
			modelError(w, err)
			return
		}
		a.respondModelInstance(w, id)
	case "check":
		if r.Method != "POST" {
			methodNotAllowed(w, "POST")
			return
		}
		var input struct {
			Model string `json:"model"`
		}
		if !decode(w, r, &input) {
			return
		}
		result, err := a.checkModel(r.Context(), id, input.Model)
		if err != nil {
			modelError(w, err)
			return
		}
		respond(w, map[string]string{"status": result.Status, "message": result.Message})
	default:
		apiError(w, http.StatusNotFound, "route not found")
	}
}

func (a *App) serveModelRoute(w http.ResponseWriter, r *http.Request, name string) {
	if !modelName.MatchString(name) {
		apiError(w, http.StatusBadRequest, "a route name uses lowercase letters, numbers, '.', '_' or '-'")
		return
	}
	var err error
	switch r.Method {
	case "PUT":
		var input struct {
			Targets []string `json:"targets"`
		}
		if !decode(w, r, &input) {
			return
		}
		err = a.putRoute(name, input.Targets)
	case "DELETE":
		err = a.deleteRoute(name)
	default:
		methodNotAllowed(w, "PUT, DELETE")
		return
	}
	if err != nil {
		modelError(w, err)
		return
	}
	a.respondModelState(w, map[string]any{})
}

// modelError reports a refused or failed model request.
func modelError(w http.ResponseWriter, err error) {
	var refused modelRequestError
	switch {
	case errors.As(err, &refused):
		apiError(w, refused.status, refused.text)
	case errors.Is(err, errModelBusy):
		apiError(w, http.StatusConflict, err.Error())
	default:
		apiError(w, http.StatusInternalServerError, err.Error())
	}
}

func (a *App) respondModelState(w http.ResponseWriter, body map[string]any) {
	state, err := a.modelState()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body["state"] = state
	respond(w, body)
}

func (a *App) respondModelInstance(w http.ResponseWriter, id string) {
	state, err := a.modelState()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, instance := range state.Instances {
		if instance.ID == id {
			respond(w, map[string]any{"instance": instance, "state": state})
			return
		}
	}
	apiError(w, http.StatusConflict, "the connection changed before it could be shown; reload Models")
}

// modelStateResponse returns the current model state for routes that change it.
func (a *App) modelStateResponse() (any, error) {
	return a.modelState()
}

// modelState reads the stored connections, catalogs and check results.
func (a *App) modelState() (modelState, error) {
	cfg, err := a.loadModelConfig()
	if err != nil {
		return modelState{}, err
	}
	store, err := modelservice.LoadCatalogs()
	if err != nil {
		return modelState{}, err
	}
	checks := a.loadModelChecks()
	state := modelState{Roster: []rosterEntry{}, Instances: []instanceView{}, Routes: []routeView{}, ActiveModels: []string{}}
	roster := map[string]modelservice.ProviderRosterItem{}
	for _, item := range modelservice.ListRoster(cfg) {
		roster[item.ID] = item
		state.Roster = append(state.Roster, rosterEntry{ID: item.ID, Label: item.Label, Adapter: item.Adapter, Protocol: item.Protocol,
			AuthMethods: append([]string{}, item.AuthMethods...), DefaultEndpoint: item.DefaultEndpoint,
			RequiresAPIKey: item.RequiresAPIKey, RequiresBaseURL: item.RequiresBaseURL, Keyless: item.AnonymousAutomation})
	}
	for _, instance := range cfg.ProviderInstances {
		if instance != nil {
			state.Instances = append(state.Instances, instanceViewOf(instance, roster, store, checks[instance.ID]))
		}
	}
	for _, route := range cfg.ModelRoutes {
		if route != nil {
			state.Routes = append(state.Routes, routeView{Name: route.Name, Targets: append([]string{}, route.Targets...)})
		}
	}
	sort.Slice(state.Routes, func(i, j int) bool { return state.Routes[i].Name < state.Routes[j].Name })
	state.ActiveModels = append(state.ActiveModels, cfg.ActiveModels...)
	if cfg.Extension != nil {
		state.Extension = extensionView{URL: cfg.Extension.URL, Connected: strings.TrimSpace(cfg.Extension.URL) != ""}
	}
	status := selectionStatus(cfg)
	state.DefaultModel, state.Configured, state.SetupError = status.DefaultModel, status.Configured, status.SetupError
	return state, nil
}

// instanceViewOf shows one connection with its chat models and their checks.
func instanceViewOf(instance *config.ProviderInstanceConfig, roster map[string]modelservice.ProviderRosterItem, store *modelservice.CatalogStore, checks map[string]modelCheck) instanceView {
	item, known := roster[instance.ProviderKind]
	view := instanceView{ID: instance.ID, Label: instance.ID, ProviderKind: instance.ProviderKind, Adapter: instance.Adapter,
		Protocol: instance.Protocol, Endpoint: instance.Endpoint, State: string(instance.State),
		CredentialReady: credentialReady(instance, item, known), Source: instanceSource(instance, item, known),
		Models: []catalogModelView{}, Checks: map[string]modelCheck{}}
	if name, _ := instance.Settings[config.ExtensionDisplayNameSetting].(string); strings.TrimSpace(name) != "" {
		view.Label = strings.TrimSpace(name)
	} else if known && item.Label != "" {
		view.Label = item.Label
	}
	entry := store.Entries[instance.ID]
	if !modelservice.ValidInstanceCatalog(instance.ID, entry, instance) {
		return view
	}
	for _, model := range entry.Models {
		id := strings.TrimSpace(model.ID)
		if _, err := config.ParseExactModelTarget(instance.ID + "/" + id); err != nil || !modelservice.ServesChat(model.Surfaces) {
			continue
		}
		view.Models = append(view.Models, catalogModelView{ID: id, DisplayName: strings.TrimSpace(model.DisplayName)})
		if check, ok := checks[id]; ok {
			view.Checks[id] = check
		}
	}
	return view
}

// instanceSource tells where a connection comes from.
func instanceSource(instance *config.ProviderInstanceConfig, item modelservice.ProviderRosterItem, known bool) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(instance.Adapter), config.ProviderAdapterExtension):
		return "extension"
	case loopbackEndpoint(instance.Endpoint):
		return "local"
	case strings.TrimSpace(instance.AuthConnectionRef) == "" && known && item.AnonymousAutomation:
		return "free"
	}
	return "key"
}

func loopbackEndpoint(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

// credentialReady reports whether the credential a connection needs is stored.
func credentialReady(instance *config.ProviderInstanceConfig, item modelservice.ProviderRosterItem, known bool) bool {
	if ref := strings.TrimSpace(instance.AuthConnectionRef); ref != "" {
		_, err := modelservice.ResolveCredentialReference(ref)
		return err == nil
	}
	if instance.ExtensionProvider() != "" {
		kind, _ := instance.Settings[config.ExtensionCredentialSetting].(string)
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "none":
			return true
		case "oauth":
			key, _ := instance.Settings[config.ExtensionCredentialKeySetting].(string)
			if strings.TrimSpace(key) == "" {
				return false
			}
			credential, err := auth.GetCredential(strings.TrimSpace(key))
			return err == nil && credential != nil && (credential.AccessToken != "" || credential.RefreshToken != "")
		}
		return false
	}
	return !known || !item.RequiresAPIKey
}
