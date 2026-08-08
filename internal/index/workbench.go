package index

import (
	"encoding/json"
	"fmt"
	"time"
)

const workbenchSchema = `
CREATE TABLE IF NOT EXISTS background_jobs (
  id            TEXT PRIMARY KEY,
  op            TEXT NOT NULL,
  scope         TEXT,
  status        TEXT NOT NULL,
  progress      TEXT,
  result_json   TEXT,
  error         TEXT,
  estimate_json TEXT,
  cost_json     TEXT,
  started_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  ended_at      INTEGER
);
CREATE INDEX IF NOT EXISTS idx_background_jobs_started
  ON background_jobs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_background_jobs_status
  ON background_jobs(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS recovery_runs (
  uid          TEXT PRIMARY KEY,
  job_id       TEXT,
  op           TEXT NOT NULL,
  scope_json   TEXT NOT NULL,
  status       TEXT NOT NULL,
  sessions     INTEGER DEFAULT 0,
  assayed      INTEGER DEFAULT 0,
  evidence     INTEGER DEFAULT 0,
  failed       INTEGER DEFAULT 0,
  bytes        INTEGER DEFAULT 0,
  backend      TEXT,
  model        TEXT,
  depth        TEXT,
  error        TEXT,
  started_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  ended_at     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_recovery_runs_started
  ON recovery_runs(started_at DESC);

CREATE TABLE IF NOT EXISTS work_threads (
  recipe_id       TEXT PRIMARY KEY,
  backend         TEXT,
  model           TEXT,
  cli_session_id  TEXT,
  budget_tokens   INTEGER NOT NULL DEFAULT 1200000,
  estimated_spent INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  FOREIGN KEY(recipe_id) REFERENCES refinery_recipes(uid) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS work_messages (
  uid        TEXT PRIMARY KEY,
  recipe_id  TEXT NOT NULL,
  role       TEXT NOT NULL,
  body       TEXT NOT NULL,
  job_id     TEXT,
  created_at INTEGER NOT NULL,
  FOREIGN KEY(recipe_id) REFERENCES refinery_recipes(uid) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_work_messages_recipe
  ON work_messages(recipe_id, created_at);
`

func (d *DB) migrateWorkbench() error {
	_, err := d.sql.Exec(workbenchSchema)
	return err
}

