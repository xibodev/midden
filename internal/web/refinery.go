package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/create"
	agentexec "github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/refine"
	"github.com/mekjr1/midden/internal/refinery"
)

type refinerySourceView struct {
	Tool        string    `json:"tool"`
	Name        string    `json:"name"`
	Sessions    int       `json:"sessions"`
	Nuggets     int       `json:"nuggets"`
	Ready       bool      `json:"ready"`
	LastUpdated time.Time `json:"last_updated,omitempty"`
}

type refineryOverview struct {
	Onboarding      bool                           `json:"onboarding"`
	NeedsAssay      bool                           `json:"needs_assay"`
	Sources         []refinerySourceView           `json:"sources"`
	Stats           map[string]any                 `json:"stats"`
	Yield           refinery.YieldMap              `json:"yield"`
	Workspaces      []refinery.WorkspaceSummary    `json:"workspaces"`
	Recipes         []index.Recipe                 `json:"recipes"`
	Outputs         []index.RefineryOutput         `json:"outputs"`
	Runs            []index.RefineryRun            `json:"runs"`
	AgentProposals  []refinery.AgentProposal       `json:"agent_proposals"`
	Personalization refinery.PersonalizationReport `json:"personalization"`
	Updates         []refinery.Update              `json:"updates"`
}

// handleRefineryOverview is the product-level read model shared by the Home,
// Mine, Studio, Knowledge, Agent Forge, and Personalization journeys.
func (s *Server) handleRefineryOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	snap := s.cache.get(s)
	nuggets, err := s.db.Nuggets(index.NuggetQuery{Limit: 5000})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	totals, err := s.db.Aggregate("")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recipes, err := s.db.Recipes(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	outputs, err := s.db.RefineryOutputs("", 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runs, err := s.db.RefineryRuns("", 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	artifacts, _ := s.db.Artifacts(0)

	sources := sourceViews(snap.Sessions, nuggets)
	indexedAt := ""
	if !snap.IndexedAt.IsZero() {
		indexedAt = snap.IndexedAt.Format(time.RFC3339)
	}
	overview := refineryOverview{
		Onboarding:      len(nuggets) == 0 && totals.Assayed == 0,
		NeedsAssay:      totals.Assayed == 0,
		Sources:         sources,
		Yield:           refinery.AssessYield(nuggets, snap.Sessions, totals),
		Workspaces:      refinery.Workspaces(snap.Sessions, nuggets),
		Recipes:         recipes,
		Outputs:         outputs,
		Runs:            runs,
		AgentProposals:  refinery.AgentProposals(nuggets),
		Personalization: refinery.AssessPersonalization(nuggets),
		Updates:         refinery.Updates(recipes, outputs, nuggets),
		Stats: map[string]any{
			"sessions": len(snap.Sessions), "nuggets": len(nuggets),
			"assayed": totals.Assayed, "artifacts": len(artifacts),
			"recipes": len(recipes), "outputs": len(outputs),
			"indexed_at": indexedAt,
		},
	}
	setSnapshotHeader(w, snap)
	writeJSON(w, overview)
}

func sourceViews(sessions []core.Session, nuggets []index.Nugget) []refinerySourceView {
	names := map[string]string{
		string(core.ToolCopilot):  "Copilot CLI",
		string(core.ToolClaude):   "Claude Code",
		string(core.ToolOpencode): "OpenCode",
	}
	byTool := map[string]*refinerySourceView{}
	for tool, name := range names {
		byTool[tool] = &refinerySourceView{Tool: tool, Name: name}
	}
	for _, session := range sessions {
		item := byTool[string(session.Tool)]
		if item == nil {
			continue
		}
		item.Sessions++
		item.Ready = true
		if session.Updated.After(item.LastUpdated) {
			item.LastUpdated = session.Updated
		}
	}
	for _, nugget := range nuggets {
		item := byTool[nugget.Tool]
		if item != nil {
			item.Nuggets++
		}
	}
	order := []string{string(core.ToolCopilot), string(core.ToolClaude), string(core.ToolOpencode)}
	out := make([]refinerySourceView, 0, len(order))
	for _, tool := range order {
		out = append(out, *byTool[tool])
	}
	return out
}

// handleRefineryRecipe returns the complete production plan, including the
// exact evidence set and the current review/run state.
func (s *Server) handleRefineryRecipe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	recipe, err := s.db.Recipe(r.URL.Query().Get("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	evidence, err := s.db.NuggetsByIDs(recipe.EvidenceIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	candidates, err := s.db.Nuggets(index.NuggetQuery{Workspace: recipe.Workspace, Limit: 500})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	outputs, _ := s.db.RefineryOutputs(recipe.UID, 100)
	runs, _ := s.db.RefineryRuns(recipe.UID, 30)
	estimate, report := s.productionEstimate(recipe, evidence)
	writeJSON(w, map[string]any{
		"recipe": recipe, "evidence": evidence, "candidates": candidates,
		"evidence_report": report, "estimate": estimate,
		"estimate_text": productionEstimateText(recipe, estimate), "outputs": outputs, "runs": runs,
		"templates": refinery.Templates(),
	})
}

// handleRefineryOutput reads a generated draft and its provenance sidecar.
func (s *Server) handleRefineryOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	output, err := s.db.RefineryOutput(r.URL.Query().Get("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	path, err := safeRefineryFile(output.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	body := []byte(nil)
	if isTextOutputFormat(output.Format) {
		body, err = os.ReadFile(path)
		if err != nil {
			http.Error(w, "output file not found", http.StatusNotFound)
			return
		}
	} else if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
		http.Error(w, "output file not found", http.StatusNotFound)
		return
	}
	provenance := ""
	if output.ProvenancePath != "" {
		if path, err := safeRefineryFile(output.ProvenancePath); err == nil {
			if value, err := os.ReadFile(path); err == nil {
				provenance = string(value)
			}
		}
	}
	writeJSON(w, map[string]any{
		"output": output, "body": string(body), "provenance": provenance,
		"binary": !isTextOutputFormat(output.Format),
	})
}

func isTextOutputFormat(format string) bool {
	switch strings.ToLower(format) {
	case "png", "jpg", "jpeg", "gif", "webp", "mp4", "webm", "pdf":
		return false
	default:
		return true
	}
}

type refineryActionRequest struct {
	Action      string   `json:"action"`
	RecipeID    string   `json:"recipe_id"`
	OutputID    string   `json:"output_id"`
	Workspace   string   `json:"workspace"`
	Prompt      string   `json:"prompt"`
	Title       string   `json:"title"`
	Decision    string   `json:"decision"`
	Destination string   `json:"destination"`
	Body        string   `json:"body"`
	OutputKinds []string `json:"output_kinds"`
	EvidenceIDs []string `json:"evidence_ids"`
	ProposalID  string   `json:"proposal_id"`
}

// handleRefineryAction applies short, explicit state transitions. Expensive
// productions remain asynchronous jobs through /api/action.
func (s *Server) handleRefineryAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var req refineryActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	var (
		result any
		err    error
	)
	switch req.Action {
	case "preview_design":
		result, err = s.previewRecipe(req)
	case "design":
		result, err = s.designRecipe(req)
	case "update_recipe":
		result, err = s.updateRecipe(req)
	case "save_evidence":
		result, err = s.updateRecipeEvidence(req, false)
	case "approve_evidence":
		result, err = s.updateRecipeEvidence(req, true)
	case "review_output":
		result, err = s.reviewRefineryOutput(req)
	case "export_output":
		result, err = s.exportRefineryOutput(req)
	case "clone_recipe":
		result, err = s.cloneRecipe(req)
	case "archive_recipe":
		result, err = s.archiveRecipe(req)
	default:
		http.Error(w, "unknown refinery action: "+req.Action, http.StatusBadRequest)
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, result)
}

func (s *Server) designRecipe(req refineryActionRequest) (any, error) {
	recipe, err := s.buildRecipe(req)
	if err != nil {
		return nil, err
	}
	if err := s.db.PutRecipe(&recipe); err != nil {
		return nil, err
	}
	s.db.RecordOp("recipe_design", "", "", 0, 0, recipe.UID, true)
	return map[string]any{"recipe": recipe}, nil
}

func (s *Server) previewRecipe(req refineryActionRequest) (any, error) {
	recipe, err := s.buildRecipe(req)
	if err != nil {
		return nil, err
	}
	evidence, err := s.db.NuggetsByIDs(recipe.EvidenceIDs)
	if err != nil {
		return nil, err
	}
	estimate, report := s.productionEstimate(recipe, evidence)
	estimatedSeconds := 0
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			estimatedSeconds += 60
		}
	}
	return map[string]any{
		"recipe": recipe, "evidence_report": report, "estimate": estimate,
		"estimate_text": productionEstimateText(recipe, estimate),
		"backend":       "auto-detect signed-in CLI",
		"model":         "backend default", "estimated_seconds": estimatedSeconds,
	}, nil
}

