package index

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/cost"
)

const refinerySchema = `
CREATE TABLE IF NOT EXISTS refinery_recipes (
  uid          TEXT PRIMARY KEY,
  title        TEXT NOT NULL,
  workspace    TEXT,
  request      TEXT,
  status       TEXT NOT NULL,
  outputs      TEXT NOT NULL,
  evidence_ids TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  approved_at  INTEGER
);
CREATE INDEX IF NOT EXISTS idx_refinery_recipes_updated
  ON refinery_recipes(updated_at DESC);

CREATE TABLE IF NOT EXISTS refinery_runs (
  uid         TEXT PRIMARY KEY,
  recipe_id   TEXT NOT NULL,
  status      TEXT NOT NULL,
  backend     TEXT,
  model       TEXT,
  estimate    TEXT,
  stages      TEXT NOT NULL,
  error       TEXT,
  started_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  ended_at    INTEGER,
  FOREIGN KEY(recipe_id) REFERENCES refinery_recipes(uid) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_refinery_runs_recipe
  ON refinery_runs(recipe_id, started_at DESC);

CREATE TABLE IF NOT EXISTS refinery_outputs (
  uid             TEXT PRIMARY KEY,
  recipe_id       TEXT NOT NULL,
  run_id          TEXT,
  kind            TEXT NOT NULL,
  title           TEXT NOT NULL,
  maker           TEXT,
  format          TEXT,
  status          TEXT NOT NULL,
  path            TEXT NOT NULL,
  provenance_path TEXT,
  evidence_ids    TEXT NOT NULL,
  quality         REAL,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  reviewed_at     INTEGER,
  exported_at     INTEGER,
  FOREIGN KEY(recipe_id) REFERENCES refinery_recipes(uid) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_refinery_outputs_recipe
  ON refinery_outputs(recipe_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_refinery_outputs_status
  ON refinery_outputs(status, updated_at DESC);
`

// RecipeOutputSpec is one deliverable in a refinery production plan.
type RecipeOutputSpec struct {
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Audience      string `json:"audience"`
	Maker         string `json:"maker"`
	Format        string `json:"format"`
	CostClass     string `json:"cost_class"`
	RequiresModel bool   `json:"requires_model"`
}

// Recipe is a saved, reviewable multi-output production plan.
type Recipe struct {
	UID         string             `json:"uid"`
	Title       string             `json:"title"`
	Workspace   string             `json:"workspace"`
	Request     string             `json:"request"`
	Status      string             `json:"status"`
	Outputs     []RecipeOutputSpec `json:"outputs"`
	EvidenceIDs []string           `json:"evidence_ids"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	ApprovedAt  time.Time          `json:"approved_at,omitempty"`
}

// RefineryRunStage is one visible checkpoint in a production run.
type RefineryRunStage struct {
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// RefineryRun persists a production timeline beyond the lifetime of the UI.
type RefineryRun struct {
	UID       string             `json:"uid"`
	RecipeID  string             `json:"recipe_id"`
	Status    string             `json:"status"`
	Backend   string             `json:"backend,omitempty"`
	Model     string             `json:"model,omitempty"`
	Estimate  cost.Estimate      `json:"estimate"`
	Stages    []RefineryRunStage `json:"stages"`
	Error     string             `json:"error,omitempty"`
	StartedAt time.Time          `json:"started_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	EndedAt   time.Time          `json:"ended_at,omitempty"`
}

func (r RefineryRun) MarshalJSON() ([]byte, error) {
	type wire RefineryRun
	value := wire(r)
	if value.Stages == nil {
		value.Stages = []RefineryRunStage{}
	}
	return json.Marshal(value)
}

