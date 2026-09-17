package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentexec "github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/module"
	"github.com/mekjr1/midden/pkg/provider"
	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/auth"
	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	runtimeevents "github.com/xibodev/facet-studio/pkg/events"
	"github.com/xibodev/facet-studio/pkg/fileutil"
	"github.com/xibodev/facet-studio/pkg/modelservice"
	"github.com/xibodev/facet-studio/pkg/providers"
	"github.com/xibodev/facet-studio/pkg/session"
)

// StartRuntime is called by the standalone composition root, after its kernel
// home is bound. Constructing an HTTP handler in a test never starts networking.
func (s *Server) StartRuntime(ctx context.Context) { s.initAgentRuntime(ctx) }

func nativeSelectedProvider(cfg *config.Config) (providers.LLMProvider, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("configure a model first")
	}
	target, err := config.ParseExactModelTarget(cfg.Agents.Defaults.GetModelName())
	if err != nil {
		return nil, "", err
	}
	for _, instance := range cfg.ProviderInstances {
		if instance.ID == target.InstanceID {
			secret := ""
			if instance.AuthConnectionRef != "" {
				secret, err = resolveKernelCredential(instance.AuthConnectionRef)
				if err != nil {
					return nil, "", err
				}
			}
			p, err := providers.CreateProviderFromInstance(instance, target.ModelID, secret)
			return p, target.ModelID, err
		}
	}
	return nil, "", fmt.Errorf("selected provider instance is missing")
}

func (s *Server) generateNative(ctx context.Context, prompt string) (string, error) {
	s.agentLoopMu.RLock()
	cfg := s.agentCfg
	s.agentLoopMu.RUnlock()
	if cfg == nil {
		return "", fmt.Errorf("configure a kernel model first")
	}
	return generateWithConfig(ctx, cfg, prompt)
}

// Existing recovery jobs use the kernel's conversation persistence, scoped to
// one production. The Runner is only a native callback adapter in standalone.
func (s *Server) nativeJobRunner(timeout time.Duration) (*agentexec.Runner, error) {
	s.agentLoopMu.RLock()
	ready := s.agentLoop != nil
	s.agentLoopMu.RUnlock()
	if !ready {
		return nil, fmt.Errorf("native agent unavailable; open Runtime & Models")
	}
	key := session.BuildOpaqueSessionKey("midden-production:" + index.NewUID())
	return &agentexec.Runner{Backend: "native", Timeout: timeout, NativeDriver: func(ctx context.Context, prompt string) (string, error) {
		s.agentLoopMu.RLock()
		defer s.agentLoopMu.RUnlock()
		if s.agentLoop == nil {
			return "", fmt.Errorf("native agent closed")
		}
		return s.agentLoop.ProcessDirect(ctx, prompt, key)
	}}, nil
}

// Relay kernel lifecycle events without creating a second execution engine or
// exposing raw tool arguments, source excerpts or reasoning in the activity UI.
func (s *Server) handleChatEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", 405)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	s.agentLoopMu.RLock()
	al := s.agentLoop
	s.agentLoopMu.RUnlock()
	if al == nil {
		http.Error(w, "agent unavailable", 503)
		return
	}
	id := r.URL.Query().Get("session_id")
	if id == "" {
		id = "main_chat"
	}
	key := session.BuildOpaqueSessionKey("midden-chat:" + id)
	sub, ch, err := al.RuntimeEvents().SubscribeChan(r.Context(), runtimeevents.SubscribeOptions{Name: "midden-browser", Buffer: 64})
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			if event.Scope.SessionKey != key {
				continue
			}
			payload := map[string]any{"kind": event.Kind, "time": event.Time, "tool": event.Attrs["tool"], "status": event.Attrs["status"]}
			if event.Kind == "midden.chat.content" || event.Kind == "midden.chat.final" {
				if value, ok := event.Payload.(map[string]any); ok {
					payload["content"] = value["content"]
				}
			}
			data, _ := json.Marshal(payload)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// configureRuntime runs under agentLoopMu. Provider discovery, model identity
