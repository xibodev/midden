package main

// Stand-in until Compa detects local model servers.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// localProbeTimeout bounds each probe, so detection answers within it.
const localProbeTimeout = 1500 * time.Millisecond

// localModelServer is a model server found on this machine.
type localModelServer struct {
	Kind                string   `json:"kind"`
	Label               string   `json:"label"`
	Endpoint            string   `json:"endpoint"`
	ProviderKind        string   `json:"providerKind"`
	Models              []string `json:"models"`
	ConnectedInstanceID string   `json:"connectedInstanceId,omitempty"`
}

// localServerProbe says where one kind of model server listens and how it
// lists its models: Ollama's native tags, or an OpenAI-compatible list.
type localServerProbe struct {
	kind         string
	label        string
	providerKind string
	endpoint     string // what a provider instance connects to
	listURL      string // what the probe asks for the model list
	ollama       bool
}

func localOpenAIProbe(kind, label, origin string) localServerProbe {
	return localServerProbe{kind: kind, label: label, providerKind: "custom_openai", endpoint: origin + "/v1", listURL: origin + "/v1/models"}
}

// localServerProbes are the servers POST /api/models/local looks for, on the
// loopback interface only. Tests replace it.
var localServerProbes = []localServerProbe{
	{kind: "ollama", label: "Ollama", providerKind: "ollama", endpoint: "http://127.0.0.1:11434", listURL: "http://127.0.0.1:11434/api/tags", ollama: true},
	localOpenAIProbe("lmstudio", "LM Studio", "http://127.0.0.1:1234"),
	localOpenAIProbe("llamacpp", "llama.cpp or LocalAI", "http://127.0.0.1:8080"),
	localOpenAIProbe("vllm", "vLLM", "http://127.0.0.1:8000"),
	localOpenAIProbe("jan", "Jan", "http://127.0.0.1:1337"),
}

func (a *App) serveLocalModelServers(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	servers := detectLocalModelServers(r.Context(), localServerProbes)
	a.modelMu.Lock()
	cfg, err := a.loadModelConfig()
	a.modelMu.Unlock()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range servers {
		servers[i].ConnectedInstanceID = localConnectedInstance(cfg, servers[i].Endpoint)
	}
	respond(w, map[string]any{"servers": servers})
}

// detectLocalModelServers runs every probe at once and returns the servers
// that answered with a model list, in probe order.
func detectLocalModelServers(ctx context.Context, probes []localServerProbe) []localModelServer {
	transport := &http.Transport{}
	if shared, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = shared.Clone()
	}
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	client := &http.Client{
		Transport:     transport,
		Timeout:       localProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer transport.CloseIdleConnections()
	found := make([]*localModelServer, len(probes))
	var wg sync.WaitGroup
	for i, probe := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = probe.detect(ctx, client)
		}()
	}
	wg.Wait()
	servers := []localModelServer{}
	for _, server := range found {
		if server != nil {
			servers = append(servers, *server)
		}
	}
	return servers
}

// detect asks the probe's server for its models, or returns nil when nothing
// that lists models answers there.
func (p localServerProbe) detect(ctx context.Context, client *http.Client) *localModelServer {
	if !localLoopbackURL(p.listURL) || !localLoopbackURL(p.endpoint) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, localProbeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.listURL, nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	var listing struct {
		Models *[]struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&listing); err != nil {
		return nil
	}
	var ids []string
	switch {
	case p.ollama && listing.Models != nil:
		for _, model := range *listing.Models {
			id := model.Name
			if id == "" {
				id = model.Model
			}
			ids = append(ids, id)
		}
	case !p.ollama && listing.Data != nil:
		for _, model := range *listing.Data {
			ids = append(ids, model.ID)
		}
	default:
		return nil
	}
	models := []string{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(models, id) {
			models = append(models, id)
		}
	}
	return &localModelServer{Kind: p.kind, Label: p.label, Endpoint: p.endpoint, ProviderKind: p.providerKind, Models: models}
}

// localLoopbackURL reports whether raw is an http(s) URL whose host is a
// loopback IP address.
func localLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return false
	}
	ip := net.ParseIP(parsed.Hostname())
	return ip != nil && ip.IsLoopback()
}

// localConnectedInstance returns the provider instance that already reaches
// the server at endpoint, preferring an enabled one, or "".
func localConnectedInstance(cfg *kernelConfig, endpoint string) string {
	want := localOrigin(endpoint)
	if want == "" {
		return ""
	}
	match := ""
	for _, instance := range cfg.Instances {
		if strings.EqualFold(instance.Adapter, adapterExtension) || localOrigin(instance.Endpoint) != want {
			continue
		}
		if instance.State == instanceEnabled {
			return instance.ID
		}
		if match == "" {
			match = instance.ID
		}
	}
	return match
}

// localOrigin is the scheme, host and port of raw, with "localhost" read as
// 127.0.0.1, so one local server matches however its address was written.
func localOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		host = "127.0.0.1"
	}
	port := parsed.Port()
	if port == "" {
		switch scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return ""
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
