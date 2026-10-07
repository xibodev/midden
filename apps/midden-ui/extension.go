package main

// An extension service is a separate process that serves model providers over
// llmgw-core's extension protocol. These routes connect Midden to one service,
// keep one provider instance per provider it serves, and store the credential
// each provider needs: none, a pasted token, or a sign-in. Secrets are
// write-only: no answer carries the service secret, a token or a sign-in.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	core "github.com/xibodev/llmgw-core"
	"github.com/xibodev/llmgw-core/extension"
	"github.com/xibodev/llmgw-core/oauthflow"
)

const (
	extensionProviderKind = "extension"
	// extensionFlowTTL caps how long a sign-in waits for its owner.
	extensionFlowTTL      = 10 * time.Minute
	extensionCallTimeout  = 15 * time.Second
	extensionSyncTimeout  = 30 * time.Second
	extensionTokenPrefix  = "ext-token-"
	extensionSignInPrefix = "ext-signin-"
	extensionRefPrefix    = "credential:"
)

// extensionSignInMethods are the sign-ins Midden completes: a device code the
// owner approves elsewhere, or a code the owner pastes back. A browser
// redirect would need a callback the provider knows about.
var extensionSignInMethods = []oauthflow.Method{oauthflow.MethodDevice, oauthflow.MethodManual}

var (
	extensionIDUnsafe          = regexp.MustCompile(`[^a-z0-9._-]+`)
	extensionProviderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
)

// extensionProviderView is one service provider Midden keeps an instance for.
type extensionProviderView struct {
	InstanceID    string   `json:"instanceId"`
	Provider      string   `json:"provider"`
	Name          string   `json:"name"`
	Credential    string   `json:"credential"`
	Ready         bool     `json:"ready"`
	SignInMethods []string `json:"signInMethods"`
}

type extensionSignInReply struct {
	FlowID           string `json:"flowId"`
	Method           string `json:"method"`
	UserCode         string `json:"userCode,omitempty"`
	VerificationURI  string `json:"verificationUri,omitempty"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	Interval         int    `json:"interval,omitempty"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
}

// extensionError is a failed extension request and its HTTP status.
type extensionError struct {
	status int
	text   string
}

func (e *extensionError) Error() string { return e.text }

func extensionFail(status int, format string, args ...any) error {
	return &extensionError{status: status, text: fmt.Sprintf(format, args...)}
}

// writeExtensionError answers err with its status; any other error is a
// refused model setting change, such as one during a running turn. Secrets a
// service may have echoed are removed.
func (a *App) writeExtensionError(w http.ResponseWriter, err error, secrets ...string) {
	status := http.StatusConflict
	var failure *extensionError
	var refused modelRequestError
	switch {
	case errors.As(err, &failure):
		status = failure.status
	case errors.As(err, &refused):
		status = refused.status
	}
	apiError(w, status, a.extensionRedact(err.Error(), secrets...))
}

// extensionRedact removes the given secrets and the stored service secret
// from text.
func (a *App) extensionRedact(text string, secrets ...string) string {
	if stored, err := extensionDaemonSecret(a.paths.Kernel); err == nil {
		secrets = append(secrets, stored)
	}
	return redact(text, secrets...)
}

// extensionStoredSecrets returns the secrets stored under keys, for
// redaction.
func (a *App) extensionStoredSecrets(keys ...string) []string {
	var secrets []string
	for _, key := range keys {
		if credential, err := kernelAuth(a.paths.Kernel).get(key); err == nil && credential != nil {
			secrets = append(secrets, credential.AccessToken, credential.RefreshToken, credential.IDToken)
		}
	}
	return secrets
}

func (a *App) respondExtensionState(w http.ResponseWriter, payload map[string]any) {
	state, err := a.modelStateResponse()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	payload["state"] = state
	respond(w, payload)
}

// extensionServiceURL validates a service URL: http(s), with no credentials,
// query or fragment. The URL is never echoed, since it may hold a password.
func extensionServiceURL(raw string) (string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", extensionFail(http.StatusBadRequest, "the extension service URL must be an http(s) URL such as http://127.0.0.1:18888")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", extensionFail(http.StatusBadRequest, "the extension service URL must not carry credentials, a query or a fragment")
	}
	return endpoint, nil
}

