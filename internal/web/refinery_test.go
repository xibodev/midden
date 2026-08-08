package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

func TestRefineryDesignAndEvidenceApproval(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PutNuggets([]index.Nugget{
		{
			UID: "evidence-1", Tool: "claude", SessionID: "session-1",
			Kind: "decision", Title: "Use explicit approval",
			Body: "Every destination write requires review.", Workspace: "payments",
			Confidence: .96, CreatedAt: time.Now(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	call := func(body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/refinery/action", bytes.NewReader(raw))
		req.Host = "127.0.0.1:7777"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Midden-Request", "1")
		rec := httptest.NewRecorder()
		server.handleRefineryAction(rec, req)
		return rec
	}

	design := call(map[string]any{
		"action": "design", "workspace": "payments",
		"prompt": "Create a tutorial and diagram.",
	})
	if design.Code != http.StatusOK {
		t.Fatalf("design status=%d body=%s", design.Code, design.Body.String())
	}
	var designed struct {
		Recipe index.Recipe `json:"recipe"`
	}
	if err := json.Unmarshal(design.Body.Bytes(), &designed); err != nil {
		t.Fatal(err)
	}
	if designed.Recipe.UID == "" || len(designed.Recipe.EvidenceIDs) != 1 {
		t.Fatalf("designed recipe=%#v", designed.Recipe)
	}

	approve := call(map[string]any{
		"action": "approve_evidence", "recipe_id": designed.Recipe.UID,
		"evidence_ids": []string{"evidence-1"},
	})
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approve.Code, approve.Body.String())
	}
	got, err := db.Recipe(designed.Recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != refinery.RecipeApproved || got.ApprovedAt.IsZero() {
		t.Fatalf("approved recipe=%#v", got)
	}
}

func TestRefineryExportRequiresReviewedOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	recipe := index.Recipe{Title: "Safe export", Status: refinery.RecipeReview}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "artifacts", "refinery", "safe-export")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tutorial.md")
	if err := os.WriteFile(path, []byte("# Draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := index.RefineryOutput{
		RecipeID: recipe.UID, Kind: "tutorial", Title: "Tutorial",
		Format: "markdown", Status: refinery.OutputDraft, Path: path,
	}
	if err := db.PutRefineryOutput(&output); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	if _, err := server.exportRefineryOutput(refineryActionRequest{
		OutputID: output.UID, Destination: "local_vault",
	}); err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("draft export error=%v", err)
	}
	if _, err := server.reviewRefineryOutput(refineryActionRequest{
		OutputID: output.UID, Decision: refinery.OutputReviewed, Body: "# Reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := server.exportRefineryOutput(refineryActionRequest{
		OutputID: output.UID, Destination: "local_vault",
	})
	if err != nil {
		t.Fatal(err)
	}
	exported := result.(map[string]any)["path"].(string)
	body, err := os.ReadFile(exported)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Reviewed" {
		t.Fatalf("exported body=%q", body)
	}
}

func TestRefineryExportsAreVersionedByRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Versioned export", Status: refinery.RecipeReview}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}

	var exported []string
	for _, runID := range []string{"run-one", "run-two"} {
		dir := filepath.Join(home, "artifacts", "refinery", runID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "tutorial.md")
		if err := os.WriteFile(path, []byte(runID), 0o644); err != nil {
			t.Fatal(err)
		}
		output := index.RefineryOutput{
			RecipeID: recipe.UID, RunID: runID, Kind: "tutorial",
			Title: "Tutorial", Format: "markdown", Status: refinery.OutputReviewed,
			Path: path,
		}
		if err := db.PutRefineryOutput(&output); err != nil {
			t.Fatal(err)
		}
		result, err := server.exportRefineryOutput(refineryActionRequest{
			OutputID: output.UID, Destination: "local_vault",
		})
		if err != nil {
			t.Fatal(err)
		}
		exported = append(exported, result.(map[string]any)["path"].(string))
	}
	if exported[0] == exported[1] {
		t.Fatalf("two runs exported to the same path: %q", exported[0])
	}
	for i, path := range exported {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != []string{"run-one", "run-two"}[i] {
			t.Fatalf("%s body=%q", path, body)
		}
	}
}