// buildRecipe delegates to the canonical planner.
//
// The logic moved to internal/create so CLI, module and a future in-process
// kernel reach the same planning semantics. This face keeps only the mapping
// from its own request type -- which is exactly what a face should own.
func (s *Server) buildRecipe(req refineryActionRequest) (index.Recipe, error) {
	return create.Planner{DB: s.db}.Design(create.PlanRequest{
		Prompt:      req.Prompt,
		Workspace:   req.Workspace,
		OutputKinds: req.OutputKinds,
		EvidenceIDs: req.EvidenceIDs,
		Title:       req.Title,
	})
}

func (s *Server) updateRecipe(req refineryActionRequest) (any, error) {
	recipe, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		return nil, err
	}
	if recipe.Status == refinery.RecipeRunning || recipe.Status == refinery.RecipeArchived {
		return nil, fmt.Errorf("a %s recipe cannot be edited", recipe.Status)
	}
	if strings.TrimSpace(req.Title) != "" {
		recipe.Title = strings.TrimSpace(req.Title)
	}
	if strings.TrimSpace(req.Prompt) != "" {
		recipe.Request = strings.TrimSpace(req.Prompt)
	}
	if len(req.OutputKinds) > 0 {
		var outputs []index.RecipeOutputSpec
		for _, kind := range uniqueStrings(req.OutputKinds) {
			output, ok := refinery.FindTemplate(kind)
			if !ok {
				return nil, fmt.Errorf("unknown refinery output %q", kind)
			}
			outputs = append(outputs, output)
		}
		recipe.Outputs = outputs
	}
	recipe.Status = refinery.RecipeEvidenceReview
	recipe.ApprovedAt = time.Time{}
	if err := s.db.PutRecipe(&recipe); err != nil {
		return nil, err
	}
	return map[string]any{"recipe": recipe}, nil
}

