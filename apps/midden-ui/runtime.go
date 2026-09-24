package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	"github.com/xibodev/facet-studio/pkg/media"
	"github.com/xibodev/facet-studio/pkg/session"
	"github.com/xibodev/facet-studio/pkg/tools"
)

type kernelHost struct {
	loop *agent.AgentLoop
	app  *App
}
type browserChannel struct {
	Streaming config.StreamingConfig `json:"streaming"`
}

func init() { config.RegisterChannelSettings("midden-ui", browserChannel{}) }

func newKernel(app *App) (engine, error) {
	if app.projected == "" {
		return nil, fmt.Errorf("install the canonical Midden bundle before running an agent")
	}
	if err := os.Setenv(config.EnvHome, filepath.Join(app.opts.State, "kernel")); err != nil {
		return nil, err
	}
	if err := os.Setenv(config.EnvBuiltinSkills, app.projected); err != nil {
		return nil, err
	}
	if err := os.Setenv("MIDDEN_HOME", filepath.Join(app.opts.State, "core")); err != nil {
		return nil, err
	}
	provider, model, err := app.providerConfig()
	if err != nil {
		return nil, err
	}
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{model}
	kernelWorkspace := filepath.Join(app.opts.State, "kernel-workspace")
	cfg.Agents.Defaults.Workspace = kernelWorkspace
	cfg.Agents.Defaults.ModelName = model.ModelName
	cfg.Agents.Defaults.MaxToolIterations = 32
	cfg.Agents.Defaults.MaxTokens = 8192
	cfg.Agents.Defaults.ContextWindow = 128000
	cfg.Agents.Defaults.MaxLLMRetries = 1
	cfg.Agents.Defaults.RestrictToWorkspace = true
	cfg.Agents.List = []config.AgentConfig{{ID: "main", Default: true, Name: "Midden", Workspace: kernelWorkspace, Skills: []string{"midden-investigation", "midden-article", "midden-presentation", "midden-long-form"}}}
	cfg.Evolution.Enabled = false
	cfg.Heartbeat.Enabled = false
	cfg.Hooks.Defaults.ApprovalTimeoutMS = 300000
	cfg.Tools = config.ToolsConfig{FilterSensitiveData: true, Exec: config.ExecConfig{EnableDenyPatterns: true, AllowRemote: true, TimeoutSeconds: 120}}
	allowed := []*regexp.Regexp{regexp.MustCompile("^" + regexp.QuoteMeta(app.projected) + `(?:[\\/]|$)`)}
	executor, err := tools.NewExecToolWithConfig(app.opts.Workspace, true, cfg, allowed)
	if err != nil {
		return nil, fmt.Errorf("create workspace shell tool: %w", err)
	}
	extras := []agent.Tool{
		tools.NewReadFileLinesTool(app.opts.Workspace, true, 1<<20, allowed),
		tools.NewWriteFileTool(app.opts.Workspace, true),
		tools.NewEditFileTool(app.opts.Workspace, true),
		tools.NewAppendFileTool(app.opts.Workspace, true),
		tools.NewListDirTool(app.opts.Workspace, true, allowed),
		tools.NewLoadImageTool(app.opts.Workspace, true, 16<<20, nil, allowed),
		executor,
	}
	settings, _ := json.Marshal(browserChannel{Streaming: config.StreamingConfig{Enabled: true, MinGrowthChars: 1, ThrottleSeconds: 1}})
	cfg.Channels = config.ChannelsConfig{"midden-ui": {Enabled: true, Type: "midden-ui", Settings: config.RawNode(settings)}}
	messageBus := bus.NewMessageBus()
	host := &kernelHost{app: app}
	messageBus.SetStreamDelegate(host)
	host.loop = agent.NewAgentLoop(cfg, messageBus, provider, agent.WithToolProviders(coreTools{app: app, extra: extras}))
	host.loop.SetMediaStore(media.NewFileMediaStore())
	if err = host.loop.MountHook(agent.NamedHook("midden-operator", host)); err != nil {
		host.loop.Close()
		return nil, err
	}
	instance := host.loop.GetRegistry().GetDefaultAgent()
	instance.ContextBuilder = agent.NewContextBuilder(app.opts.Workspace)
	instance.Tools.SetAllowlist([]string{"read_file", "write_file", "edit_file", "append_file", "list_dir", "load_image", "exec", "midden"})
	instance.Sessions = session.NewSessionManager(filepath.Join(app.opts.State, "kernel-history"))
	return host, nil
}
func (h *kernelHost) Process(ctx context.Context, text, id string) (string, error) {
	return h.loop.ProcessDirectWithChannel(ctx, text, session.BuildOpaqueSessionKey("midden-ui:"+id), "midden-ui", id)
}
func (h *kernelHost) Close() { h.loop.Close() }
func (h *kernelHost) GetStreamer(ctx context.Context, channel, chatID, sessionKey string) (bus.Streamer, bool) {
	return &answerStream{app: h.app, sessionID: chatID}, true
}