func TestCompleteRecipeRequiresEveryPlannedOutputFromSuccessfulRun(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{
		Title: "Partial run", Status: refinery.RecipeReview,
		Outputs: []index.RecipeOutputSpec{
			{Kind: "tutorial"}, {Kind: "diagram"},
		},
	}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	run := index.RefineryRun{RecipeID: recipe.UID, Status: "done"}
	if err := db.PutRefineryRun(&run); err != nil {
		t.Fatal(err)
	}
	first := index.RefineryOutput{
		RecipeID: recipe.UID, RunID: run.UID, Kind: "tutorial",
		Title: "Tutorial", Status: refinery.OutputReviewed, Path: "one.md",
	}
	if err := db.PutRefineryOutput(&first); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	server.completeRecipeIfReviewed(recipe.UID)
	got, err := db.Recipe(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == refinery.RecipeComplete {
		t.Fatal("partial run was marked complete")
	}

	second := index.RefineryOutput{
		RecipeID: recipe.UID, RunID: run.UID, Kind: "diagram",
		Title: "Diagram", Status: refinery.OutputRejected, Path: "two.d2",
	}
	if err := db.PutRefineryOutput(&second); err != nil {
		t.Fatal(err)
	}
	server.completeRecipeIfReviewed(recipe.UID)
	got, err = db.Recipe(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != refinery.RecipeComplete {
		t.Fatalf("full reviewed run status=%q, want complete", got.Status)
	}
}

func TestProductionPreviewDoesNotInvokeModelOrWriteOutputs(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PutNuggets([]index.Nugget{
		{
			UID: "n1", Tool: "claude", SessionID: "s1", Kind: "decision",
			Title: "Keep exports local", Body: "Review before export.",
			Workspace: "payments", Confidence: .95,
		},
	}); err != nil {
		t.Fatal(err)
	}
	template, _ := refinery.FindTemplate("retrieval_pack")
	recipe := index.Recipe{
		Title: "Data pack", Workspace: "payments", Status: refinery.RecipeApproved,
		Outputs: []index.RecipeOutputSpec{template}, EvidenceIDs: []string{"n1"},
		ApprovedAt: time.Now(),
	}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	result, err := server.doProduction("missing-job-is-safe", actionRequest{
		RecipeID: recipe.UID, Apply: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview := result.(map[string]any)
	if preview["preview"] != true {
		t.Fatalf("preview=%#v", preview)
	}
	estimate := preview["estimate"].(cost.Estimate)
	if estimate.Mid != 0 || estimate.High != 0 {
		t.Fatalf("deterministic estimate=%#v, want zero model cost", estimate)
	}
	outputs, err := db.RefineryOutputs(recipe.UID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 0 {
		t.Fatalf("preview wrote outputs: %#v", outputs)
	}
}

func TestRefineryUIContainsCompleteJourneyNavigation(t *testing.T) {
	htmlBytes, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	for _, label := range []string{
		"Recover", "Studio", "Library", "Cleanup", "Activity", "Tools",
	} {
		if !strings.Contains(html, label) {
			t.Errorf("embedded UI missing %q", label)
		}
	}
	jsBytes, err := uiFS.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBytes)
	for _, endpoint := range []string{
		"/api/refinery", "/api/refinery/output",
		"/api/refinery/action", "/api/refinery/connections",
		"/api/recovery-runs", "/api/work-items", "/api/work-item",
		"/api/work-console", "/api/output-download", "/api/cleanup-candidates",
	} {
		if !strings.Contains(js, endpoint) {
			t.Errorf("embedded UI does not use %s", endpoint)
		}
	}
}

func TestPreviewDesignDoesNotPersistRecipe(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PutNuggets([]index.Nugget{{
		UID: "n1", Tool: "claude", SessionID: "s1", Kind: "decision",
		Title: "Keep exports local", Body: "Review first.",
		Workspace: "orvantix", Confidence: .9,
	}}); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	result, err := server.previewRecipe(refineryActionRequest{
		Workspace: "orvantix",
		Prompt:    "Create a retrieval pack and evaluation pack.",
	})
	if err != nil {
		t.Fatal(err)
	}
	recipe := result.(map[string]any)["recipe"].(index.Recipe)
	if recipe.Title != "orvantix retrieval pack and evaluation pack" {
		t.Fatalf("preview title=%q", recipe.Title)
	}
	recipes, err := db.Recipes(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipes) != 0 {
		t.Fatalf("preview persisted recipes: %#v", recipes)
	}
}

func TestDesignWithoutEvidenceIsRejected(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	_, err = server.designRecipe(refineryActionRequest{Prompt: "Create a tutorial"})
	if err == nil || !strings.Contains(err.Error(), "no reclaimed evidence") {
		t.Fatalf("design error=%v", err)
	}
}

func TestMineWaitsForExistingScanLock(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.AcquireScanLock()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = first.Release()
	}()
	started := time.Now()
	second, err := server.acquireScanLockForJob("missing-job", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if time.Since(started) < 70*time.Millisecond {
		t.Fatal("mine did not wait for the existing scan lock")
	}
}

func TestMineScopeKeepsExactSessionUnbounded(t *testing.T) {
	exact := mineScope(actionRequest{
		SessionKeys: []string{"claude:old-session"}, Days: 0,
	})
	if exact.Days != 0 {
		t.Fatalf("exact session days=%d, want unbounded", exact.Days)
	}
	defaultScope := mineScope(actionRequest{})
	if defaultScope.Days != 30 {
		t.Fatalf("default days=%d, want 30", defaultScope.Days)
	}
}

func TestEmptyNuggetsHandlerReturnsJSONArray(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := &Server{db: db}
	req := httptest.NewRequest(http.MethodGet, "/api/nuggets", nil)
	rec := httptest.NewRecorder()
	server.handleNuggets(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestEmbeddedUIContainsPersistentWorkbench(t *testing.T) {
	body, err := uiFS.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)
	for _, want := range []string{
		"work_chat", "Same session", "session_ids", "previewEvidenceExtraction",
		"renderTaskDock", "renderOwnedPreview", "openCleanupCandidate",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
}

func TestStructuredReviewUpdatesEvidenceAndProvenance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIDDEN_HOME", home)
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{Title: "Review pack", Status: refinery.RecipeReview}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "artifacts", "refinery", "review-pack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "retrieval.jsonl")
	provenancePath := outputPath + ".provenance.json"
	if err := os.WriteFile(outputPath, []byte("{\"id\":\"e1\"}\n{\"id\":\"e2\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	provenance := map[string]any{
		"status": "draft",
		"evidence": []map[string]any{
			{"evidence_id": "e1", "title": "one"},
			{"evidence_id": "e2", "title": "two"},
		},
	}
	body, _ := json.Marshal(provenance)
	if err := os.WriteFile(provenancePath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	output := index.RefineryOutput{
		RecipeID: recipe.UID, Kind: "retrieval_pack", Title: "Retrieval",
		Format: "jsonl", Status: refinery.OutputDraft, Path: outputPath,
		ProvenancePath: provenancePath, EvidenceIDs: []string{"e1", "e2"},
	}
	if err := db.PutRefineryOutput(&output); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	if _, err := server.reviewRefineryOutput(refineryActionRequest{
		OutputID: output.UID, Decision: refinery.OutputReviewed,
		Body: "{\"id\":\"e2\"}\n", EvidenceIDs: []string{"e2"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.RefineryOutput(output.UID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.EvidenceIDs) != 1 || got.EvidenceIDs[0] != "e2" {
		t.Fatalf("reviewed evidence ids=%#v", got.EvidenceIDs)
	}
	updated, err := os.ReadFile(provenancePath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(updated, &manifest); err != nil {
		t.Fatal(err)
	}
	items := manifest["evidence"].([]any)
	if manifest["status"] != refinery.OutputReviewed || len(items) != 1 ||
		items[0].(map[string]any)["evidence_id"] != "e2" {
		t.Fatalf("updated provenance=%#v", manifest)
	}
}

func TestJSONLReviewDerivesEvidenceFromSavedBody(t *testing.T) {
	ids, err := evidenceIDsFromJSONL(
		"{\"id\":\"e1\",\"text\":\"one\"}\n" +
			"{\"provenance\":{\"chosen_evidence_id\":\"e2\",\"rejected_evidence_id\":\"e3\"}}\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "e1,e2,e3" {
		t.Fatalf("derived ids=%#v", ids)
	}
	if _, err := evidenceIDsFromJSONL("{not-json}\n"); err == nil {
		t.Fatal("invalid JSONL was accepted")
	}
}

func TestCompletedRecipeReturnsToReviewWhenOutputReopens(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := index.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recipe := index.Recipe{
		Title: "Review state", Status: refinery.RecipeComplete,
		Outputs: []index.RecipeOutputSpec{{Kind: "retrieval_pack"}},
	}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	run := index.RefineryRun{RecipeID: recipe.UID, Status: "done"}
	if err := db.PutRefineryRun(&run); err != nil {
		t.Fatal(err)
	}
	output := index.RefineryOutput{
		RecipeID: recipe.UID, RunID: run.UID, Kind: "retrieval_pack",
		Title: "Retrieval", Status: refinery.OutputDraft, Path: "draft.jsonl",
	}
	if err := db.PutRefineryOutput(&output); err != nil {
		t.Fatal(err)
	}
	server := &Server{db: db, cache: newSnapshotCache(), jobs: NewJobs()}
	server.completeRecipeIfReviewed(recipe.UID)
	got, err := db.Recipe(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != refinery.RecipeReview {
		t.Fatalf("status=%q, want review", got.Status)
	}
}