func (s *Server) updateRecipeEvidence(req refineryActionRequest, approve bool) (any, error) {
	recipe, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		return nil, err
	}
	if recipe.Status == refinery.RecipeRunning || recipe.Status == refinery.RecipeArchived {
		return nil, fmt.Errorf("evidence cannot be changed while the recipe is %s", recipe.Status)
	}
	ids := uniqueStrings(req.EvidenceIDs)
	evidence, err := s.db.NuggetsByIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(evidence) != len(ids) {
		return nil, fmt.Errorf("one or more selected evidence items no longer exist")
	}
	recipe.EvidenceIDs = ids
	recipe.Status = refinery.RecipeEvidenceReview
	recipe.ApprovedAt = time.Time{}
	report := refinery.AssessEvidence(recipe, evidence)
	if approve {
		if report.Blocked {
			return nil, fmt.Errorf("the evidence set is blocked")
		}
		recipe.Status = refinery.RecipeApproved
		recipe.ApprovedAt = time.Now()
	}
	if err := s.db.PutRecipe(&recipe); err != nil {
		return nil, err
	}
	detail := "saved"
	if approve {
		detail = "approved"
	}
	s.db.RecordOp("evidence_"+detail, "", "", 0, 0,
		fmt.Sprintf("recipe=%s items=%d quality=%.1f", recipe.UID, len(ids), report.Quality), true)
	return map[string]any{"recipe": recipe, "evidence_report": report}, nil
}

func (s *Server) reviewRefineryOutput(req refineryActionRequest) (any, error) {
	output, err := s.db.RefineryOutput(req.OutputID)
	if err != nil {
		return nil, err
	}
	switch req.Decision {
	case refinery.OutputDraft, refinery.OutputReviewed, refinery.OutputRejected:
	default:
		return nil, fmt.Errorf("decision must be draft, reviewed, or rejected")
	}
	if req.Decision == refinery.OutputReviewed && strings.TrimSpace(req.Body) == "" {
		if isTextOutputFormat(output.Format) {
			return nil, fmt.Errorf("a reviewed output cannot be empty")
		}
		path, err := safeRefineryFile(output.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, fmt.Errorf("a reviewed binary output must be a non-empty owned file")
		}
	}
	selectedEvidenceIDs := req.EvidenceIDs
	if output.Format == "jsonl" && req.Body != "" {
		selectedEvidenceIDs, err = evidenceIDsFromJSONL(req.Body)
		if err != nil {
			return nil, err
		}
	}
	if selectedEvidenceIDs != nil {
		allowed := map[string]bool{}
		for _, id := range output.EvidenceIDs {
			allowed[id] = true
		}
		selected := uniqueStrings(selectedEvidenceIDs)
		for _, id := range selected {
			if !allowed[id] {
				return nil, fmt.Errorf("evidence %s is not part of this output", id)
			}
		}
		if req.Decision == refinery.OutputReviewed && len(selected) == 0 {
			return nil, fmt.Errorf("a reviewed structured output must retain at least one evidence item")
		}
		output.EvidenceIDs = selected
	}
	if req.Body != "" {
		path, err := safeRefineryFile(output.Path)
		if err != nil {
			return nil, err
		}
		body := redact.Text(req.Body).Text
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("save reviewed output: %w", err)
		}
	}
	if output.ProvenancePath != "" {
		if err := updateOutputProvenance(output, req.Decision); err != nil {
			return nil, err
		}
	}
	output.Status = req.Decision
	if req.Decision == refinery.OutputReviewed {
		output.ReviewedAt = time.Now()
	} else {
		output.ReviewedAt = time.Time{}
	}
	if err := s.db.PutRefineryOutput(&output); err != nil {
		return nil, err
	}
	s.db.RecordOp("output_review", "", "", 0, 0,
		fmt.Sprintf("output=%s decision=%s", output.UID, output.Status), true)
	s.completeRecipeIfReviewed(output.RecipeID)
	return map[string]any{"output": output}, nil
}

func evidenceIDsFromJSONL(body string) ([]string, error) {
	var ids []string
	for lineNo, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			return nil, fmt.Errorf("JSONL line %d is invalid: %w", lineNo+1, err)
		}
		ids = append(ids, evidenceIDsFromValue(value)...)
	}
	return uniqueStrings(ids), nil
}

func evidenceIDsFromValue(value any) []string {
	var out []string
	switch typed := value.(type) {
	case map[string]any:
		// JSON object iteration order is deliberately undefined in Go. Keep
		// evidence order stable so saving the same reviewed JSONL cannot
		// randomly reshuffle the output's evidence and provenance.
		idKeys := []string{"id", "evidence_id", "chosen_evidence_id", "rejected_evidence_id"}
		for _, key := range idKeys {
			if id, ok := typed[key].(string); ok && id != "" {
				out = append(out, id)
			}
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if key != "id" && key != "evidence_id" &&
				key != "chosen_evidence_id" && key != "rejected_evidence_id" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := typed[key]
			out = append(out, evidenceIDsFromValue(item)...)
		}
	case []any:
		for _, item := range typed {
			out = append(out, evidenceIDsFromValue(item)...)
		}
	}
	return out
}

