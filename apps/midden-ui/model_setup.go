package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/xibodev/compa/pkg/auth"
	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
	"github.com/xibodev/compa/pkg/providers"
)

// Bounds of the requests model setup sends to a provider.
const (
	catalogTimeout = 30 * time.Second
	checkTimeout   = 25 * time.Second
	freeTimeout    = 2 * time.Minute
)

// modelName matches Compa's instance ids and route names.
var modelName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)

// modelRequestError is a refused model request and its HTTP status.
type modelRequestError struct {
	status int
	text   string
}

func (e modelRequestError) Error() string { return e.text }

func modelFailure(status int, format string, args ...any) error {
	return modelRequestError{status: status, text: fmt.Sprintf(format, args...)}
}

// credentialKey is the auth store key of a connection's API key.
func credentialKey(instanceID string) string { return "midden-" + instanceID }

func findInstance(cfg *config.Config, id string) *config.ProviderInstanceConfig {
	for _, instance := range cfg.ProviderInstances {
		if instance != nil && instance.ID == id {
			return instance
		}
	}
	return nil
}

func routeIndex(cfg *config.Config, name string) int {
	return slices.IndexFunc(cfg.ModelRoutes, func(route *config.ModelRouteConfig) bool { return route != nil && route.Name == name })
}

func rosterByID(cfg *config.Config) map[string]modelservice.ProviderRosterItem {
	roster := map[string]modelservice.ProviderRosterItem{}
	for _, item := range modelservice.ListRoster(cfg) {
		roster[item.ID] = item
	}
	return roster
}

// instanceInput is the body of POST /api/models/instances.
type instanceInput struct {
	ProviderKind string `json:"providerKind"`
	Endpoint     string `json:"endpoint"`
	APIKey       string `json:"apiKey"`
	Label        string `json:"label"`
}

// createInstance connects a roster provider: it lists the provider's models
// with the given key, stores the key, saves the catalog and enables the
// connection. Its first chat model becomes the default when none is set.
func (a *App) createInstance(ctx context.Context, input instanceInput) (string, error) {
	item, ok := rosterByID(nil)[strings.TrimSpace(input.ProviderKind)]
	if !ok || item.Adapter == "" {
		return "", modelFailure(http.StatusBadRequest, "choose a provider from the list")
	}
	endpoint, err := connectionEndpoint(input.Endpoint, item.DefaultEndpoint)
	if err != nil {
		return "", modelFailure(http.StatusBadRequest, "%v", err)
	}
	key := strings.TrimSpace(input.APIKey)
	if key == "" && item.RequiresAPIKey {
		return "", modelFailure(http.StatusBadRequest, "%s needs an API key", item.Label)
	}
	if len(key) > 4096 || strings.ContainsFunc(key, unicode.IsControl) {
		return "", modelFailure(http.StatusBadRequest, "the API key must be one line of at most 4096 characters")
	}
	label := strings.TrimSpace(input.Label)
	if len([]rune(label)) > 80 || strings.ContainsFunc(label, unicode.IsControl) {
		return "", modelFailure(http.StatusBadRequest, "the label must be one line of at most 80 characters")
	}
	instance := &config.ProviderInstanceConfig{ProviderKind: item.ID, Adapter: item.Adapter, Protocol: item.Protocol,
		Endpoint: endpoint, State: config.ProviderInstanceStateEnabled}
	if label != "" {
		instance.Settings = map[string]any{config.ExtensionDisplayNameSetting: label}
	}
	err = a.changeModelConfig(func(cfg *config.Config, undo *modelUndo) error {
		instance.ID = uniqueInstanceID(cfg, item.ID)
		if key != "" {
			instance.AuthConnectionRef = "credential:" + credentialKey(instance.ID)
		}
		cfg.ProviderInstances = append(cfg.ProviderInstances, instance)
		if err := cfg.ValidateProviderInstances(); err != nil {
			return modelFailure(http.StatusBadRequest, "%v", err)
		}
		models, err := syncInstanceCatalog(ctx, instance, key)
		if err != nil {
			return err
		}
		if err := storeCredential(instance, key, undo); err != nil {
			return err
		}
		if err := saveCatalog(instance, models, undo); err != nil {
			return err
		}
		if err := a.forgetModelChecks(instance.ID, undo); err != nil {
			return err
		}
		modelservice.AdoptDefaultModel(cfg, firstChatModel(instance.ID, models))
		return nil
	})
	if err != nil {
		return "", err
	}
	return instance.ID, nil
}

