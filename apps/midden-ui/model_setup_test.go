package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	copilotauth "github.com/xibodev/llm-provider-auth/copilot"
)

func modelSetupRequest(t *testing.T, app *App, path string, input ModelInput) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:18890"+path, strings.NewReader(string(raw)))
	r.Header.Set("X-Midden-CSRF", app.csrf)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestModelCatalogReadsSelectedServiceWithoutSavingCredentials(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-setup-key" {
			t.Error("catalog request did not use the explicitly selected service and credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"synthetic-model"}]}`))
	}))
	defer service.Close()
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	w := modelSetupRequest(t, app, "/api/models", ModelInput{Provider: "openai", Endpoint: service.URL + "/v1", APIKey: "synthetic-setup-key"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"synthetic-model"`) {
		t.Fatalf("catalog not available: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "synthetic-setup-key") {
		t.Fatal("catalog echoed the credential")
	}
	if _, err := os.Stat(filepath.Join(app.opts.State, "model.json")); !os.IsNotExist(err) {
		t.Fatal("discovery silently selected a model")
	}
	if _, err := os.Stat(filepath.Join(app.opts.State, "kernel", "auth.json")); !os.IsNotExist(err) {
		t.Fatal("discovery silently stored an input credential")
	}
}

func TestModelCheckRequiresAToolCapableResponse(t *testing.T) {
	for _, toolCapable := range []bool{false, true} {
		t.Run(map[bool]string{false: "text-only", true: "tool-capable"}[toolCapable], func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Messages []struct{ Role, Content string }
					Tools    []struct {
						Function struct{ Name string }
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if len(input.Tools) != 1 || input.Tools[0].Function.Name != "midden_connection_check" {
					t.Error("connection check did not request its inert probe tool")
				}
				w.Header().Set("Content-Type", "application/json")
				if toolCapable {
					w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"check","type":"function","function":{"name":"midden_connection_check","arguments":"{\"ok\":true}"}}]},"finish_reason":"tool_calls"}]}`))
				} else {
					w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I cannot use tools."},"finish_reason":"stop"}]}`))
				}
			}))
			defer service.Close()
			app, err := NewApp(testOptions(t))
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			w := modelSetupRequest(t, app, "/api/model/check", ModelInput{Provider: "openai", Model: "synthetic", Endpoint: service.URL})
			want := 400
			if toolCapable {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("toolCapable=%v response=%d %s", toolCapable, w.Code, w.Body.String())
			}
		})
	}
}

func TestCopilotConnectionCanBeAuthorizedBeforeChoosingAModel(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	oldStart, oldPoll := startDevice, pollDevice
	t.Cleanup(func() { startDevice = oldStart; pollDevice = oldPoll })
	startDevice = func() (*copilotauth.DeviceCode, error) {
		return &copilotauth.DeviceCode{DeviceCode: "synthetic-device", UserCode: "ABCD-EFGH", VerificationURI: "https://github.com/login/device", ExpiresIn: 900}, nil
	}
	pollDevice = func(string) copilotauth.DevicePollResult {
		return copilotauth.DevicePollResult{Status: "authorized", AccessToken: "synthetic-account-token"}
	}
	flow, err := app.StartLogin("")
	if err != nil {
		t.Fatalf("sign-in was incorrectly gated on knowing a model identifier: %v", err)
	}
	result, err := app.PollLogin(flow.ID)
	if err != nil || result.Status != "success" || app.model.CredentialRef == "" {
		t.Fatal("account connection was not saved", err)
	}
	if app.model.Provider != "github-copilot" || app.model.Model != "" {
		t.Fatal("sign-in invented a model selection")
	}
	if err := app.SetModel(ModelInput{Provider: "github-copilot"}); err == nil {
		t.Fatal("saving a model must still require a real identifier")
	}
}

func TestModelChangeDoesNotReuseCredentialAtAnotherEndpoint(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.SetModel(ModelInput{Provider: "openai", Model: "a", Endpoint: "https://first.invalid/v1", APIKey: "synthetic-private-key"}); err != nil {
		t.Fatal(err)
	}
	if err := app.SetModel(ModelInput{Provider: "openai", Model: "b", Endpoint: "https://second.invalid/v1"}); err != nil {
		t.Fatal(err)
	}
	if app.model.CredentialRef != "" {
		t.Fatal("changing endpoint silently reused a credential from another service")
	}
}

func TestModelCheckDoesNotEchoStoredCredentialsInProviderErrors(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"rejected synthetic-probe-secret"}}`))
	}))
	defer service.Close()
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	input := ModelInput{Provider: "openai", Model: "synthetic", Endpoint: service.URL, APIKey: "synthetic-probe-secret"}
	if err := app.SetModel(input); err != nil {
		t.Fatal(err)
	}
	input.APIKey = ""
	w := modelSetupRequest(t, app, "/api/model/check", input)
	if w.Code != 400 {
		t.Fatal("provider rejection was not surfaced")
	}
	if strings.Contains(w.Body.String(), "synthetic-probe-secret") {
		t.Fatal("upstream error echoed the stored credential")
	}
}

func TestCopilotAuthIsScopedAndDoesNotAssumeGitHubCLIIsCopilot(t *testing.T) {
	oldCache, oldGh := copilotauth.CacheDir, copilotauth.UseGhCLI
	t.Cleanup(func() { copilotauth.CacheDir, copilotauth.UseGhCLI = oldCache, oldGh })
	copilotauth.CacheDir, copilotauth.UseGhCLI = t.TempDir(), true
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if copilotauth.UseGhCLI || copilotauth.CacheDir != filepath.Join(opts.State, "kernel", "copilot") {
		t.Fatal("Copilot auth would use unrelated GitHub CLI login or an unscoped cache")
	}
}

func TestRuntimeFailureDoesNotPersistProviderEchoedCredential(t *testing.T) {
	app, _, _ := kernelApp(t)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"rejected synthetic-runtime-secret"}}`))
	}))
	defer service.Close()
	if err := app.SetModel(ModelInput{Provider: "openai", Model: "synthetic", Endpoint: service.URL, APIKey: "synthetic-runtime-secret"}); err != nil {
		t.Fatal(err)
	}
	s, err := app.NewSession("provider failure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTurn(s.ID, "Create a note."); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	outcomes := observedOutcomes(t, app, s.ID)
	if len(outcomes) != 1 || outcomes[0].Status != "failed" {
		t.Fatal("expected a visible failed outcome")
	}
	raw, err := os.ReadFile(filepath.Join(app.opts.State, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-runtime-secret") {
		t.Fatal("provider-echoed credential leaked into durable conversation history")
	}
}
