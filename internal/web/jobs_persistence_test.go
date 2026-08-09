package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestJobsSurfaceDurableWriteFailure(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	jobs := NewJobs(db)
	job := jobs.create("mine", "exact session")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	jobs.update(job.ID, func(current *Job) { current.Progress = "assaying" })
	got, ok := jobs.get(job.ID)
	if !ok || got.Status != Failed || !strings.Contains(got.Error, "could not persist job state") {
		t.Fatalf("job=%#v found=%v", got, ok)
	}
}

func TestRecoveryPersistenceFailureFailsVisibleJob(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	jobs := NewJobs(db)
	job := jobs.create("mine", "exact session")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, jobs: jobs, cache: newSnapshotCache()}
	server.runJob(job.ID, actionRequest{Op: "mine"})
	got, ok := jobs.get(job.ID)
	if !ok || got.Status != Failed || !strings.Contains(got.Error, "could not persist recovery run") {
		t.Fatalf("job=%#v found=%v", got, ok)
	}
}

func TestJobsPageOverlaysLiveActiveState(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	if err := db.PutBackgroundJob(index.StoredJob{
		ID: "job-live", Op: "mine", Scope: "last 7d", Status: "running",
		Progress: "durable progress", Started: now,
	}); err != nil {
		t.Fatal(err)
	}
	jobs := &Jobs{db: db, jobs: map[string]*Job{
		"job-live": {ID: "job-live", Op: "mine", Scope: "last 7d", Status: Running, Progress: "live progress", Started: now},
	}}
	page, total := jobs.page(10, 0)
	if total != 1 || len(page) != 1 || page[0].Progress != "live progress" {
		t.Fatalf("page=%#v total=%d", page, total)
	}
}

func TestJobsHandlerPagesAndReportsDurableTotal(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 12; i++ {
		if err := db.PutBackgroundJob(index.StoredJob{ID: fmt.Sprintf("job-%02d", i), Op: "mine", Status: "done", Started: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{db: db, jobs: NewJobs(db), cache: newSnapshotCache()}
	rec := httptest.NewRecorder()
	server.handleJobs(rec, httptest.NewRequest(http.MethodGet, "/api/jobs?limit=5&offset=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []*Job `json:"items"`
		Total int    `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 12 || len(got.Items) != 5 {
		t.Fatalf("items=%d total=%d", len(got.Items), got.Total)
	}
}

func TestSecondJobsManagerPreservesHealthyOwner(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := NewJobs(db)
	defer first.Close()
	job := first.create("mine", "exact session")
	second := NewJobs(db)
	defer second.Close()
	got, err := db.BackgroundJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != string(Queued) {
		t.Fatalf("second manager interrupted live owner: %#v", got)
	}
}

func TestStaleOwnerInterruptsLinkedJobAndRecoveryRun(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := NewJobs(db)
	job := first.create("mine", "exact session")
	run := &index.RecoveryRun{JobID: job.ID, Op: "mine", Scope: json.RawMessage(`{"days":0}`), Status: "running"}
	if err := db.PutRecoveryRun(run); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second := NewJobs(db)
	defer second.Close()
	stored, err := db.BackgroundJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != string(Failed) || !strings.Contains(stored.Error, "owner stopped heartbeating") {
		t.Fatalf("stale job=%#v", stored)
	}
	runs, err := db.RecoveryRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" || !strings.Contains(runs[0].Error, "owner stopped heartbeating") {
		t.Fatalf("recovery runs=%#v", runs)
	}
}

func TestRecoveryAuditOmitsRequestBodies(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db}
	server.auditRecoveryOperation(actionRequest{
		Op: "reclaim", SessionKeys: []string{"claude:session-1"},
		Instruction: "private request body", Question: "private question body",
	}, errors.New("private failure body"))
	ops, err := db.Operations(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Op != "reclaim" || ops[0].OK ||
		strings.Contains(ops[0].Detail, "private") || !strings.Contains(ops[0].Detail, "1 exact session") {
		t.Fatalf("audit=%#v", ops)
	}
}
