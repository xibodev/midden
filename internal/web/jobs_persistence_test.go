package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
)

func TestJobsPersistAcrossManagers(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first := NewJobs(db)
	job := first.create("mine", "last 7d")
	first.update(job.ID, func(current *Job) {
		current.Status = Done
		current.Progress = "complete"
		current.Result = map[string]any{
			"assayed": 4, "answer": "private answer", "question": "private question",
			"recipe":  index.Recipe{UID: "recipe", Title: "Safe title", Request: "private request"},
			"message": index.WorkMessage{RecipeID: "recipe", Role: "agent", Body: "private body"},
		}
	})

	second := NewJobs(db)
	got, ok := second.get(job.ID)
	if !ok {
		t.Fatal("persisted job was not found")
	}
	if got.Status != Done || got.Progress != "complete" {
		t.Fatalf("job=%#v", got)
	}
	result, ok := got.Result.(map[string]any)
	if !ok || result["assayed"].(float64) != 4 {
		t.Fatalf("result=%#v", got.Result)
	}
	if _, exists := result["answer"]; exists {
		t.Fatalf("durable result retained answer: %#v", result)
	}
	if _, exists := result["question"]; exists {
		t.Fatalf("durable result retained question: %#v", result)
	}
	message, ok := result["message"].(map[string]any)
	if !ok {
		t.Fatalf("message summary=%#v", result["message"])
	}
	if _, exists := message["body"]; exists {
		t.Fatalf("durable message retained body: %#v", message)
	}
	recipe, ok := result["recipe"].(map[string]any)
	if !ok {
		t.Fatalf("recipe summary=%#v", result["recipe"])
	}
	if _, exists := recipe["request"]; exists {
		t.Fatalf("durable recipe retained request: %#v", recipe)
	}
}

func TestRecoveryRunsHandlerReturnsDurableHistory(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run := index.RecoveryRun{
		Op: "mine", Scope: json.RawMessage(`{"days":7}`),
		Status: "done", Sessions: 4, Assayed: 3,
	}
	if err := db.PutRecoveryRun(&run); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/recovery-runs", nil)
	server.handleRecoveryRuns(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var runs []index.RecoveryRun
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].UID != run.UID || runs[0].Assayed != 3 {
		t.Fatalf("runs=%#v", runs)
	}
}

func TestExactSessionFilterIncludesToolIdentity(t *testing.T) {
	sessions := []core.Session{
		{Tool: core.ToolClaude, ID: "shared"},
		{Tool: core.ToolCopilot, ID: "shared"},
	}
	filtered := (actionRequest{
		SessionKeys: []string{sessionIdentity(string(core.ToolClaude), "shared")},
	}).filterExactSessions(sessions)
	if len(filtered) != 1 || filtered[0].Tool != core.ToolClaude {
		t.Fatalf("filtered=%#v", filtered)
	}
}

func TestMoveArchiveFilePreservesContent(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	target := filepath.Join(dir, "archive", "source.jsonl")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("{\"id\":\"one\"}\n")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := moveArchiveFile(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("target=%q", got)
	}
}
