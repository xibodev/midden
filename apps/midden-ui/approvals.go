package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xibodev/compa/pkg/agent"
)

// ApproveTool runs read-only and cache-writing midden calls without a card;
// workspace writes and the other tools keep the operator's permission card.
func (h *kernelHost) ApproveTool(ctx context.Context, request *agent.ToolApprovalRequest) (agent.ApprovalDecision, error) {
	call := Event{Type: "tool", Tool: request.Tool, Arguments: request.Arguments, Status: "pending", CallID: h.beginToolCall(request.Tool)}
	var invalid error
	if request.Tool == "midden" {
		effect, err := h.app.middenEffect(request.Arguments)
		invalid, call.Effect = err, string(effect)
	}
	h.app.emit(call)
	if invalid != nil {
		return h.deny(call, invalid.Error()), nil
	}
	if path, ok := request.Arguments["path"].(string); ok {
		write := request.Tool == "write_file" || request.Tool == "edit_file" || request.Tool == "append_file"
		if err := h.app.toolPath(path, write); err != nil {
			return h.deny(call, err.Error()), nil
		}
	}
	switch request.Tool {
	case "read_file", "list_dir", "load_image":
		return agent.ApprovalDecision{Approved: true}, nil
	case "midden":
		if call.Effect != string(effectWritesWorkspace) {
			return agent.ApprovalDecision{Approved: true}, nil
		}
	}
	pending := &permission{ID: randomID(), Tool: request.Tool, Arguments: request.Arguments, decision: make(chan bool, 1)}
	h.app.mu.Lock()
	h.app.permissions[pending.ID] = pending
	h.app.mu.Unlock()
	h.app.emit(Event{Type: "permission", Tool: request.Tool, Arguments: request.Arguments, PermissionID: pending.ID, CallID: call.CallID, Effect: call.Effect})
	defer func() { h.app.mu.Lock(); delete(h.app.permissions, pending.ID); h.app.mu.Unlock() }()
	timer := time.NewTimer(4 * time.Minute)
	defer timer.Stop()
	select {
	case allow := <-pending.decision:
		h.app.emit(Event{Type: "permission_result", PermissionID: pending.ID, Allow: &allow})
		if !allow {
			return h.deny(call, "Denied by operator; do not retry through another tool"), nil
		}
		return agent.ApprovalDecision{Approved: true, Reason: "Allowed once by operator"}, nil
	case <-ctx.Done():
		allow := false
		h.app.emit(Event{Type: "permission_result", PermissionID: pending.ID, Allow: &allow})
		return h.deny(call, "Turn cancelled"), ctx.Err()
	case <-timer.C:
		allow := false
		h.app.emit(Event{Type: "permission_result", PermissionID: pending.ID, Allow: &allow})
		return h.deny(call, "Operator approval timed out"), nil
	}
}

// middenEffect validates the midden tool input with the shared validator and
// classifies it.
func (a *App) middenEffect(arguments map[string]any) (coreEffect, error) {
	args, err := coreToolArgs(arguments)
	if err != nil {
		return "", err
	}
	if err = a.validateCoreArgs(args); err != nil {
		return "", err
	}
	return classifyCoreArgs(args)
}

// deny ends a call the kernel will not run and reports the text the model
// receives, mirroring the kernel's denial wording.
func (h *kernelHost) deny(call Event, reason string) agent.ApprovalDecision {
	h.endToolCall(call.Tool)
	call.Status = "failed"
	call.Result, call.ResultTruncated = clipEventResult("Tool execution denied by approval hook: " + reason)
	h.app.emit(call)
	return agent.ApprovalDecision{Approved: false, Reason: reason}
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
