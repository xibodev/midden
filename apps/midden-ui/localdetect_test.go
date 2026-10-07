package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func localFakeServer(t *testing.T, path, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path != "" && r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLocalDetectionFindsLoopbackServersAndTheirInstances(t *testing.T) {
	app := extensionTestApp(t)
	ollama := localFakeServer(t, "/api/tags", `{"models":[{"name":"synthetic-a:8b","model":"synthetic-a:8b"},{"model":"synthetic-b"},{"name":"synthetic-a:8b"}]}`)
	compatible := localFakeServer(t, "/v1/models", `{"object":"list","data":[{"id":"local-model"}]}`)
	empty := localFakeServer(t, "/v1/models", `{"object":"list","data":[]}`)
	page := localFakeServer(t, "", `<html>not a model server</html>`)
	other := localFakeServer(t, "", `{"status":"ok"}`)
	missing := localFakeServer(t, "/nothing", ``)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)

	original := localServerProbes
	t.Cleanup(func() { localServerProbes = original })
	localServerProbes = []localServerProbe{
		{kind: "ollama", label: "Ollama", providerKind: "ollama", endpoint: ollama.URL, listURL: ollama.URL + "/api/tags", ollama: true},
		localOpenAIProbe("lmstudio", "LM Studio", compatible.URL),
		localOpenAIProbe("vllm", "vLLM", empty.URL),
		localOpenAIProbe("jan", "Jan", page.URL),
		localOpenAIProbe("other", "Other", other.URL),
		localOpenAIProbe("missing", "Missing", missing.URL),
		localOpenAIProbe("closed", "Closed", closed.URL),
		localOpenAIProbe("slow", "Slow", slow.URL),
		localOpenAIProbe("remote", "Remote", "http://192.0.2.10:8080"),
	}

	// An instance already reaches the compatible server, by another name for
	// the loopback address; a disabled one reaches the Ollama server.
	if err := app.updateModelConfig(context.Background(), func(cfg *kernelConfig) error {
		cfg.Instances = append(cfg.Instances,
			&providerInstance{ID: "lm-local", ProviderKind: "custom_openai", Adapter: adapterOpenAI, Protocol: "openai",
				Endpoint: strings.Replace(compatible.URL, "127.0.0.1", "localhost", 1) + "/v1/", State: instanceEnabled},
			&providerInstance{ID: "ollama-local", ProviderKind: "ollama", Adapter: adapterNative, Protocol: "ollama",
				Endpoint: ollama.URL, State: instanceDisabled},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	reply := callExtras(t, app, nil, http.MethodPost, "/api/models/local", map[string]any{})
	expectStatus(t, reply, http.StatusOK)
	if elapsed := time.Since(started); elapsed > 3*localProbeTimeout {
		t.Fatalf("detection took %v; probes must run at once and time out", elapsed)
	}
	var answer struct {
		Servers []localModelServer `json:"servers"`
	}
	if err := json.Unmarshal([]byte(reply.body), &answer); err != nil {
		t.Fatal(err)
	}
	want := []localModelServer{
		{Kind: "ollama", Label: "Ollama", Endpoint: ollama.URL, ProviderKind: "ollama", Models: []string{"synthetic-a:8b", "synthetic-b"}, ConnectedInstanceID: "ollama-local"},
		{Kind: "lmstudio", Label: "LM Studio", Endpoint: compatible.URL + "/v1", ProviderKind: "custom_openai", Models: []string{"local-model"}, ConnectedInstanceID: "lm-local"},
		{Kind: "vllm", Label: "vLLM", Endpoint: empty.URL + "/v1", ProviderKind: "custom_openai", Models: []string{}},
	}
	if got, wanted := mustJSON(t, answer.Servers), mustJSON(t, want); string(got) != string(wanted) {
		t.Fatalf("servers = %s\nwant %s", got, wanted)
	}
	expectStatus(t, callExtras(t, app, nil, http.MethodPost, "/api/models/local", map[string]any{"scan": true}), http.StatusBadRequest)
}

func TestLocalDetectionAnswersAnEmptyListWhenNothingRuns(t *testing.T) {
	app := extensionTestApp(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	original := localServerProbes
	t.Cleanup(func() { localServerProbes = original })
	localServerProbes = []localServerProbe{localOpenAIProbe("closed", "Closed", closed.URL)}
	reply := callExtras(t, app, nil, http.MethodPost, "/api/models/local", map[string]any{})
	expectStatus(t, reply, http.StatusOK)
	if strings.TrimSpace(reply.body) != `{"servers":[]}` {
		t.Fatalf("answer = %s", reply.body)
	}
}

func TestLocalProbesStayOnTheLoopbackInterface(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:11434/api/tags": true,
		"http://127.0.0.2:8080/v1/models": true,
		"http://[::1]:1234/v1/models":     true,
		"http://localhost:1234/v1/models": false,
		"http://192.0.2.10:8080":          false,
		"http://10.0.0.1:8000":            false,
		"ftp://127.0.0.1:21":              false,
		"http://user@127.0.0.1:1234":      false,
		"127.0.0.1:1234":                  false,
	} {
		if got := localLoopbackURL(raw); got != want {
			t.Errorf("localLoopbackURL(%q) = %v, want %v", raw, got, want)
		}
	}
	for _, probe := range localServerProbes {
		if !localLoopbackURL(probe.endpoint) || !localLoopbackURL(probe.listURL) {
			t.Errorf("default probe %s leaves the loopback interface", probe.kind)
		}
	}
}