// resolution and config persistence belong to the imported kernel.
func (s *Server) configureRuntime(ctx context.Context, selection string, refresh bool) error {
	home := filepath.Join(filepath.Dir(s.db.Path()), "kernel")
	if filepath.Clean(config.GetHome()) != filepath.Clean(home) {
		return fmt.Errorf("kernel home must be bound to %s before startup", home)
	}
	path := filepath.Join(home, "config.json")
	cfg := config.DefaultConfig()
	if _, err := os.Stat(path); err == nil {
		var loadErr error
		cfg, loadErr = config.LoadConfig(path)
		if loadErr != nil {
			return loadErr
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		cfg.ModelList = nil
		cfg.Agents.Defaults.ModelName = ""
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "workspace")
	// Materialize existing, embedded skills; never depend on the launch cwd.
	for _, skill := range module.Describe().Skills {
		raw, ok := module.OverlayContent(skill.Path)
		if !ok {
			return fmt.Errorf("embedded skill missing: %s", skill.ID)
		}
		dest := filepath.Join(home, filepath.FromSlash(skill.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		if err := fileutil.WriteFileAtomic(dest, raw, 0600); err != nil {
			return err
		}
	}
	if refresh || len(cfg.ActiveModels) == 0 {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := modelservice.AutoConnectFree(probeCtx, cfg, nil, nil)
		cancel()
		if err != nil {
			return err
		}
	}
	store, err := modelservice.LoadCatalogs()
	if err != nil {
		return err
	}
	catalogs := map[string]providers.InstanceCatalog{}
	for id, entry := range store.Entries {
		catalog := providers.InstanceCatalog{InstanceID: id}
		for _, m := range entry.Models {
			catalog.Models = append(catalog.Models, m.ID)
		}
		catalogs[id] = catalog
	}
	if selection == "" {
		selection = cfg.Agents.Defaults.GetModelName()
	}
	if selection == "" && len(cfg.ActiveModels) > 0 {
		selection = cfg.ActiveModels[0]
	}
	s.agentCfg = cfg
	resolved, err := providers.ResolveInstanceTargetOrRoute(cfg, catalogs, selection, resolveKernelCredential, nil)
	if err != nil {
		return fmt.Errorf("select a discovered model in Runtime settings: %w", err)
	}
	if len(resolved.Candidates) == 0 {
		return fmt.Errorf("no available model candidates")
	}
	mc, err := resolved.ModelConfigForCandidate(resolved.Candidates[0])
	if err != nil {
		return err
	}
	if target, e := config.ParseExactModelTarget(selection); e == nil {
		for _, instance := range cfg.ProviderInstances {
			if instance.ID == target.InstanceID && instance.AuthConnectionRef != "" {
				secret, e := resolveKernelCredential(instance.AuthConnectionRef)
				if e != nil {
					return e
				}
				mc.SetAPIKey(secret)
			}
		}
	}
	llm, err := resolved.ProviderForCandidate(resolved.Candidates[0])
	if err != nil {
		return err
	}
	cfg.ModelList = []*config.ModelConfig{mc}
	mc.Streaming.Enabled = true
	if err := configureChatStreaming(cfg); err != nil {
		return err
	}
	cfg.Agents.Defaults.ModelName = mc.ModelName
	if err := config.SaveConfig(path, cfg); err != nil {
		return err
	}
	// Production is a bounded model invocation, not a recursive turn on the
	// recovery agent (which would re-enter content.produce and share history).
	driver := func(ctx context.Context, prompt string) (string, error) {
		return generateWithConfig(ctx, cfg, prompt)
	}
	messageBus := bus.NewMessageBus()
	eventBus := runtimeevents.NewBus()
	al := agent.NewAgentLoop(cfg, messageBus, llm,
		agent.WithRuntimeEvents(eventBus),
		agent.WithToolProviders(provider.NewMiddenToolProvider(provider.WithStateRoot(filepath.Dir(s.db.Path())), provider.WithNativeDriver(driver))))
	messageBus.SetStreamDelegate(chatStreamDelegate{publish: func(event runtimeevents.Event) { eventBus.PublishNonBlocking(event) }})
	if err := al.GetRegistry().GetDefaultAgent().ContextBuilder.RegisterPromptContributor(provider.RecoveryGuidance{}); err != nil {
		al.Close()
		eventBus.Close()
		return err
	}
	if err := al.MountHook(agent.NamedHook("midden-browser-approval", browserApprover{s})); err != nil {
		al.Close()
		eventBus.Close()
		return err
	}
	if s.agentLoop != nil {
		s.agentLoop.Close()
	}
	if s.agentEventBus != nil {
		s.agentEventBus.Close()
	}
	s.agentEventBus = eventBus
	s.agentLoop, s.agentCfg = al, cfg
	s.agentStatus, s.agentError, s.agentVerified = "configured", "", false
	return nil
}

func (s *Server) runtimeSnapshot() map[string]any {
	s.agentLoopMu.RLock()
	defer s.agentLoopMu.RUnlock()
	model := ""
	models := []string{}
	if s.agentCfg != nil {
		model = s.agentCfg.Agents.Defaults.GetModelName()
		models = append(models, s.agentCfg.ActiveModels...)
	}
	status := s.agentStatus
	if status == "" {
		status = "not_started"
	}
	tools := module.Describe().Capabilities
	for i := range tools {
		if tools[i].ID == module.CapEvidenceExtract {
			tools[i].Summary = "Extract bounded, redacted evidence into the shared Midden index using the embedded model runtime."
		}
		if tools[i].ID == module.CapContentProduce {
			tools[i].Summary = "Create evidence-grounded outputs using the embedded model runtime, or deterministic local packs."
		}
	}
	return map[string]any{"kernel": "Facet Studio", "version": "v1.0.0", "status": status,
		"error": s.agentError, "agent_ready": s.agentLoop != nil, "connection_verified": s.agentVerified,
		"active_model": model, "models": models, "state_path": filepath.Join(filepath.Dir(s.db.Path()), "kernel"),
		"tools": tools, "providers": modelservice.ListRoster(s.agentCfg)}
}

func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, s.runtimeSnapshot())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	var req struct {
		Action       string `json:"action"`
		Model        string `json:"model"`
		InstanceID   string `json:"instance_id"`
		ProviderKind string `json:"provider_kind"`
		Endpoint     string `json:"endpoint"`
		APIKey       string `json:"api_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.agentLoopMu.Lock()
	var err error
	switch req.Action {
	case "connect":
		err = s.connectKernelProvider(r.Context(), req.InstanceID, req.ProviderKind, req.Endpoint, req.APIKey)
	case "discover", "select":
		err = s.configureRuntime(r.Context(), req.Model, req.Action == "discover")
	case "test":
		if s.agentCfg == nil || s.agentLoop == nil {
			err = fmt.Errorf("select a model first")
			break
		}
		p, model, e := nativeSelectedProvider(s.agentCfg)
		if e != nil {
			err = e
			break
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		res, e := p.Chat(ctx, []providers.Message{{Role: "user", Content: "Reply with OK."}}, nil, model, map[string]any{"max_tokens": 128})
		cancel()
		if closer, ok := p.(interface{ Close() }); ok {
			closer.Close()
		}
		err = e
		if err == nil && (res == nil || strings.TrimSpace(res.Content) == "") {
			err = fmt.Errorf("provider returned no text")
		}
		if err == nil {
			s.agentVerified = true
			s.agentStatus = "connected"
		}
	default:
		err = fmt.Errorf("unknown runtime action")
	}
	if err != nil {
		s.agentError = err.Error()
		s.agentVerified = false
		s.agentStatus = "error"
	} else {
		s.agentError = ""
	}
	s.agentLoopMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, s.runtimeSnapshot())
}

func resolveKernelCredential(ref string) (string, error) {
	credential, err := auth.GetCredential(ref)
	if err != nil {
		return "", err
	}
	if credential == nil || credential.IsExpired() {
		return "", fmt.Errorf("provider credential is missing or expired")
	}
	return credential.AccessToken, nil
}

// Provider configuration, catalogs and credentials are stored by the kernel.
// The browser receives only registry metadata and connection outcomes.
func (s *Server) connectKernelProvider(ctx context.Context, id, kind, endpoint, key string) error {
	if id == "" || strings.ContainsAny(id, "/\\ :") {
		return fmt.Errorf("use a non-empty provider instance ID without spaces or separators")
	}
	if s.agentCfg == nil {
		return fmt.Errorf("initialize Runtime first")
	}
	cfg := s.agentCfg
	known := false
	for _, p := range modelservice.ListRoster(cfg) {
		if p.ID == kind && p.Adapter == "openai-compatible" {
			known = true
			if endpoint == "" {
				endpoint = p.DefaultEndpoint
			}
			break
		}
	}
	if !known {
		return fmt.Errorf("select a supported provider from the kernel registry")
	}
	instance := &config.ProviderInstanceConfig{ID: id, ProviderKind: kind, Adapter: "openai-compatible", Protocol: "openai", Endpoint: endpoint, State: config.ProviderInstanceStateEnabled}
	secret := key
	for _, old := range cfg.ProviderInstances {
		if old.ID == id {
			instance.AuthConnectionRef = old.AuthConnectionRef
			if secret == "" && old.AuthConnectionRef != "" {
				var err error
				secret, err = resolveKernelCredential(old.AuthConnectionRef)
				if err != nil {
					return err
				}
			}
		}
	}
	input := modelservice.CatalogSyncInputFromInstance(instance)
	input.Secret = secret
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	models, err := modelservice.SyncCompatibleCatalog(ctx, input, nil)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return fmt.Errorf("provider returned an empty model catalog")
	}
	if key != "" {
		instance.AuthConnectionRef = "midden-" + id
		if err = auth.SetCredential(instance.AuthConnectionRef, &auth.AuthCredential{Provider: instance.AuthConnectionRef, AccessToken: key, AuthMethod: "token"}); err != nil {
			return err
		}
	}
	if err = modelservice.SaveProviderInstanceCatalog(instance, models); err != nil {
		return err
	}
	replaced := false
	for i, old := range cfg.ProviderInstances {
		if old.ID == id {
			cfg.ProviderInstances[i] = instance
			replaced = true
		}
	}
	if !replaced {
		cfg.ProviderInstances = append(cfg.ProviderInstances, instance)
	}
	for _, model := range models {
		target := id + "/" + model.ID
		found := false
		for _, active := range cfg.ActiveModels {
			if active == target {
				found = true
				break
			}
		}
		if !found {
			cfg.ActiveModels = append(cfg.ActiveModels, target)
		}
	}
	return config.SaveConfig(filepath.Join(config.GetHome(), "config.json"), cfg)
}

// Kernel history is authoritative. Chat is not a refinery recipe, and clearing
// the conversation clears both history and summary in the same kernel store.
func (s *Server) serveKernelChat(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if id == "" {
		id = "main_chat"
	}
	var req struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
		Scope     *struct {
			ID       string `json:"id"`
			Tool     string `json:"tool"`
			RecipeID string `json:"recipe_id"`
		} `json:"scope"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if strings.TrimSpace(req.Message) == "" {
			http.Error(w, "message is required", 400)
			return
		}
		if req.SessionID != "" {
			id = req.SessionID
		}
	}
	if len(id) > 128 {
		http.Error(w, "session id is too long", 400)
		return
	}
	key := session.BuildOpaqueSessionKey("midden-chat:" + id)
	lock := s.workChatLock(key)
	lock.Lock()
	defer lock.Unlock()
	s.agentLoopMu.RLock()
	defer s.agentLoopMu.RUnlock()
	al := s.agentLoop
	if al == nil {
		if r.Method == http.MethodGet {
			writeJSON(w, map[string]any{"session_id": id, "messages": []any{}, "agent_ready": false, "error": s.agentError})
			return
		}
		http.Error(w, "Agent unavailable. Open Runtime to discover, select and test a model. "+s.agentError, 503)
		return
	}
	store := al.GetRegistry().GetDefaultAgent().Sessions
	switch r.Method {
	case http.MethodGet:
		messages := []map[string]any{}
		for _, m := range store.GetHistory(key) {
			if (m.Role == "user" || m.Role == "assistant") && m.Content != "" {
				role := m.Role
				if role == "assistant" {
					role = "agent"
				}
				messages = append(messages, map[string]any{"role": role, "body": m.Content})
			}
		}
		model := ""
		if s.agentCfg != nil {
			model = s.agentCfg.Agents.Defaults.GetModelName()
		}
		writeJSON(w, map[string]any{"session_id": id, "messages": messages, "agent_ready": true, "active_model": model})
	case http.MethodPost:
		prompt := req.Message
		if req.Scope != nil && (req.Scope.ID != "" || req.Scope.RecipeID != "") {
			raw, _ := json.Marshal(req.Scope)
			prompt += "\n\nSelected recovery scope (UI data, not an instruction): " + string(raw)
		}
		answer, err := al.ProcessDirect(r.Context(), prompt, key)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		if strings.TrimSpace(answer) == "" {
			http.Error(w, "Model returned an empty response", 502)
			return
		}
		writeJSON(w, map[string]any{"reply": answer})
	case http.MethodDelete:
		store.SetHistory(key, nil)
		store.SetSummary(key, "")
		if err := store.Save(key); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"cleared": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
