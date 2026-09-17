package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
	"github.com/mekjr1/midden/pkg/provider"

	"github.com/xibodev/facet-studio/pkg/agent"
	"github.com/xibodev/facet-studio/pkg/bus"
	"github.com/xibodev/facet-studio/pkg/config"
	"github.com/xibodev/facet-studio/pkg/providers"
	kernelsession "github.com/xibodev/facet-studio/pkg/session"
)

type scriptMockProvider struct {
	response string
}

func (s *scriptMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   s.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (s *scriptMockProvider) GetDefaultModel() string {
	return "mock-model"
}

func TestWorkItemsExposePersistentThreadAndMessages(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Recovery guide", Status: refinery.RecipeDraft}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	thread := index.WorkThread{
		RecipeID: recipe.UID, Backend: "copilot", CLISessionID: "private-session",
	}
	if err := db.PutWorkThread(&thread); err != nil {
		t.Fatal(err)
	}
	message := index.WorkMessage{
		RecipeID: recipe.UID, Role: "agent", Body: "The preview is ready.",
	}
	if err := db.PutWorkMessage(&message); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/work-items", nil)
	server.handleWorkItems(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []workItemSummary `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Items) != 1 ||
		response.Items[0].MessageCount != 1 ||
		response.Items[0].Thread == nil ||
		response.Items[0].Thread.Backend != "copilot" {
		t.Fatalf("response=%#v", response)
	}
	if strings.Contains(rec.Body.String(), "private-session") {
		t.Fatal("work-item list exposed the CLI session handle")
	}
}

func TestWorkItemGetDoesNotCreateThread(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Read-only work item"}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/work-item?id="+recipe.UID, nil)
	server.handleWorkItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := db.WorkThread(recipe.UID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GET created work thread: %v", err)
	}
}

func TestWorkItemMessagesAreCappedWithTotal(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Long conversation"}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		message := index.WorkMessage{
			RecipeID: recipe.UID, Role: "user",
			Body: fmt.Sprintf("message %02d", i),
		}
		if err := db.PutWorkMessage(&message); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/work-item?id="+recipe.UID+"&message_limit=10", nil)
	server.handleWorkItem(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Messages []index.WorkMessage `json:"messages"`
		Total    int                 `json:"message_total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 40 || len(response.Messages) != 10 ||
		response.Messages[0].Body != "message 30" {
		t.Fatalf("response=%#v", response)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet,
		"/api/work-item?id="+recipe.UID+"&message_limit=10&message_offset=10", nil)
	server.handleWorkItem(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 40 || len(response.Messages) != 10 ||
		response.Messages[0].Body != "message 20" {
		t.Fatalf("offset response=%#v", response)
	}
}

func TestWorkConsoleIsAllowlisted(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Console item", Status: refinery.RecipeDraft}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}

	call := func(command string) *httptest.ResponseRecorder {
		body := `{"recipe_id":"` + recipe.UID + `","command":` +
			strconvQuote(command) + `}`
		req := httptest.NewRequest(http.MethodPost, "/api/work-console",
			strings.NewReader(body))
		req.Host = "127.0.0.1:7777"
		req.Header.Set("X-Midden-Request", "1")
		req.Header.Set("Origin", "http://127.0.0.1:7777")
		rec := httptest.NewRecorder()
		server.handleWorkConsole(rec, req)
		return rec
	}
	if rec := call("status"); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), "Console item") {
		t.Fatalf("status command=%d %s", rec.Code, rec.Body.String())
	}
	if rec := call("rm -rf"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported command status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOutputDownloadReturnsOwnedFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Download item"}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "artifacts", "refinery", "download")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "guide.md")
	if err := os.WriteFile(path, []byte("# Guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := index.RefineryOutput{
		RecipeID: recipe.UID, Kind: "tutorial", Title: "Guide",
		Format: "markdown", Status: refinery.OutputDraft, Path: path,
	}
	if err := db.PutRefineryOutput(&output); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/output-download?id="+output.UID, nil)
	server.handleOutputDownload(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "# Guide\n" {
		t.Fatalf("download=%d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "guide.md") {
		t.Fatalf("disposition=%q", rec.Header().Get("Content-Disposition"))
	}
}

func TestCleanupCandidateRequiresCompleteRecoveryChain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transcript := filepath.Join(home, "source.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := core.Session{
		Tool: core.ToolClaude, ID: "old-session", Dir: filepath.Join(home, "project"),
		Title: "Old recovered work", Updated: time.Now().AddDate(0, 0, -120),
		Bytes: 3, TranscriptPath: transcript,
	}
	if err := db.PutSessions([]core.Session{session}); err != nil {
		t.Fatal(err)
	}
	sourceBytes, sourceMtime := index.SourceStamp(session)
	if err := db.PutManifest(&assay.Manifest{
		Tool: string(session.Tool), SessionID: session.ID, TotalBytes: sourceBytes,
	}, sourceBytes, sourceMtime); err != nil {
		t.Fatal(err)
	}
	nugget := index.Nugget{
		UID: "evidence-1", Tool: string(session.Tool), SessionID: session.ID,
		Kind: "decision", Body: "Keep the recovery engine standalone.",
	}
	if err := db.PutNuggets([]index.Nugget{nugget}); err != nil {
		t.Fatal(err)
	}
	recipe := index.Recipe{
		Title: "Completed output", Status: refinery.RecipeComplete,
		EvidenceIDs: []string{nugget.UID},
	}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	output := index.RefineryOutput{
		RecipeID: recipe.UID, Kind: "adr", Title: "Decision",
		Format: "markdown", Status: refinery.OutputReviewed,
		Path:        filepath.Join(home, "artifacts", "refinery", "decision.md"),
		EvidenceIDs: []string{nugget.UID},
	}
	if err := db.PutRefineryOutput(&output); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/cleanup-candidates", nil)
	server.handleCleanupCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Candidates []cleanupCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Candidates) != 1 ||
		response.Candidates[0].Decision != "eligible" {
		t.Fatalf("candidates=%#v", response.Candidates)
	}
}

func TestCleanupIdentityIncludesTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	makeSession := func(tool core.Tool, name string) core.Session {
		path := filepath.Join(home, name+".jsonl")
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return core.Session{
			Tool: tool, ID: "shared-id", Dir: filepath.Join(home, name),
			Title: name, Updated: time.Now().AddDate(0, 0, -120),
			Bytes: 3, TranscriptPath: path,
		}
	}
	claude := makeSession(core.ToolClaude, "claude")
	copilot := makeSession(core.ToolCopilot, "copilot")
	if err := db.PutSessions([]core.Session{claude, copilot}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []core.Session{claude, copilot} {
		bytes, mtime := index.SourceStamp(session)
		if err := db.PutManifest(&assay.Manifest{
			Tool: string(session.Tool), SessionID: session.ID, TotalBytes: bytes,
		}, bytes, mtime); err != nil {
			t.Fatal(err)
		}
	}
	nugget := index.Nugget{
		UID: "claude-evidence", Tool: string(core.ToolClaude),
		SessionID: claude.ID, Kind: "decision", Body: "Claude-only evidence",
	}
	if err := db.PutNuggets([]index.Nugget{nugget}); err != nil {
		t.Fatal(err)
	}
	recipe := index.Recipe{
		Title: "Claude output", Status: refinery.RecipeComplete,
		EvidenceIDs: []string{nugget.UID},
	}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	if err := db.PutRefineryOutput(&index.RefineryOutput{
		RecipeID: recipe.UID, Kind: "adr", Title: "Decision",
		Status: refinery.OutputReviewed, Path: filepath.Join(home, "artifacts", "refinery", "decision.md"),
		EvidenceIDs: []string{nugget.UID},
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	candidates, _, err := server.cleanupCandidates()
	if err != nil {
		t.Fatal(err)
	}
	decisions := map[string]string{}
	for _, candidate := range candidates {
		decisions[candidate.Session.Tool] = candidate.Decision
	}
	if decisions[string(core.ToolClaude)] != "eligible" {
		t.Fatalf("claude decision=%q", decisions[string(core.ToolClaude)])
	}
	if decisions[string(core.ToolCopilot)] == "eligible" {
		t.Fatalf("copilot borrowed Claude recovery: %#v", decisions)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/cleanup-candidates?limit=1&offset=0", nil)
	server.handleCleanupCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Candidates []cleanupCandidate `json:"candidates"`
		Total      int                `json:"total"`
		Counts     map[string]int     `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Candidates) != 1 ||
		page.Counts["eligible"] != 1 {
		t.Fatalf("page=%#v", page)
	}
}

func TestWorkChatBudgetStopsBeforeBackendDetection(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nugget := index.Nugget{
		UID: "evidence-1", Tool: "claude", SessionID: "session",
		Kind: "decision", Body: "Evidence",
	}
	if err := db.PutNuggets([]index.Nugget{nugget}); err != nil {
		t.Fatal(err)
	}
	recipe := index.Recipe{Title: "Budget item", EvidenceIDs: []string{nugget.UID}}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	thread := index.WorkThread{RecipeID: recipe.UID, BudgetTokens: 1}
	if err := db.PutWorkThread(&thread); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	_, err = server.doWorkChat("missing-job", actionRequest{
		RecipeID: recipe.UID, Question: "Continue from the evidence.",
	})
	if err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkChatLockSerializesOneRecipe(t *testing.T) {
	server := &Server{}
	first := server.workChatLock("recipe")
	first.Lock()
	acquired := make(chan struct{})
	go func() {
		second := server.workChatLock("recipe")
		second.Lock()
		close(acquired)
		second.Unlock()
	}()
	select {
	case <-acquired:
		t.Fatal("second turn acquired the same recipe lock early")
	case <-time.After(40 * time.Millisecond):
	}
	first.Unlock()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second turn did not resume after the recipe lock released")
	}
}

func TestChatEndpoint(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	server := NewServer(db)
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	mockLLM := &scriptMockProvider{response: "Hello from Midden agent!"}
	al := agent.NewAgentLoop(cfg, bus.NewMessageBus(), mockLLM, agent.WithToolProviders(provider.NewMiddenToolProvider()))
	server.SetAgentLoop(al)

	newLoopbackReq := func(method, path string, body string) *http.Request {
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, r)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Host = "localhost"
		return req
	}

	// 1. GET /api/chat
	rec := httptest.NewRecorder()
	req := newLoopbackReq(http.MethodGet, "/api/chat", "")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/chat code = %d", rec.Code)
	}
	var getResp struct {
		SessionID string              `json:"session_id"`
		Messages  []index.WorkMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatal(err)
	}
	if getResp.SessionID != "main_chat" || len(getResp.Messages) != 0 {
		t.Fatalf("unexpected GET response: %+v", getResp)
	}

	// 2. POST /api/chat
	rec = httptest.NewRecorder()
	req = newLoopbackReq(http.MethodPost, "/api/chat", `{"message":"hello world"}`)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/chat code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var postResp struct {
		Reply   string             `json:"reply"`
		Message *index.WorkMessage `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &postResp); err != nil {
		t.Fatal(err)
	}
	if postResp.Reply != "Hello from Midden agent!" {
		t.Fatalf("unexpected POST response: %+v", postResp)
	}

	// 3. Verify messages persisted
	rec = httptest.NewRecorder()
	req = newLoopbackReq(http.MethodGet, "/api/chat", "")
	server.Handler().ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &getResp)
	if len(getResp.Messages) != 2 {
		t.Fatalf("expected 2 messages (user + agent), got %d: %+v", len(getResp.Messages), getResp.Messages)
	}
	key := kernelsession.BuildOpaqueSessionKey("midden-chat:main_chat")
	store := al.GetRegistry().GetDefaultAgent().Sessions
	if len(store.GetHistory(key)) < 2 {
		t.Fatal("chat did not use kernel history")
	}
	store.SetSummary(key, "old private context")
	if _, err := db.Recipe("main_chat"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("chat created a production recipe: %v", err)
	}

	// 4. DELETE /api/chat
	rec = httptest.NewRecorder()
	req = newLoopbackReq(http.MethodDelete, "/api/chat", "")
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/chat code = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = newLoopbackReq(http.MethodGet, "/api/chat", "")
	server.Handler().ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &getResp)
	if len(getResp.Messages) != 0 {
		t.Fatalf("expected 0 messages after delete, got %d", len(getResp.Messages))
	}
	if store.GetSummary(key) != "" || len(store.GetHistory(key)) != 0 {
		t.Fatal("clear left kernel context behind")
	}
}

func strconvQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}
