package web

import (
	"database/sql"
	"encoding/json"
	"errors"
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
)

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
	var items []workItemSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MessageCount != 1 ||
		items[0].Thread == nil || items[0].Thread.Backend != "copilot" {
		t.Fatalf("items=%#v", items)
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

func strconvQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}