func updateOutputProvenance(output index.RefineryOutput, status string) error {
	path, err := safeRefineryFile(output.ProvenancePath)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read output provenance: %w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("decode output provenance: %w", err)
	}
	selected := map[string]bool{}
	for _, id := range output.EvidenceIDs {
		selected[id] = true
	}
	if raw, ok := manifest["evidence"].([]any); ok {
		filtered := make([]any, 0, len(raw))
		for _, item := range raw {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := row["evidence_id"].(string)
			if selected[id] {
				filtered = append(filtered, row)
			}
		}
		manifest["evidence"] = filtered
	}
	manifest["status"] = status
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(updated, '\n'), 0o644); err != nil {
		return fmt.Errorf("write output provenance: %w", err)
	}
	return nil
}

func (s *Server) exportRefineryOutput(req refineryActionRequest) (any, error) {
	output, err := s.db.RefineryOutput(req.OutputID)
	if err != nil {
		return nil, err
	}
	if output.Status != refinery.OutputReviewed && output.Status != refinery.OutputExported {
		return nil, fmt.Errorf("review this output before exporting it")
	}
	destination := strings.TrimSpace(req.Destination)
	if destination == "" {
		destination = "local_vault"
	}
	if destination != "local_vault" {
		return nil, fmt.Errorf("%s export is not enabled; create a reviewed local draft first", destination)
	}
	recipe, err := s.db.Recipe(output.RecipeID)
	if err != nil {
		return nil, err
	}
	source, err := safeRefineryFile(output.Path)
	if err != nil {
		return nil, err
	}
	version := output.RunID
	if version == "" {
		version = output.UID
	}
	dir := filepath.Join(index.Dir(), "exports", refinery.Slug(recipe.Title), version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, filepath.Base(source))
	if err := copyFile(source, target); err != nil {
		return nil, err
	}
	provenanceTarget := ""
	if output.ProvenancePath != "" {
		if provenance, err := safeRefineryFile(output.ProvenancePath); err == nil {
			provenanceTarget = filepath.Join(dir, filepath.Base(provenance))
			if err := copyFile(provenance, provenanceTarget); err != nil {
				return nil, err
			}
		}
	}
	output.Status = refinery.OutputExported
	output.ExportedAt = time.Now()
	if err := s.db.PutRefineryOutput(&output); err != nil {
		return nil, err
	}
	s.db.RecordOp("output_export", "", "", 0, 0,
		fmt.Sprintf("output=%s destination=%s path=%s", output.UID, destination, target), true)
	s.completeRecipeIfReviewed(output.RecipeID)
	return map[string]any{
		"output": output, "destination": destination, "path": target,
		"provenance_path": provenanceTarget,
	}, nil
}

func (s *Server) cloneRecipe(req refineryActionRequest) (any, error) {
	source, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		return nil, err
	}
	nuggets, err := s.db.Nuggets(index.NuggetQuery{Workspace: source.Workspace, Limit: 5000})
	if err != nil {
		return nil, err
	}
	clone := source
	clone.UID = ""
	clone.Title = source.Title + " update"
	clone.Status = refinery.RecipeDraft
	clone.EvidenceIDs = refinery.DefaultEvidence(nuggets, source.Workspace, source.Outputs, 120)
	clone.CreatedAt = time.Now()
	clone.UpdatedAt = time.Now()
	clone.ApprovedAt = time.Time{}
	if err := s.db.PutRecipe(&clone); err != nil {
		return nil, err
	}
	return map[string]any{"recipe": clone}, nil
}

func (s *Server) archiveRecipe(req refineryActionRequest) (any, error) {
	recipe, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		return nil, err
	}
	if recipe.Status == refinery.RecipeRunning {
		return nil, fmt.Errorf("a running recipe cannot be archived")
	}
	recipe.Status = refinery.RecipeArchived
	if err := s.db.PutRecipe(&recipe); err != nil {
		return nil, err
	}
	return map[string]any{"recipe": recipe}, nil
}