// RefineryOutput is one generated draft and its review/export state.
type RefineryOutput struct {
	UID            string    `json:"uid"`
	RecipeID       string    `json:"recipe_id"`
	RunID          string    `json:"run_id,omitempty"`
	Kind           string    `json:"kind"`
	Title          string    `json:"title"`
	Maker          string    `json:"maker"`
	Format         string    `json:"format"`
	Status         string    `json:"status"`
	Path           string    `json:"path"`
	ProvenancePath string    `json:"provenance_path,omitempty"`
	EvidenceIDs    []string  `json:"evidence_ids"`
	Quality        float64   `json:"quality"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ReviewedAt     time.Time `json:"reviewed_at,omitempty"`
	ExportedAt     time.Time `json:"exported_at,omitempty"`
}

func (d *DB) migrateRefinery() error {
	_, err := d.sql.Exec(refinerySchema)
	return err
}

// PutRecipe inserts or replaces a recipe while preserving its original
// creation time. The pointer is updated with generated ids and timestamps.
func (d *DB) PutRecipe(recipe *Recipe) error {
	if recipe == nil {
		return fmt.Errorf("recipe is required")
	}
	now := time.Now()
	if recipe.UID == "" {
		recipe.UID = NewUID()
	}
	if recipe.CreatedAt.IsZero() {
		recipe.CreatedAt = now
	}
	recipe.UpdatedAt = now
	if recipe.Status == "" {
		recipe.Status = "draft"
	}
	outputs, err := json.Marshal(recipe.Outputs)
	if err != nil {
		return fmt.Errorf("encode recipe outputs: %w", err)
	}
	evidence, err := json.Marshal(recipe.EvidenceIDs)
	if err != nil {
		return fmt.Errorf("encode recipe evidence: %w", err)
	}
	_, err = d.sql.Exec(`
		INSERT INTO refinery_recipes
		  (uid,title,workspace,request,status,outputs,evidence_ids,created_at,updated_at,approved_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
		  title=excluded.title, workspace=excluded.workspace, request=excluded.request,
		  status=excluded.status, outputs=excluded.outputs, evidence_ids=excluded.evidence_ids,
		  updated_at=excluded.updated_at, approved_at=excluded.approved_at`,
		recipe.UID, recipe.Title, recipe.Workspace, recipe.Request, recipe.Status,
		string(outputs), string(evidence), recipe.CreatedAt.Unix(), recipe.UpdatedAt.Unix(),
		unixSeconds(recipe.ApprovedAt))
	return err
}

// ClaimRecipeForRun atomically moves an approved or failed recipe into the
// running state. The compare-and-set prevents duplicate spend when two tabs
// or processes try to start the same recipe at once.
func (d *DB) ClaimRecipeForRun(uid string) (bool, error) {
	result, err := d.sql.Exec(`
		UPDATE refinery_recipes
		SET status = 'running', updated_at = ?
		WHERE uid = ? AND status IN ('approved','failed')`,
		time.Now().Unix(), uid)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// Recipe returns one saved production plan.
// ClaimRecipeForDraft permits reversible local authoring without fabricating
// evidence approval. Approval timestamps and host receipts are left unchanged.
func (d *DB) ClaimRecipeForDraft(uid string) (bool, error) {
	result, err := d.sql.Exec(`UPDATE refinery_recipes SET status='running',updated_at=?
WHERE uid=? AND status IN ('draft','evidence_review','review','complete','approved','failed')`, time.Now().Unix(), uid)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (d *DB) Recipe(uid string) (Recipe, error) {
	var recipe Recipe
	var outputs, evidence string
	var created, updated, approved int64
	err := d.sql.QueryRow(`
		SELECT uid,title,COALESCE(workspace,''),COALESCE(request,''),status,
		       outputs,evidence_ids,created_at,updated_at,COALESCE(approved_at,0)
		FROM refinery_recipes WHERE uid = ?`, uid).
		Scan(&recipe.UID, &recipe.Title, &recipe.Workspace, &recipe.Request,
			&recipe.Status, &outputs, &evidence, &created, &updated, &approved)
	if err != nil {
		return recipe, err
	}
	if err := json.Unmarshal([]byte(outputs), &recipe.Outputs); err != nil {
		return recipe, fmt.Errorf("decode recipe outputs: %w", err)
	}
	if err := json.Unmarshal([]byte(evidence), &recipe.EvidenceIDs); err != nil {
		return recipe, fmt.Errorf("decode recipe evidence: %w", err)
	}
	recipe.CreatedAt = time.Unix(created, 0)
	recipe.UpdatedAt = time.Unix(updated, 0)
	if approved > 0 {
		recipe.ApprovedAt = time.Unix(approved, 0)
	}
	return recipe, nil
}

// Recipes lists saved production plans, newest first.
func (d *DB) Recipes(limit int) ([]Recipe, error) {
	query := `
		SELECT uid,title,COALESCE(workspace,''),COALESCE(request,''),status,
		       outputs,evidence_ids,created_at,updated_at,COALESCE(approved_at,0)
		FROM refinery_recipes WHERE status != 'chat' ORDER BY updated_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	recipes := make([]Recipe, 0)
	for rows.Next() {
		var recipe Recipe
		var outputs, evidence string
		var created, updated, approved int64
		if err := rows.Scan(&recipe.UID, &recipe.Title, &recipe.Workspace,
			&recipe.Request, &recipe.Status, &outputs, &evidence, &created,
			&updated, &approved); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(outputs), &recipe.Outputs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidence), &recipe.EvidenceIDs); err != nil {
			return nil, err
		}
		recipe.CreatedAt = time.Unix(created, 0)
		recipe.UpdatedAt = time.Unix(updated, 0)
		if approved > 0 {
			recipe.ApprovedAt = time.Unix(approved, 0)
		}
		recipes = append(recipes, recipe)
	}
	return recipes, rows.Err()
}