// extensionInstanceID is the instance id of a service provider, or "" when
// nothing of its id is usable.
func extensionInstanceID(provider string) string {
	id := strings.Trim(extensionIDUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(provider)), "-"), "-._")
	if id == "" {
		return ""
	}
	return "ext-" + id
}

// extensionSupport returns a service provider's credential kind and the
// sign-in methods Midden completes for it, and whether Midden can use it: it
// must have a usable id, serve a surface llmgw-core defines, declare its
// credential and, when it needs a sign-in, offer one Midden completes.
func extensionSupport(info extension.ProviderInfo) (kind string, methods []string, ok bool) {
	kind, methods = string(info.CredentialKind()), []string{}
	if !extensionProviderIDPattern.MatchString(info.ID) || info.ID == "info" ||
		extensionInstanceID(info.ID) == "" || extensionSurface(info) == "" {
		return kind, methods, false
	}
	switch extension.CredentialKind(kind) {
	case extension.CredentialNone, extension.CredentialToken:
		return kind, methods, true
	case extension.CredentialOAuth:
		for _, method := range info.OAuthMethods {
			if slices.Contains(extensionSignInMethods, method) && !slices.Contains(methods, string(method)) {
				methods = append(methods, string(method))
			}
		}
		return kind, methods, len(methods) > 0
	}
	return kind, methods, false
}

// extensionStoreSecret stores the service secret, or removes it when empty.
func (a *App) extensionStoreSecret(secret string) error {
	store := kernelAuth(a.paths.Kernel)
	if secret == "" {
		return store.remove(extensionDaemonKey)
	}
	return store.set(extensionDaemonKey, &authCredential{AccessToken: secret, Provider: extensionDaemonKey, AuthMethod: "token"})
}

func extensionInstanceIndex(cfg *kernelConfig, id string) int {
	return slices.IndexFunc(cfg.Instances, func(instance *providerInstance) bool { return instance.ID == id })
}

func extensionCredentialKind(instance *providerInstance) string {
	kind, _ := instance.Settings[settingExtensionCredential].(string)
	return kind
}

// extensionInstanceOf returns the index of the extension instance id, which
// must belong to the connected service and need the credential kind.
func extensionInstanceOf(cfg *kernelConfig, id string, kind extension.CredentialKind) (int, error) {
	index := extensionInstanceIndex(cfg, id)
	if index < 0 || cfg.Instances[index].extensionProvider() == "" {
		return -1, extensionFail(http.StatusNotFound, "extension provider %q not found; connect the extension service again", id)
	}
	instance := cfg.Instances[index]
	if cfg.Extension == nil || strings.TrimSpace(instance.Endpoint) != cfg.Extension.URL {
		return -1, extensionFail(http.StatusConflict, "the connected extension service does not serve %q; connect it again", id)
	}
	if extensionCredentialKind(instance) != string(kind) {
		if kind == extension.CredentialToken {
			return -1, extensionFail(http.StatusBadRequest, "this provider does not take a pasted token")
		}
		return -1, extensionFail(http.StatusBadRequest, "this provider does not use sign-in")
	}
	return index, nil
}

// extensionCredentialReady reports whether an extension instance has the
// credential its provider needs.
func (a *App) extensionCredentialReady(instance *providerInstance) bool {
	switch extension.CredentialKind(extensionCredentialKind(instance)) {
	case extension.CredentialNone:
		return true
	case extension.CredentialToken:
		_, err := resolveCredentialRef(a.paths.Kernel, instance.AuthConnectionRef)
		return err == nil
	case extension.CredentialOAuth:
		key := instance.setting(settingExtensionCredentialKey)
		if key == "" {
			return false
		}
		_, err := authTokenStore{kernelAuth(a.paths.Kernel)}.Load(context.Background(), key)
		return err == nil
	}
	return false
}

// extensionSyncCatalog lists an instance's models through the service; secret
// is a pasted token, empty for other credentials.
func (a *App) extensionSyncCatalog(ctx context.Context, instance *providerInstance, secret string) ([]catalogModel, error) {
	ctx, cancel := context.WithTimeout(ctx, extensionSyncTimeout)
	defer cancel()
	models, err := listInstanceModels(ctx, a.paths.Kernel, instance, secret)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, errors.New("the provider listed no models")
	}
	return models, nil
}

func extensionModelSet(models []catalogModel) map[string]bool {
	set := make(map[string]bool, len(models))
	for _, model := range models {
		set[strings.TrimSpace(model.ID)] = true
	}
	return set
}

