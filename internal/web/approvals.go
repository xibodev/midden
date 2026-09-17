package web

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mekjr1/midden/internal/index"
	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/session"
)

// A pending browser reply to a kernel-owned ToolApprover callback. The kernel
// owns execution, timeouts and cancellation; this is only its UI adapter.
type browserApproval struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	session   string
	answer    chan bool
}

type browserApprover struct{ server *Server }

func (a browserApprover) ApproveTool(ctx context.Context, req *agent.ToolApprovalRequest) (agent.ApprovalDecision, error) {
	switch req.Tool {
	case "midden_sessions_list", "midden_sessions_assay", "midden_content_types", "midden_evidence_list", "midden_recipes_list", "midden_recipes_inspect", "midden_recipes_preview", "midden_outputs_inspect", "read_file", "list_dir", "search":
		return agent.ApprovalDecision{Approved: true}, nil
	}
	pending := &browserApproval{ID: index.NewUID(), Tool: req.Tool, Arguments: req.Arguments, session: req.Meta.SessionKey, answer: make(chan bool, 1)}
	s := a.server
	s.approvalMu.Lock()
	if s.approvals == nil {
		s.approvals = map[string]*browserApproval{}
	}
	s.approvals[pending.ID] = pending
	s.approvalMu.Unlock()
	defer func() { s.approvalMu.Lock(); delete(s.approvals, pending.ID); s.approvalMu.Unlock() }()
	select {
	case <-ctx.Done():
		return agent.ApprovalDecision{Reason: "approval cancelled or expired"}, ctx.Err()
	case approved := <-pending.answer:
		return agent.ApprovalDecision{Approved: approved, Reason: "browser user decision"}, nil
	}
}

func (s *Server) handleChatApprovals(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session_id")
	if id == "" {
		id = "main_chat"
	}
	key := session.BuildOpaqueSessionKey("midden-chat:" + id)
	if r.Method == http.MethodGet {
		s.approvalMu.Lock()
		defer s.approvalMu.Unlock()
		items := []*browserApproval{}
		for _, p := range s.approvals {
			if p.session == key {
				items = append(items, p)
			}
		}
		writeJSON(w, items)
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
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	p := s.approvals[req.ID]
	if p == nil || p.session != key {
		http.Error(w, "approval expired or not in this session", 404)
		return
	}
	select {
	case p.answer <- req.Approved:
		delete(s.approvals, req.ID)
		writeJSON(w, map[string]bool{"accepted": true})
	default:
		http.Error(w, "already answered", 409)
	}
}