func (d *DB) RecipesPage(limit, offset int, search string, excludeArchived bool) ([]Recipe, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := []string{`status != 'chat'`}
	args := []any{}
	if excludeArchived {
		where = append(where, `status != 'archived'`)
	}
	if strings.TrimSpace(search) != "" {
		pattern := "%" + escapeLike(strings.TrimSpace(search)) + "%"
		where = append(where,
			`(title LIKE ? ESCAPE '\' OR workspace LIKE ? ESCAPE '\' OR request LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM refinery_recipes`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `
		SELECT uid,title,COALESCE(workspace,''),COALESCE(request,''),status,
		       outputs,evidence_ids,created_at,updated_at,COALESCE(approved_at,0)
		FROM refinery_recipes` + whereSQL + `
		ORDER BY updated_at DESC, uid DESC LIMIT ? OFFSET ?`
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := d.sql.Query(query, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	recipes := make([]Recipe, 0, limit)
	for rows.Next() {
		var recipe Recipe
		var outputs, evidence string
		var created, updated, approved int64
		if err := rows.Scan(&recipe.UID, &recipe.Title, &recipe.Workspace,
			&recipe.Request, &recipe.Status, &outputs, &evidence, &created,
			&updated, &approved); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(outputs), &recipe.Outputs); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(evidence), &recipe.EvidenceIDs); err != nil {
			return nil, 0, err
		}
		recipe.CreatedAt = time.Unix(created, 0)
		recipe.UpdatedAt = time.Unix(updated, 0)
		if approved > 0 {
			recipe.ApprovedAt = time.Unix(approved, 0)
		}
		recipes = append(recipes, recipe)
	}
	return recipes, total, rows.Err()
}

// PutRefineryRun persists the current production timeline.
func (d *DB) PutRefineryRun(run *RefineryRun) error {
	if run == nil {
		return fmt.Errorf("run is required")
	}
	now := time.Now()
	if run.UID == "" {
		run.UID = NewUID()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.UpdatedAt = now
	if run.Status == "" {
		run.Status = "queued"
	}
	estimate, err := json.Marshal(run.Estimate)
	if err != nil {
		return err
	}
	stages, err := json.Marshal(run.Stages)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`
		INSERT INTO refinery_runs
		  (uid,recipe_id,status,backend,model,estimate,stages,error,started_at,updated_at,ended_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
		  status=excluded.status, backend=excluded.backend, model=excluded.model,
		  estimate=excluded.estimate, stages=excluded.stages, error=excluded.error,
		  updated_at=excluded.updated_at, ended_at=excluded.ended_at`,
		run.UID, run.RecipeID, run.Status, run.Backend, run.Model,
		string(estimate), string(stages), run.Error, run.StartedAt.Unix(),
		run.UpdatedAt.Unix(), unixSeconds(run.EndedAt))
	return err
}

// RefineryRuns lists production runs, optionally for one recipe.
func (d *DB) RefineryRuns(recipeID string, limit int) ([]RefineryRun, error) {
	query := `
		SELECT uid,recipe_id,status,COALESCE(backend,''),COALESCE(model,''),
		       COALESCE(estimate,'{}'),stages,COALESCE(error,''),
		       started_at,updated_at,COALESCE(ended_at,0)
		FROM refinery_runs`
	var args []any
	if recipeID != "" {
		query += " WHERE recipe_id = ?"
		args = append(args, recipeID)
	}
	query += " ORDER BY started_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]RefineryRun, 0)
	for rows.Next() {
		var run RefineryRun
		var estimate, stages string
		var started, updated, ended int64
		if err := rows.Scan(&run.UID, &run.RecipeID, &run.Status, &run.Backend,
			&run.Model, &estimate, &stages, &run.Error, &started, &updated,
			&ended); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(estimate), &run.Estimate); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(stages), &run.Stages); err != nil {
			return nil, err
		}
		run.StartedAt = time.Unix(started, 0)
		run.UpdatedAt = time.Unix(updated, 0)
		if ended > 0 {
			run.EndedAt = time.Unix(ended, 0)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// PutRefineryOutput inserts or updates a generated draft.
func (d *DB) PutRefineryOutput(output *RefineryOutput) error {
	if output == nil {
		return fmt.Errorf("output is required")
	}
	now := time.Now()
	if output.UID == "" {
		output.UID = NewUID()
	}
	if output.CreatedAt.IsZero() {
		output.CreatedAt = now
	}
	output.UpdatedAt = now
	if output.Status == "" {
		output.Status = "draft"
	}
	evidence, err := json.Marshal(output.EvidenceIDs)
	if err != nil {
		return err
	}
	_, err = d.sql.Exec(`
		INSERT INTO refinery_outputs
		  (uid,recipe_id,run_id,kind,title,maker,format,status,path,provenance_path,
		   evidence_ids,quality,created_at,updated_at,reviewed_at,exported_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
		  status=excluded.status, path=excluded.path,
		  provenance_path=excluded.provenance_path, evidence_ids=excluded.evidence_ids,
		  quality=excluded.quality, updated_at=excluded.updated_at,
		  reviewed_at=excluded.reviewed_at, exported_at=excluded.exported_at`,
		output.UID, output.RecipeID, output.RunID, output.Kind, output.Title,
		output.Maker, output.Format, output.Status, output.Path,
		output.ProvenancePath, string(evidence), output.Quality,
		output.CreatedAt.Unix(), output.UpdatedAt.Unix(),
		unixSeconds(output.ReviewedAt), unixSeconds(output.ExportedAt))
	return err
}

// RefineryOutput returns one generated draft.
func (d *DB) RefineryOutput(uid string) (RefineryOutput, error) {
	var output RefineryOutput
	var evidence string
	var created, updated, reviewed, exported int64
	err := d.sql.QueryRow(`
		SELECT uid,recipe_id,COALESCE(run_id,''),kind,title,COALESCE(maker,''),
		       COALESCE(format,''),status,path,COALESCE(provenance_path,''),
		       evidence_ids,COALESCE(quality,0),created_at,updated_at,
		       COALESCE(reviewed_at,0),COALESCE(exported_at,0)
		FROM refinery_outputs WHERE uid = ?`, uid).
		Scan(&output.UID, &output.RecipeID, &output.RunID, &output.Kind,
			&output.Title, &output.Maker, &output.Format, &output.Status,
			&output.Path, &output.ProvenancePath, &evidence, &output.Quality,
			&created, &updated, &reviewed, &exported)
	if err != nil {
		return output, err
	}
	if err := json.Unmarshal([]byte(evidence), &output.EvidenceIDs); err != nil {
		return output, err
	}
	output.CreatedAt = time.Unix(created, 0)
	output.UpdatedAt = time.Unix(updated, 0)
	if reviewed > 0 {
		output.ReviewedAt = time.Unix(reviewed, 0)
	}
	if exported > 0 {
		output.ExportedAt = time.Unix(exported, 0)
	}
	return output, nil
}

// RefineryOutputs lists generated drafts, optionally for one recipe.
func (d *DB) RefineryOutputs(recipeID string, limit int) ([]RefineryOutput, error) {
	query := `
		SELECT uid,recipe_id,COALESCE(run_id,''),kind,title,COALESCE(maker,''),
		       COALESCE(format,''),status,path,COALESCE(provenance_path,''),
		       evidence_ids,COALESCE(quality,0),created_at,updated_at,
		       COALESCE(reviewed_at,0),COALESCE(exported_at,0)
		FROM refinery_outputs`
	var args []any
	if recipeID != "" {
		query += " WHERE recipe_id = ?"
		args = append(args, recipeID)
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	outputs := make([]RefineryOutput, 0)
	for rows.Next() {
		var output RefineryOutput
		var evidence string
		var created, updated, reviewed, exported int64
		if err := rows.Scan(&output.UID, &output.RecipeID, &output.RunID,
			&output.Kind, &output.Title, &output.Maker, &output.Format,
			&output.Status, &output.Path, &output.ProvenancePath, &evidence,
			&output.Quality, &created, &updated, &reviewed, &exported); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidence), &output.EvidenceIDs); err != nil {
			return nil, err
		}
		output.CreatedAt = time.Unix(created, 0)
		output.UpdatedAt = time.Unix(updated, 0)
		if reviewed > 0 {
			output.ReviewedAt = time.Unix(reviewed, 0)
		}
		if exported > 0 {
			output.ExportedAt = time.Unix(exported, 0)
		}
		outputs = append(outputs, output)
	}
	return outputs, rows.Err()
}

// NuggetsByIDs returns only explicitly selected evidence, preserving the
// caller's order. Selection ids are exact values, never LIKE patterns.
func (d *DB) NuggetsByIDs(ids []string) ([]Nugget, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := d.sql.Query(`
		SELECT uid,tool,session_id,kind,COALESCE(title,''),body,COALESCE(tags,''),
		       COALESCE(workspace,''),COALESCE(repo,''),COALESCE(confidence,0),
		       COALESCE(model,''),COALESCE(redacted,0),COALESCE(turn_ref,''),created_at
		FROM nuggets WHERE uid IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]Nugget{}
	for rows.Next() {
		var nugget Nugget
		var tags string
		var redacted int
		var created int64
		if err := rows.Scan(&nugget.UID, &nugget.Tool, &nugget.SessionID,
			&nugget.Kind, &nugget.Title, &nugget.Body, &tags,
			&nugget.Workspace, &nugget.Repo, &nugget.Confidence,
			&nugget.Model, &redacted, &nugget.TurnRef, &created); err != nil {
			return nil, err
		}
		if tags != "" {
			nugget.Tags = strings.Split(tags, ",")
		}
		nugget.Redacted = redacted == 1
		nugget.CreatedAt = time.Unix(created, 0)
		byID[nugget.UID] = nugget
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Nugget, 0, len(ids))
	for _, id := range ids {
		if nugget, ok := byID[id]; ok {
			out = append(out, nugget)
		}
	}
	return out, nil
}