// extensionAdoptDefault makes the instance's first chat model the default
// model when none is set.
func extensionAdoptDefault(cfg *kernelConfig, instanceID string, models []catalogModel) {
	adoptDefaultModel(cfg, firstChatModel(instanceID, models))
}

// extensionEnable saves an instance's model list and enables it at index,
// dropping selections of models the list no longer holds.
func (a *App) extensionEnable(cfg *kernelConfig, index int, instance *providerInstance, models []catalogModel) error {
	if err := saveInstanceCatalog(a.paths.Kernel, instance, models); err != nil {
		return fmt.Errorf("save the provider's models: %w", err)
	}
	instance.State = instanceEnabled
	cfg.Instances[index] = instance
	available := extensionModelSet(models)
	dropTargets(cfg, func(target exactTarget) bool { return target.Instance != instance.ID || available[target.Model] })
	extensionAdoptDefault(cfg, instance.ID, models)
	return validateModelSettings(cfg)
}

// extensionCredentialKeys are the auth store keys Midden may keep for an
// extension instance.
func extensionCredentialKeys(instanceID string) []string {
	return []string{extensionTokenPrefix + instanceID, extensionSignInPrefix + instanceID}
}

func (a *App) serveExtensionConnect(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL    string  `json:"url"`
		Secret *string `json:"secret"`
	}
	if !decode(w, r, &input) {
		return
	}
	endpoint, err := extensionServiceURL(input.URL)
	if err != nil {
		a.writeExtensionError(w, err)
		return
	}
	// An omitted secret reuses the stored one only for the service it was
	// saved for, so a changed URL never carries it to another server.
	secret := ""
	if input.Secret != nil {
		secret = strings.TrimSpace(*input.Secret)
	} else {
		a.modelMu.Lock()
		cfg, err := a.loadModelConfig()
		a.modelMu.Unlock()
		if err != nil {
			a.writeExtensionError(w, err)
			return
		}
		if cfg.Extension != nil && cfg.Extension.URL == endpoint {
			if secret, err = extensionDaemonSecret(a.paths.Kernel); err != nil {
				a.writeExtensionError(w, err)
				return
			}
		}
	}
	client, err := newExtensionClient(endpoint, secret)
	if err != nil {
		a.writeExtensionError(w, extensionFail(http.StatusBadRequest, "%v", err), secret)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extensionCallTimeout)
	info, err := client.Info(ctx)
	cancel()
	if err != nil {
		a.writeExtensionError(w, extensionFail(http.StatusBadGateway, "could not reach the extension service: %v", err), secret)
		return
	}
	var (
		previous *authCredential
		replaced bool
		stale    []string
		views    []extensionProviderView
	)
	store := kernelAuth(a.paths.Kernel)
	err = a.updateModelConfig(r.Context(), func(cfg *kernelConfig) error {
		var err error
		if previous, err = store.get(extensionDaemonKey); err != nil {
			return fmt.Errorf("read the extension service secret: %w", err)
		}
		// Keyless providers list their models with the stored secret.
		if err := a.extensionStoreSecret(secret); err != nil {
			return fmt.Errorf("save the extension service secret: %w", err)
		}
		replaced = true
		if stale, err = a.extensionReconcile(r.Context(), cfg, endpoint, info); err != nil {
			return err
		}
		views = a.extensionProviderViews(cfg, info)
		return validateModelSettings(cfg)
	})
	if err != nil {
		if replaced {
			_ = store.set(extensionDaemonKey, previous)
		}
		a.writeExtensionError(w, err, secret)
		return
	}
	if len(stale) > 0 {
		if err := store.remove(stale...); err != nil {
			apiError(w, http.StatusInternalServerError, "the extension service is connected, but replaced credentials could not be removed: "+err.Error())
			return
		}
	}
	a.respondExtensionState(w, map[string]any{"providers": views})
}