func (s *Server) completeRecipeIfReviewed(recipeID string) {
	recipe, err := s.db.Recipe(recipeID)
	if err != nil {
		return
	}
	runs, err := s.db.RefineryRuns(recipeID, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != "done" {
		return
	}
	outputs, err := s.db.RefineryOutputs(recipeID, 0)
	if err != nil {
		return
	}
	var current []index.RefineryOutput
	for _, output := range outputs {
		if output.RunID == runs[0].UID {
			current = append(current, output)
		}
	}
	if len(current) != len(recipe.Outputs) {
		if recipe.Status == refinery.RecipeComplete {
			recipe.Status = refinery.RecipeReview
			_ = s.db.PutRecipe(&recipe)
		}
		return
	}
	allFinal := true
	for _, output := range current {
		switch output.Status {
		case refinery.OutputReviewed, refinery.OutputRejected, refinery.OutputExported:
		default:
			allFinal = false
		}
	}
	if allFinal {
		recipe.Status = refinery.RecipeComplete
	} else {
		recipe.Status = refinery.RecipeReview
	}
	_ = s.db.PutRecipe(&recipe)
}

// doMine performs the free deterministic first pass: refresh scoped session
// metadata, classify transcripts, and calculate the potential yield. It never
// invokes a model.
func mineScope(req actionRequest) core.Scope {
	scope := req.scope()
	if scope.Days <= 0 && req.SessionID == "" &&
		len(req.SessionIDs) == 0 && len(req.SessionKeys) == 0 {
		scope.Days = 30
	}
	return scope
}

func collectMineSessions(req actionRequest, scope core.Scope) ([]core.Session, []error) {
	if len(req.SessionKeys) > 0 {
		// Exact keys carry the tool identity, so never enumerate every
		// installed adapter just to filter the selection afterward.
		return adapter.CollectExact(req.SessionKeys)
	}
	sessions, collected := adapter.CollectDetailed(scope)
	return req.filterExactSessions(sessions), collected.Errors
}

func (s *Server) doMine(id string, req actionRequest) (any, error) {
	scope := mineScope(req)
	lock, err := s.acquireScanLockForJob(id, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	s.jobs.update(id, func(job *Job) { job.Progress = "reading session sources" })
	sessions, collectionErrors := collectMineSessions(req, scope)
	if (len(req.SessionKeys) > 0 || len(sessions) == 0) && len(collectionErrors) > 0 {
		return nil, collectionErrors[0]
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions in scope to mine")
	}
	generation, err := s.db.NextScanGeneration()
	if err != nil {
		return nil, err
	}
	if err := s.db.PutSessionsWithGeneration(sessions, generation, time.Now()); err != nil {
		return nil, err
	}

	result := struct {
		Sessions     int      `json:"sessions"`
		Assayed      int      `json:"assayed"`
		Skipped      int      `json:"skipped_unchanged"`
		NoTranscript int      `json:"no_transcript"`
		Failed       int      `json:"failed"`
		Bytes        int64    `json:"bytes_assayed"`
		Errors       []string `json:"errors,omitempty"`
		Free         bool     `json:"free"`
	}{Sessions: len(sessions), Free: true}

	for i, session := range sessions {
		s.jobs.update(id, func(job *Job) {
			job.Progress = fmt.Sprintf("assaying %d/%d  %s", i+1, len(sessions),
				core.Truncate(session.Title, 40))
		})
		if session.Tool != core.ToolOpencode &&
			(session.TranscriptPath == "" || !fileExists(session.TranscriptPath)) {
			result.NoTranscript++
			result.Failed++
			continue
		}
		sourceBytes, sourceMtime := index.SourceStamp(session)
		if s.db.ManifestFresh(string(session.Tool), session.ID, sourceBytes, sourceMtime) {
			result.Skipped++
			continue
		}
		assayer, ok := adapter.Find(session.Tool).(adapter.Assayer)
		if !ok {
			result.Failed++
			continue
		}
		manifest, err := assayer.Assay(session, 200)
		if err != nil {
			result.Failed++
			continue
		}
		if err := s.db.PutManifest(manifest, sourceBytes, sourceMtime); err != nil {
			result.Failed++
			continue
		}
		result.Assayed++
		result.Bytes += manifest.TotalBytes
	}
	for _, err := range collectionErrors {
		result.Errors = append(result.Errors, err.Error())
	}
	if err := mineAllWorkFailed(result.Sessions, result.Assayed, result.Skipped, result.Failed); err != nil {
		return nil, err
	}
	s.cache.invalidate()
	return result, nil
}

// mineAllWorkFailed prevents a completed status when every eligible assay
// failed. An unchanged manifest is a valid no-op; a failed assay is not.
func mineAllWorkFailed(sessions, assayed, skipped, failed int) error {
	if sessions == 0 {
		return fmt.Errorf("no sessions in scope to mine")
	}
	if assayed == 0 && skipped == 0 && failed > 0 {
		return fmt.Errorf("mine failed: all %d eligible session mining steps failed", failed)
	}
	return nil
}

func (s *Server) acquireScanLockForJob(jobID string, maxWait time.Duration) (*index.ScanLock, error) {
	deadline := time.Now().Add(maxWait)
	for {
		lock, err := s.db.AcquireScanLock()
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, index.ErrScanLocked) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("source refresh is still running after %s; try again once it completes", maxWait.Round(time.Second))
		}
		s.jobs.update(jobID, func(job *Job) {
			job.Progress = "finishing the source refresh before mining"
		})
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Server) productionEstimate(recipe index.Recipe, evidence []index.Nugget) (cost.Estimate, refinery.EvidenceReport) {
	report := refinery.AssessEvidence(recipe, evidence)
	modelOutputs := 0
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			modelOutputs++
		}
	}
	if modelOutputs == 0 {
		return cost.Estimate{Op: "refinery"}, report
	}
	raw := len(refinery.EvidencePreamble(recipe, evidence))/4 + 400
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			raw += len(refinery.OutputRequest(output))/4 + 350
		}
	}
	stats, _ := s.db.CalibrationFor("refinery")
	return cost.Predict("refinery", raw, stats), report
}

