package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Bounds of the requests model setup sends to a provider.
const (
	catalogTimeout = 30 * time.Second
	checkTimeout   = 25 * time.Second
	freeTimeout    = 2 * time.Minute
)

// modelName matches Compa's instance ids and route names.
var modelName = stableName

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

// instanceInput is the body of POST /api/models/instances.
type instanceInput struct {
	ProviderKind string `json:"providerKind"`
	Endpoint     string `json:"endpoint"`
	APIKey       string `json:"apiKey"`
	Label        string `json:"label"`
}

// createInstance connects a roster provider: it lists the provider's models
// with the given key, stores the key, saves the model list and enables the
// connection. Its first chat model becomes the default when none is set.
func (a *App) createInstance(ctx context.Context, input instanceInput) (string, error) {
	item, ok := rosterByID()[strings.TrimSpace(input.ProviderKind)]
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
	instance := &providerInstance{ProviderKind: item.ID, Adapter: item.Adapter, Protocol: item.Protocol, Endpoint: endpoint, State: instanceEnabled}
	if label != "" {
		instance.Settings = map[string]any{settingDisplayName: label}
	}
	err = a.changeModelConfig(ctx, func(cfg *kernelConfig, undo *modelUndo) error {
		instance.ID = uniqueInstanceID(cfg, item.ID)
		if key != "" {
			instance.AuthConnectionRef = "credential:" + credentialKey(instance.ID)
		}
		cfg.Instances = append(cfg.Instances, instance)
		if err := validateModelSettings(cfg); err != nil {
			return modelFailure(http.StatusBadRequest, "%v", err)
		}
		models, err := a.syncInstanceCatalog(ctx, instance, key)
		if err != nil {
			return err
		}
		if key != "" {
			credential := &authCredential{AccessToken: key, Provider: instance.ProviderKind, AuthMethod: apiKeyAuthMethod}
			if err := a.storeCredential(credentialKey(instance.ID), credential, undo); err != nil {
				return err
			}
		}
		if err := a.saveCatalog(instance, models, undo); err != nil {
			return err
		}
		if err := a.forgetModelChecks(instance.ID, undo); err != nil {
			return err
		}
		adoptDefaultModel(cfg, firstChatModel(instance.ID, models))
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
func uniqueInstanceID(cfg *kernelConfig, base string) string {
	id := base
	for n := 2; cfg.instance(id) != nil; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

// syncInstanceCatalog lists the models instance reaches with secret.
func (a *App) syncInstanceCatalog(ctx context.Context, instance *providerInstance, secret string) ([]catalogModel, error) {
	if _, ok := instanceRuntimeType(instance); !ok {
		return nil, modelFailure(http.StatusBadRequest, "Compa cannot list this provider's models")
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	models, err := listInstanceModels(ctx, a.paths.Kernel, instance, secret)
	if err != nil {
		return nil, modelFailure(http.StatusBadGateway, "%s", clip(redact("the model list could not be read: "+err.Error(), secret), 500))
	}
	if len(models) == 0 {
		return nil, modelFailure(http.StatusBadGateway, "the provider listed no models; check the endpoint and key")
	}
	return models, nil
}

// firstChatModel returns the exact target of the first listed chat model.
func firstChatModel(instanceID string, models []catalogModel) string {
	for _, model := range models {
		target := instanceID + "/" + strings.TrimSpace(model.ID)
		if _, err := parseExactTarget(target); err == nil && servesChat(model.Surfaces) {
			return target
		}
	}
	return ""
}

// instanceSecret resolves an instance's stored key, if it names one.
func (a *App) instanceSecret(instance *providerInstance) (string, error) {
	if ref := strings.TrimSpace(instance.AuthConnectionRef); ref != "" {
		return resolveCredentialRef(a.paths.Kernel, ref)
	}
	return "", nil
}

// syncInstance lists an instance's models again. Targets on models it no
// longer lists leave the shortlist, routes and selections.
func (a *App) syncInstance(ctx context.Context, id string) error {
	return a.changeModelConfig(ctx, func(cfg *kernelConfig, undo *modelUndo) error {
		instance := cfg.instance(id)
		if instance == nil {
			return modelFailure(http.StatusNotFound, "model connection %q not found", id)
		}
		if instance.State != instanceEnabled {
			return modelFailure(http.StatusConflict, "model connection %q is turned off", id)
		}
		secret, err := a.instanceSecret(instance)
		if err != nil {
			return modelFailure(http.StatusConflict, "the stored key is unavailable (%v); remove this connection and add it again", err)
		}
		models, err := a.syncInstanceCatalog(ctx, instance, secret)
		if err != nil {
			return err
		}
		if err := a.saveCatalog(instance, models, undo); err != nil {
			return err
		}
		listed := map[string]bool{}
		for _, model := range models {
			listed[strings.TrimSpace(model.ID)] = true
		}
		dropTargets(cfg, func(target exactTarget) bool { return target.Instance != id || listed[target.Model] })
		return nil
	})
}

// deleteInstance removes a connection with its model list, stored key and
// check results. A connection a route uses stays until the route stops using
// it.
func (a *App) deleteInstance(ctx context.Context, id string) error {
	return a.changeModelConfig(ctx, func(cfg *kernelConfig, undo *modelUndo) error {
		index := slices.IndexFunc(cfg.Instances, func(instance *providerInstance) bool { return instance.ID == id })
		if index < 0 {
			return modelFailure(http.StatusNotFound, "model connection %q not found", id)
		}
		if routes := routesUsing(cfg, id); len(routes) > 0 {
			return modelFailure(http.StatusConflict, "route %s uses this connection; remove it from the route first", strings.Join(routes, ", "))
		}
		removed := cfg.Instances[index]
		cfg.Instances = slices.Delete(cfg.Instances, index, index+1)
		dropTargets(cfg, func(target exactTarget) bool { return target.Instance != id })
		if err := a.keepCatalogs(undo); err != nil {
			return err
		}
		if err := deleteInstanceCatalog(a.paths.Kernel, id); err != nil {
			return fmt.Errorf("delete the model list: %w", err)
		}
		if removed.AuthConnectionRef == "credential:"+credentialKey(id) {
			if err := a.dropCredential(credentialKey(id), undo); err != nil {
				return err
			}
		}
		return a.forgetModelChecks(id, undo)
	})
}

func routesUsing(cfg *kernelConfig, id string) []string {
	var names []string
	for _, route := range cfg.Routes {
		if slices.ContainsFunc(route.Targets, func(raw string) bool {
			target, err := parseExactTarget(raw)
			return err == nil && target.Instance == id
		}) {
			names = append(names, route.Name)
		}
	}
	sort.Strings(names)
	return names
}

// setDefaultModel sets the default selection, an exact chat target or a
// route name; "" clears it.
func (a *App) setDefaultModel(ctx context.Context, selection string) error {
	selection = strings.TrimSpace(selection)
	return a.changeModelConfig(ctx, func(cfg *kernelConfig, _ *modelUndo) error {
		catalogs, err := loadCatalogs(a.paths.Kernel)
		if err != nil {
			return err
		}
		if err := checkSelection(cfg, catalogs, selection); err != nil {
			return modelFailure(http.StatusBadRequest, "%q cannot be the default model: %v", selection, err)
		}
		if _, err := parseExactTarget(selection); err == nil {
			if err := checkChatTargets(cfg, catalogs, []string{selection}); err != nil {
				return modelFailure(http.StatusBadRequest, "%v", err)
			}
		}
		cfg.SetDefaultModel(selection)
		return nil
	})
}

// putRoute creates or replaces a route: ordered chat targets, where the next
// answers when one is busy or fails.
func (a *App) putRoute(ctx context.Context, name string, targets []string) error {
	if len(targets) > 32 {
		return modelFailure(http.StatusBadRequest, "a route holds at most 32 models")
	}
	route := &modelRoute{Name: name, Targets: make([]string, 0, len(targets))}
	for _, target := range targets {
		route.Targets = append(route.Targets, strings.TrimSpace(target))
	}
	return a.changeModelConfig(ctx, func(cfg *kernelConfig, _ *modelUndo) error {
		if index := cfg.routeIndex(name); index >= 0 {
			cfg.Routes[index] = route
		} else {
			cfg.Routes = append(cfg.Routes, route)
		}
		if err := validateModelSettings(cfg); err != nil {
			return modelFailure(http.StatusBadRequest, "%v", err)
		}
		catalogs, err := loadCatalogs(a.paths.Kernel)
		if err != nil {
			return err
		}
		if err := checkChatTargets(cfg, catalogs, route.Targets); err != nil {
			return modelFailure(http.StatusBadRequest, "%v", err)
		}
		return nil
	})
}

// deleteRoute removes a route; a selection naming it is cleared.
func (a *App) deleteRoute(ctx context.Context, name string) error {
	return a.changeModelConfig(ctx, func(cfg *kernelConfig, _ *modelUndo) error {
		index := cfg.routeIndex(name)
		if index < 0 {
			return modelFailure(http.StatusNotFound, "route %q not found", name)
		}
		cfg.Routes = slices.Delete(cfg.Routes, index, index+1)
		cfg.eachSelection(func(selection string) string {
			if strings.TrimSpace(selection) == name {
				return ""
			}
			return selection
		})
		return nil
	})
}

// checkChatTargets reports a target that is not a chat model in an enabled
// connection's model list.
func checkChatTargets(cfg *kernelConfig, catalogs map[string]*catalogEntry, targets []string) error {
	for _, raw := range targets {
		target, err := parseExactTarget(raw)
		if err != nil {
			return fmt.Errorf("target %q: %v", raw, err)
		}
		instance, entry := cfg.instance(target.Instance), catalogs[target.Instance]
		index := -1
		if instance != nil && instance.State == instanceEnabled && validCatalog(target.Instance, entry, instance) {
			index = slices.IndexFunc(entry.Models, func(model catalogModel) bool { return strings.TrimSpace(model.ID) == target.Model })
		}
		if index < 0 {
			return fmt.Errorf("target %q is not in an enabled connection's model list", raw)
		}
		if !servesChat(entry.Models[index].Surfaces) {
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
	// The probe is admitted like a model change: it never overlaps a turn, and
	// the connection it tests cannot be replaced before its result is saved.
	a.modelMu.Lock()
	defer a.modelMu.Unlock()
	release, err := a.admitModelChange(modelFailure(http.StatusConflict, "stop the active turn before checking a model"))
	if err != nil {
		return modelCheck{}, err
	}
	defer release()
	cfg, err := a.loadModelConfig()
	if err != nil {
		return modelCheck{}, err
	}
	instance := cfg.instance(id)
	if instance == nil {
		return modelCheck{}, modelFailure(http.StatusNotFound, "model connection %q not found", id)
	}
	catalogs, err := loadCatalogs(a.paths.Kernel)
	if err != nil {
		return modelCheck{}, err
	}
	if err := checkSelection(cfg, catalogs, id+"/"+model); err != nil {
		return modelCheck{}, modelFailure(http.StatusBadRequest, "%v", err)
	}
	secret, err := a.instanceSecret(instance)
	if err == nil {
		ctx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()
		err = a.probeInstance(ctx, instance, secret, model)
	}
	result := modelCheck{Status: "tested", Message: "The model returned the required tool call. No workspace files were read or changed."}
	if err != nil {
		result = modelCheck{Status: "failed", Message: clip(redact(err.Error(), secret), 500)}
	}
	result.At = time.Now().UTC().Format(time.RFC3339)
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

func (a *App) probeInstance(ctx context.Context, instance *providerInstance, secret, model string) error {
	provider, err := newCoreProvider(a.paths.Kernel, instance)
	if err != nil {
		return err
	}
	credential, err := instanceCredential(ctx, a.paths.Kernel, instance, secret)
	if err != nil {
		return err
	}
	return probeToolCall(ctx, provider, credential, model)
}
