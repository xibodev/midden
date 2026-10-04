package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/xibodev/llmgw-core"
	"github.com/xibodev/llmgw-core/extension"
	"github.com/xibodev/llmgw-core/oauthflow"

	"github.com/xibodev/compa/pkg/auth"
	"github.com/xibodev/compa/pkg/config"
	"github.com/xibodev/compa/pkg/modelservice"
)

// Synthetic secrets the fake extension service hands out or expects. None may
// ever reach an answer or config.json.
const (
	fakeServiceSecret = "service-secret-synthetic"
	fakePastedToken   = "pasted-token-synthetic"
	fakeAccessToken   = "access-token-synthetic"
	fakeRefreshToken  = "refresh-token-synthetic"
	fakeDeviceCode    = "device-code-synthetic"
	fakeVerifier      = "verifier-synthetic"
	fakeOAuthState    = "oauth-state-synthetic"
	fakeAuthCode      = "auth-code-synthetic"
	wrongSecret       = "wrong-secret-synthetic"
	rejectedToken     = "rejected-token-synthetic"
)

var fakeSecrets = []string{fakeServiceSecret, fakePastedToken, fakeAccessToken, fakeRefreshToken, fakeDeviceCode, fakeVerifier, wrongSecret, rejectedToken}

// fakeExtensionService serves the extension protocol routes Midden uses. It
// echoes rejected secrets in its errors, as a careless service might.
type fakeExtensionService struct {
	*httptest.Server
	mu        sync.Mutex
	providers []extension.ProviderInfo
	polls     int
}