// extensionReconcile records the service at endpoint and keeps one instance
// per usable provider it serves. A new instance starts disabled until its
// credential is ready; a keyless one is enabled once its models load. An
// instance keeps its state and credential while its provider asks for the
// same kind on the same service; otherwise its credential is dropped and the
// keys returned for removal. A provider the service no longer serves stays
// configured but disabled. Selections of disabled instances, and of models a
// reloaded list lacks, are dropped.
func (a *App) extensionReconcile(ctx context.Context, cfg *kernelConfig, endpoint string, info extension.InfoResponse) ([]string, error) {
	cfg.Extension = &extensionDaemon{URL: endpoint}
	var stale []string
	served := map[string]bool{}
	disabled := map[string]bool{}
	var keyless []*providerInstance
	for _, provider := range info.Providers {
		kind, _, ok := extensionSupport(provider)
		id := extensionInstanceID(provider.ID)
		if !ok || served[id] {
			continue
		}
		index := extensionInstanceIndex(cfg, id)
		if index >= 0 && cfg.Instances[index].extensionProvider() == "" {
			continue // the id belongs to an instance of another kind
		}
		served[id] = true
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			name = provider.ID
		}
		instance := &providerInstance{
			ID: id, ProviderKind: extensionProviderKind, Adapter: adapterExtension,
			Protocol: extensionSurface(provider), Endpoint: endpoint,
			Settings: map[string]any{
				settingExtensionProvider:   provider.ID,
				settingExtensionCredential: kind,
				settingDisplayName:         name,
				settingExtensionSurfaces:   extensionSurfaces(provider),
			},
			State: instanceDisabled,
		}
		if index >= 0 {
			existing := cfg.Instances[index]
			instance.Headers, instance.Runtime = existing.Headers, existing.Runtime
			if extensionCredentialKind(existing) == kind && existing.Endpoint == endpoint && existing.extensionProvider() == provider.ID {
				instance.State = existing.State
				instance.AuthConnectionRef = existing.AuthConnectionRef
				if key, ok := existing.Settings[settingExtensionCredentialKey]; ok {
					instance.Settings[settingExtensionCredentialKey] = key
				}
			} else {
				if existing.State == instanceEnabled {
					disabled[id] = true
				}
				stale = append(stale, extensionCredentialKeys(id)...)
			}
			cfg.Instances[index] = instance
		} else {
			cfg.Instances = append(cfg.Instances, instance)
		}
		if kind == string(extension.CredentialNone) {
			keyless = append(keyless, instance)
		}
	}
	for _, instance := range cfg.Instances {
		if instance.extensionProvider() != "" && !served[instance.ID] && instance.State == instanceEnabled {
			instance.State = instanceDisabled
			disabled[instance.ID] = true
		}
	}
	loaded := map[string]map[string]bool{}
	catalogs := map[string][]catalogModel{}
	for _, instance := range keyless {
		// A model list that does not load leaves the instance as it was.
		models, err := a.extensionSyncCatalog(ctx, instance, "")
		if err != nil {
			continue
		}
		if err := saveInstanceCatalog(a.paths.Kernel, instance, models); err != nil {
			return nil, fmt.Errorf("save the provider's models: %w", err)
		}
		instance.State = instanceEnabled
		delete(disabled, instance.ID)
		loaded[instance.ID], catalogs[instance.ID] = extensionModelSet(models), models
	}
	dropTargets(cfg, func(target exactTarget) bool {
		if disabled[target.Instance] {
			return false
		}
		if available, ok := loaded[target.Instance]; ok {
			return available[target.Model]
		}
		return true
	})
	for _, instance := range keyless {
		if models, ok := catalogs[instance.ID]; ok {
			extensionAdoptDefault(cfg, instance.ID, models)
		}
	}
	return stale, nil
}

// extensionProviderViews lists the providers of info that have an instance.
func (a *App) extensionProviderViews(cfg *kernelConfig, info extension.InfoResponse) []extensionProviderView {
	views := []extensionProviderView{}
	seen := map[string]bool{}
	for _, provider := range info.Providers {
		kind, methods, ok := extensionSupport(provider)
		id := extensionInstanceID(provider.ID)
		if !ok || seen[id] {
			continue
		}
		index := extensionInstanceIndex(cfg, id)
		if index < 0 || cfg.Instances[index].extensionProvider() != provider.ID {
			continue
		}
		seen[id] = true
		instance := cfg.Instances[index]
		views = append(views, extensionProviderView{
			InstanceID: id, Provider: provider.ID, Name: instance.setting(settingDisplayName), Credential: kind,
			Ready: a.extensionCredentialReady(instance), SignInMethods: methods,
		})
	}
	return views
}

