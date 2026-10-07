package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelStateWithoutConfiguration(t *testing.T) {
	app := newTestApp(t)
	reply := callModels(t, app, "GET", "/api/models/state", nil)
	if reply.code != 200 {
		t.Fatalf("state: %d %s", reply.code, reply.raw)
	}
	for _, want := range []string{`"instances":[]`, `"routes":[]`, `"activeModels":[]`, `"defaultModel":""`,
		`"configured":false`, `"setupError":""`, `"extension":{"url":"","connected":false}`} {
		if !strings.Contains(reply.raw, want) {
			t.Fatalf("state lacks %s: %s", want, reply.raw)
		}
	}
	keyed, keyless := false, false
	for _, entry := range reply.State.Roster {
		if entry.ID == "" || entry.Label == "" || entry.Adapter == "" || entry.AuthMethods == nil {
			t.Fatalf("incomplete roster entry: %+v", entry)
		}
		keyed = keyed || entry.RequiresAPIKey
		keyless = keyless || entry.Keyless
	}
	if !keyed || !keyless {
		t.Fatal("the roster did not come from Compa's registry")
	}
	if status := app.Status()["model"].(modelStatus); status != (modelStatus{}) {
		t.Fatalf("unconfigured status: %+v", status)
	}
	if _, err := os.Stat(kernelConfigPath(app.paths.Kernel)); !os.IsNotExist(err) {
		t.Fatal("reading the state wrote a configuration")
	}
	if callModels(t, app, "GET", "/api/models/unknown", nil).code != 404 || callModels(t, app, "POST", "/api/models/state", struct{}{}).code != 405 {
		t.Fatal("unknown routes and methods were not refused")
	}
}

func TestConnectingAProviderKeepsItsKeyInTheAuthStoreOnly(t *testing.T) {
	const key = "synthetic-connection-key"
	service := fakeModelService(t, key, true, "synthetic-chat", "synthetic-other")
	app := newTestApp(t)
	input := map[string]string{"providerKind": "custom_openai", "endpoint": service.URL + "/v1/", "apiKey": key, "label": "Synthetic service"}
	created := callModels(t, app, "POST", "/api/models/instances", input)
	if created.code != 200 {
		t.Fatalf("create: %d %s", created.code, created.raw)
	}
	instance := created.Instance
	if instance.ID != "custom_openai" || instance.Label != "Synthetic service" || instance.Endpoint != service.URL+"/v1" ||
		instance.State != "enabled" || !instance.CredentialReady || instance.Source != "local" || len(instance.Models) != 2 {
		t.Fatalf("unexpected connection: %+v", instance)
	}
	if created.State.DefaultModel != "custom_openai/synthetic-chat" || !created.State.Configured {
		t.Fatalf("the first model did not become the default: %+v", created.State)
	}
	checked := callModels(t, app, "POST", "/api/models/instances/custom_openai/check", map[string]string{"model": "synthetic-other"})
	if checked.code != 200 || checked.Status != "tested" {
		t.Fatalf("check: %d %s", checked.code, checked.raw)
	}
	state := callModels(t, app, "GET", "/api/models/state", nil)
	if result := state.State.Instances[0].Checks["synthetic-other"]; result.Status != "tested" || result.At == "" {
		t.Fatalf("check result not shown: %+v", state.State.Instances[0].Checks)
	}
	synced := callModels(t, app, "POST", "/api/models/instances/custom_openai/sync", struct{}{})
	if synced.code != 200 || len(synced.Instance.Models) != 2 {
		t.Fatalf("sync: %d %s", synced.code, synced.raw)
	}
	second := callModels(t, app, "POST", "/api/models/instances", input)
	if second.code != 200 || second.Instance.ID != "custom_openai-2" || second.State.DefaultModel != "custom_openai/synthetic-chat" {
		t.Fatalf("second connection: %d %s", second.code, second.raw)
	}
	status, _ := json.Marshal(app.Status())
	if !strings.Contains(string(status), `"model":{"configured":true,"defaultModel":"custom_openai/synthetic-chat","summary":"custom_openai/synthetic-chat","setupError":""}`) {
		t.Fatalf("status: %s", status)
	}
	for _, text := range []string{created.raw, checked.raw, state.raw, synced.raw, second.raw, string(status)} {
		if strings.Contains(text, key) {
			t.Fatal("a response repeated the API key")
		}
	}
	for _, path := range []string{kernelConfigPath(app.paths.Kernel), catalogsPath(app.paths.Kernel), app.modelChecksPath(), filepath.Join(app.paths.App, "sessions.json")} {
		raw, err := os.ReadFile(path)
		if (err != nil && !os.IsNotExist(err)) || strings.Contains(string(raw), key) {
			t.Fatalf("%s holds the API key: %v", path, err)
		}
	}
	raw, _ := os.ReadFile(kernelConfigPath(app.paths.Kernel))
	if !strings.Contains(string(raw), `"auth_connection_ref": "credential:midden-custom_openai"`) {
		t.Fatalf("the connection does not reference its stored key: %s", raw)
	}
	stored, err := kernelAuth(app.paths.Kernel).get("midden-custom_openai")
	if err != nil || stored == nil || stored.AccessToken != key || stored.Provider != "custom_openai" || stored.AuthMethod != apiKeyAuthMethod {
		t.Fatal("the API key was not stored for its connection", err)
	}
}

