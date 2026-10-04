package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xibodev/compa/pkg/agent"
)

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