func (a *App) serveExtensionDisconnect(w http.ResponseWriter, r *http.Request) {
	var removed []string
	err := a.updateModelConfig(r.Context(), func(cfg *kernelConfig) error {
		removed = nil
		cfg.Extension = nil
		cfg.Instances = slices.DeleteFunc(cfg.Instances, func(instance *providerInstance) bool {
			if instance.extensionProvider() != "" {
				removed = append(removed, instance.ID)
				return true
			}
			return false
		})
		dropTargets(cfg, func(target exactTarget) bool { return !slices.Contains(removed, target.Instance) })
		return validateModelSettings(cfg)
	})
	if err != nil {
		a.writeExtensionError(w, err)
		return
	}
	var problems []string
	for _, id := range removed {
		if err := deleteInstanceCatalog(a.paths.Kernel, id); err != nil {
			problems = append(problems, err.Error())
		}
	}
	keys := []string{extensionDaemonKey}
	for _, id := range removed {
		keys = append(keys, extensionCredentialKeys(id)...)
	}
	if err := kernelAuth(a.paths.Kernel).remove(keys...); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		apiError(w, http.StatusInternalServerError, "the extension service was disconnected, but stored data could not be removed: "+strings.Join(problems, "; "))
		return
	}
	a.respondExtensionState(w, map[string]any{})
}

func (a *App) serveExtensionToken(w http.ResponseWriter, r *http.Request, instanceID string) {
	var input struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &input) {
		return
	}
	token := strings.TrimSpace(input.Token)
	if token == "" {
		apiError(w, http.StatusBadRequest, "token is required")
		return
	}
	key := extensionTokenPrefix + instanceID
	var (
		previous *authCredential
		stored   bool
	)
	store := kernelAuth(a.paths.Kernel)
	err := a.updateModelConfig(r.Context(), func(cfg *kernelConfig) error {
		index, err := extensionInstanceOf(cfg, instanceID, extension.CredentialToken)
		if err != nil {
			return err
		}
		instance := cfg.Instances[index].clone()
		instance.AuthConnectionRef = extensionRefPrefix + key
		delete(instance.Settings, settingExtensionCredentialKey)
		models, err := a.extensionSyncCatalog(r.Context(), instance, token)
		if err != nil {
			return extensionFail(http.StatusBadGateway, "the provider did not accept the token: %v", err)
		}
		if previous, err = store.get(key); err != nil {
			return fmt.Errorf("read stored credentials: %w", err)
		}
		if err := store.set(key, &authCredential{AccessToken: token, Provider: key, AuthMethod: "token"}); err != nil {
			return fmt.Errorf("save the token: %w", err)
		}
		stored = true
		return a.extensionEnable(cfg, index, instance, models)
	})
	if err != nil {
		if stored {
			_ = store.set(key, previous)
		}
		a.writeExtensionError(w, err, token)
		return
	}
	a.respondExtensionState(w, map[string]any{})
}

// extensionSignInDriver is a service provider's sign-in driver whose flows
// last at most extensionFlowTTL and whose addresses are web pages. A refused
// address fails the start, so no flow is kept for it.
type extensionSignInDriver struct{ *extension.OAuthDriver }

func (d extensionSignInDriver) Start(ctx context.Context, request oauthflow.StartRequest) (oauthflow.Authorization, error) {
	authorization, err := d.OAuthDriver.Start(ctx, request)
	if err != nil {
		return authorization, err
	}
	for _, address := range []string{authorization.VerificationURI, authorization.VerificationURIComplete, authorization.AuthorizationURL} {
		if address != "" && !safeSignInAddress(address) {
			return oauthflow.Authorization{}, extensionFail(http.StatusBadGateway, "the extension service returned a sign-in address that isn't a web page")
		}
	}
	if authorization.ExpiresIn <= 0 || authorization.ExpiresIn > extensionFlowTTL {
		authorization.ExpiresIn = extensionFlowTTL
	}
	return authorization, nil
}

// extensionSignIns holds each App's sign-in service; flows live in memory.
var extensionSignIns sync.Map // *App -> *oauthflow.Service

func (a *App) extensionSignInService() (*oauthflow.Service, error) {
	if service, ok := extensionSignIns.Load(a); ok {
		return service.(*oauthflow.Service), nil
	}
	service, err := oauthflow.New(oauthflow.Options{
		Store:         oauthflow.NewMemoryFlowStore(oauthflow.MemoryFlowStoreOptions{MaxFlowsPerCaller: 8}),
		Credentials:   authTokenStore{kernelAuth(a.paths.Kernel)},
		Drivers:       a.extensionDriverFor,
		CredentialKey: a.extensionSignInKey,
	})
	if err != nil {
		return nil, err
	}
	actual, _ := extensionSignIns.LoadOrStore(a, service)
	return actual.(*oauthflow.Service), nil
}