func productionEstimateText(recipe index.Recipe, estimate cost.Estimate) string {
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			return estimate.String()
		}
	}
	return "free local production · 0 model calls"
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// doProduction generates every approved output from one warm model context,
// while deterministic packs remain free. It always stops at draft review.
func (s *Server) doProduction(jobID string, req actionRequest) (_ any, returnErr error) {
	recipe, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		return nil, err
	}
	evidence, err := s.db.NuggetsByIDs(recipe.EvidenceIDs)
	if err != nil {
		return nil, err
	}
	estimate, report := s.productionEstimate(recipe, evidence)
	s.jobs.update(jobID, func(job *Job) { job.Estimate = &estimate })
	if !req.Apply {
		estimatedSeconds := 0
		for _, output := range recipe.Outputs {
			if output.RequiresModel {
				estimatedSeconds += 60
			}
		}
		return map[string]any{
			"preview": true, "recipe": recipe, "evidence_report": report,
			"estimate": estimate, "estimate_text": productionEstimateText(recipe, estimate),
			"backend":           valueOr(req.Backend, "auto-detect signed-in CLI"),
			"model":             valueOr(req.Model, "backend default"),
			"estimated_seconds": estimatedSeconds,
		}, nil
	}

	if recipe.Status != refinery.RecipeApproved && recipe.Status != refinery.RecipeFailed {
		return nil, fmt.Errorf("approve the evidence before running this recipe")
	}
	if report.Blocked {
		return nil, fmt.Errorf("the approved evidence is no longer available")
	}
	if len(evidence) != len(recipe.EvidenceIDs) {
		return nil, fmt.Errorf("the evidence set changed; review it again before running")
	}
	claimed, err := s.db.ClaimRecipeForRun(recipe.UID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, fmt.Errorf("this recipe is already running or is no longer approved")
	}
	recipe.Status = refinery.RecipeRunning

	run := index.RefineryRun{
		RecipeID: recipe.UID, Status: "running", Estimate: estimate,
		Backend: req.Backend, Model: req.Model, Stages: productionStages(recipe.Outputs),
	}
	defer func() {
		if returnErr == nil {
			return
		}
		failActiveStage(&run, returnErr.Error())
		run.Status = "failed"
		run.Error = returnErr.Error()
		run.EndedAt = time.Now()
		_ = s.db.PutRefineryRun(&run)
		recipe.Status = refinery.RecipeFailed
		_ = s.db.PutRecipe(&recipe)
	}()
	if err := s.db.PutRefineryRun(&run); err != nil {
		return nil, err
	}

	setStage(&run, "evidence", "running", fmt.Sprintf("%d approved items", len(evidence)))
	_ = s.db.PutRefineryRun(&run)
	s.jobs.update(jobID, func(job *Job) { job.Progress = "loading approved evidence" })
	setStage(&run, "evidence", "done", fmt.Sprintf("%d items · %.1f%% quality", len(evidence), report.Quality))
	_ = s.db.PutRefineryRun(&run)

	modelOutputs := 0
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			modelOutputs++
		}
	}

	var (
		conversation *agentexec.Conversation
		modelLabel   = "deterministic"
		ledger       *cost.Run
		ledgerDone   bool
	)
	var generated []index.RefineryOutput
	defer func() {
		if ledger == nil || ledgerDone {
			return
		}
		ledger.Items = len(generated)
		ledger.EndedAt = time.Now()
		ledger.OK = returnErr == nil
		if returnErr != nil {
			ledger.Note = returnErr.Error()
		}
		// A failed ledger write is REPORTED, never discarded. The work is done
		// and the user holds the output, so failing the job over bookkeeping
		// would be the wrong trade -- but a silently unrecorded spend is how
		// `midden cost` quietly stops matching what was actually paid for.
		//
		// The module face already does this (internal/module/ledger.go). This
		// face discarded the same error, so the same failure was visible
		// through one door and invisible through the other.
		if err := s.db.PutRun(*ledger); err != nil {
			s.jobs.update(jobID, func(job *Job) {
				job.Progress = "model spend was NOT recorded in the cost ledger: " +
					err.Error() + ". The work completed; only the accounting " +
					"failed, so `midden cost` will under-report."
			})
		}
		time.Sleep(1500 * time.Millisecond)
		s.settle(jobID, ledger.UID)
	}()
	if modelOutputs > 0 {
		backend, err := agentexec.Detect(req.Backend)
		if err != nil {
			return nil, err
		}
		runner := &agentexec.Runner{Backend: backend, Model: req.Model, Pure: true, Timeout: 20 * time.Minute}
		conversation = runner.NewConversation()
		modelLabel = req.Model
		if modelLabel == "" {
			modelLabel = string(backend) + ":default"
		}
		run.Backend, run.Model = string(backend), modelLabel
		ledger = &cost.Run{
			UID: index.NewUID(), Op: "refinery", Scope: recipe.Title,
			Backend: string(backend), EstTokens: estimate.RawTokens,
			StartedAt: time.Now(), CLISessions: []string{conversation.SessionID()},
		}
		if err := s.db.PutRun(*ledger); err != nil {
			return nil, fmt.Errorf("record production cost ledger: %w", err)
		}
		setStage(&run, "context", "running", "one warm context for every narrative output")
		_ = s.db.PutRefineryRun(&run)
		s.jobs.update(jobID, func(job *Job) { job.Progress = "loading one warm evidence context" })
		if _, err := conversation.Prime(context.Background(), refinery.EvidencePreamble(recipe, evidence)); err != nil {
			return nil, fmt.Errorf("load evidence context: %w", err)
		}
		setStage(&run, "context", "done", "context loaded once")
		_ = s.db.PutRefineryRun(&run)
	} else {
		setStage(&run, "context", "done", "all selected outputs are deterministic local packs")
		_ = s.db.PutRefineryRun(&run)
	}

	dir := filepath.Join(index.Dir(), "artifacts", "refinery",
		refinery.Slug(recipe.Title)+"-"+shortID(recipe.UID), shortID(run.UID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	for i, spec := range recipe.Outputs {
		stageKey := fmt.Sprintf("output-%d", i)
		setStage(&run, stageKey, "running", spec.Maker)
		_ = s.db.PutRefineryRun(&run)
		s.jobs.update(jobID, func(job *Job) {
			job.Progress = fmt.Sprintf("creating %d/%d  %s", i+1, len(recipe.Outputs), spec.Title)
		})

		body, deterministic, err := refinery.DeterministicOutput(spec, recipe, evidence)
		outputModel := "deterministic"
		if err != nil {
			return nil, err
		}
		if !deterministic {
			if conversation == nil {
				return nil, fmt.Errorf("%s requires a model backend", spec.Title)
			}
			response, err := conversation.Ask(context.Background(), refinery.OutputRequest(spec))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", spec.Title, err)
			}
			body = refine.CleanOutput(response.Output)
			if strings.TrimSpace(body) == "" {
				return nil, fmt.Errorf("%s returned an empty draft", spec.Title)
			}
			outputModel = modelLabel
		}
		redactionSummary := ""
		if scan := redact.Scan(body); len(scan) > 0 {
			redacted := redact.Text(body)
			body = redacted.Text
			redactionSummary = redact.Summary(scan)
		}
		body = refinery.WrapOutput(spec, recipe, body, outputModel, len(evidence))
		fileName := fmt.Sprintf("%02d-%s%s", i+1, refinery.Slug(spec.Title), refinery.FileExtension(spec))
		path := filepath.Join(dir, fileName)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return nil, err
		}
		provenancePath := path + ".provenance.json"
		provenance, err := productionProvenance(recipe, run, spec, evidence, outputModel, redactionSummary)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(provenancePath, provenance, 0o644); err != nil {
			return nil, err
		}

		output := index.RefineryOutput{
			RecipeID: recipe.UID, RunID: run.UID, Kind: spec.Kind,
			Title: spec.Title, Maker: spec.Maker, Format: spec.Format,
			Status: refinery.OutputDraft, Path: path, ProvenancePath: provenancePath,
			EvidenceIDs: append([]string(nil), recipe.EvidenceIDs...), Quality: report.Quality,
		}
		if err := s.db.PutRefineryOutput(&output); err != nil {
			return nil, err
		}
		if err := s.db.PutArtifact(index.Artifact{
			Kind: "refinery:" + spec.Kind, Title: spec.Title, Path: path,
			Scope: recipe.Workspace, NuggetIDs: recipe.EvidenceIDs, Model: outputModel,
		}); err != nil {
			return nil, err
		}
		generated = append(generated, output)
		setStage(&run, stageKey, "done", "draft written with provenance")
		_ = s.db.PutRefineryRun(&run)
	}

	if ledger != nil {
		ledger.Items = len(generated)
		ledger.EndedAt = time.Now()
		ledger.OK = true
		if err := s.db.PutRun(*ledger); err != nil {
			return nil, fmt.Errorf("finalize production cost ledger: %w", err)
		}
		s.jobs.update(jobID, func(job *Job) { job.Progress = "settling production cost" })
		time.Sleep(1500 * time.Millisecond)
		s.settle(jobID, ledger.UID)
		ledgerDone = true
	}

	setStage(&run, "review", "waiting", fmt.Sprintf("%d draft(s) require human review", len(generated)))
	run.Status = "done"
	run.EndedAt = time.Now()
	if err := s.db.PutRefineryRun(&run); err != nil {
		return nil, err
	}
	recipe.Status = refinery.RecipeReview
	if err := s.db.PutRecipe(&recipe); err != nil {
		return nil, err
	}
	s.db.RecordOp("production", "", "", 0, 0,
		fmt.Sprintf("recipe=%s run=%s outputs=%d", recipe.UID, run.UID, len(generated)), true)
	s.cache.invalidate()
	return map[string]any{
		"recipe": recipe, "run": run, "outputs": generated,
		"review_required": true,
	}, nil
}

