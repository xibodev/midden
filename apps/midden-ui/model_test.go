package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// fixtureInstance is a synthetic OpenAI-compatible connection at endpoint.
func fixtureInstance(endpoint string) *providerInstance {
	return &providerInstance{ID: "fixture", ProviderKind: "custom_openai", Adapter: adapterOpenAI,
		Protocol: "openai", Endpoint: endpoint, State: instanceEnabled}
}

// storeTestModel stores fixtureInstance(endpoint), with key when given, and its
// model "fixture" as the default model, as the Models page stores them.
func storeTestModel(t *testing.T, app *App, endpoint, key string) {
	t.Helper()
	instance := fixtureInstance(endpoint)
	if key != "" {
		instance.AuthConnectionRef = "credential:" + credentialKey(instance.ID)
	}
	err := app.changeModelConfig(context.Background(), func(cfg *kernelConfig, undo *modelUndo) error {
		cfg.Instances = slices.DeleteFunc(cfg.Instances, func(existing *providerInstance) bool { return existing.ID == instance.ID })
		cfg.Instances = append(cfg.Instances, instance)
		cfg.SetDefaultModel(instance.ID + "/fixture")
		if key != "" {
			if err := app.storeCredential(credentialKey(instance.ID), &authCredential{AccessToken: key, AuthMethod: apiKeyAuthMethod}, undo); err != nil {
				return err
			}
		}
		return app.saveCatalog(instance, []catalogModel{{ID: "fixture"}}, undo)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

// fakeModelService is a synthetic OpenAI-compatible service listing models.
// Its chat endpoint calls the check tool when toolCalls is set. A request
// without key is rejected with an answer that repeats its credentials.
func fakeModelService(t *testing.T, key string, toolCalls bool, models ...string) *httptest.Server {
	t.Helper()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":{"message":"rejected %s"}}`, r.Header.Get("Authorization"))
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/models":
			data := []map[string]string{}
			for _, id := range models {
				data = append(data, map[string]string{"id": id, "object": "model"})
			}
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
		case r.Method == "POST" && r.URL.Path == "/v1/chat/completions":
			io.Copy(io.Discard, r.Body)
			if toolCalls {
				w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"check","type":"function","function":{"name":"midden_connection_check","arguments":"{\"ok\":true}"}}]},"finish_reason":"tool_calls"}]}`))
			} else {
				w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I cannot use tools."},"finish_reason":"stop"}]}`))
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(service.Close)
	return service
}

// modelReply is a decoded /api/models response.
type modelReply struct {
	code     int
	raw      string
	State    modelState    `json:"state"`
	Instance instanceView  `json:"instance"`
	Outcomes []freeOutcome `json:"outcomes"`
	Status   string        `json:"status"`
	Message  string        `json:"message"`
	Error    string        `json:"error"`
}

func callModels(t *testing.T, app *App, method, path string, body any) modelReply {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:18890"+path, reader)
	r.Header.Set("X-Midden-CSRF", app.csrf)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, keyed(app, r))
	reply := modelReply{code: w.Code, raw: w.Body.String()}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatalf("%s %s: %v: %s", method, path, err, reply.raw)
	}
	return reply
}

func TestModelCredentialStaysInTheKernelsAuthStore(t *testing.T) {
	app := newTestApp(t)
	storeTestModel(t, app, "https://example.invalid/v1", "synthetic-credential-value")
	raw, err := os.ReadFile(kernelConfigPath(app.paths.Kernel))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-credential-value") {
		t.Fatal("credential leaked into nonsecret model settings")
	}
	if secret, err := resolveCredentialRef(app.paths.Kernel, "credential:"+credentialKey("fixture")); err != nil || secret != "synthetic-credential-value" {
		t.Fatal("stored reference not usable", err)
	}
}

func TestConfiguredSourceStoreCannotHoldTheAppData(t *testing.T) {
	opts := testOptions(t)
	opts.SourceEnv = map[string]string{"MIDDEN_CLAUDE_ROOT": opts.Data}
	if _, err := NewApp(opts); err == nil {
		t.Fatal("a session record store was accepted as the App's data folder")
	}
}