// extensionSignInKey names where a finished sign-in is saved. A sign-in for
// an instance the service no longer has is not saved.
func (a *App) extensionSignInKey(_ context.Context, completion oauthflow.Completion) (string, error) {
	a.modelMu.Lock()
	cfg, err := a.loadModelConfig()
	a.modelMu.Unlock()
	if err != nil {
		return "", err
	}
	if _, err := extensionInstanceOf(cfg, completion.Instance, extension.CredentialOAuth); err != nil {
		return "", err
	}
	return extensionSignInPrefix + completion.Instance, nil
}

// extensionDriverFor returns the sign-in driver of an instance's provider on
// the connected service. It reads the configuration on every flow step.
func (a *App) extensionDriverFor(instanceID string, method oauthflow.Method) (oauthflow.Driver, error) {
	if !slices.Contains(extensionSignInMethods, method) {
		return nil, extensionFail(http.StatusBadRequest, `method must be "device" or "manual"`)
	}
	a.modelMu.Lock()
	cfg, err := a.loadModelConfig()
	a.modelMu.Unlock()
	if err != nil {
		return nil, err
	}
	index, err := extensionInstanceOf(cfg, instanceID, extension.CredentialOAuth)
	if err != nil {
		return nil, err
	}
	instance := cfg.Instances[index]
	secret, err := extensionDaemonSecret(a.paths.Kernel)
	if err != nil {
		return nil, err
	}
	client, err := newExtensionClient(instance.Endpoint, secret)
	if err != nil {
		return nil, err
	}
	return extensionSignInDriver{client.OAuthDriver(instance.extensionProvider())}, nil
}

func (a *App) serveExtensionSignInStart(w http.ResponseWriter, r *http.Request, instanceID string) {
	var input struct {
		Method string `json:"method"`
	}
	if !decode(w, r, &input) {
		return
	}
	method := oauthflow.Method(strings.TrimSpace(input.Method))
	if !slices.Contains(extensionSignInMethods, method) {
		apiError(w, http.StatusBadRequest, `method must be "device" or "manual"`)
		return
	}
	service, err := a.extensionSignInService()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extensionCallTimeout)
	view, err := service.Start(ctx, core.LocalCaller(), instanceID, method)
	cancel()
	if err != nil {
		var failure *extensionError
		if !errors.As(err, &failure) {
			err = extensionFail(http.StatusBadGateway, "could not start the sign-in: %s", strings.TrimPrefix(err.Error(), "oauthflow: "))
		}
		a.writeExtensionError(w, err)
		return
	}
	reply := extensionSignInReply{
		FlowID: view.ID, Method: string(view.Method), UserCode: view.UserCode, VerificationURI: view.VerificationURI,
		AuthorizationURL: view.AuthorizationURL, Interval: view.Interval,
	}
	if !view.ExpiresAt.IsZero() {
		reply.ExpiresAt = view.ExpiresAt.UTC().Format(time.RFC3339)
	}
	respond(w, reply)
}

func (a *App) serveExtensionSignInPoll(w http.ResponseWriter, r *http.Request, flowID string) {
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	service, err := a.extensionSignInService()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extensionCallTimeout)
	view, err := service.Poll(ctx, core.LocalCaller(), flowID)
	cancel()
	a.answerExtensionSignIn(w, r, view, err)
}

func (a *App) serveExtensionSignInComplete(w http.ResponseWriter, r *http.Request, flowID string) {
	var input struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &input) {
		return
	}
	completion, err := extensionSignInInput(input.Code)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	service, err := a.extensionSignInService()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extensionCallTimeout)
	view, err := service.Complete(ctx, core.LocalCaller(), flowID, completion)
	cancel()
	a.answerExtensionSignIn(w, r, view, err)
}