func productionStages(outputs []index.RecipeOutputSpec) []index.RefineryRunStage {
	stages := []index.RefineryRunStage{
		{Key: "evidence", Label: "Load approved evidence", Status: "queued"},
		{Key: "context", Label: "Prepare production context", Status: "queued"},
	}
	for i, output := range outputs {
		stages = append(stages, index.RefineryRunStage{
			Key: fmt.Sprintf("output-%d", i), Label: "Create " + output.Title, Status: "queued",
		})
	}
	return append(stages, index.RefineryRunStage{
		Key: "review", Label: "Human review and export", Status: "queued",
	})
}

func setStage(run *index.RefineryRun, key, status, detail string) {
	now := time.Now()
	for i := range run.Stages {
		if run.Stages[i].Key != key {
			continue
		}
		run.Stages[i].Status = status
		run.Stages[i].Detail = detail
		if status == "running" && run.Stages[i].StartedAt.IsZero() {
			run.Stages[i].StartedAt = now
		}
		if status == "done" || status == "failed" || status == "waiting" {
			if run.Stages[i].StartedAt.IsZero() {
				run.Stages[i].StartedAt = now
			}
			run.Stages[i].EndedAt = now
		}
		return
	}
}

func failActiveStage(run *index.RefineryRun, detail string) {
	for i := range run.Stages {
		if run.Stages[i].Status == "running" {
			setStage(run, run.Stages[i].Key, "failed", detail)
			return
		}
	}
}