func TestModelCheckRequiresAToolCapableResponse(t *testing.T) {
	service := fakeModelService(t, "", false, "text-only")
	app := newTestApp(t)
	if reply := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": service.URL + "/v1"}); reply.code != 200 {
		t.Fatalf("create: %d %s", reply.code, reply.raw)
	}
	reply := callModels(t, app, "POST", "/api/models/instances/custom_openai/check", map[string]string{"model": "text-only"})
	if reply.code != 200 || reply.Status != "failed" || !strings.Contains(reply.Message, "required tool call") {
		t.Fatalf("text-only model passed the check: %d %s", reply.code, reply.raw)
	}
	state := callModels(t, app, "GET", "/api/models/state", nil)
	if state.State.Instances[0].Checks["text-only"].Status != "failed" {
		t.Fatal("failed check not stored")
	}
	for path, want := range map[string]int{
		"/api/models/instances/custom_openai/check": 400,
		"/api/models/instances/missing/check":       404,
	} {
		if got := callModels(t, app, "POST", path, map[string]string{"model": "not-listed"}).code; got != want {
			t.Fatalf("%s: %d, want %d", path, got, want)
		}
	}
}

func TestProviderErrorsNeverRepeatTheKey(t *testing.T) {
	const key = "synthetic-echoed-key"
	app := newTestApp(t)
	listing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"object":"list","data":[{"id":"synthetic","object":"model"}]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"rejected ` + r.Header.Get("Authorization") + `"}}`))
	}))
	defer listing.Close()
	created := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": listing.URL + "/v1", "apiKey": key})
	if created.code != 200 {
		t.Fatalf("create: %d %s", created.code, created.raw)
	}
	checked := callModels(t, app, "POST", "/api/models/instances/custom_openai/check", map[string]string{"model": "synthetic"})
	if checked.code != 200 || checked.Status != "failed" || strings.Contains(checked.raw, key) {
		t.Fatalf("provider rejection: %d %s", checked.code, checked.raw)
	}
	raw, _ := os.ReadFile(app.modelChecksPath())
	if strings.Contains(string(raw), key) {
		t.Fatal("stored check result repeats the key")
	}
	rejecting := fakeModelService(t, "synthetic-other-key", true, "synthetic")
	refused := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": rejecting.URL + "/v1", "apiKey": key})
	if refused.code != http.StatusBadGateway || strings.Contains(refused.raw, key) {
		t.Fatalf("catalog rejection: %d %s", refused.code, refused.raw)
	}
	if stored, _ := kernelAuth(app.paths.Kernel).get("midden-custom_openai-2"); stored != nil {
		t.Fatal("a refused connection kept its key")
	}
	if len(callModels(t, app, "GET", "/api/models/state", nil).State.Instances) != 1 {
		t.Fatal("a refused connection was saved")
	}
}

func TestRoutesAndDefaultSelection(t *testing.T) {
	service := fakeModelService(t, "", true, "alpha", "beta")
	app := newTestApp(t)
	if reply := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": service.URL + "/v1"}); reply.code != 200 {
		t.Fatalf("create: %d %s", reply.code, reply.raw)
	}
	selection := func(value string) modelReply {
		return callModels(t, app, "PUT", "/api/models/default", map[string]string{"selection": value})
	}
	if reply := selection("custom_openai/missing"); reply.code != 400 {
		t.Fatalf("unknown default accepted: %d %s", reply.code, reply.raw)
	}
	if reply := selection("custom_openai/beta"); reply.code != 200 || reply.State.DefaultModel != "custom_openai/beta" || !reply.State.Configured {
		t.Fatalf("default: %d %s", reply.code, reply.raw)
	}
	for _, bad := range []struct {
		name    string
		targets []string
	}{
		{"main", []string{}},
		{"main", []string{"custom_openai/missing"}},
		{"main", []string{"custom_openai/alpha", "custom_openai/alpha"}},
		{"main", []string{"unknown/alpha"}},
		{"Main", []string{"custom_openai/alpha"}},
	} {
		if reply := callModels(t, app, "PUT", "/api/models/routes/"+bad.name, map[string]any{"targets": bad.targets}); reply.code != 400 {
			t.Fatalf("invalid route %s %v accepted: %d %s", bad.name, bad.targets, reply.code, reply.raw)
		}
	}
	created := callModels(t, app, "PUT", "/api/models/routes/main", map[string]any{"targets": []string{"custom_openai/alpha", "custom_openai/beta"}})
	if created.code != 200 || len(created.State.Routes) != 1 || strings.Join(created.State.Routes[0].Targets, ",") != "custom_openai/alpha,custom_openai/beta" {
		t.Fatalf("route: %d %s", created.code, created.raw)
	}
	updated := callModels(t, app, "PUT", "/api/models/routes/main", map[string]any{"targets": []string{"custom_openai/beta"}})
	if updated.code != 200 || strings.Join(updated.State.Routes[0].Targets, ",") != "custom_openai/beta" {
		t.Fatalf("route update: %d %s", updated.code, updated.raw)
	}
	if reply := selection("main"); reply.code != 200 || !reply.State.Configured {
		t.Fatalf("route as default: %d %s", reply.code, reply.raw)
	}
	if status := app.Status()["model"].(modelStatus); status.Summary != "Route main · 1 model" || !status.Configured {
		t.Fatalf("status: %+v", status)
	}
	if reply := callModels(t, app, "DELETE", "/api/models/instances/custom_openai", nil); reply.code != 409 || !strings.Contains(reply.Error, "main") {
		t.Fatalf("deleted a connection a route uses: %d %s", reply.code, reply.raw)
	}
	deleted := callModels(t, app, "DELETE", "/api/models/routes/main", nil)
	if deleted.code != 200 || len(deleted.State.Routes) != 0 || deleted.State.DefaultModel != "" || deleted.State.Configured {
		t.Fatalf("route delete: %d %s", deleted.code, deleted.raw)
	}
	if reply := callModels(t, app, "DELETE", "/api/models/routes/main", nil); reply.code != 404 {
		t.Fatalf("deleted a missing route: %d", reply.code)
	}
	if reply := callModels(t, app, "GET", "/api/models/routes/main", nil); reply.code != 405 {
		t.Fatalf("route read: %d", reply.code)
	}
	if reply := selection("custom_openai/alpha"); reply.code != 200 {
		t.Fatalf("default: %d %s", reply.code, reply.raw)
	}
	removed := callModels(t, app, "DELETE", "/api/models/instances/custom_openai", nil)
	if removed.code != 200 || len(removed.State.Instances) != 0 || removed.State.DefaultModel != "" {
		t.Fatalf("connection delete: %d %s", removed.code, removed.raw)
	}
}

// fakeFreeRun stands in for compa-kernel model auto-free: it connects the
// first keyless provider as Compa does and prints Compa's report.
func fakeFreeRun(t *testing.T, ids ...string) {
	t.Helper()
	previous := freeRunner
	t.Cleanup(func() { freeRunner = previous })
	freeRunner = func(_ context.Context, _ string, env []string) ([]byte, error) {
		home := ""
		for _, entry := range env {
			if value, ok := strings.CutPrefix(entry, "COMPA_HOME="); ok {
				home = value
			}
		}
		cfg, err := loadKernelConfig(home)
		if err != nil {
			return nil, err
		}
		instance := &providerInstance{ID: ids[0], ProviderKind: ids[0], Adapter: adapterOpenAI, Protocol: "openai",
			Endpoint: "https://free.invalid/v1", State: instanceEnabled}
		cfg.Instances = append(cfg.Instances, instance)
		cfg.ActiveModels = append(cfg.ActiveModels, ids[0]+"/free-chat")
		adoptDefaultModel(cfg, ids[0]+"/free-chat")
		if err := saveInstanceCatalog(home, instance, []catalogModel{{ID: "free-chat"}, {ID: "free-other"}}); err != nil {
			return nil, err
		}
		if err := saveKernelConfig(home, cfg); err != nil {
			return nil, err
		}
		return []byte("Probing the free providers that need no key...\nCatalogs discovered: 3 | Inference verified: 1\n" +
			"- " + ids[0] + ": verified\n" +
			"- " + ids[1] + ": connected (rate_limited) - It is busy right now; try again in a minute.\n" +
			"- synthetic-listed: connected (no_model) - It lists no free chat model Compa adds on its own; you can still add its models by hand.\n" +
			"- synthetic-unknown: failed - Its model list could not be read.\n  + " + ids[0] + "\n"), nil
	}
}

func TestFreeModelsConnectThroughCompa(t *testing.T) {
	app := newTestApp(t)
	var keyless []rosterEntry
	for _, entry := range callModels(t, app, "GET", "/api/models/state", nil).State.Roster {
		if entry.Keyless {
			keyless = append(keyless, entry)
		}
	}
	if len(keyless) < 2 {
		t.Fatal("Compa's roster lists fewer than two free providers")
	}
	if reply := callModels(t, app, "POST", "/api/models/free", struct{}{}); reply.code != http.StatusServiceUnavailable {
		t.Fatalf("free models without a kernel: %d %s", reply.code, reply.raw)
	}
	app.opts.Kernel = "synthetic-kernel"
	fakeFreeRun(t, keyless[0].ID, keyless[1].ID)
	reply := callModels(t, app, "POST", "/api/models/free", struct{}{})
	if reply.code != 200 || len(reply.Outcomes) != 4 {
		t.Fatalf("free: %d %s", reply.code, reply.raw)
	}
	want := []freeOutcome{
		{InstanceID: keyless[0].ID, Label: keyless[0].Label, Status: "answers_text", Models: 2},
		{InstanceID: keyless[1].ID, Label: keyless[1].Label, Status: "busy", Error: "It is busy right now; try again in a minute."},
		{InstanceID: "synthetic-listed", Label: "synthetic-listed", Status: "connected",
			Error: "It lists no free chat model Compa adds on its own; you can still add its models by hand."},
		{InstanceID: "synthetic-unknown", Label: "synthetic-unknown", Status: "failed", Error: "Its model list could not be read."},
	}
	for i := range want {
		if reply.Outcomes[i] != want[i] {
			t.Fatalf("outcome %d: %+v, want %+v", i, reply.Outcomes[i], want[i])
		}
	}
	state := reply.State
	if len(state.Instances) != 1 || state.Instances[0].ID != keyless[0].ID || state.Instances[0].Source != "free" ||
		len(state.Instances[0].Models) != 2 || !state.Instances[0].CredentialReady {
		t.Fatalf("free connection: %+v", state.Instances)
	}
	if state.DefaultModel != keyless[0].ID+"/free-chat" || !state.Configured || strings.Join(state.ActiveModels, ",") != keyless[0].ID+"/free-chat" {
		t.Fatalf("free default: %+v", state)
	}
	freeRunner = func(context.Context, string, []string) ([]byte, error) {
		return []byte("Probing the free providers that need no key...\nError: auto-connect free failed: synthetic refusal\n"), errors.New("exit status 1")
	}
	failed := callModels(t, app, "POST", "/api/models/free", struct{}{})
	if failed.code != http.StatusBadGateway || !strings.Contains(failed.Error, "synthetic refusal") {
		t.Fatalf("a failed check: %d %s", failed.code, failed.raw)
	}
}

func TestFreeResultsReadCompasReport(t *testing.T) {
	results := parseFreeResults("noise\n- llm7: verified\n- kilo_code: connected (rate_limited) - Busy - try later.\n- a: failed\n-  bad line\n- Upper: verified\n")
	want := []freeResult{{ID: "llm7", Status: "verified"}, {ID: "kilo_code", Status: "connected", Class: "rate_limited", Error: "Busy - try later."},
		{ID: "a", Status: "failed"}}
	if len(results) != len(want) {
		t.Fatalf("results = %+v", results)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("result %d = %+v, want %+v", i, results[i], want[i])
		}
	}
}
func TestSyncDropsModelsTheProviderNoLongerLists(t *testing.T) {
	var listed atomic.Value
	listed.Store(`{"object":"list","data":[{"id":"alpha"},{"id":"beta"}]}`)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(listed.Load().(string)))
	}))
	defer service.Close()
	app := newTestApp(t)
	if reply := callModels(t, app, "POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": service.URL + "/v1"}); reply.code != 200 {
		t.Fatalf("create: %d %s", reply.code, reply.raw)
	}
	if reply := callModels(t, app, "PUT", "/api/models/default", map[string]string{"selection": "custom_openai/beta"}); reply.code != 200 {
		t.Fatalf("default: %d %s", reply.code, reply.raw)
	}
	listed.Store(`{"object":"list","data":[{"id":"alpha"}]}`)
	synced := callModels(t, app, "POST", "/api/models/instances/custom_openai/sync", struct{}{})
	if synced.code != 200 || len(synced.Instance.Models) != 1 || synced.Instance.Models[0].ID != "alpha" || synced.State.DefaultModel != "" {
		t.Fatalf("sync: %d %s", synced.code, synced.raw)
	}
	listed.Store(`{"object":"list","data":[]}`)
	if reply := callModels(t, app, "POST", "/api/models/instances/custom_openai/sync", struct{}{}); reply.code != http.StatusBadGateway {
		t.Fatalf("an empty model list replaced the catalog: %d %s", reply.code, reply.raw)
	}
	if reply := callModels(t, app, "POST", "/api/models/instances/missing/sync", struct{}{}); reply.code != 404 {
		t.Fatalf("sync of a missing connection: %d", reply.code)
	}
}

func TestModelChangesAreRefusedDuringATurn(t *testing.T) {
	app := newTestApp(t)
	storeTestModel(t, app, "http://127.0.0.1:9/v1", "")
	app.opts.Kernel = "synthetic-kernel"
	previous := freeRunner
	t.Cleanup(func() { freeRunner = previous })
	freeRunner = func(context.Context, string, []string) ([]byte, error) {
		t.Error("free providers were checked during a turn")
		return nil, nil
	}
	app.mu.Lock()
	app.active = &activeTurn{ID: "synthetic-turn", SessionID: "synthetic", cancel: func() {}}
	app.mu.Unlock()
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/models/free", struct{}{}},
		{"POST", "/api/models/instances", map[string]string{"providerKind": "custom_openai", "endpoint": "http://127.0.0.1:9/v1"}},
		{"POST", "/api/models/instances/fixture/sync", struct{}{}},
		{"POST", "/api/models/instances/fixture/check", map[string]string{"model": "fixture"}},
		{"DELETE", "/api/models/instances/fixture", nil},
		{"PUT", "/api/models/default", map[string]string{"selection": ""}},
		{"PUT", "/api/models/routes/main", map[string]any{"targets": []string{"fixture/fixture"}}},
		{"DELETE", "/api/models/routes/main", nil},
	} {
		if reply := callModels(t, app, request.method, request.path, request.body); reply.code != http.StatusConflict {
			t.Fatalf("%s %s during a turn: %d %s", request.method, request.path, reply.code, reply.raw)
		}
	}
	app.mu.Lock()
	app.active = nil
	app.modelChanging = true
	app.mu.Unlock()
	s, err := app.NewSession("refused")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTurn(s.ID, "Start while settings change."); err == nil || !strings.Contains(err.Error(), "being changed or checked") {
		t.Fatal("a turn started during a model change", err)
	}
	app.mu.Lock()
	app.modelChanging = false
	app.mu.Unlock()
	if state := callModels(t, app, "GET", "/api/models/state", nil).State; state.DefaultModel != "fixture/fixture" || len(state.Instances) != 1 {
		t.Fatalf("refused changes altered the state: %+v", state)
	}
}

func TestModelCheckHoldsTurnsAndChangesUntilItsResultIsSaved(t *testing.T) {
	probing, release := make(chan struct{}), make(chan struct{})
	var started sync.Once
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		started.Do(func() { close(probing) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"check","type":"function","function":{"name":"midden_connection_check","arguments":"{\"ok\":true}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer service.Close()
	app := newTestApp(t)
	storeTestModel(t, app, service.URL, "")
	serve := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:18890"+path, strings.NewReader(body))
		r.Header.Set("X-Midden-CSRF", app.csrf)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, keyed(app, r))
		return w
	}
	checked := make(chan *httptest.ResponseRecorder, 1)
	go func() { checked <- serve("POST", "/api/models/instances/fixture/check", `{"model":"fixture"}`) }()
	<-probing
	s, err := app.NewSession("during a check")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTurn(s.ID, "Start during a check."); err == nil || !strings.Contains(err.Error(), "being changed or checked") {
		t.Fatal("a turn started while a model was being checked", err)
	}
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() { deleted <- serve("DELETE", "/api/models/instances/fixture", "") }()
	select {
	case reply := <-deleted:
		t.Fatalf("the connection changed while it was being checked: %d %s", reply.Code, reply.Body)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if reply := <-checked; reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), `"status":"tested"`) {
		t.Fatalf("check: %d %s", reply.Code, reply.Body)
	}
	if reply := <-deleted; reply.Code != http.StatusOK {
		t.Fatalf("delete after the check: %d %s", reply.Code, reply.Body)
	}
	// A connection re-created under the same name starts unchecked.
	storeTestModel(t, app, service.URL, "")
	if checks := app.loadModelChecks(); len(checks["fixture"]) != 0 {
		t.Fatalf("a re-created connection inherited the earlier check: %+v", checks)
	}
}

func TestTurnFailureDoesNotPersistAnEchoedCredential(t *testing.T) {
	app := newTestApp(t)
	storeTestModel(t, app, "http://127.0.0.1:9/v1", "synthetic-runtime-secret")
	app.runtime = outcomeEngine{process: func(context.Context, string, string, string) (string, error) {
		return "", errors.New("provider said: rejected synthetic-runtime-secret")
	}}
	s, err := app.NewSession("provider failure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTurn(s.ID, "Create a note."); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	outcomes := observedOutcomes(t, app, s.ID)
	if len(outcomes) != 1 || outcomes[0].Status != "failed" || !strings.Contains(outcomes[0].Error, "[redacted]") {
		t.Fatalf("expected a visible, redacted failed outcome: %+v", outcomes)
	}
	raw, err := os.ReadFile(filepath.Join(app.paths.App, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-runtime-secret") {
		t.Fatal("a provider-echoed credential leaked into the conversation history")
	}
}
