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

	"github.com/xibodev/compa/pkg/auth"
	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
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
func writeExtensionError(w http.ResponseWriter, err error, secrets ...string) {
	status := http.StatusConflict
	var failure *extensionError
	if errors.As(err, &failure) {
		status = failure.status
	}
	apiError(w, status, extensionRedact(err.Error(), secrets...))
}

// extensionRedact removes the given secrets and the stored service secret
// from text.
func extensionRedact(text string, secrets ...string) string {
	if stored, err := extensionDaemonSecret(); err == nil {
		secrets = append(secrets, stored)
	}
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}

// extensionStoredSecrets returns the secrets stored under keys, for
// redaction.
func extensionStoredSecrets(keys ...string) []string {
	var secrets []string
	for _, key := range keys {
		if credential, err := auth.GetCredential(key); err == nil && credential != nil {
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
		extensionInstanceID(info.ID) == "" || modelservice.ExtensionSurface(info) == "" {
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

func extensionDaemonSecret() (string, error) {
	credential, err := auth.GetCredential(auth.ExtensionDaemonKey)
	if err != nil {
		return "", fmt.Errorf("read the extension service secret: %w", err)
	}
	if credential == nil {
		return "", nil
	}
	return credential.AccessToken, nil
}

// extensionStoreSecret stores the service secret, or removes it when empty.
func extensionStoreSecret(secret string) error {
	if secret == "" {
		return auth.DeleteCredential(auth.ExtensionDaemonKey)
	}
	return auth.SetCredential(auth.ExtensionDaemonKey, &auth.AuthCredential{AccessToken: secret, Provider: auth.ExtensionDaemonKey, AuthMethod: "token"})
}

// extensionRestoreCredential puts back what key held before a failed change.
func extensionRestoreCredential(key string, previous *auth.AuthCredential) {
	if previous != nil {
		_ = auth.SetCredential(key, previous)
		return
	}
	_ = auth.DeleteCredential(key)
}

func extensionInstanceIndex(cfg *config.Config, id string) int {
	for i, instance := range cfg.ProviderInstances {
		if instance != nil && instance.ID == id {
			return i
		}
	}
	return -1
}

func extensionClone(instance *config.ProviderInstanceConfig) *config.ProviderInstanceConfig {
	clone := *instance
	clone.Settings = make(map[string]any, len(instance.Settings))
	for key, value := range instance.Settings {
		clone.Settings[key] = value
	}
	return &clone
}

func extensionCredentialKind(instance *config.ProviderInstanceConfig) string {
	kind, _ := instance.Settings[config.ExtensionCredentialSetting].(string)
	return kind
}

// extensionInstanceOf returns the index of the extension instance id, which
// must belong to the connected service and need the credential kind.
func extensionInstanceOf(cfg *config.Config, id string, kind extension.CredentialKind) (int, error) {
	index := extensionInstanceIndex(cfg, id)
	if index < 0 || cfg.ProviderInstances[index].ExtensionProvider() == "" {
		return -1, extensionFail(http.StatusNotFound, "extension provider %q not found; connect the extension service again", id)
	}
	instance := cfg.ProviderInstances[index]
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
func extensionCredentialReady(instance *config.ProviderInstanceConfig) bool {
	switch extension.CredentialKind(extensionCredentialKind(instance)) {
	case extension.CredentialNone:
		return true
	case extension.CredentialToken:
		_, err := modelservice.ResolveCredentialReference(instance.AuthConnectionRef)
		return err == nil
	case extension.CredentialOAuth:
		key, _ := instance.Settings[config.ExtensionCredentialKeySetting].(string)
		if strings.TrimSpace(key) == "" {
			return false
		}
		_, err := auth.DefaultTokenStore().Load(context.Background(), key)
		return err == nil
	}
	return false
}

// extensionSyncCatalog lists an instance's models through the service; secret
// is a pasted token, empty for other credentials.
func extensionSyncCatalog(ctx context.Context, instance *config.ProviderInstanceConfig, secret string) ([]modelservice.CatalogModel, error) {
	input := modelservice.CatalogSyncInputFromInstance(instance)
	input.Secret = secret
	ctx, cancel := context.WithTimeout(ctx, extensionSyncTimeout)
	defer cancel()
	models, err := modelservice.SyncCatalog(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, errors.New("the provider listed no models")
	}
	return models, nil
}

func extensionModelSet(models []modelservice.CatalogModel) map[string]bool {
	set := make(map[string]bool, len(models))
	for _, model := range models {
		set[strings.TrimSpace(model.ID)] = true
	}
	return set
}

// extensionAdoptDefault makes the instance's first chat model the default
// model when none is set.
func extensionAdoptDefault(cfg *config.Config, instanceID string, models []modelservice.CatalogModel) {
	for _, model := range models {
		target := config.ExactModelTarget{InstanceID: instanceID, ModelID: model.ID}.String()
		if _, err := config.ParseExactModelTarget(target); err == nil && modelservice.ServesChat(model.Surfaces) {
			modelservice.AdoptDefaultModel(cfg, target)
			return
		}
	}
}

// extensionValidate refuses a configuration the kernel could not load.
func extensionValidate(cfg *config.Config) error {
	if err := cfg.ValidateProviderInstances(); err != nil {
		return err
	}
	return cfg.ValidateModelSelections()
}

// extensionEnable saves an instance's catalog and enables it at index,
// dropping selections of models the catalog no longer lists.
func extensionEnable(cfg *config.Config, index int, instance *config.ProviderInstanceConfig, models []modelservice.CatalogModel) error {
	if err := modelservice.SaveProviderInstanceCatalog(instance, models); err != nil {
		return fmt.Errorf("save the provider's models: %w", err)
	}
	instance.State = config.ProviderInstanceStateEnabled
	cfg.ProviderInstances[index] = instance
	available := extensionModelSet(models)
	modelservice.DropTargets(cfg, func(target config.ExactModelTarget) bool {
		return target.InstanceID != instance.ID || available[target.ModelID]
	})
	extensionAdoptDefault(cfg, instance.ID, models)
	return extensionValidate(cfg)
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
		writeExtensionError(w, err)
		return
	}
	// An omitted secret reuses the stored one only for the service it was
	// saved for, so a changed URL never carries it to another host.
	secret := ""
	if input.Secret != nil {
		secret = strings.TrimSpace(*input.Secret)
	} else {
		a.modelMu.Lock()
		cfg, err := a.loadModelConfig()
		a.modelMu.Unlock()
		if err != nil {
			writeExtensionError(w, err)
			return
		}
		if cfg.Extension != nil && cfg.Extension.URL == endpoint {
			if secret, err = extensionDaemonSecret(); err != nil {
				writeExtensionError(w, err)
				return
			}
		}
	}
	client, err := modelservice.NewExtensionClient(endpoint, secret)
	if err != nil {
		writeExtensionError(w, extensionFail(http.StatusBadRequest, "%v", err), secret)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extensionCallTimeout)
	info, err := client.Info(ctx)
	cancel()
	if err != nil {
		writeExtensionError(w, extensionFail(http.StatusBadGateway, "could not reach the extension service: %v", err), secret)
		return
	}
	var (
		previous *auth.AuthCredential
		replaced bool
		stale    []string
		views    []extensionProviderView
	)
	err = a.updateModelConfig(func(cfg *config.Config) error {
		var err error
		if previous, err = auth.GetCredential(auth.ExtensionDaemonKey); err != nil {
			return fmt.Errorf("read the extension service secret: %w", err)
		}
		// Keyless providers list their models with the stored secret.
		if err := extensionStoreSecret(secret); err != nil {
			return fmt.Errorf("save the extension service secret: %w", err)
		}
		replaced = true
		if stale, err = extensionReconcile(r.Context(), cfg, endpoint, info); err != nil {
			return err
		}
		views = extensionProviderViews(cfg, info)
		return extensionValidate(cfg)
	})
	if err != nil {
		if replaced {
			extensionRestoreCredential(auth.ExtensionDaemonKey, previous)
		}
		writeExtensionError(w, err, secret)
		return
	}
	if len(stale) > 0 {
		if err := auth.DeleteCredentials(stale...); err != nil {
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
// reloaded catalog lacks, are dropped.
func extensionReconcile(ctx context.Context, cfg *config.Config, endpoint string, info extension.InfoResponse) ([]string, error) {
	cfg.Extension = &config.ExtensionDaemonConfig{URL: endpoint}
	var stale []string
	served := map[string]bool{}
	disabled := map[string]bool{}
	var keyless []*config.ProviderInstanceConfig
	for _, provider := range info.Providers {
		kind, _, ok := extensionSupport(provider)
		id := extensionInstanceID(provider.ID)
		if !ok || served[id] {
			continue
		}
		index := extensionInstanceIndex(cfg, id)
		if index >= 0 && cfg.ProviderInstances[index].ExtensionProvider() == "" {
			continue // the id belongs to an instance of another kind
		}
		served[id] = true
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			name = provider.ID
		}
		instance := &config.ProviderInstanceConfig{
			ID: id, ProviderKind: extensionProviderKind, Adapter: config.ProviderAdapterExtension,
			Protocol: modelservice.ExtensionSurface(provider), Endpoint: endpoint,
			Settings: map[string]any{
				config.ExtensionProviderSetting:    provider.ID,
				config.ExtensionCredentialSetting:  kind,
				config.ExtensionDisplayNameSetting: name,
				config.ExtensionSurfacesSetting:    modelservice.ExtensionSurfaces(provider),
			},
			State: config.ProviderInstanceStateDisabled,
		}
		if index >= 0 {
			existing := cfg.ProviderInstances[index]
			instance.Headers, instance.Runtime = existing.Headers, existing.Runtime
			if extensionCredentialKind(existing) == kind && existing.Endpoint == endpoint && existing.ExtensionProvider() == provider.ID {
				instance.State = existing.State
				instance.AuthConnectionRef = existing.AuthConnectionRef
				if key, ok := existing.Settings[config.ExtensionCredentialKeySetting]; ok {
					instance.Settings[config.ExtensionCredentialKeySetting] = key
				}
			} else {
				if existing.State == config.ProviderInstanceStateEnabled {
					disabled[id] = true
				}
				stale = append(stale, extensionCredentialKeys(id)...)
			}
			cfg.ProviderInstances[index] = instance
		} else {
			cfg.ProviderInstances = append(cfg.ProviderInstances, instance)
		}
		if kind == string(extension.CredentialNone) {
			keyless = append(keyless, instance)
		}
	}
	for _, instance := range cfg.ProviderInstances {
		if instance != nil && instance.ExtensionProvider() != "" && !served[instance.ID] && instance.State == config.ProviderInstanceStateEnabled {
			instance.State = config.ProviderInstanceStateDisabled
			disabled[instance.ID] = true
		}
	}
	loaded := map[string]map[string]bool{}
	catalogs := map[string][]modelservice.CatalogModel{}
	for _, instance := range keyless {
		// A catalog that does not load leaves the instance as it was.
		models, err := extensionSyncCatalog(ctx, instance, "")
		if err != nil {
			continue
		}
		if err := modelservice.SaveProviderInstanceCatalog(instance, models); err != nil {
			return nil, fmt.Errorf("save the provider's models: %w", err)
		}
		instance.State = config.ProviderInstanceStateEnabled
		delete(disabled, instance.ID)
		loaded[instance.ID], catalogs[instance.ID] = extensionModelSet(models), models
	}
	modelservice.DropTargets(cfg, func(target config.ExactModelTarget) bool {
		if disabled[target.InstanceID] {
			return false
		}
		if available, ok := loaded[target.InstanceID]; ok {
			return available[target.ModelID]
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
func extensionProviderViews(cfg *config.Config, info extension.InfoResponse) []extensionProviderView {
	views := []extensionProviderView{}
	seen := map[string]bool{}
	for _, provider := range info.Providers {
		kind, methods, ok := extensionSupport(provider)
		id := extensionInstanceID(provider.ID)
		if !ok || seen[id] {
			continue
		}
		index := extensionInstanceIndex(cfg, id)
		if index < 0 || cfg.ProviderInstances[index].ExtensionProvider() != provider.ID {
			continue
		}
		seen[id] = true
		instance := cfg.ProviderInstances[index]
		name, _ := instance.Settings[config.ExtensionDisplayNameSetting].(string)
		views = append(views, extensionProviderView{
			InstanceID: id, Provider: provider.ID, Name: name, Credential: kind,
			Ready: extensionCredentialReady(instance), SignInMethods: methods,
		})
	}
	return views
}

func (a *App) serveExtensionDisconnect(w http.ResponseWriter, r *http.Request) {
	var removed []string
	err := a.updateModelConfig(func(cfg *config.Config) error {
		removed = nil
		cfg.Extension = nil
		kept := make([]*config.ProviderInstanceConfig, 0, len(cfg.ProviderInstances))
		for _, instance := range cfg.ProviderInstances {
			if instance != nil && instance.ExtensionProvider() != "" {
				removed = append(removed, instance.ID)
				continue
			}
			kept = append(kept, instance)
		}
		cfg.ProviderInstances = kept
		modelservice.DropTargets(cfg, func(target config.ExactModelTarget) bool {
			return !slices.Contains(removed, target.InstanceID)
		})
		return extensionValidate(cfg)
	})
	if err != nil {
		writeExtensionError(w, err)
		return
	}
	var problems []string
	for _, id := range removed {
		if err := modelservice.DeleteProviderInstanceCatalog(id); err != nil {
			problems = append(problems, err.Error())
		}
	}
	keys := []string{auth.ExtensionDaemonKey}
	for _, id := range removed {
		keys = append(keys, extensionCredentialKeys(id)...)
	}
	if err := auth.DeleteCredentials(keys...); err != nil {
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
		previous *auth.AuthCredential
		stored   bool
	)
	err := a.updateModelConfig(func(cfg *config.Config) error {
		index, err := extensionInstanceOf(cfg, instanceID, extension.CredentialToken)
		if err != nil {
			return err
		}
		instance := extensionClone(cfg.ProviderInstances[index])
		instance.AuthConnectionRef = extensionRefPrefix + key
		delete(instance.Settings, config.ExtensionCredentialKeySetting)
		models, err := extensionSyncCatalog(r.Context(), instance, token)
		if err != nil {
			return extensionFail(http.StatusBadGateway, "the provider did not accept the token: %v", err)
		}
		if previous, err = auth.GetCredential(key); err != nil {
			return fmt.Errorf("read stored credentials: %w", err)
		}
		if err := auth.SetCredential(key, &auth.AuthCredential{AccessToken: token, Provider: key, AuthMethod: "token"}); err != nil {
			return fmt.Errorf("save the token: %w", err)
		}
		stored = true
		return extensionEnable(cfg, index, instance, models)
	})
	if err != nil {
		if stored {
			extensionRestoreCredential(key, previous)
		}
		writeExtensionError(w, err, token)
		return
	}
	a.respondExtensionState(w, map[string]any{})
}

// extensionCredentialStore keeps finished sign-ins in the kernel's auth
// store. Instances name their sign-in through their settings, so Resolve
// names none.
type extensionCredentialStore struct{ *auth.TokenStore }

func (extensionCredentialStore) Resolve(context.Context, core.Caller, string) (string, error) {
	return "", core.ErrNoCredential
}

// extensionCappedDriver is a service provider's sign-in driver whose flows
// last at most extensionFlowTTL.
type extensionCappedDriver struct{ *extension.OAuthDriver }

func (d extensionCappedDriver) Start(ctx context.Context, request oauthflow.StartRequest) (oauthflow.Authorization, error) {
	authorization, err := d.OAuthDriver.Start(ctx, request)
	if err == nil && (authorization.ExpiresIn <= 0 || authorization.ExpiresIn > extensionFlowTTL) {
		authorization.ExpiresIn = extensionFlowTTL
	}
	return authorization, err
}

// extensionSignIns holds each App's sign-in service; flows live in memory.
var extensionSignIns sync.Map // *App -> *oauthflow.Service

func (a *App) extensionSignInService() (*oauthflow.Service, error) {
	if service, ok := extensionSignIns.Load(a); ok {
		return service.(*oauthflow.Service), nil
	}
	service, err := oauthflow.New(oauthflow.Options{
		Store:         oauthflow.NewMemoryFlowStore(oauthflow.MemoryFlowStoreOptions{MaxFlowsPerCaller: 8}),
		Credentials:   extensionCredentialStore{auth.DefaultTokenStore()},
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
	instance := cfg.ProviderInstances[index]
	secret, err := extensionDaemonSecret()
	if err != nil {
		return nil, err
	}
	client, err := modelservice.NewExtensionClient(instance.Endpoint, secret)
	if err != nil {
		return nil, err
	}
	return extensionCappedDriver{client.OAuthDriver(instance.ExtensionProvider())}, nil
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
		writeExtensionError(w, err)
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
		respond(w, map[string]any{"status": "failed", "error": extensionRedact(message)})
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
		respond(w, map[string]any{"status": "pending", "error": extensionRedact(strings.TrimPrefix(err.Error(), "oauthflow: "))})
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
// its models and enables it. A catalog that does not load keeps the sign-in
// on a disabled instance, so a later sync can enable it.
func (a *App) finishExtensionSignIn(w http.ResponseWriter, r *http.Request, view oauthflow.View) {
	key := view.CredentialKey
	var loadErr error
	err := a.updateModelConfig(func(cfg *config.Config) error {
		loadErr = nil
		index, err := extensionInstanceOf(cfg, view.Instance, extension.CredentialOAuth)
		if err != nil {
			return err
		}
		instance := extensionClone(cfg.ProviderInstances[index])
		instance.AuthConnectionRef = ""
		instance.Settings[config.ExtensionCredentialKeySetting] = key
		cfg.ProviderInstances[index] = instance
		models, err := extensionSyncCatalog(r.Context(), instance, "")
		if err != nil {
			loadErr = err
			return extensionValidate(cfg)
		}
		return extensionEnable(cfg, index, instance, models)
	})
	secrets := extensionStoredSecrets(key)
	if err != nil {
		respond(w, map[string]any{"status": "failed", "error": extensionRedact("signed in, but the sign-in could not be saved: "+err.Error(), secrets...)})
		return
	}
	payload := map[string]any{"status": "complete"}
	if loadErr != nil {
		payload["error"] = extensionRedact("signed in, but the provider's models could not load: "+loadErr.Error(), secrets...)
	}
	a.respondExtensionState(w, payload)
}