// extensionSignInInput reads what the owner pasted: the code a manual sign-in
// shows, or the whole address the provider sent them to, whose state must
// then match the sign-in's.
func extensionSignInInput(raw string) (oauthflow.CompleteInput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return oauthflow.CompleteInput{}, errors.New("paste the code or the address the provider sent you to")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return oauthflow.CompleteInput{Code: raw}, nil
	}
	query := parsed.Query()
	if query.Get("code") == "" && query.Get("error") == "" && parsed.Fragment != "" {
		if fragment, err := url.ParseQuery(parsed.Fragment); err == nil {
			query = fragment
		}
	}
	input := oauthflow.CompleteInput{Code: query.Get("code"), State: query.Get("state"), Error: query.Get("error")}
	if input.Code == "" && input.Error == "" {
		return oauthflow.CompleteInput{}, errors.New("that address holds no sign-in code")
	}
	return input, nil
}

// answerExtensionSignIn answers a poll or completion. A finished sign-in
// records its credential on the instance, loads its models and enables it.
func (a *App) answerExtensionSignIn(w http.ResponseWriter, r *http.Request, view oauthflow.View, err error) {
	failed := func(message string) {
		respond(w, map[string]any{"status": "failed", "error": a.extensionRedact(message)})
	}
	switch {
	case errors.Is(err, oauthflow.ErrFlowNotFound):
		apiError(w, http.StatusNotFound, "sign-in not found or expired; start it again")
	case errors.Is(err, oauthflow.ErrWrongMethod):
		apiError(w, http.StatusBadRequest, "this sign-in does not offer that step")
	case errors.Is(err, oauthflow.ErrCodeRequired):
		apiError(w, http.StatusBadRequest, "paste the code or the address the provider sent you to")
	case errors.Is(err, oauthflow.ErrStateMismatch):
		apiError(w, http.StatusBadRequest, "that address belongs to a different sign-in")
	case errors.Is(err, oauthflow.ErrSlowDown):
		respond(w, map[string]any{"status": "pending"})
	case errors.Is(err, oauthflow.ErrFlowExpired):
		failed("the sign-in expired; start it again")
	case errors.Is(err, oauthflow.ErrAccessDenied):
		failed("the sign-in was refused")
	case err != nil && view.Status == oauthflow.StatusPending:
		// A failed poll leaves the flow pending; the next poll retries.
		respond(w, map[string]any{"status": "pending", "error": a.extensionRedact(strings.TrimPrefix(err.Error(), "oauthflow: "))})
	case err != nil:
		failed(strings.TrimPrefix(err.Error(), "oauthflow: "))
	case view.Status == oauthflow.StatusPending:
		respond(w, map[string]any{"status": "pending"})
	case view.Status == oauthflow.StatusComplete:
		a.finishExtensionSignIn(w, r, view)
	default:
		failed("the sign-in ended without a credential")
	}
}

// finishExtensionSignIn points the instance at its saved sign-in, then loads
// its models and enables it. A model list that does not load keeps the
// sign-in on a disabled instance, so a later sync can enable it.
func (a *App) finishExtensionSignIn(w http.ResponseWriter, r *http.Request, view oauthflow.View) {
	key := view.CredentialKey
	var loadErr error
	err := a.updateModelConfig(r.Context(), func(cfg *kernelConfig) error {
		loadErr = nil
		index, err := extensionInstanceOf(cfg, view.Instance, extension.CredentialOAuth)
		if err != nil {
			return err
		}
		instance := cfg.Instances[index].clone()
		instance.AuthConnectionRef = ""
		if instance.Settings == nil {
			instance.Settings = map[string]any{}
		}
		instance.Settings[settingExtensionCredentialKey] = key
		cfg.Instances[index] = instance
		models, err := a.extensionSyncCatalog(r.Context(), instance, "")
		if err != nil {
			loadErr = err
			return validateModelSettings(cfg)
		}
		return a.extensionEnable(cfg, index, instance, models)
	})
	secrets := a.extensionStoredSecrets(key)
	if err != nil {
		respond(w, map[string]any{"status": "failed", "error": a.extensionRedact("signed in, but the sign-in could not be saved: "+err.Error(), secrets...)})
		return
	}
	payload := map[string]any{"status": "complete"}
	if loadErr != nil {
		payload["error"] = a.extensionRedact("signed in, but the provider's models could not load: "+loadErr.Error(), secrets...)
	}
	a.respondExtensionState(w, payload)
}

// safeSignInAddress admits only web pages for the sign-in links a person opens:
// https anywhere, or http on this computer, never with user info.
func safeSignInAddress(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return false
}