// connectionEndpoint returns the given endpoint, or the provider's default.
func connectionEndpoint(given, fallback string) (string, error) {
	endpoint := strings.TrimSpace(given)
	if endpoint == "" {
		endpoint = strings.TrimSpace(fallback)
	}
	if endpoint == "" {
		return "", fmt.Errorf("this provider needs an endpoint URL")
	}
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("the endpoint must be an http(s) URL without credentials, query or fragment")
	}
	return strings.TrimRight(endpoint, "/"), nil
}

// uniqueInstanceID is base, or base with the first free numeric suffix.
func uniqueInstanceID(cfg *config.Config, base string) string {
	id := base
	for n := 2; findInstance(cfg, id) != nil; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

// syncInstanceCatalog lists the models instance reaches with secret.
func syncInstanceCatalog(ctx context.Context, instance *config.ProviderInstanceConfig, secret string) ([]modelservice.CatalogModel, error) {
	if !modelservice.CatalogSyncSupported(instance) {
		return nil, modelFailure(http.StatusBadRequest, "Compa cannot list this provider's models")
	}
	input := modelservice.CatalogSyncInputFromInstance(instance)
	input.Secret = secret
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	models, err := modelservice.SyncCatalog(ctx, input)
	if err != nil {
		return nil, modelFailure(http.StatusBadGateway, "%s", clip(redact("the model list could not be read: "+err.Error(), secret), 500))
	}
	if len(models) == 0 {
		return nil, modelFailure(http.StatusBadGateway, "the provider listed no models; check the endpoint and key")
	}
	return models, nil
}

// firstChatModel returns the exact target of the first listed chat model.
func firstChatModel(instanceID string, models []modelservice.CatalogModel) string {
	for _, model := range models {
		target := instanceID + "/" + strings.TrimSpace(model.ID)
		if _, err := config.ParseExactModelTarget(target); err == nil && modelservice.ServesChat(model.Surfaces) {
			return target
		}
	}
	return ""
}

// storeCredential stores a connection's API key under its own auth store key.
func storeCredential(instance *config.ProviderInstanceConfig, key string, undo *modelUndo) error {
	if key == "" {
		return nil
	}
	name := credentialKey(instance.ID)
	previous, err := auth.GetCredential(name)
	if err != nil {
		return fmt.Errorf("read stored keys: %w", err)
	}
	credential := &auth.AuthCredential{AccessToken: key, Provider: instance.ProviderKind, AuthMethod: modelservice.APIKeyAuthMethod}
	if err := auth.SetCredential(name, credential); err != nil {
		return fmt.Errorf("store the API key: %w", err)
	}
	undo.add(func() { restoreCredential(name, previous) })
	return nil
}

// dropCredential deletes a stored key as part of a change.
func dropCredential(name string, undo *modelUndo) error {
	previous, err := auth.GetCredential(name)
	if err != nil {
		return fmt.Errorf("read stored keys: %w", err)
	}
	if previous == nil {
		return nil
	}
	if err := auth.DeleteCredential(name); err != nil {
		return fmt.Errorf("delete the stored key: %w", err)
	}
	undo.add(func() { restoreCredential(name, previous) })
	return nil
}

func restoreCredential(name string, previous *auth.AuthCredential) {
	if previous != nil {
		_ = auth.SetCredential(name, previous)
	} else {
		_ = auth.DeleteCredential(name)
	}
}

// keepCatalogs registers the restore of the saved catalogs as they are now.
func keepCatalogs(undo *modelUndo) error {
	previous, err := modelservice.LoadCatalogs()
	if err != nil {
		return fmt.Errorf("read model lists: %w", err)
	}
	undo.add(func() { _ = modelservice.SaveCatalogs(previous) })
	return nil
}

// saveCatalog saves an instance's catalog as part of a change.
func saveCatalog(instance *config.ProviderInstanceConfig, models []modelservice.CatalogModel, undo *modelUndo) error {
	if err := keepCatalogs(undo); err != nil {
		return err
	}
	if err := modelservice.SaveProviderInstanceCatalog(instance, models); err != nil {
		return fmt.Errorf("save the model list: %w", err)
	}
	return nil
}

// syncInstance lists an instance's models again. Targets on models it no
// longer lists leave the active models, routes and selections.
func (a *App) syncInstance(ctx context.Context, id string) error {
	return a.changeModelConfig(func(cfg *config.Config, undo *modelUndo) error {
		instance := findInstance(cfg, id)
		if instance == nil {
			return modelFailure(http.StatusNotFound, "model connection %q not found", id)
		}
		if instance.State != config.ProviderInstanceStateEnabled {
			return modelFailure(http.StatusConflict, "model connection %q is turned off", id)
		}
		secret := ""
		if ref := strings.TrimSpace(instance.AuthConnectionRef); ref != "" {
			var err error
			if secret, err = modelservice.ResolveCredentialReference(ref); err != nil {
				return modelFailure(http.StatusConflict, "the stored key is unavailable (%v); remove this connection and add it again", err)
			}
		}
		models, err := syncInstanceCatalog(ctx, instance, secret)
		if err != nil {
			return err
		}
		if err := saveCatalog(instance, models, undo); err != nil {
			return err
		}
		listed := map[string]bool{}
		for _, model := range models {
			listed[strings.TrimSpace(model.ID)] = true
		}
		modelservice.DropTargets(cfg, func(target config.ExactModelTarget) bool {
			return target.InstanceID != id || listed[target.ModelID]
		})
		return nil
	})
}

// deleteInstance removes a connection with its catalog, stored key and check
// results. A connection a route uses stays until the route stops using it.
func (a *App) deleteInstance(id string) error {
	return a.changeModelConfig(func(cfg *config.Config, undo *modelUndo) error {
		index := slices.IndexFunc(cfg.ProviderInstances, func(instance *config.ProviderInstanceConfig) bool {
			return instance != nil && instance.ID == id
		})
		if index < 0 {
			return modelFailure(http.StatusNotFound, "model connection %q not found", id)
		}
		if routes := routesUsing(cfg, id); len(routes) > 0 {
			return modelFailure(http.StatusConflict, "route %s uses this connection; remove it from the route first", strings.Join(routes, ", "))
		}
		removed := cfg.ProviderInstances[index]
		cfg.ProviderInstances = slices.Delete(cfg.ProviderInstances, index, index+1)
		modelservice.DropTargets(cfg, func(target config.ExactModelTarget) bool { return target.InstanceID != id })
		if err := keepCatalogs(undo); err != nil {
			return err
		}
		if err := modelservice.DeleteProviderInstanceCatalog(id); err != nil {
			return fmt.Errorf("delete the model list: %w", err)
		}
		if removed.AuthConnectionRef == "credential:"+credentialKey(id) {
			if err := dropCredential(credentialKey(id), undo); err != nil {
				return err
			}
		}
		return a.forgetModelChecks(id, undo)
	})
}

func routesUsing(cfg *config.Config, id string) []string {
	var names []string
	for _, route := range cfg.ModelRoutes {
		if route == nil {
			continue
		}
		for _, raw := range route.Targets {
			if target, err := config.ParseExactModelTarget(raw); err == nil && target.InstanceID == id {
				names = append(names, route.Name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// freeOutcome is one free provider's result as the Models page shows it.
type freeOutcome struct {
	InstanceID string `json:"instanceId"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Models     int    `json:"models"`
}

// connectFree checks Compa's free providers that need no key and connects
// those that answer. The first model that answered becomes the default when
// none is set.
func (a *App) connectFree(ctx context.Context) ([]freeOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, freeTimeout)
	defer cancel()
	var result *modelservice.AutoConnectResult
	err := a.changeModelConfig(func(cfg *config.Config, undo *modelUndo) error {
		if err := keepCatalogs(undo); err != nil {
			return err
		}
		var err error
		if result, err = modelservice.AutoConnectFree(ctx, cfg, a.freeVerify); err != nil {
			return err
		}
		if result.Verified == 0 {
			return errNoModelChange
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	roster := rosterByID(nil)
	outcomes := make([]freeOutcome, 0, len(result.Outcomes))
	for _, outcome := range result.Outcomes {
		label := outcome.RegistryID
		if item, ok := roster[outcome.RegistryID]; ok && item.Label != "" {
			label = item.Label
		}
		outcomes = append(outcomes, freeOutcome{InstanceID: outcome.ProviderID, Label: label,
			Status: freeStatus(outcome), Error: outcome.Error, Models: len(outcome.Models)})
	}
	return outcomes, nil
}

// freeStatus words Compa's outcome status for the Models page.
func freeStatus(outcome modelservice.AnonymousProviderOutcome) string {
	switch {
	case outcome.Status == "verified":
		return "answers_text"
	case outcome.ErrorClass == "rate_limited":
		return "busy"
	case outcome.Status == "connected":
		return "connected"
	}
	return "failed"
}

// setDefaultModel sets the default selection, an exact chat target or a
// route name; "" clears it.
func (a *App) setDefaultModel(selection string) error {
	selection = strings.TrimSpace(selection)
	return a.changeModelConfig(func(cfg *config.Config, _ *modelUndo) error {
		if err := kernelModelResolver().Check(cfg, selection); err != nil {
			return modelFailure(http.StatusBadRequest, "%q cannot be the default model: %v", selection, err)
		}
		if _, err := config.ParseExactModelTarget(selection); err == nil {
			if err := checkChatTargets(cfg, []string{selection}); err != nil {
				return modelFailure(http.StatusBadRequest, "%v", err)
			}
		}
		cfg.Agents.Defaults.ModelName = selection
		return nil
	})
}

// putRoute creates or replaces a route: ordered chat targets, where the next
// answers when one is busy or fails.
func (a *App) putRoute(name string, targets []string) error {
	if len(targets) > 32 {
		return modelFailure(http.StatusBadRequest, "a route holds at most 32 models")
	}
	route := &config.ModelRouteConfig{Name: name, Targets: make([]string, 0, len(targets))}
	for _, target := range targets {
		route.Targets = append(route.Targets, strings.TrimSpace(target))
	}
	return a.changeModelConfig(func(cfg *config.Config, _ *modelUndo) error {
		if index := routeIndex(cfg, name); index >= 0 {
			cfg.ModelRoutes[index] = route
		} else {
			cfg.ModelRoutes = append(cfg.ModelRoutes, route)
		}
		if err := cfg.ValidateProviderInstances(); err != nil {
			return modelFailure(http.StatusBadRequest, "%v", err)
		}
		if err := checkChatTargets(cfg, route.Targets); err != nil {
			return modelFailure(http.StatusBadRequest, "%v", err)
		}
		return nil
	})
}

// deleteRoute removes a route; a selection naming it is cleared.
func (a *App) deleteRoute(name string) error {
	return a.changeModelConfig(func(cfg *config.Config, _ *modelUndo) error {
		index := routeIndex(cfg, name)
		if index < 0 {
			return modelFailure(http.StatusNotFound, "route %q not found", name)
		}
		cfg.ModelRoutes = slices.Delete(cfg.ModelRoutes, index, index+1)
		unset := func(selection *string) {
			if strings.TrimSpace(*selection) == name {
				*selection = ""
			}
		}
		defaults := &cfg.Agents.Defaults
		unset(&defaults.ModelName)
		unset(&defaults.ImageModel)
		if defaults.Routing != nil {
			unset(&defaults.Routing.LightModel)
		}
		for i := range cfg.Agents.List {
			unset(&cfg.Agents.List[i].Model)
		}
		return nil
	})
}

// checkChatTargets reports a target that is not a chat model in an enabled
// connection's catalog.
func checkChatTargets(cfg *config.Config, targets []string) error {
	store, err := modelservice.LoadCatalogs()
	if err != nil {
		return fmt.Errorf("read model lists: %w", err)
	}
	for _, raw := range targets {
		target, err := config.ParseExactModelTarget(raw)
		if err != nil {
			return fmt.Errorf("target %q: %v", raw, err)
		}
		instance := findInstance(cfg, target.InstanceID)
		entry := store.Entries[target.InstanceID]
		index := -1
		if instance != nil && instance.State == config.ProviderInstanceStateEnabled && modelservice.ValidInstanceCatalog(target.InstanceID, entry, instance) {
			index = slices.IndexFunc(entry.Models, func(model modelservice.CatalogModel) bool { return strings.TrimSpace(model.ID) == target.ModelID })
		}
		if index < 0 {
			return fmt.Errorf("target %q is not in an enabled connection's model list", raw)
		}
		if !modelservice.ServesChat(entry.Models[index].Surfaces) {
			return fmt.Errorf("target %q is not a chat model", raw)
		}
	}
	return nil
}

// checkModel asks one model for an inert tool call and stores the result.
// Stand-in until Compa reports tool capability.
func (a *App) checkModel(ctx context.Context, id, model string) (modelCheck, error) {
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 300 {
		return modelCheck{}, modelFailure(http.StatusBadRequest, "choose a model to check")
	}
	a.mu.Lock()
	busy := a.active != nil
	a.mu.Unlock()
	if busy {
		return modelCheck{}, modelFailure(http.StatusConflict, "stop the active turn before checking a model")
	}
	cfg, err := a.loadModelConfig()
	if err != nil {
		return modelCheck{}, err
	}
	if findInstance(cfg, id) == nil {
		return modelCheck{}, modelFailure(http.StatusNotFound, "model connection %q not found", id)
	}
	secret := ""
	resolver := modelservice.NewResolver(
		modelservice.WithCredentialResolver(func(ref string) (string, error) {
			value, err := modelservice.ResolveCredentialReference(ref)
			secret = value
			return value, err
		}),
		modelservice.WithProviderFactory(protectedInstanceProvider))
	resolution, err := resolver.Resolve(cfg, id+"/"+model)
	if err != nil {
		return modelCheck{}, modelFailure(http.StatusBadRequest, "%v", err)
	}
	candidate := resolution.Candidates[0]
	result := modelCheck{Status: "tested", Message: "The model returned the required tool call. No workspace files were read or changed."}
	provider, err := resolution.ProviderForCandidate(candidate)
	if err == nil {
		err = probeToolCall(ctx, provider, candidate.Model)
		if closer, ok := provider.(providers.StatefulProvider); ok {
			closer.Close()
		}
	}
	if err != nil {
		result = modelCheck{Status: "failed", Message: clip(redact(err.Error(), secret), 500)}
	}
	result.At = time.Now().UTC().Format(time.RFC3339)
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	if current, err := a.loadModelConfig(); err != nil || findInstance(current, id) == nil {
		return result, nil
	}
	checks := a.loadModelChecks()
	if checks[id] == nil {
		checks[id] = map[string]modelCheck{}
	}
	checks[id][model] = result
	if err := a.saveModelChecks(checks); err != nil {
		return modelCheck{}, fmt.Errorf("save the check result: %w", err)
	}
	return result, nil
}

// probeToolCall asks a model to call an inert tool; nothing is executed.
func probeToolCall(ctx context.Context, provider providers.LLMProvider, model string) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	response, err := provider.Chat(ctx,
		[]providers.Message{{Role: "user", Content: "This is a connection check with no user files. Call midden_connection_check with ok=true. Do not answer with prose."}},
		[]providers.ToolDefinition{{Type: "function", Function: providers.ToolFunctionDefinition{
			Name: "midden_connection_check", Description: "An inert tool-capability probe. No command or file operation is executed.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false},
		}}}, model, map[string]any{"max_tokens": 128})
	if err != nil {
		return err
	}
	if response != nil {
		for _, call := range response.ToolCalls {
			name, arguments := call.Name, call.Arguments
			if call.Function != nil {
				name = call.Function.Name
				if arguments == nil {
					if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil {
						return fmt.Errorf("the model returned malformed probe arguments: %w", err)
					}
				}
			}
			if name == "midden_connection_check" && arguments["ok"] == true {
				return nil
			}
		}
	}
	return fmt.Errorf("the service responded but did not return the required tool call; this model is not verified for tool use")
}