type answerStream struct {
	app       *App
	sessionID string
}

func (s *answerStream) Update(ctx context.Context, text string) error {
	s.app.emit(Event{Type: "delta", SessionID: s.sessionID, Text: text})
	return ctx.Err()
}
func (s *answerStream) Finalize(ctx context.Context, text string) error { return s.Update(ctx, text) }
func (s *answerStream) Cancel(context.Context)                          {}
func (h *kernelHost) ApproveTool(ctx context.Context, request *agent.ToolApprovalRequest) (agent.ApprovalDecision, error) {
	h.app.emit(Event{Type: "tool", Tool: request.Tool, Arguments: request.Arguments, Status: "pending"})
	if path, ok := request.Arguments["path"].(string); ok {
		write := request.Tool == "write_file" || request.Tool == "edit_file" || request.Tool == "append_file"
		if err := h.app.toolPath(path, write); err != nil {
			return agent.ApprovalDecision{Approved: false, Reason: err.Error()}, nil
		}
	}
	switch request.Tool {
	case "read_file", "list_dir", "load_image":
		return agent.ApprovalDecision{Approved: true}, nil
	}
	pending := &permission{ID: randomID(), Tool: request.Tool, Arguments: request.Arguments, decision: make(chan bool, 1)}
	h.app.mu.Lock()
	h.app.permissions[pending.ID] = pending
	h.app.mu.Unlock()
	h.app.emit(Event{Type: "permission", Tool: request.Tool, Arguments: request.Arguments, PermissionID: pending.ID})
	defer func() { h.app.mu.Lock(); delete(h.app.permissions, pending.ID); h.app.mu.Unlock() }()
	timer := time.NewTimer(4 * time.Minute)
	defer timer.Stop()
	select {
	case allow := <-pending.decision:
		h.app.emit(Event{Type: "permission_result", PermissionID: pending.ID, Allow: &allow})
		reason := "Allowed once by operator"
		if !allow {
			reason = "Denied by operator; do not retry through another tool"
		}
		return agent.ApprovalDecision{Approved: allow, Reason: reason}, nil
	case <-ctx.Done():
		allow := false
		h.app.emit(Event{Type: "permission_result", PermissionID: pending.ID, Allow: &allow})
		return agent.ApprovalDecision{Approved: false, Reason: "Turn cancelled"}, ctx.Err()
	case <-timer.C:
		allow := false
		h.app.emit(Event{Type: "permission_result", PermissionID: pending.ID, Allow: &allow})
		return agent.ApprovalDecision{Approved: false, Reason: "Operator approval timed out"}, nil
	}

}

func (a *App) toolPath(name string, write bool) error {
	if write {
		return a.writePath(name)
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.opts.Workspace, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if a.projected != "" {
		if rel, err := filepath.Rel(a.projected, resolved); err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return nil
		}
	}
	if rel, err := filepath.Rel(a.opts.State, resolved); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return fmt.Errorf("host state and credentials are not source material")
	}
	rel, err := filepath.Rel(a.opts.Workspace, resolved)
	if err != nil || rel != "." && !filepath.IsLocal(rel) {
		return fmt.Errorf("file is outside the workspace and mounted bundle")
	}
	return nil
}
