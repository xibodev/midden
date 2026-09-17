package web

import (
	"context"
	"encoding/json"
	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/session"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserApprovalWaitsForMatchingSessionDecision(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan agent.ApprovalDecision, 1)
	go func() {
		d, _ := (browserApprover{s}).ApproveTool(ctx, &agent.ToolApprovalRequest{Tool: "midden_seed_create", Meta: agent.HookMeta{SessionKey: session.BuildOpaqueSessionKey("midden-chat:main_chat")}})
		result <- d
	}()
	var pending []*browserApproval
	for len(pending) == 0 && ctx.Err() == nil {
		w := httptest.NewRecorder()
		s.handleChatApprovals(w, httptest.NewRequest(http.MethodGet, "/api/chat/approvals", nil))
		if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	if len(pending) != 1 {
		t.Fatal("no pending approval")
	}
	w := httptest.NewRecorder()
	body := `{"id":"` + pending[0].ID + `","approved":true}`
	wrong := httptest.NewRequest(http.MethodPost, "/api/chat/approvals?session_id=other", strings.NewReader(body))
	wrong.Host = "localhost"
	wrong.Header.Set("X-Midden-Request", "1")
	wrong.Header.Set("Origin", "http://localhost")
	s.handleChatApprovals(w, wrong)
	if w.Code != 404 {
		t.Fatal("cross-session approval accepted")
	}
	w = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat/approvals", strings.NewReader(body))
	request.Host = "localhost"
	request.Header.Set("X-Midden-Request", "1")
	request.Header.Set("Origin", "http://localhost")
	s.handleChatApprovals(w, request)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if d := <-result; !d.Approved {
		t.Fatal(d)
	}
}