func productionProvenance(recipe index.Recipe, run index.RefineryRun,
	spec index.RecipeOutputSpec, evidence []index.Nugget, model, redactions string) ([]byte, error) {
	items := make([]map[string]any, 0, len(evidence))
	for _, nugget := range evidence {
		items = append(items, map[string]any{
			"evidence_id": nugget.UID, "kind": nugget.Kind, "title": nugget.Title,
			"tool": nugget.Tool, "session_id": nugget.SessionID,
			"turn_ref": nugget.TurnRef, "confidence": nugget.Confidence,
			"redacted": nugget.Redacted, "extraction_model": nugget.Model,
		})
	}
	return json.MarshalIndent(map[string]any{
		"recipe_id": recipe.UID, "run_id": run.UID, "output_kind": spec.Kind,
		"title": spec.Title, "maker": spec.Maker, "model": model,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"status":       "draft", "redaction_summary": redactions,
		"raw_transcripts_included": false, "evidence": items,
	}, "", "  ")
}

type refineryConnectionView struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Outcome     string `json:"outcome"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Cost        string `json:"cost"`
	Action      string `json:"action"`
	Managed     bool   `json:"managed"`
}

// handleRefineryConnections groups capabilities by the value they unlock.
// Loading it performs local PATH checks only; service probes remain explicit.
func (s *Server) handleRefineryConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	connections := []refineryConnectionView{
		{ID: "local-vault", Category: "Knowledge and destinations", Name: "Local vault / Obsidian", Description: "Export reviewed Markdown, source assets, and provenance to an owner-controlled folder.", Outcome: "Durable local knowledge", Status: "ready", Detail: "Built in", Cost: "free", Action: "Export after review"},
		toolConnection("pandoc", "Books and documents", "Pandoc", "Render reviewed Markdown to HTML, PDF, EPUB, or DOCX.", "Multi-format documents", "free", "pandoc"),
		toolConnection("quarto", "Books and documents", "Quarto", "Build book sites, reports, and previewable project documentation.", "Books and reports", "free", "quarto"),
		toolConnection("marp", "Slides and visuals", "Marp", "Render reviewed slide Markdown to HTML or PDF.", "Presentation decks", "free", "marp"),
		toolConnection("d2", "Slides and visuals", "D2", "Render evidence-grounded architecture diagrams from text source.", "Technical diagrams", "free", "d2"),
		toolConnection("typst", "Books and documents", "Typst", "Typeset polished reports and handbooks locally.", "Polished PDF", "free", "typst"),
		toolConnection("promptfoo", "Agent improvement", "Promptfoo", "Compare current and proposed agent behavior against evidence-derived cases.", "Evaluation reports", "spends", "promptfoo"),
		{ID: "anki", Category: "Knowledge and destinations", Name: "Anki", Description: "Export reviewed tab-separated flashcards; Anki-Connect remains an optional local connection.", Outcome: "Spaced repetition", Status: "ready", Detail: "File export is built in", Cost: "free", Action: "Create a learning recipe"},
		{ID: "github", Category: "Publishing destinations", Name: "GitHub", Description: "Reviewed local drafts can later become branches, issues, or release drafts.", Outcome: "Repository publishing", Status: pathStatus("gh"), Detail: pathDetail("gh"), Cost: "free", Action: "Local draft first"},
		{ID: "training", Category: "Personal model lab", Name: "Unsloth / LLaMA Factory / Axolotl", Description: "Receive privacy-reviewed JSONL and manifests only after readiness gates pass.", Outcome: "Optional training handoff", Status: "guided", Detail: "Exports data and configuration; training never starts automatically.", Cost: "lab", Action: "View readiness"},
	}
	managed, err := s.integrationViews()
	if err == nil {
		for _, item := range managed {
			category, outcome := "Knowledge and destinations", "Private notebook and audio"
			if item.ID == "openmontage" {
				category, outcome = "Video", "Rendered video production"
			}
			status := item.State
			if status == "connected" {
				status = "ready"
			}
			connections = append(connections, refineryConnectionView{
				ID: item.ID, Category: category, Name: item.Name,
				Description: item.Description, Outcome: outcome, Status: status,
				Detail: item.StateDetail, Cost: item.Cost, Action: "Configure or test",
				Managed: true,
			})
		}
	}
	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].Category != connections[j].Category {
			return connections[i].Category < connections[j].Category
		}
		return connections[i].Name < connections[j].Name
	})
	writeJSON(w, connections)
}

func toolConnection(id, category, name, description, outcome, costClass, binary string) refineryConnectionView {
	return refineryConnectionView{
		ID: id, Category: category, Name: name, Description: description,
		Outcome: outcome, Status: pathStatus(binary), Detail: pathDetail(binary),
		Cost: costClass, Action: "Detect or install",
	}
}

func pathStatus(binary string) string {
	if _, err := osexec.LookPath(binary); err == nil {
		return "ready"
	}
	return "not_installed"
}

func pathDetail(binary string) string {
	if path, err := osexec.LookPath(binary); err == nil {
		return path
	}
	return binary + " is not on PATH"
}

func safeRefineryFile(path string) (string, error) {
	root, err := filepath.Abs(filepath.Join(index.Dir(), "artifacts", "refinery"))
	if err != nil {
		return "", fmt.Errorf("refinery artifacts unavailable")
	}
	want, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid output path")
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}
	if realWant, err := filepath.EvalSymlinks(want); err == nil {
		want = realWant
	}
	rel, err := filepath.Rel(root, want)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("output is outside the refinery artifacts directory")
	}
	return want, nil
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