// StoredJob is the durable representation of a web background operation.
type StoredJob struct {
	ID       string          `json:"id"`
	Op       string          `json:"op"`
	Scope    string          `json:"scope"`
	Status   string          `json:"status"`
	Progress string          `json:"progress"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	Estimate json.RawMessage `json:"estimate,omitempty"`
	Cost     json.RawMessage `json:"cost,omitempty"`
	Started  time.Time       `json:"started"`
	Updated  time.Time       `json:"updated"`
	Ended    time.Time       `json:"ended,omitempty"`
}

func (d *DB) PutBackgroundJob(job StoredJob) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	now := time.Now()
	if job.Started.IsZero() {
		job.Started = now
	}
	job.Updated = now
	if job.Status == "" {
		job.Status = "queued"
	}
	_, err := d.sql.Exec(`
		INSERT INTO background_jobs
		  (id,op,scope,status,progress,result_json,error,estimate_json,cost_json,
		   started_at,updated_at,ended_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  status=excluded.status, progress=excluded.progress,
		  result_json=excluded.result_json, error=excluded.error,
		  estimate_json=excluded.estimate_json, cost_json=excluded.cost_json,
		  updated_at=excluded.updated_at, ended_at=excluded.ended_at`,
		job.ID, job.Op, job.Scope, job.Status, job.Progress,
		nullableJSON(job.Result), job.Error, nullableJSON(job.Estimate),
		nullableJSON(job.Cost), job.Started.Unix(), job.Updated.Unix(),
		unixSeconds(job.Ended))
	return err
}

func (d *DB) BackgroundJob(id string) (StoredJob, error) {
	var job StoredJob
	var result, estimate, cost string
	var started, updated, ended int64
	err := d.sql.QueryRow(`
		SELECT id,op,COALESCE(scope,''),status,COALESCE(progress,''),
		       COALESCE(result_json,''),COALESCE(error,''),
		       COALESCE(estimate_json,''),COALESCE(cost_json,''),
		       started_at,updated_at,COALESCE(ended_at,0)
		FROM background_jobs WHERE id = ?`, id).
		Scan(&job.ID, &job.Op, &job.Scope, &job.Status, &job.Progress,
			&result, &job.Error, &estimate, &cost, &started, &updated, &ended)
	if err != nil {
		return job, err
	}
	job.Result = rawJSON(result)
	job.Estimate = rawJSON(estimate)
	job.Cost = rawJSON(cost)
	job.Started = time.Unix(started, 0)
	job.Updated = time.Unix(updated, 0)
	if ended > 0 {
		job.Ended = time.Unix(ended, 0)
	}
	return job, nil
}

func (d *DB) BackgroundJobs(limit int) ([]StoredJob, error) {
	query := `
		SELECT id,op,COALESCE(scope,''),status,COALESCE(progress,''),
		       COALESCE(result_json,''),COALESCE(error,''),
		       COALESCE(estimate_json,''),COALESCE(cost_json,''),
		       started_at,updated_at,COALESCE(ended_at,0)
		FROM background_jobs ORDER BY started_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]StoredJob, 0)
	for rows.Next() {
		var job StoredJob
		var result, estimate, cost string
		var started, updated, ended int64
		if err := rows.Scan(&job.ID, &job.Op, &job.Scope, &job.Status,
			&job.Progress, &result, &job.Error, &estimate, &cost,
			&started, &updated, &ended); err != nil {
			return nil, err
		}
		job.Result = rawJSON(result)
		job.Estimate = rawJSON(estimate)
		job.Cost = rawJSON(cost)
		job.Started = time.Unix(started, 0)
		job.Updated = time.Unix(updated, 0)
		if ended > 0 {
			job.Ended = time.Unix(ended, 0)
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// InterruptBackgroundJobs closes the durable state of work that could not
// survive a process restart. A future worker may make selected operations
// resumable, but reporting "running" forever is never acceptable.
func (d *DB) InterruptBackgroundJobs() error {
	now := time.Now().Unix()
	_, err := d.sql.Exec(`
		UPDATE background_jobs
		SET status='failed', progress='interrupted', ended_at=?, updated_at=?,
		    error='Midden restarted before this job completed'
		WHERE status IN ('queued','running')`, now, now)
	return err
}

// RecoveryRun is one durable assay or evidence-extraction scope.
type RecoveryRun struct {
	UID      string          `json:"uid"`
	JobID    string          `json:"job_id,omitempty"`
	Op       string          `json:"op"`
	Scope    json.RawMessage `json:"scope"`
	Status   string          `json:"status"`
	Sessions int             `json:"sessions"`
	Assayed  int             `json:"assayed"`
	Evidence int             `json:"evidence"`
	Failed   int             `json:"failed"`
	Bytes    int64           `json:"bytes"`
	Backend  string          `json:"backend,omitempty"`
	Model    string          `json:"model,omitempty"`
	Depth    string          `json:"depth,omitempty"`
	Error    string          `json:"error,omitempty"`
	Started  time.Time       `json:"started"`
	Updated  time.Time       `json:"updated"`
	Ended    time.Time       `json:"ended,omitempty"`
}

func (d *DB) PutRecoveryRun(run *RecoveryRun) error {
	if run == nil {
		return fmt.Errorf("recovery run is required")
	}
	now := time.Now()
	if run.UID == "" {
		run.UID = NewUID()
	}
	if run.Started.IsZero() {
		run.Started = now
	}
	run.Updated = now
	if run.Status == "" {
		run.Status = "queued"
	}
	if len(run.Scope) == 0 {
		run.Scope = json.RawMessage(`{}`)
	}
	_, err := d.sql.Exec(`
		INSERT INTO recovery_runs
		  (uid,job_id,op,scope_json,status,sessions,assayed,evidence,failed,bytes,
		   backend,model,depth,error,started_at,updated_at,ended_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET
		  status=excluded.status, sessions=excluded.sessions,
		  assayed=excluded.assayed, evidence=excluded.evidence,
		  failed=excluded.failed, bytes=excluded.bytes,
		  backend=excluded.backend, model=excluded.model, depth=excluded.depth,
		  error=excluded.error, updated_at=excluded.updated_at,
		  ended_at=excluded.ended_at`,
		run.UID, run.JobID, run.Op, string(run.Scope), run.Status,
		run.Sessions, run.Assayed, run.Evidence, run.Failed, run.Bytes,
		run.Backend, run.Model, run.Depth, run.Error, run.Started.Unix(),
		run.Updated.Unix(), unixSeconds(run.Ended))
	return err
}

func (d *DB) RecoveryRuns(limit int) ([]RecoveryRun, error) {
	query := `
		SELECT uid,COALESCE(job_id,''),op,scope_json,status,
		       COALESCE(sessions,0),COALESCE(assayed,0),COALESCE(evidence,0),
		       COALESCE(failed,0),COALESCE(bytes,0),COALESCE(backend,''),
		       COALESCE(model,''),COALESCE(depth,''),COALESCE(error,''),
		       started_at,updated_at,COALESCE(ended_at,0)
		FROM recovery_runs ORDER BY started_at DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := d.sql.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]RecoveryRun, 0)
	for rows.Next() {
		var run RecoveryRun
		var scope string
		var started, updated, ended int64
		if err := rows.Scan(&run.UID, &run.JobID, &run.Op, &scope, &run.Status,
			&run.Sessions, &run.Assayed, &run.Evidence, &run.Failed,
			&run.Bytes, &run.Backend, &run.Model, &run.Depth, &run.Error,
			&started, &updated, &ended); err != nil {
			return nil, err
		}
		run.Scope = rawJSON(scope)
		run.Started = time.Unix(started, 0)
		run.Updated = time.Unix(updated, 0)
		if ended > 0 {
			run.Ended = time.Unix(ended, 0)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// WorkThread binds one refinery recipe to one resumable AI CLI session and a
// cumulative budget envelope.
type WorkThread struct {
	RecipeID       string    `json:"recipe_id"`
	Backend        string    `json:"backend,omitempty"`
	Model          string    `json:"model,omitempty"`
	CLISessionID   string    `json:"cli_session_id,omitempty"`
	BudgetTokens   int       `json:"budget_tokens"`
	EstimatedSpent int       `json:"estimated_spent"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (d *DB) PutWorkThread(thread *WorkThread) error {
	if thread == nil || thread.RecipeID == "" {
		return fmt.Errorf("work thread recipe is required")
	}
	now := time.Now()
	if thread.CreatedAt.IsZero() {
		thread.CreatedAt = now
	}
	thread.UpdatedAt = now
	if thread.BudgetTokens <= 0 {
		thread.BudgetTokens = 1_200_000
	}
	_, err := d.sql.Exec(`
		INSERT INTO work_threads
		  (recipe_id,backend,model,cli_session_id,budget_tokens,estimated_spent,
		   created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(recipe_id) DO UPDATE SET
		  backend=excluded.backend, model=excluded.model,
		  cli_session_id=excluded.cli_session_id,
		  budget_tokens=excluded.budget_tokens,
		  estimated_spent=excluded.estimated_spent,
		  updated_at=excluded.updated_at`,
		thread.RecipeID, thread.Backend, thread.Model, thread.CLISessionID,
		thread.BudgetTokens, thread.EstimatedSpent, thread.CreatedAt.Unix(),
		thread.UpdatedAt.Unix())
	return err
}

func (d *DB) WorkThread(recipeID string) (WorkThread, error) {
	var thread WorkThread
	var created, updated int64
	err := d.sql.QueryRow(`
		SELECT recipe_id,COALESCE(backend,''),COALESCE(model,''),
		       COALESCE(cli_session_id,''),budget_tokens,estimated_spent,
		       created_at,updated_at
		FROM work_threads WHERE recipe_id = ?`, recipeID).
		Scan(&thread.RecipeID, &thread.Backend, &thread.Model,
			&thread.CLISessionID, &thread.BudgetTokens,
			&thread.EstimatedSpent, &created, &updated)
	if err != nil {
		return thread, err
	}
	thread.CreatedAt = time.Unix(created, 0)
	thread.UpdatedAt = time.Unix(updated, 0)
	return thread, nil
}

type WorkMessage struct {
	UID       string    `json:"uid"`
	RecipeID  string    `json:"recipe_id"`
	Role      string    `json:"role"`
	Body      string    `json:"body"`
	JobID     string    `json:"job_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (d *DB) PutWorkMessage(message *WorkMessage) error {
	if message == nil || message.RecipeID == "" {
		return fmt.Errorf("work message recipe is required")
	}
	if message.Role != "user" && message.Role != "agent" && message.Role != "system" {
		return fmt.Errorf("unsupported work message role %q", message.Role)
	}
	if message.UID == "" {
		message.UID = NewUID()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now()
	}
	_, err := d.sql.Exec(`
		INSERT INTO work_messages (uid,recipe_id,role,body,job_id,created_at)
		VALUES (?,?,?,?,?,?)`,
		message.UID, message.RecipeID, message.Role, message.Body,
		message.JobID, message.CreatedAt.Unix())
	return err
}

func (d *DB) WorkMessages(recipeID string, limit int) ([]WorkMessage, error) {
	query := `
		SELECT uid,recipe_id,role,body,COALESCE(job_id,''),created_at
		FROM work_messages WHERE recipe_id = ?
		ORDER BY created_at,rowid`
	args := []any{recipeID}
	if limit > 0 {
		query = `
			SELECT uid,recipe_id,role,body,COALESCE(job_id,''),created_at
			FROM (
			  SELECT rowid AS ordinal,uid,recipe_id,role,body,job_id,created_at
			  FROM work_messages WHERE recipe_id = ?
			  ORDER BY created_at DESC,rowid DESC LIMIT ?
			) ORDER BY created_at,ordinal`
		args = append(args, limit)
	}
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]WorkMessage, 0)
	for rows.Next() {
		var message WorkMessage
		var created int64
		if err := rows.Scan(&message.UID, &message.RecipeID, &message.Role,
			&message.Body, &message.JobID, &created); err != nil {
			return nil, err
		}
		message.CreatedAt = time.Unix(created, 0)
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return string(value)
}

func rawJSON(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(value)
}
