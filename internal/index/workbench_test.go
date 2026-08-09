package index

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackgroundJobsPersistAndInterrupt(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	job := StoredJob{
		ID: "job-1", Op: "mine", Scope: "last 7d", Status: "running",
		Progress: "assaying 2/4", Result: json.RawMessage(`{"count":2}`),
		Estimate: json.RawMessage(`{"mid":100}`),
	}
	if err := db.PutBackgroundJob(job); err != nil {
		t.Fatal(err)
	}
	got, err := db.BackgroundJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.Progress != "assaying 2/4" ||
		string(got.Result) != `{"count":2}` {
		t.Fatalf("job=%#v", got)
	}

	if err := db.InterruptBackgroundJobs(); err != nil {
		t.Fatal(err)
	}
	got, err = db.BackgroundJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || !strings.Contains(got.Error, "restarted") ||
		got.Ended.IsZero() {
		t.Fatalf("interrupted job=%#v", got)
	}
}

func TestRecoveryRunRoundTrip(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	run := RecoveryRun{
		JobID: "job-2", Op: "reclaim", Scope: json.RawMessage(`{"days":7}`),
		Status: "done", Sessions: 3, Evidence: 14, Bytes: 2048,
		Backend: "copilot", Model: "default", Depth: "summary",
		Ended: time.Now(),
	}
	if err := db.PutRecoveryRun(&run); err != nil {
		t.Fatal(err)
	}
	runs, err := db.RecoveryRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].UID != run.UID || runs[0].Evidence != 14 ||
		string(runs[0].Scope) != `{"days":7}` {
		t.Fatalf("runs=%#v", runs)
	}
}

func TestWorkThreadAndMessagesRoundTrip(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	recipe := Recipe{Title: "Recovery guide"}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	thread := WorkThread{
		RecipeID: recipe.UID, Backend: "copilot",
		CLISessionID: "session-1", BudgetTokens: 900000,
		EstimatedSpent: 12000,
	}
	if err := db.PutWorkThread(&thread); err != nil {
		t.Fatal(err)
	}
	got, err := db.WorkThread(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CLISessionID != "session-1" || got.BudgetTokens != 900000 {
		t.Fatalf("thread=%#v", got)
	}

	for _, message := range []WorkMessage{
		{RecipeID: recipe.UID, Role: "user", Body: "Make it direct."},
		{RecipeID: recipe.UID, Role: "agent", Body: "Updated."},
	} {
		message := message
		if err := db.PutWorkMessage(&message); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := db.WorkMessages(recipe.UID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" ||
		messages[1].Body != "Updated." {
		t.Fatalf("messages=%#v", messages)
	}
}

func TestReconcileOrphanedJobsAndRunsClosesEveryActiveRow(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"owned", "orphan"} {
		if err := db.PutBackgroundJob(StoredJob{ID: id, Op: "mine", Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"owned", "orphan", "missing-job"} {
		run := &RecoveryRun{JobID: id, Op: "mine", Scope: json.RawMessage(`{}`), Status: "running"}
		if err := db.PutRecoveryRun(run); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := db.ReconcileOrphanedJobsAndRuns([]string{"owned"}, "owner absent")
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	owned, err := db.BackgroundJob("owned")
	if err != nil || owned.Status != "running" {
		t.Fatalf("owned=%#v err=%v", owned, err)
	}
	orphan, err := db.BackgroundJob("orphan")
	if err != nil || orphan.Status != "failed" || !strings.Contains(orphan.Error, "owner absent") {
		t.Fatalf("orphan=%#v err=%v", orphan, err)
	}
	runs, err := db.RecoveryRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.JobID == "owned" && run.Status != "running" {
			t.Fatalf("healthy run was interrupted: %#v", run)
		}
		if run.JobID != "owned" && (run.Status != "failed" || !strings.Contains(run.Error, "owner absent")) {
			t.Fatalf("orphan run=%#v", run)
		}
	}
}

func TestRecoveryRunsPageHasBoundedItemsAndTruthfulTotal(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 101; i++ {
		run := &RecoveryRun{UID: fmt.Sprintf("run-%03d", i), Op: "mine", Scope: json.RawMessage(`{}`), Status: "done"}
		if err := db.PutRecoveryRun(run); err != nil {
			t.Fatal(err)
		}
	}
	runs, total, err := db.RecoveryRunsPage(10, 100)
	if err != nil || total != 101 || len(runs) != 1 {
		t.Fatalf("runs=%d total=%d err=%v", len(runs), total, err)
	}
}