func newFakeExtensionService(t *testing.T) *fakeExtensionService {
	t.Helper()
	chat := []core.ModelSurface{core.ModelSurfaceChatCompletions}
	fake := &fakeExtensionService{providers: []extension.ProviderInfo{
		{ID: "provider-a", Name: "Provider A", Surfaces: chat, HasOAuth: true, HasRefresh: true, Credential: extension.CredentialOAuth,
			OAuthMethods: []oauthflow.Method{oauthflow.MethodBrowser, oauthflow.MethodDevice, oauthflow.MethodManual}},
		{ID: "provider-b", Name: "Provider B", Surfaces: chat, Credential: extension.CredentialNone},
		{ID: "provider-c", Surfaces: chat, Credential: extension.CredentialToken},
		// Midden cannot use these: a browser-only sign-in, and no surface
		// llmgw-core defines.
		{ID: "provider-d", Surfaces: chat, HasOAuth: true, Credential: extension.CredentialOAuth, OAuthMethods: []oauthflow.Method{oauthflow.MethodBrowser}},
		{ID: "provider-e", Surfaces: []core.ModelSurface{"smell"}, Credential: extension.CredentialNone},
	}}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (f *fakeExtensionService) setProviders(change func([]extension.ProviderInfo) []extension.ProviderInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.providers = change(slices.Clone(f.providers))
}

func (f *fakeExtensionService) provider(id string) (extension.ProviderInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, provider := range f.providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return extension.ProviderInfo{}, false
}

func writeFakeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

// fakeApproval is the device answer that hands out the sign-in record.
func fakeApproval() oauthflow.PollResult {
	result := oauthflow.PollResult{Status: oauthflow.PollApproved}
	result.Record.AccessToken, result.Record.RefreshToken = fakeAccessToken, fakeRefreshToken
	result.Record.TokenType, result.Record.Expiry = "Bearer", time.Now().Add(time.Hour)
	return result
}

func (f *fakeExtensionService) serve(w http.ResponseWriter, r *http.Request) {
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != fakeServiceSecret {
		writeFakeJSON(w, http.StatusUnauthorized, map[string]string{"error": "secret " + got + " is not accepted"})
		return
	}
	route, ok := strings.CutPrefix(r.URL.Path, extension.PathPrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if route == "info" && r.Method == http.MethodGet {
		f.mu.Lock()
		info := extension.InfoResponse{Version: "1.0.0-test", Providers: slices.Clone(f.providers)}
		f.mu.Unlock()
		writeFakeJSON(w, http.StatusOK, info)
		return
	}
	id, step, _ := strings.Cut(route, "/")
	provider, served := f.provider(id)
	if !served {
		writeFakeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider"})
		return
	}
	switch step {
	case "models":
		want := map[extension.CredentialKind]string{extension.CredentialOAuth: fakeAccessToken, extension.CredentialToken: fakePastedToken}[provider.Credential]
		if got := r.Header.Get(extension.HeaderCredentialToken); got != want {
			writeFakeJSON(w, http.StatusUnauthorized, map[string]string{"error": "credential " + got + " is not accepted"})
			return
		}
		writeFakeJSON(w, http.StatusOK, extension.ModelsResponse{Models: []core.ModelInfo{
			{ID: id + "-model-1", Object: "model", OwnedBy: id},
			{ID: id + "-model-2", Object: "model", OwnedBy: id},
		}})
	case "oauth/start":
		var request extension.OAuthStartRequest
		json.NewDecoder(r.Body).Decode(&request)
		switch request.Method {
		case oauthflow.MethodDevice:
			writeFakeJSON(w, http.StatusOK, extension.OAuthStartResponse{Authorization: oauthflow.Authorization{
				UserCode: "WXYZ-2345", VerificationURI: "https://example.test/device", Interval: 20 * time.Millisecond,
				Secrets: oauthflow.Secrets{DeviceCode: fakeDeviceCode},
			}})
		case oauthflow.MethodManual:
			writeFakeJSON(w, http.StatusOK, extension.OAuthStartResponse{Authorization: oauthflow.Authorization{
				AuthorizationURL: "https://example.test/authorize?state=" + fakeOAuthState, ExpiresIn: time.Hour,
				Secrets: oauthflow.Secrets{State: fakeOAuthState, Verifier: fakeVerifier},
			}})
		default:
			writeFakeJSON(w, http.StatusBadRequest, extension.OAuthStartResponse{Error: "unsupported sign-in method"})
		}
	case "oauth/poll":
		var request extension.OAuthPollRequest
		json.NewDecoder(r.Body).Decode(&request)
		if request.Flow.Secrets.DeviceCode != fakeDeviceCode {
			writeFakeJSON(w, http.StatusBadRequest, extension.OAuthPollResponse{Error: "unknown device code"})
			return
		}
		f.mu.Lock()
		f.polls++
		approved := f.polls >= 2
		f.mu.Unlock()
		result := oauthflow.PollResult{Status: oauthflow.PollPending}
		if approved {
			result = fakeApproval()
		}
		writeFakeJSON(w, http.StatusOK, extension.OAuthPollResponse{Result: result})
	case "oauth/exchange":
		var request extension.OAuthExchangeRequest
		json.NewDecoder(r.Body).Decode(&request)
		if request.Code != fakeAuthCode || request.Flow.Secrets.Verifier != fakeVerifier {
			writeFakeJSON(w, http.StatusBadRequest, extension.OAuthExchangeResponse{Error: "the code was not accepted"})
			return
		}
		writeFakeJSON(w, http.StatusOK, extension.OAuthExchangeResponse{Record: fakeApproval().Record})
	default:
		http.NotFound(w, r)
	}
}

func extensionTestApp(t *testing.T) *App {
	t.Helper()
	opts := testOptions(t)
	t.Setenv(config.EnvHome, filepath.Join(opts.State, "kernel"))
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

type extrasReply struct {
	status int
	body   string
	value  map[string]any
}

// callExtras sends one request straight to serveModelExtras and keeps every
// answer body in bodies, when given, for the secret checks.
func callExtras(t *testing.T, app *App, bodies *[]string, method, target string, body any) extrasReply {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	recorder := httptest.NewRecorder()
	if !app.serveModelExtras(recorder, httptest.NewRequest(method, target, reader)) {
		t.Fatalf("%s %s was not served", method, target)
	}
	reply := extrasReply{status: recorder.Code, body: recorder.Body.String()}
	if bodies != nil {
		*bodies = append(*bodies, reply.body)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply.value); err != nil {
		t.Fatalf("%s %s answered %d with invalid JSON %q", method, target, reply.status, reply.body)
	}
	return reply
}

func expectStatus(t *testing.T, reply extrasReply, status int) {
	t.Helper()
	if reply.status != status {
		t.Fatalf("status %d, want %d: %s", reply.status, status, reply.body)
	}
}

func modelConfigOf(t *testing.T, app *App) *config.Config {
	t.Helper()
	app.modelMu.Lock()
	defer app.modelMu.Unlock()
	cfg, err := app.loadModelConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func instanceOf(t *testing.T, cfg *config.Config, id string) *config.ProviderInstanceConfig {
	t.Helper()
	if index := extensionInstanceIndex(cfg, id); index >= 0 {
		return cfg.ProviderInstances[index]
	}
	t.Fatalf("instance %q is missing", id)
	return nil
}

func storedCredential(t *testing.T, key string) *auth.AuthCredential {
	t.Helper()
	credential, err := auth.GetCredential(key)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func catalogModels(t *testing.T, id string) []string {
	t.Helper()
	store, err := modelservice.LoadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if entry := store.Entries[id]; entry != nil {
		for _, model := range entry.Models {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func connectFakeService(t *testing.T, app *App, fake *fakeExtensionService, bodies *[]string) extrasReply {
	t.Helper()
	reply := callExtras(t, app, bodies, http.MethodPut, "/api/models/extension", map[string]any{"url": fake.URL + "/", "secret": fakeServiceSecret})
	expectStatus(t, reply, http.StatusOK)
	return reply
}

func assertNoSecrets(t *testing.T, app *App, bodies []string) {
	t.Helper()
	raw, err := os.ReadFile(app.modelConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range fakeSecrets {
		for _, body := range bodies {
			if strings.Contains(body, secret) {
				t.Fatalf("an answer carries %q: %s", secret, body)
			}
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("config.json carries %q", secret)
		}
	}
}

func TestExtensionServiceConnectsProvidersByCredential(t *testing.T) {
	app := extensionTestApp(t)
	fake := newFakeExtensionService(t)
	var bodies []string

	reply := callExtras(t, app, &bodies, http.MethodPut, "/api/models/extension", map[string]any{"url": fake.URL, "secret": wrongSecret})
	expectStatus(t, reply, http.StatusBadGateway)
	if cfg := modelConfigOf(t, app); cfg.Extension != nil || len(cfg.ProviderInstances) != 0 {
		t.Fatal("a refused connection changed the model configuration")
	}
	if storedCredential(t, auth.ExtensionDaemonKey) != nil {
		t.Fatal("a refused secret was stored")
	}

	reply = connectFakeService(t, app, fake, &bodies)
	if _, ok := reply.value["state"]; !ok {
		t.Fatalf("answer lacks the model state: %s", reply.body)
	}
	var answer struct {
		Providers []extensionProviderView `json:"providers"`
	}
	json.Unmarshal([]byte(reply.body), &answer)
	want := []extensionProviderView{
		{InstanceID: "ext-provider-a", Provider: "provider-a", Name: "Provider A", Credential: "oauth", SignInMethods: []string{"device", "manual"}},
		{InstanceID: "ext-provider-b", Provider: "provider-b", Name: "Provider B", Credential: "none", Ready: true, SignInMethods: []string{}},
		{InstanceID: "ext-provider-c", Provider: "provider-c", Name: "provider-c", Credential: "token", SignInMethods: []string{}},
	}
	if got, _ := json.Marshal(answer.Providers); !bytes.Equal(got, mustJSON(t, want)) {
		t.Fatalf("providers = %s, want %s", got, mustJSON(t, want))
	}

	cfg := modelConfigOf(t, app)
	if cfg.Extension == nil || cfg.Extension.URL != fake.URL || len(cfg.ProviderInstances) != 3 {
		t.Fatalf("service or instances not recorded: %+v", cfg.Extension)
	}
	for id, state := range map[string]config.ProviderInstanceState{"ext-provider-a": "disabled", "ext-provider-b": "enabled", "ext-provider-c": "disabled"} {
		instance := instanceOf(t, cfg, id)
		if instance.State != state || instance.ProviderKind != "extension" || instance.Adapter != config.ProviderAdapterExtension ||
			instance.Protocol != config.ExtensionSurfaceChatCompletions || instance.Endpoint != fake.URL ||
			instance.ExtensionProvider() != strings.TrimPrefix(id, "ext-") {
			t.Fatalf("instance %s = %+v", id, instance)
		}
	}
	if cfg.Agents.Defaults.ModelName != "ext-provider-b/provider-b-model-1" {
		t.Fatalf("default model = %q, want the keyless provider's first model", cfg.Agents.Defaults.ModelName)
	}
	if credential := storedCredential(t, auth.ExtensionDaemonKey); credential == nil || credential.AccessToken != fakeServiceSecret {
		t.Fatal("the service secret was not stored")
	}

	// A pasted token must list the provider's models before it is kept.
	reply = callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-c/token", map[string]any{"token": rejectedToken})
	expectStatus(t, reply, http.StatusBadGateway)
	if storedCredential(t, "ext-token-ext-provider-c") != nil || instanceOf(t, modelConfigOf(t, app), "ext-provider-c").State != "disabled" {
		t.Fatal("a rejected token was kept")
	}
	reply = callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-a/token", map[string]any{"token": fakePastedToken})
	expectStatus(t, reply, http.StatusBadRequest)
	reply = callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-c/token", map[string]any{"token": fakePastedToken})
	expectStatus(t, reply, http.StatusOK)
	instance := instanceOf(t, modelConfigOf(t, app), "ext-provider-c")
	if instance.State != "enabled" || instance.AuthConnectionRef != "credential:ext-token-ext-provider-c" {
		t.Fatalf("token instance = %+v", instance)
	}
	if credential := storedCredential(t, "ext-token-ext-provider-c"); credential == nil || credential.AccessToken != fakePastedToken {
		t.Fatal("the token was not stored under its instance key")
	}
	if got := catalogModels(t, "ext-provider-c"); !slices.Equal(got, []string{"provider-c-model-1", "provider-c-model-2"}) {
		t.Fatalf("token catalog = %v", got)
	}

	// A device sign-in completes through polling.
	reply = callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-a/signin", map[string]any{"method": "device"})
	expectStatus(t, reply, http.StatusOK)
	flowID, _ := reply.value["flowId"].(string)
	if flowID == "" || reply.value["method"] != "device" || reply.value["userCode"] != "WXYZ-2345" || reply.value["verificationUri"] != "https://example.test/device" {
		t.Fatalf("device sign-in = %s", reply.body)
	}
	if expires, err := time.Parse(time.RFC3339, reply.value["expiresAt"].(string)); err != nil || time.Until(expires) > extensionFlowTTL {
		t.Fatalf("expiresAt = %v", reply.value["expiresAt"])
	}
	if callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-a/signin", map[string]any{"method": "browser"}).status != http.StatusBadRequest {
		t.Fatal("a browser sign-in was started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		reply = callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/signin/"+flowID+"/poll", map[string]any{})
		expectStatus(t, reply, http.StatusOK)
		if reply.value["status"] == "complete" {
			break
		}
		if reply.value["status"] != "pending" || time.Now().After(deadline) {
			t.Fatalf("poll = %s", reply.body)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, ok := reply.value["state"]; !ok || reply.value["error"] != nil {
		t.Fatalf("completed poll = %s", reply.body)
	}
	instance = instanceOf(t, modelConfigOf(t, app), "ext-provider-a")
	if instance.State != "enabled" || instance.AuthConnectionRef != "" || instance.Settings[config.ExtensionCredentialKeySetting] != "ext-signin-ext-provider-a" {
		t.Fatalf("signed-in instance = %+v", instance)
	}
	if record, err := auth.DefaultTokenStore().Load(context.Background(), "ext-signin-ext-provider-a"); err != nil || record.AccessToken != fakeAccessToken {
		t.Fatalf("sign-in not saved in the token store: %v", err)
	}
	if got := catalogModels(t, "ext-provider-a"); len(got) != 2 {
		t.Fatalf("signed-in catalog = %v", got)
	}

	assertNoSecrets(t, app, bodies)

	reply = callExtras(t, app, &bodies, http.MethodDelete, "/api/models/extension", nil)
	expectStatus(t, reply, http.StatusOK)
	if _, ok := reply.value["state"]; !ok {
		t.Fatalf("answer lacks the model state: %s", reply.body)
	}
	cfg = modelConfigOf(t, app)
	if cfg.Extension != nil || len(cfg.ProviderInstances) != 0 || cfg.Agents.Defaults.ModelName != "" {
		t.Fatalf("disconnect left %d instances, service %+v, default %q", len(cfg.ProviderInstances), cfg.Extension, cfg.Agents.Defaults.ModelName)
	}
	for _, key := range []string{auth.ExtensionDaemonKey, "ext-token-ext-provider-c", "ext-signin-ext-provider-a"} {
		if storedCredential(t, key) != nil {
			t.Fatalf("disconnect left the credential %q", key)
		}
	}
	for _, id := range []string{"ext-provider-a", "ext-provider-b", "ext-provider-c"} {
		if got := catalogModels(t, id); len(got) != 0 {
			t.Fatalf("disconnect left the catalog of %s", id)
		}
	}
	assertNoSecrets(t, app, bodies)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestExtensionManualSignInAcceptsTheRedirectAddress(t *testing.T) {
	app := extensionTestApp(t)
	fake := newFakeExtensionService(t)
	var bodies []string
	connectFakeService(t, app, fake, &bodies)

	start := func() string {
		reply := callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-a/signin", map[string]any{"method": "manual"})
		expectStatus(t, reply, http.StatusOK)
		if !strings.HasPrefix(reply.value["authorizationUrl"].(string), "https://example.test/authorize?") {
			t.Fatalf("manual sign-in = %s", reply.body)
		}
		// The service asked for an hour; Midden keeps a flow ten minutes.
		if expires, err := time.Parse(time.RFC3339, reply.value["expiresAt"].(string)); err != nil || time.Until(expires) > extensionFlowTTL {
			t.Fatalf("expiresAt = %v", reply.value["expiresAt"])
		}
		return reply.value["flowId"].(string)
	}
	complete := func(flowID, code string) extrasReply {
		return callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/signin/"+flowID+"/complete", map[string]any{"code": code})
	}

	flowID := start()
	expectStatus(t, complete(flowID, "https://example.test/callback?code="+fakeAuthCode+"&state=another-state"), http.StatusBadRequest)
	expectStatus(t, callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/signin/"+flowID+"/poll", map[string]any{}), http.StatusBadRequest)
	reply := complete(flowID, "https://example.test/callback?code="+fakeAuthCode+"&state="+fakeOAuthState)
	expectStatus(t, reply, http.StatusOK)
	if reply.value["status"] != "complete" {
		t.Fatalf("complete = %s", reply.body)
	}
	instance := instanceOf(t, modelConfigOf(t, app), "ext-provider-a")
	if instance.State != "enabled" || instance.Settings[config.ExtensionCredentialKeySetting] != "ext-signin-ext-provider-a" {
		t.Fatalf("signed-in instance = %+v", instance)
	}
	expectStatus(t, complete(flowID, fakeAuthCode), http.StatusNotFound)

	// A refused code ends that sign-in and keeps the earlier one.
	reply = complete(start(), "not-the-code")
	expectStatus(t, reply, http.StatusOK)
	if reply.value["status"] != "failed" || !strings.Contains(reply.value["error"].(string), "not accepted") {
		t.Fatalf("refused code = %s", reply.body)
	}
	if instanceOf(t, modelConfigOf(t, app), "ext-provider-a").State != "enabled" {
		t.Fatal("a refused code disabled the signed-in provider")
	}
	reply = complete(start(), "https://example.test/callback?error=access_denied&state="+fakeOAuthState)
	expectStatus(t, reply, http.StatusOK)
	if reply.value["status"] != "failed" {
		t.Fatalf("denied sign-in = %s", reply.body)
	}
	expectStatus(t, callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/signin/unknown-flow/poll", map[string]any{}), http.StatusNotFound)
	assertNoSecrets(t, app, bodies)
}

func TestExtensionReconnectFollowsTheServiceProviders(t *testing.T) {
	app := extensionTestApp(t)
	fake := newFakeExtensionService(t)
	var bodies []string
	connectFakeService(t, app, fake, &bodies)
	expectStatus(t, callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-c/token", map[string]any{"token": fakePastedToken}), http.StatusOK)
	if err := app.updateModelConfig(func(cfg *config.Config) error {
		cfg.ModelRoutes = append(cfg.ModelRoutes, &config.ModelRouteConfig{Name: "main", Targets: []string{"ext-provider-b/provider-b-model-1", "ext-provider-c/provider-c-model-1"}})
		cfg.ActiveModels = []string{"ext-provider-b/provider-b-model-2", "ext-provider-c/provider-c-model-2"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The service stops serving one provider, and another now needs no token.
	fake.setProviders(func(providers []extension.ProviderInfo) []extension.ProviderInfo {
		providers = slices.DeleteFunc(providers, func(p extension.ProviderInfo) bool { return p.ID == "provider-b" })
		for i := range providers {
			if providers[i].ID == "provider-c" {
				providers[i].Credential = extension.CredentialNone
			}
		}
		return providers
	})
	// Without a secret, the stored one is reused for the same service.
	reply := callExtras(t, app, &bodies, http.MethodPut, "/api/models/extension", map[string]any{"url": fake.URL})
	expectStatus(t, reply, http.StatusOK)
	var answer struct {
		Providers []extensionProviderView `json:"providers"`
	}
	json.Unmarshal([]byte(reply.body), &answer)
	if len(answer.Providers) != 2 || answer.Providers[0].InstanceID != "ext-provider-a" || answer.Providers[1].Credential != "none" {
		t.Fatalf("providers = %s", reply.body)
	}
	cfg := modelConfigOf(t, app)
	if instanceOf(t, cfg, "ext-provider-b").State != "disabled" {
		t.Fatal("a provider the service dropped stayed enabled")
	}
	instance := instanceOf(t, cfg, "ext-provider-c")
	if instance.State != "enabled" || instance.AuthConnectionRef != "" || extensionCredentialKind(instance) != "none" {
		t.Fatalf("provider whose credential changed = %+v", instance)
	}
	if storedCredential(t, "ext-token-ext-provider-c") != nil {
		t.Fatal("the replaced token was kept")
	}
	// The dropped default gives way to the first model of the provider that
	// became keyless; the other selections of the dropped provider go.
	if cfg.Agents.Defaults.ModelName != "ext-provider-c/provider-c-model-1" || !slices.Equal(cfg.ActiveModels, []string{"ext-provider-c/provider-c-model-2"}) ||
		len(cfg.ModelRoutes) != 1 || !slices.Equal(cfg.ModelRoutes[0].Targets, []string{"ext-provider-c/provider-c-model-1"}) {
		t.Fatalf("selections after the provider left: default %q, active %v", cfg.Agents.Defaults.ModelName, cfg.ActiveModels)
	}

	// Another address without a secret never receives the stored one.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("the stored secret was sent to another address")
		}
		writeFakeJSON(w, http.StatusOK, extension.InfoResponse{})
	}))
	defer other.Close()
	reply = callExtras(t, app, &bodies, http.MethodPut, "/api/models/extension", map[string]any{"url": other.URL})
	expectStatus(t, reply, http.StatusOK)
	cfg = modelConfigOf(t, app)
	if cfg.Extension.URL != other.URL || storedCredential(t, auth.ExtensionDaemonKey) != nil {
		t.Fatal("the secret of the previous service was kept for another address")
	}
	for _, instance := range cfg.ProviderInstances {
		if instance.State != "disabled" {
			t.Fatalf("instance %s of the previous service stayed enabled", instance.ID)
		}
	}
	reply = callExtras(t, app, &bodies, http.MethodPost, "/api/models/extension/ext-provider-c/token", map[string]any{"token": fakePastedToken})
	expectStatus(t, reply, http.StatusConflict)
	assertNoSecrets(t, app, bodies)
}

func TestExtensionServiceURLMustBeAPlainHTTPAddress(t *testing.T) {
	app := extensionTestApp(t)
	for _, raw := range []string{"", "127.0.0.1:18888", "ftp://127.0.0.1:18888", "http://user:pw-synthetic@127.0.0.1:18888", "http://127.0.0.1:18888/?pw-synthetic", "http://127.0.0.1:18888/#pw-synthetic", "http://:18888"} {
		reply := callExtras(t, app, nil, http.MethodPut, "/api/models/extension", map[string]any{"url": raw})
		expectStatus(t, reply, http.StatusBadRequest)
		if strings.Contains(reply.body, "pw-synthetic") {
			t.Fatalf("the answer echoes the URL: %s", reply.body)
		}
	}
	expectStatus(t, callExtras(t, app, nil, http.MethodPut, "/api/models/extension", map[string]any{"url": "http://127.0.0.1:18888", "extra": true}), http.StatusBadRequest)
}

func TestExtensionSignInInputReadsCodesAndAddresses(t *testing.T) {
	cases := []struct {
		raw     string
		want    oauthflow.CompleteInput
		wantErr bool
	}{
		{raw: "  code-1  ", want: oauthflow.CompleteInput{Code: "code-1"}},
		{raw: "4/0-code:with?marks", want: oauthflow.CompleteInput{Code: "4/0-code:with?marks"}},
		{raw: "https://example.test/cb?code=code-2&state=s-2", want: oauthflow.CompleteInput{Code: "code-2", State: "s-2"}},
		{raw: "http://127.0.0.1:1455/cb#code=code-3&state=s-3", want: oauthflow.CompleteInput{Code: "code-3", State: "s-3"}},
		{raw: "https://example.test/cb?error=access_denied&state=s-4", want: oauthflow.CompleteInput{State: "s-4", Error: "access_denied"}},
		{raw: "https://example.test/cb?state=s-5", wantErr: true},
		{raw: "   ", wantErr: true},
	}
	for _, tc := range cases {
		got, err := extensionSignInInput(tc.raw)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("extensionSignInInput(%q) = %+v, %v", tc.raw, got, err)
		}
	}
}

func TestModelExtrasServesOnlyItsRoutes(t *testing.T) {
	app := extensionTestApp(t)
	for _, path := range []string{"/api/models/state", "/api/models/instances", "/api/models/localhost", "/api/models/extension/ext-a", "/api/models/extension/ext-a/token/x", "/api/models/extension/signin/flow", "/api/models/extension//token"} {
		if app.serveModelExtras(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, http.NoBody)) {
			t.Errorf("%s was served", path)
		}
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/models/local"},
		{http.MethodGet, "/api/models/extension"},
		{http.MethodPost, "/api/models/extension"},
		{http.MethodGet, "/api/models/extension/ext-a/token"},
		{http.MethodPut, "/api/models/extension/ext-a/signin"},
		{http.MethodGet, "/api/models/extension/signin/flow/poll"},
		{http.MethodDelete, "/api/models/extension/signin/flow/complete"},
	} {
		expectStatus(t, callExtras(t, app, nil, route.method, route.path, nil), http.StatusMethodNotAllowed)
	}
}
