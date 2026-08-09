package web

// Jobs run the operations that take time or spend money.
//
// The UI was previously read-only, which made it an instrument panel rather
// than a tool: every action required dropping to a terminal. These handlers
// close the loop — see, decide, act, and see what it cost — without leaving
// the page.
//
// Long operations run asynchronously with progress, because a reclaim can take
// a minute and a browser request should not hold open for it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/dispose"
	"github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/handoff"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/opennotebook"
	"github.com/mekjr1/midden/internal/plugins"
	"github.com/mekjr1/midden/internal/reclaim"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/refine"
	"github.com/mekjr1/midden/internal/render"
	"github.com/mekjr1/midden/internal/summary"
)

// JobStatus is where a job has got to.
type JobStatus string

const (
	Queued  JobStatus = "queued"
	Running JobStatus = "running"
	Done    JobStatus = "done"
	Failed  JobStatus = "failed"
)

// Job is one asynchronous operation.
type Job struct {
	ID       string         `json:"id"`
	Op       string         `json:"op"`
	Scope    string         `json:"scope"`
	Status   JobStatus      `json:"status"`
	Progress string         `json:"progress"`
	Started  time.Time      `json:"started"`
	Ended    time.Time      `json:"ended,omitempty"`
	Result   any            `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
	Cost     *cost.Run      `json:"cost,omitempty"`
	Estimate *cost.Estimate `json:"estimate,omitempty"`
}

// Jobs tracks running and finished work.
type Jobs struct {
	mu           sync.RWMutex
	jobs         map[string]*Job
	db           *index.DB
	lease        *jobLeaseManager
	startupError error
}

func NewJobs(dbs ...*index.DB) *Jobs {
	jobs := &Jobs{jobs: map[string]*Job{}}
	if len(dbs) == 0 || dbs[0] == nil {
		jobs.startupError = fmt.Errorf("durable job ownership requires an index database")
		return jobs
	}
	jobs.db = dbs[0]
	lease, err := newJobLeaseManager(jobs.db)
	if err != nil {
		jobs.startupError = err
		return jobs
	}
	jobs.lease = lease
	if err := lease.recoverStaleJobs(); err != nil {
		jobs.startupError = err
	}
	return jobs
}

// Close stops this instance heartbeat during orderly server shutdown.
func (j *Jobs) Close() {
	if j.lease != nil {
		j.lease.Close()
	}
}

func (j *Jobs) create(op, scope string) *Job {
	j.mu.Lock()
	job := &Job{
		ID:      "job-" + index.NewUID(),
		Op:      op,
		Scope:   scope,
		Status:  Queued,
		Started: time.Now(),
	}
	j.jobs[job.ID] = job
	copy := *job
	j.mu.Unlock()
	if err := j.persist(copy); err != nil {
		j.markPersistenceFailure(job.ID, err)
		return job
	}
	if err := j.claimOwnership(job.ID); err != nil {
		j.markOwnershipFailure(job.ID, err)
	}
	return job
}

// claimOwnership makes durable state and an active owner lease prerequisites
// for execution. A visible failed job is safer than work that no process can
// later account for or recover.
func (j *Jobs) claimOwnership(id string) error {
	if j.db == nil {
		return fmt.Errorf("durable job ownership requires an index database")
	}
	if j.lease == nil {
		j.mu.RLock()
		err := j.startupError
		j.mu.RUnlock()
		if err != nil {
			return fmt.Errorf("initialize job ownership: %w", err)
		}
		return fmt.Errorf("initialize job ownership")
	}
	if err := j.lease.Claim(id); err != nil {
		return err
	}
	if err := j.lease.Owns(id); err != nil {
		j.lease.Release(id)
		return err
	}
	return nil
}

// canRun is the final dispatch guard. It deliberately rechecks the lease at
// the execution boundary so a call path cannot bypass create's ownership
// claim and start unowned work.
func (j *Jobs) canRun(id string) error {
	j.mu.RLock()
	job, ok := j.jobs[id]
	status := Queued
	if ok {
		status = job.Status
	}
	startupErr := j.startupError
	lease := j.lease
	db := j.db
	j.mu.RUnlock()
	if !ok {
		return fmt.Errorf("job %s is not registered", id)
	}
	if status != Queued {
		return fmt.Errorf("job %s is not dispatchable (status %s)", id, status)
	}
	if db == nil {
		return fmt.Errorf("durable job ownership requires an index database")
	}
	if startupErr != nil {
		return fmt.Errorf("initialize job ownership: %w", startupErr)
	}
	if lease == nil {
		return fmt.Errorf("job ownership is unavailable")
	}
	return lease.Owns(id)
}

func (j *Jobs) update(id string, fn func(*Job)) {
	j.mu.Lock()
	job, ok := j.jobs[id]
	if !ok {
		j.mu.Unlock()
		return
	}
	fn(job)
	copy := *job
	j.mu.Unlock()
	if err := j.persist(copy); err != nil {
		j.markPersistenceFailure(id, err)
		return
	}
	if (copy.Status == Done || copy.Status == Failed) && j.lease != nil {
		j.lease.Release(id)
	}
}

func (j *Jobs) markPersistenceFailure(id string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	if !ok {
		return
	}
	job.Status = Failed
	job.Progress = "persistence failed"
	if job.Error == "" {
		job.Error = fmt.Sprintf("could not persist job state: %v", err)
	} else if !strings.Contains(job.Error, "could not persist job state") {
		job.Error += fmt.Sprintf("; could not persist job state: %v", err)
	}
	if job.Ended.IsZero() {
		job.Ended = time.Now()
	}
}

func (j *Jobs) markOwnershipFailure(id string, err error) {
	j.mu.Lock()
	job, ok := j.jobs[id]
	var copy Job
	if ok {
		job.Status = Failed
		job.Progress = "ownership unavailable"
		job.Error = fmt.Sprintf("could not establish job ownership: %v", err)
		job.Ended = time.Now()
		copy = *job
	}
	j.mu.Unlock()
	if ok {
		_ = j.persist(copy)
	}
	if j.lease != nil {
		j.lease.Release(id)
	}
}

func (j *Jobs) get(id string) (*Job, bool) {
	j.mu.RLock()
	job, ok := j.jobs[id]
	if ok {
		copy := *job
		j.mu.RUnlock()
		return &copy, true
	}
	j.mu.RUnlock()

	if j.db == nil {
		return nil, false
	}
	stored, err := j.db.BackgroundJob(id)
	if err != nil {
		return nil, false
	}
	return jobFromStored(stored), true
}

func (j *Jobs) list() []*Job {
	jobs, _ := j.page(40, 0)
	return jobs
}

// page overlays live state over durable history. A running operation can move
// through several progress updates before SQLite returns the latest row; the
// operator must see the owned in-memory state rather than a stale durable copy.
func (j *Jobs) page(limit, offset int) ([]*Job, int) {
	if limit <= 0 {
		limit = 40
	}
	if limit > 50 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	j.mu.RLock()
	startupError := j.startupError
	live := make(map[string]Job, len(j.jobs))
	for id, job := range j.jobs {
		live[id] = *job
	}
	j.mu.RUnlock()

	if j.db == nil {
		return j.memoryPage(live, startupError, limit, offset)
	}
	stored, total, err := j.db.BackgroundJobsPage(limit, offset)
	if err != nil {
		return j.memoryPage(live, fmt.Errorf("load durable jobs: %w", err), limit, offset)
	}

	out := make([]*Job, 0, len(stored)+1)
	seen := make(map[string]bool, len(stored))
	for _, row := range stored {
		job := jobFromStored(row)
		if current, ok := live[job.ID]; ok && (current.Status == Queued || current.Status == Running) {
			job = &current
		}
		seen[job.ID] = true
		out = append(out, job)
	}

	// A failed durable write has no row to overlay. Keep that active (or
	// queued) job visible on the first page instead of silently dropping it.
	extra := 0
	if offset == 0 {
		for id, current := range live {
			if seen[id] || (current.Status != Queued && current.Status != Running) {
				continue
			}
			out = append(out, &current)
			extra++
		}
	}
	if startupError != nil && offset == 0 {
		out = append(out, persistenceFailureJob("startup", startupError))
		extra++
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Started.After(out[b].Started) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, total + extra
}

func (j *Jobs) memoryPage(live map[string]Job, startupError error, limit, offset int) ([]*Job, int) {
	out := make([]*Job, 0, len(live)+1)
	for _, job := range live {
		copy := job
		out = append(out, &copy)
	}
	if startupError != nil {
		out = append(out, persistenceFailureJob("startup", startupError))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Started.After(out[b].Started) })
	total := len(out)
	if offset >= total {
		return []*Job{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return out[offset:end], total
}

func persistenceFailureJob(scope string, err error) *Job {
	return &Job{
		ID: "persistence-" + scope, Op: "recovery", Scope: scope,
		Status: Failed, Progress: "persistence failed", Started: time.Now(),
		Ended: time.Now(), Error: fmt.Sprintf("could not restore durable job state: %v", err),
	}
}

func (j *Jobs) persist(job Job) error {
	if j.db == nil {
		return nil
	}
	result, err := json.Marshal(durableJobResult(job.Result))
	if err != nil {
		return fmt.Errorf("encode durable result: %w", err)
	}
	estimate, err := json.Marshal(job.Estimate)
	if err != nil {
		return fmt.Errorf("encode estimate: %w", err)
	}
	costRun, err := json.Marshal(job.Cost)
	if err != nil {
		return fmt.Errorf("encode cost: %w", err)
	}
	if err := j.db.PutBackgroundJob(index.StoredJob{
		ID: job.ID, Op: job.Op, Scope: job.Scope, Status: string(job.Status),
		Progress: job.Progress, Result: normalizedJobJSON(result),
		Error: job.Error, Estimate: normalizedJobJSON(estimate),
		Cost: normalizedJobJSON(costRun), Started: job.Started, Ended: job.Ended,
	}); err != nil {
		return fmt.Errorf("write durable job: %w", err)
	}
	return nil
}

func durableJobResult(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			switch strings.ToLower(key) {
			case "answer", "body", "nuggets", "evidence", "provenance",
				"question", "prompt", "request", "instruction":
				continue
			default:
				out[key] = durableJobResult(item)
			}
		}
		return out
	case []index.Nugget:
		return map[string]any{"count": len(typed)}
	case index.Nugget:
		return map[string]any{
			"uid": typed.UID, "kind": typed.Kind, "session_id": typed.SessionID,
		}
	case index.WorkMessage:
		return map[string]any{
			"uid": typed.UID, "role": typed.Role, "recipe_id": typed.RecipeID,
			"created_at": typed.CreatedAt,
		}
	case index.Recipe:
		return map[string]any{
			"uid": typed.UID, "title": typed.Title, "status": typed.Status,
			"workspace": typed.Workspace, "outputs": len(typed.Outputs),
			"evidence": len(typed.EvidenceIDs),
		}
	case index.WorkThread:
		return map[string]any{
			"recipe_id": typed.RecipeID, "backend": typed.Backend,
			"model": typed.Model, "budget_tokens": typed.BudgetTokens,
			"estimated_spent": typed.EstimatedSpent,
		}
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = durableJobResult(item)
		}
		return out
	default:
		return value
	}
}

func jobFromStored(stored index.StoredJob) *Job {
	job := &Job{
		ID: stored.ID, Op: stored.Op, Scope: stored.Scope,
		Status: JobStatus(stored.Status), Progress: stored.Progress,
		Started: stored.Started, Ended: stored.Ended, Error: stored.Error,
	}
	if len(stored.Result) > 0 {
		_ = json.Unmarshal(stored.Result, &job.Result)
	}
	if len(stored.Estimate) > 0 {
		var estimate cost.Estimate
		if json.Unmarshal(stored.Estimate, &estimate) == nil {
			job.Estimate = &estimate
		}
	}
	if len(stored.Cost) > 0 {
		var run cost.Run
		if json.Unmarshal(stored.Cost, &run) == nil {
			job.Cost = &run
		}
	}
	return job
}

func normalizedJobJSON(value []byte) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return json.RawMessage(value)
}

// actionRequest is the body every action endpoint accepts.
type actionRequest struct {
	Op                   string   `json:"op"`
	SessionID            string   `json:"session_id"`
	SessionIDs           []string `json:"session_ids"`
	SessionKeys          []string `json:"session_keys"`
	Workspace            string   `json:"workspace"`
	Tool                 string   `json:"tool"`
	Days                 int      `json:"days"`
	Instruction          string   `json:"instruction"`
	Templates            []string `json:"templates"`
	Backend              string   `json:"backend"`
	Model                string   `json:"model"`
	Records              int      `json:"records"`
	Depth                string   `json:"depth"`
	Question             string   `json:"question"`
	Plugin               string   `json:"plugin"`
	NotebookID           string   `json:"notebook_id"`
	OpenNotebookPassword string   `json:"open_notebook_password"`
	AllWorkspaces        bool     `json:"all_workspaces"`
	RecipeID             string   `json:"recipe_id"`
	BudgetTokens         int      `json:"budget_tokens"`

	// Apply must be explicitly true for anything destructive. A missing field
	// means dry run, so a malformed request can never delete.
	Apply bool `json:"apply"`
	// Confirm is a second, separate acknowledgement for irreversible actions.
	Confirm bool `json:"confirm"`
}

func (a actionRequest) scope() core.Scope {
	sc := core.Scope{Days: a.Days, Workspace: a.Workspace, IncludeNoise: true}
	if a.SessionID != "" {
		sc.IDPrefix = a.SessionID
	}
	switch core.Tool(a.Tool) {
	case core.ToolCopilot:
		sc.Tools = []core.Tool{core.ToolCopilot}
	case core.ToolClaude:
		sc.Tools = []core.Tool{core.ToolClaude}
	case core.ToolOpencode:
		sc.Tools = []core.Tool{core.ToolOpencode}
	}
	return sc
}

func (a actionRequest) label() string {
	switch {
	case a.RecipeID != "":
		return "work item " + shortID(a.RecipeID)
	case len(a.SessionKeys) > 0:
		return fmt.Sprintf("%d selected session(s)", len(a.SessionKeys))
	case len(a.SessionIDs) > 0:
		return fmt.Sprintf("%d selected session(s)", len(a.SessionIDs))
	case a.SessionID != "":
		return "session " + shortID(a.SessionID)
	case a.Workspace != "":
		return "workspace " + a.Workspace
	case a.Days > 0:
		return fmt.Sprintf("last %dd", a.Days)
	}
	return "all"
}

func (a actionRequest) filterExactSessions(sessions []core.Session) []core.Session {
	if len(a.SessionKeys) > 0 {
		wanted := map[string]bool{}
		for _, key := range a.SessionKeys {
			if key = strings.TrimSpace(key); key != "" {
				wanted[key] = true
			}
		}
		filtered := make([]core.Session, 0, len(wanted))
		for _, session := range sessions {
			if wanted[sessionIdentity(string(session.Tool), session.ID)] {
				filtered = append(filtered, session)
			}
		}
		return filtered
	}
	if len(a.SessionIDs) == 0 {
		return sessions
	}
	wanted := map[string]bool{}
	for _, id := range a.SessionIDs {
		if id = strings.TrimSpace(id); id != "" {
			wanted[id] = true
		}
	}
	filtered := make([]core.Session, 0, len(wanted))
	for _, session := range sessions {
		if wanted[session.ID] {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (a actionRequest) exactSession() (core.Session, error) {
	if len(a.SessionKeys) > 0 {
		sessions, _ := adapter.Collect(core.Scope{IncludeNoise: true})
		matches := a.filterExactSessions(sessions)
		if len(matches) != 1 {
			return core.Session{}, fmt.Errorf("exact session not found")
		}
		return matches[0], nil
	}
	if a.SessionID == "" {
		return core.Session{}, fmt.Errorf("a session is required")
	}
	matches, _ := adapter.Collect(core.Scope{
		IDPrefix: a.SessionID, IncludeNoise: true,
	})
	if len(matches) == 0 {
		return core.Session{}, fmt.Errorf("session not found")
	}
	if len(matches) > 1 {
		return core.Session{}, fmt.Errorf("session id is ambiguous; select the exact tool and id")
	}
	return matches[0], nil
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// handleAction starts an operation.
func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch req.Op {
	case "prune", "archive", "reclaim", "refine", "summarize", "ask", "brief",
		"refresh", "mine", "production", "open_notebook_push", "work_chat":
	default:
		http.Error(w, "unknown op: "+req.Op, http.StatusBadRequest)
		return
	}

	job := s.jobs.create(req.Op, req.label())
	if job.Status == Queued {
		go s.runJob(job.ID, req)
	}
	writeJSON(w, job)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if id := r.URL.Query().Get("id"); id != "" {
		job, ok := s.jobs.get(id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, job)
		return
	}
	limit := 40
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	if limit > 50 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total := s.jobs.page(limit, offset)
	writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": maxInt(offset, 0)})
}

// runJob dispatches to the operation implementations.
func (s *Server) runJob(id string, req actionRequest) {
	if err := s.jobs.canRun(id); err != nil {
		s.jobs.markOwnershipFailure(id, err)
		return
	}
	s.jobs.update(id, func(j *Job) {
		j.Status = Running
		j.Progress = "starting"
	})
	recoveryRun, recoveryErr := s.startRecoveryRun(id, req)
	if recoveryErr != nil {
		s.auditRecoveryOperation(req, recoveryErr)
		s.jobs.update(id, func(j *Job) {
			j.Status = Failed
			j.Progress = "recovery persistence failed"
			j.Error = fmt.Sprintf("could not persist recovery run: %v", recoveryErr)
			j.Ended = time.Now()
		})
		return
	}

	var (
		result any
		err    error
	)
	switch req.Op {
	case "prune":
		result, err = s.doPrune(id, req)
	case "archive":
		result, err = s.doArchive(id, req)
	case "reclaim":
		result, err = s.doReclaim(id, req)
	case "refine":
		result, err = s.doRefine(id, req)
	case "summarize":
		result, err = s.doSummarize(id, req)
	case "brief":
		result, err = s.doBrief(id, req)
	case "ask":
		result, err = s.doAsk(id, req)
	case "refresh":
		result, err = s.doRefresh(id)
	case "mine":
		result, err = s.doMine(id, req)
	case "production":
		result, err = s.doProduction(id, req)
	case "open_notebook_push":
		result, err = s.doOpenNotebookPush(id, req)
	case "work_chat":
		result, err = s.doWorkChat(id, req)
	}
	if finishErr := s.finishRecoveryRun(recoveryRun, result, err); finishErr != nil && err == nil {
		err = fmt.Errorf("could not persist recovery completion: %w", finishErr)
	}
	s.auditRecoveryOperation(req, err)

	s.jobs.update(id, func(j *Job) {
		j.Ended = time.Now()
		j.Result = result
		if err != nil {
			j.Status = Failed
			j.Error = err.Error()
			return
		}
		j.Status = Done
		j.Progress = "complete"
	})
}

// auditRecoveryOperation records Mine/Reclaim outcomes without retaining
// prompts, evidence bodies, or model responses in the operator audit trail.
func (s *Server) auditRecoveryOperation(req actionRequest, runErr error) {
	if req.Op != "mine" && req.Op != "reclaim" {
		return
	}
	scope := "all indexed sessions"
	if len(req.SessionKeys) > 0 {
		scope = fmt.Sprintf("%d exact session(s)", len(req.SessionKeys))
	} else if len(req.SessionIDs) > 0 {
		scope = fmt.Sprintf("%d selected session(s)", len(req.SessionIDs))
	} else if req.Days > 0 {
		scope = fmt.Sprintf("last %d day(s)", req.Days)
	}
	state := "completed"
	if runErr != nil {
		state = "failed"
	}
	_ = s.db.RecordOp(req.Op, req.Tool, "", 0, 0, "status="+state+" scope="+scope, runErr == nil)
}

func (s *Server) startRecoveryRun(jobID string, req actionRequest) (*index.RecoveryRun, error) {
	if req.Op != "mine" && req.Op != "reclaim" {
		return nil, nil
	}
	scope, _ := json.Marshal(map[string]any{
		"session_id":   req.SessionID,
		"session_ids":  req.SessionIDs,
		"session_keys": req.SessionKeys,
		"workspace":    req.Workspace,
		"tool":         req.Tool,
		"days":         req.Days,
		"records":      req.Records,
		"depth":        req.Depth,
		"apply":        req.Apply,
	})
	run := &index.RecoveryRun{
		JobID: jobID, Op: req.Op, Scope: scope, Status: "running",
		Backend: req.Backend, Model: req.Model, Depth: req.Depth,
	}
	if err := s.db.PutRecoveryRun(run); err != nil {
		return nil, fmt.Errorf("start recovery run: %w", err)
	}
	return run, nil
}

func (s *Server) finishRecoveryRun(run *index.RecoveryRun, result any, runErr error) error {
	if run == nil {
		return nil
	}
	run.Ended = time.Now()
	if runErr != nil {
		run.Status = "failed"
		run.Error = runErr.Error()
		if err := s.db.PutRecoveryRun(run); err != nil {
			return fmt.Errorf("mark failed recovery run: %w", err)
		}
		return nil
	}
	run.Status = "done"
	body, _ := json.Marshal(result)
	var summary struct {
		Sessions int   `json:"sessions"`
		Assayed  int   `json:"assayed"`
		Failed   int   `json:"failed"`
		Count    int   `json:"count"`
		Bytes    int64 `json:"bytes_assayed"`
	}
	_ = json.Unmarshal(body, &summary)
	run.Sessions = summary.Sessions
	run.Assayed = summary.Assayed
	run.Failed = summary.Failed
	run.Evidence = summary.Count
	run.Bytes = summary.Bytes
	if err := s.db.PutRecoveryRun(run); err != nil {
		return fmt.Errorf("complete recovery run: %w", err)
	}
	return nil
}

func (s *Server) handleRecoveryRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	limit, offset := historyPageParams(r)
	runs, total, err := s.db.RecoveryRunsPage(limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"items": runs, "total": total, "limit": limit, "offset": offset})
}

// doRefresh is the UI's explicit answer to stale indexed data.
//
// The index is intentionally the read path: deriving every session on every
// request made the UI hang for minutes. That performance fix only works if an
// operator can deliberately refresh the index when they need current truth.
//
// A refresh is free and writes only Midden's derived index. A partial adapter
// read is not enough to reconcile that tool, but it must not discard fresh
// results from other adapters that completed successfully.
func (s *Server) doRefresh(id string) (any, error) {
	s.reindexMu.Lock()
	if s.reindexing {
		s.reindexMu.Unlock()
		return nil, fmt.Errorf("a refresh is already running")
	}
	s.reindexing = true
	s.reindexMu.Unlock()
	completeAll := false
	defer func() {
		s.reindexMu.Lock()
		s.reindexing = false
		// A partial refresh must not suppress a background retry for five
		// minutes just because one source produced usable rows.
		if completeAll {
			s.reindexedAt = time.Now()
			s.retryAt = time.Time{}
		} else {
			s.reindexedAt = time.Time{}
			s.retryAt = time.Now()
		}
		s.reindexMu.Unlock()
	}()

	lock, err := s.db.AcquireScanLock()
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	scope := core.Scope{IncludeNoise: true}
	generation, err := s.db.NextScanGeneration()
	if err != nil {
		return nil, fmt.Errorf("reserve scan generation: %w", err)
	}
	s.jobs.update(id, func(j *Job) { j.Progress = "reading session stores" })
	sessions, collected := adapter.CollectDetailed(scope)
	scannedAt := time.Now()

	s.jobs.update(id, func(j *Job) { j.Progress = "updating the index" })
	if err := s.db.PutSessionsWithGeneration(sessions, generation, scannedAt); err != nil {
		return nil, fmt.Errorf("index sessions: %w", err)
	}

	s.jobs.update(id, func(j *Job) { j.Progress = "removing stale index rows" })
	tools := index.AuthoritativeTools(scope, collected.Complete)
	var report index.ReconcileReport
	if len(tools) > 0 {
		allRequested := collected.IsAllSourcesComplete(scope)
		report, err = s.db.ReconcileAndMark(index.SessionsForTools(sessions, tools), tools, generation, scannedAt, allRequested)
		if err != nil {
			return nil, fmt.Errorf("reconcile index: %w", err)
		}
		completeAll = report.AuthoritativeAll
	}

	s.cache.invalidate()
	var messages []string
	for _, err := range collected.Errors {
		messages = append(messages, err.Error())
	}
	return map[string]any{
		"sessions":   len(sessions),
		"reconciled": report,
		"indexed_at": s.db.AuthoritativeIndexedAt(nil).Format(time.RFC3339),
		"free":       true,
		"partial":    len(messages) > 0 || !completeAll,
		"errors":     messages,
	}, nil
}

// doOpenNotebookPush prepares a source for an existing Open Notebook
// notebook. It is deliberately an explicit, free-to-Midden action: the
// destination may use its own configured models and billing, but this does
// not spend the agentic CLI budget tracked by Midden.
//
// Only indexed nuggets leave Midden. Raw transcripts never cross this
// boundary, and the prepared document is redacted once more immediately
// before network transmission.
func (s *Server) doOpenNotebookPush(id string, req actionRequest) (any, error) {
	if req.Plugin != "" && req.Plugin != "open-notebook" {
		return nil, fmt.Errorf("Open Notebook action cannot use plugin %q", req.Plugin)
	}
	if strings.TrimSpace(req.NotebookID) == "" {
		return nil, fmt.Errorf("notebook id is required")
	}
	if req.AllWorkspaces && strings.TrimSpace(req.Workspace) != "" {
		return nil, fmt.Errorf("choose a workspace or all workspaces, not both")
	}
	if !req.AllWorkspaces && strings.TrimSpace(req.Workspace) == "" {
		return nil, fmt.Errorf("choose a workspace or explicitly include all workspaces")
	}

	s.jobs.update(id, func(j *Job) { j.Progress = "validating Open Notebook" })
	manifest, managed, err := s.effectiveIntegration("open-notebook")
	if err != nil {
		return nil, err
	}
	if !manifest.IsEnabled() {
		return nil, fmt.Errorf("Open Notebook integration is disabled")
	}
	verification := plugins.VerifyService(context.Background(), manifest, nil)
	if verification.Result.Status != plugins.Available {
		return nil, fmt.Errorf("Open Notebook is unavailable: %s", verification.Result.Detail)
	}

	prepared, err := opennotebook.Prepare(manifest)
	if err != nil {
		return nil, err
	}
	if managed && manifest.API.Auth.Header != "" && !manifest.API.Auth.Optional &&
		strings.TrimSpace(req.OpenNotebookPassword) == "" {
		return nil, fmt.Errorf("an action-scoped Open Notebook password is required for this send")
	}
	client := prepared.Client.WithPassword(req.OpenNotebookPassword)
	if err := client.CredentialsReady(); err != nil {
		return nil, err
	}

	s.jobs.update(id, func(j *Job) { j.Progress = "preparing redacted nuggets" })
	nuggets, err := s.db.Nuggets(index.NuggetQuery{Workspace: req.Workspace, Limit: 100})
	if err != nil {
		return nil, err
	}
	if len(nuggets) == 0 {
		return nil, fmt.Errorf("no nuggets match this scope")
	}
	body := redact.Text(notebookSourceBody(nuggets)).Text
	title := "Midden reclaimed evidence"
	if req.AllWorkspaces {
		title += " · all workspaces"
	} else {
		title += " · " + req.Workspace
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s.jobs.update(id, func(j *Job) { j.Progress = "uploading source to Open Notebook" })
	source, err := client.CreateTextSource(ctx, prepared.Push, req.NotebookID, title, body)
	if err != nil {
		return nil, err
	}
	link, err := client.NotebookLink(req.NotebookID)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"free":          true,
		"nuggets":       len(nuggets),
		"source_id":     source.ID,
		"source_status": source.Status,
		"notebook_url":  link,
		"note":          "Source submitted. Open Notebook continues processing in its own UI; Midden did not spend your CLI budget.",
	}, nil
}

func notebookSourceBody(nuggets []index.Nugget) string {
	var b strings.Builder
	b.WriteString("# Midden reclaimed evidence\n\n")
	b.WriteString("This source was prepared from stored nuggets, not raw AI CLI transcripts.\n\n")
	for _, nugget := range nuggets {
		fmt.Fprintf(&b, "## %s\n\n", nugget.Title)
		fmt.Fprintf(&b, "- kind: %s\n- source session: %s\n", nugget.Kind, nugget.SessionID)
		if nugget.Workspace != "" {
			fmt.Fprintf(&b, "- workspace: %s\n", nugget.Workspace)
		}
		b.WriteString("\n")
		b.WriteString(nugget.Body)
		b.WriteString("\n\n")
	}
	return b.String()
}

// doPrune previews or applies transcript pruning. Dry run unless Apply is set.
func (s *Server) doPrune(id string, req actionRequest) (any, error) {
	sessions, _ := adapter.Collect(req.scope())
	sessions = req.filterExactSessions(sessions)

	var targets []core.Session
	for _, x := range sessions {
		if x.TranscriptPath != "" && fileExists(x.TranscriptPath) && x.Bytes > 20<<20 {
			targets = append(targets, x)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no transcripts over 20 MiB in scope")
	}
	sort.Slice(targets, func(a, b int) bool { return targets[a].Bytes > targets[b].Bytes })
	if len(targets) > 10 {
		targets = targets[:10]
	}

	opts := dispose.DefaultOptions()
	workDir := filepath.Join(index.Dir(), "pruned")

	type row struct {
		Session  sessionView `json:"session"`
		Before   int64       `json:"before"`
		After    int64       `json:"after"`
		Saved    int64       `json:"saved"`
		Verified bool        `json:"verified"`
		Failures []string    `json:"failures,omitempty"`
		Applied  bool        `json:"applied"`
	}
	var rows []row
	var totalSaved int64
	var failures []string

	for i, x := range targets {
		s.jobs.update(id, func(j *Job) {
			j.Progress = fmt.Sprintf("%d/%d  %s", i+1, len(targets), core.Truncate(x.Title, 40))
		})

		if !req.Apply {
			a, ok := adapter.Find(x.Tool).(adapter.Assayer)
			if !ok {
				message := fmt.Sprintf("%s has no assay adapter", shortID(x.ID))
				rows = append(rows, row{Session: toView(x), Before: x.Bytes, Failures: []string{message}})
				failures = append(failures, message)
				continue
			}
			m, err := a.Assay(x, 0)
			if err != nil {
				message := fmt.Sprintf("%s preview failed: %v", shortID(x.ID), err)
				rows = append(rows, row{Session: toView(x), Before: x.Bytes, Failures: []string{message}})
				failures = append(failures, message)
				continue
			}
			est := (m.Bytes["exhaust"] + m.Bytes["bookkeeping"]) * 9 / 10
			rows = append(rows, row{Session: toView(x), Before: x.Bytes,
				After: x.Bytes - est, Saved: est})
			totalSaved += est
			continue
		}

		target := filepath.Join(workDir, string(x.Tool), x.ID+".jsonl")
		p, err := dispose.PruneJSONL(x.TranscriptPath, target, opts, kindOfLine)
		if err != nil {
			message := fmt.Sprintf("%s prune failed: %v", shortID(x.ID), err)
			rows = append(rows, row{Session: toView(x), Before: x.Bytes, Failures: []string{message}})
			failures = append(failures, message)
			_ = s.db.RecordOp("prune", string(x.Tool), x.ID, x.Bytes, x.Bytes, message, false)
			continue
		}
		v, verr := dispose.Verify(x.TranscriptPath, target, kindOfLine)
		rw := row{Session: toView(x), Before: p.BeforeBytes, After: p.AfterBytes,
			Saved: p.Saved(), Applied: true}
		if verr != nil {
			rw.Failures = append(rw.Failures, verr.Error())
		} else if v != nil {
			rw.Verified = v.OK
			rw.Failures = v.Failures
		}
		if !rw.Verified {
			message := fmt.Sprintf("%s prune verification failed", shortID(x.ID))
			failures = append(failures, message)
			if len(rw.Failures) == 0 {
				rw.Failures = append(rw.Failures, message)
			}
		}
		rows = append(rows, rw)
		totalSaved += p.Saved()

		_ = s.db.RecordOp("prune", string(x.Tool), x.ID, p.BeforeBytes, p.AfterBytes,
			fmt.Sprintf("verified=%v via ui", rw.Verified), rw.Verified)
	}

	result := map[string]any{
		"applied": req.Apply, "rows": rows, "total_saved": totalSaved,
		"output_dir": workDir,
	}
	if len(failures) > 0 {
		return result, fmt.Errorf("%d prune operation(s) failed; inspect the per-session results", len(failures))
	}
	return result, nil
}

// doArchive moves transcripts out of a tool's active path.
func (s *Server) doArchive(id string, req actionRequest) (any, error) {
	// Irreversible enough to demand a second acknowledgement.
	if req.Apply && !req.Confirm {
		return nil, fmt.Errorf("archive requires explicit confirmation")
	}

	sessions, _ := adapter.Collect(req.scope())
	sessions = req.filterExactSessions(sessions)
	root := index.Dir()
	if req.Apply {
		candidates, _, err := s.cleanupCandidates()
		if err != nil {
			return nil, fmt.Errorf("check recovery eligibility: %w", err)
		}
		eligible := map[string]bool{}
		for _, candidate := range candidates {
			if candidate.Decision == "eligible" {
				eligible[sessionIdentity(candidate.Session.Tool, candidate.Session.ID)] = true
			}
		}
		for _, session := range sessions {
			if !eligible[sessionIdentity(string(session.Tool), session.ID)] {
				return nil, fmt.Errorf(
					"session %s is not cleanup-eligible; inspect its recovery gates first",
					shortID(session.ID))
			}
		}
	}

	type archiveRow struct {
		Session string `json:"session"`
		Tool    string `json:"tool"`
		Bytes   int64  `json:"bytes"`
		Target  string `json:"target"`
		Applied bool   `json:"applied"`
		Error   string `json:"error,omitempty"`
	}
	var moved int64
	var rows []archiveRow
	var failures []string

	for _, x := range sessions {
		if x.TranscriptPath == "" || !fileExists(x.TranscriptPath) {
			message := fmt.Sprintf("%s transcript is unavailable", shortID(x.ID))
			rows = append(rows, archiveRow{Session: x.ID, Tool: string(x.Tool), Bytes: x.Bytes, Error: message})
			failures = append(failures, message)
			continue
		}
		if x.Live != nil {
			message := fmt.Sprintf("%s is open and cannot be archived", shortID(x.ID))
			rows = append(rows, archiveRow{Session: x.ID, Tool: string(x.Tool), Bytes: x.Bytes, Error: message})
			failures = append(failures, message)
			continue
		}
		dir := dispose.ArchivePath(root, string(x.Tool), x.ID)
		row := archiveRow{Session: x.ID, Tool: string(x.Tool), Bytes: x.Bytes, Target: dir}
		if !req.Apply {
			rows = append(rows, row)
			continue
		}

		if err := os.MkdirAll(dir, 0o755); err != nil {
			row.Error = err.Error()
			rows = append(rows, row)
			failures = append(failures, fmt.Sprintf("%s archive directory: %v", shortID(x.ID), err))
			_ = s.db.RecordOp("archive", string(x.Tool), x.ID, x.Bytes, x.Bytes, row.Error, false)
			continue
		}
		man := dispose.ArchiveManifest{
			Tool: string(x.Tool), SessionID: x.ID, Title: x.Title,
			Workspace: x.Dir, SourcePath: x.TranscriptPath, Bytes: x.Bytes,
			ArchivedAt: time.Now(),
			Note:       "Archived by midden. Move the transcript back to source_path to restore.",
		}
		mb, err := json.MarshalIndent(man, "", "  ")
		if err != nil {
			row.Error = err.Error()
			rows = append(rows, row)
			failures = append(failures, fmt.Sprintf("%s archive manifest: %v", shortID(x.ID), err))
			_ = s.db.RecordOp("archive", string(x.Tool), x.ID, x.Bytes, x.Bytes, row.Error, false)
			continue
		}
		manifestPath := filepath.Join(dir, "manifest.json")
		if err := os.WriteFile(manifestPath, mb, 0o644); err != nil {
			row.Error = err.Error()
			rows = append(rows, row)
			failures = append(failures, fmt.Sprintf("%s write archive manifest: %v", shortID(x.ID), err))
			_ = s.db.RecordOp("archive", string(x.Tool), x.ID, x.Bytes, x.Bytes, row.Error, false)
			continue
		}
		dst := filepath.Join(dir, filepath.Base(x.TranscriptPath))
		if err := moveArchiveFile(x.TranscriptPath, dst); err != nil {
			_ = os.Remove(manifestPath)
			row.Error = err.Error()
			rows = append(rows, row)
			failures = append(failures, fmt.Sprintf("%s archive move: %v", shortID(x.ID), err))
			_ = s.db.RecordOp("archive", string(x.Tool), x.ID, x.Bytes, x.Bytes, row.Error, false)
			continue
		}
		row.Applied = true
		rows = append(rows, row)
		moved += x.Bytes
		_ = s.db.RecordOp("archive", string(x.Tool), x.ID, x.Bytes, 0, dir+" (ui)", true)
	}
	result := map[string]any{"applied": req.Apply, "rows": rows, "moved": moved}
	if len(failures) > 0 {
		return result, fmt.Errorf("%d archive operation(s) failed; inspect the per-session results", len(failures))
	}
	return result, nil
}

func moveArchiveFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := copyFile(source, target); err != nil {
		return err
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		_ = os.Remove(target)
		return err
	}
	if targetInfo.Size() != sourceInfo.Size() {
		_ = os.Remove(target)
		return fmt.Errorf("archive copy size mismatch")
	}
	if err := os.Remove(source); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

// doReclaim mines sessions for nuggets.
func (s *Server) doReclaim(id string, req actionRequest) (any, error) {
	sessions, collectionErrors := collectMineSessions(req, req.scope())
	if (len(req.SessionKeys) > 0 || len(sessions) == 0) && len(collectionErrors) > 0 {
		return nil, collectionErrors[0]
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions in scope")
	}
	if len(sessions) > 20 {
		return nil, fmt.Errorf(
			"scope matches %d sessions; narrow it to 20 or fewer for one evidence run",
			len(sessions))
	}

	records := req.Records
	if records <= 0 {
		switch strings.ToLower(strings.TrimSpace(req.Depth)) {
		case "", "summary":
			records = 70
		case "deep":
			records = 140
		case "xray", "x-ray":
			records = 220
		default:
			return nil, fmt.Errorf("depth must be summary, deep, or xray")
		}
	}

	type job struct {
		session core.Session
		slice   reclaim.Slice
	}
	var jobs []job
	var raw int
	assayFailures := 0
	for _, x := range sessions {
		a, ok := adapter.Find(x.Tool).(adapter.Assayer)
		if !ok {
			assayFailures++
			continue
		}
		m, err := a.Assay(x, records)
		if err != nil {
			assayFailures++
			continue
		}
		sl := reclaim.BuildSlice(x, m, records)
		if len(sl.Candidates) == 0 {
			continue
		}
		jobs = append(jobs, job{x, sl})
		raw += sl.EstTokens()
	}
	if len(jobs) == 0 {
		if assayFailures > 0 {
			return nil, fmt.Errorf("reclaim failed: all %d eligible session assays failed", assayFailures)
		}
		return nil, fmt.Errorf("no usable evidence in scope")
	}

	stats, _ := s.db.CalibrationFor("reclaim")
	est := cost.Predict("reclaim", raw, stats)
	s.jobs.update(id, func(j *Job) { j.Estimate = &est })

	// A preview stops here: the operator sees the predicted cost first.
	if !req.Apply {
		backend := req.Backend
		if backend == "" {
			backend = "auto-detect signed-in CLI"
		}
		model := req.Model
		if model == "" {
			model = "backend default"
		}
		return map[string]any{
			"preview": true, "sessions": len(jobs),
			"estimate": est, "estimate_text": est.String(),
			"backend": backend, "model": model,
			"depth":               valueOr(req.Depth, "summary"),
			"records_per_session": records,
			"estimated_seconds":   maxInt(30, len(jobs)*75),
		}, nil
	}

	be, err := exec.Detect(req.Backend)
	if err != nil {
		return nil, err
	}
	runner := &exec.Runner{Backend: be, Model: req.Model, Pure: true}
	run := cost.Run{UID: index.NewUID(), Op: "reclaim", Scope: req.label(),
		Backend: string(be), EstTokens: raw, StartedAt: time.Now()}

	var stored []index.Nugget
	modelFailures, parseFailures, writeFailures := 0, 0, 0
	ctx := context.Background()
	for i, jb := range jobs {
		s.jobs.update(id, func(j *Job) {
			j.Progress = fmt.Sprintf("mining %d/%d  %s", i+1, len(jobs),
				core.Truncate(jb.session.Title, 38))
		})

		conv := runner.NewConversation()
		run.CLISessions = append(run.CLISessions, conv.SessionID())

		res, err := conv.Prime(ctx, jb.slice.Prompt())
		if err != nil {
			modelFailures++
			continue
		}
		model := req.Model
		if model == "" {
			model = string(be) + ":default"
		}
		ns, err := reclaim.Parse(res.Output, jb.session, model)
		if err != nil || len(ns) == 0 {
			parseFailures++
			continue
		}
		if err := s.db.PutNuggets(ns); err != nil {
			writeFailures++
			continue
		}
		stored = append(stored, ns...)
	}

	run.Items = len(stored)
	run.EndedAt = time.Now()
	run.OK = len(stored) > 0
	if err := s.db.PutRun(run); err != nil {
		return nil, fmt.Errorf("record reclaim cost run: %w", err)
	}
	if err := reclaimAllWorkFailed(len(stored), len(jobs), modelFailures, parseFailures, writeFailures); err != nil {
		return nil, err
	}

	s.jobs.update(id, func(j *Job) { j.Progress = "settling cost" })
	time.Sleep(1500 * time.Millisecond)
	s.settle(id, run.UID)

	return map[string]any{
		"nuggets": stored, "count": len(stored), "sessions": len(jobs),
		"failed": modelFailures + parseFailures + writeFailures,
		"depth":  valueOr(req.Depth, "summary"),
	}, nil
}

// reclaimAllWorkFailed turns an otherwise invisible all-failure batch into a
// failed durable job and audit record. A partial batch remains truthful: it
// succeeds with its explicit failure count.
func reclaimAllWorkFailed(stored, attempts, modelFailures, parseFailures, writeFailures int) error {
	if stored > 0 {
		return nil
	}
	failed := modelFailures + parseFailures + writeFailures
	if attempts > 0 && failed > 0 {
		return fmt.Errorf("reclaim failed: all %d evidence extraction attempts failed (model=%d, parse=%d, write=%d)", attempts, modelFailures, parseFailures, writeFailures)
	}
	return fmt.Errorf("reclaim produced no evidence")
}

// doRefine generates artifacts from nuggets, batched in one warm context.
func (s *Server) doRefine(id string, req actionRequest) (any, error) {
	ns, err := s.db.Nuggets(index.NuggetQuery{Workspace: req.Workspace, SessionID: req.SessionID})
	if err != nil {
		return nil, err
	}
	if len(ns) == 0 {
		return nil, fmt.Errorf("no nuggets in scope — reclaim first")
	}

	var wanted []refine.Template
	for _, name := range req.Templates {
		if t, ok := refine.FindTemplate(name); ok {
			wanted = append(wanted, t)
		}
	}
	if len(wanted) == 0 {
		return nil, fmt.Errorf("choose at least one artifact")
	}

	scope := req.Workspace
	if scope == "" {
		scope = "all reclaimed evidence"
	}
	ev := refine.Evidence{Scope: scope, Nuggets: ns}
	raw := ev.EstTokens() + len(wanted)*300

	stats, _ := s.db.CalibrationFor("refine")
	est := cost.Predict("refine", raw, stats)
	s.jobs.update(id, func(j *Job) { j.Estimate = &est })

	if !req.Apply {
		return map[string]any{
			"preview": true, "nuggets": len(ns), "artifacts": len(wanted),
			"estimate": est, "estimate_text": est.String(),
		}, nil
	}

	be, err := exec.Detect(req.Backend)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(index.Dir(), "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	runner := &exec.Runner{Backend: be, Model: req.Model, Pure: true, Timeout: 15 * time.Minute}
	conv := runner.NewConversation()
	run := cost.Run{UID: index.NewUID(), Op: "refine", Scope: scope,
		Backend: string(be), EstTokens: raw, StartedAt: time.Now(),
		CLISessions: []string{conv.SessionID()}}

	ctx := context.Background()
	s.jobs.update(id, func(j *Job) { j.Progress = "loading evidence into one session" })
	if _, err := conv.Prime(ctx, ev.Preamble()); err != nil {
		return nil, fmt.Errorf("prime: %w", err)
	}

	model := req.Model
	if model == "" {
		model = string(be) + ":default"
	}

	var out []map[string]any
	written := 0
	for i, t := range wanted {
		s.jobs.update(id, func(j *Job) {
			j.Progress = fmt.Sprintf("writing %d/%d  %s", i+1, len(wanted), t.Name)
		})
		res, err := conv.Ask(ctx, t.Request(""))
		if err != nil {
			out = append(out, map[string]any{"template": t.Name, "error": err.Error()})
			continue
		}
		body := refine.CleanOutput(res.Output)
		if strings.TrimSpace(body) == "" {
			out = append(out, map[string]any{"template": t.Name, "error": "empty output"})
			continue
		}

		warning := ""
		if scan := redact.Scan(body); len(scan) > 0 {
			body = redact.Text(body).Text
			warning = redact.Summary(scan)
		}
		header := fmt.Sprintf("<!-- generated by midden from %d nuggets · scope: %s · model: %s · %s -->\n\n",
			len(ns), scope, model, time.Now().Format("2006-01-02"))
		path := filepath.Join(dir, refine.Slug(scope+"-"+t.Name)+".md")
		if os.WriteFile(path, []byte(header+body), 0o644) != nil {
			continue
		}
		written++
		out = append(out, map[string]any{
			"template": t.Name, "path": path, "bytes": len(body),
			"warning": warning, "preview": firstLines(body, 40),
		})
		s.db.PutArtifact(index.Artifact{Kind: t.Name, Title: t.Title, Path: path,
			Scope: scope, NuggetIDs: refine.NuggetIDs(ns), Model: model})
	}

	run.Items = written
	run.EndedAt = time.Now()
	run.OK = written > 0
	s.db.PutRun(run)

	s.jobs.update(id, func(j *Job) { j.Progress = "settling cost" })
	time.Sleep(1500 * time.Millisecond)
	s.settle(id, run.UID)

	return map[string]any{"artifacts": out, "written": written}, nil
}

// settle reads real usage back and attaches it to the job.
func (s *Server) settle(jobID, runUID string) {
	pending, err := s.db.UnreconciledRuns()
	if err != nil {
		return
	}
	for _, r := range pending {
		var total cost.Usage
		for _, sid := range r.CLISessions {
			u, err := adapter.UsageFor(r.Backend, sid)
			if err != nil || u.Empty() {
				continue
			}
			total.Add(u)
		}
		if total.Empty() {
			continue
		}
		s.db.ReconcileRun(r.UID, total)
		if r.UID == runUID {
			runs, _ := s.db.Runs(1, r.Op)
			if len(runs) > 0 {
				s.jobs.update(jobID, func(j *Job) { j.Cost = &runs[0] })
			}
		}
	}
}

func kindOfLine(line []byte) string {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &probe) != nil || probe.Type == "" {
		return "unparsed"
	}
	return probe.Type
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// doSummarize produces a session summary at the requested depth.
//
// Shallow returns immediately with no model call, so the free tier feels free
// rather than queued behind a spinner.
func (s *Server) doSummarize(id string, req actionRequest) (any, error) {
	sess, err := req.exactSession()
	if err != nil {
		return nil, err
	}

	depth, err := summary.ParseDepth(req.Depth)
	if err != nil {
		return nil, err
	}

	ctx := summary.Context{Session: sess, Depth: depth}
	s.jobs.update(id, func(j *Job) { j.Progress = "gathering evidence" })

	if h, ok := adapter.Find(sess.Tool).(core.Harvester); ok {
		if hv, err := h.Harvest(sess, 12); err == nil {
			ctx.Harvest = hv
		}
	}
	if a, ok := adapter.Find(sess.Tool).(adapter.Assayer); ok {
		if m, err := a.Assay(sess, 80); err == nil {
			ctx.Manifest = m
		}
	}
	if depth == summary.XRay {
		s.jobs.update(id, func(j *Job) { j.Progress = "reading workspace state" })
		ctx.Workspace = summary.InspectWorkspace(sess.Dir)
	}
	ctx.Redact()

	if depth == summary.Shallow {
		return map[string]any{"depth": depth, "body": summary.RenderShallow(ctx), "free": true}, nil
	}

	raw := ctx.RawTokens()
	stats, _ := s.db.CalibrationFor("summarize")
	est := cost.Predict("summarize", raw, stats)
	s.jobs.update(id, func(j *Job) { j.Estimate = &est })

	if !req.Apply {
		return map[string]any{
			"preview": true, "depth": depth, "estimate": est,
			"estimate_text": est.String(), "describe": depth.Describe(),
		}, nil
	}

	be, err := exec.Detect(req.Backend)
	if err != nil {
		return nil, err
	}
	runner := &exec.Runner{Backend: be, Model: req.Model, Pure: true, Timeout: 12 * time.Minute}
	conv := runner.NewConversation()
	run := cost.Run{UID: index.NewUID(), Op: "summarize",
		Scope: string(depth) + " " + shortID(sess.ID), Backend: string(be),
		EstTokens: raw, StartedAt: time.Now(), CLISessions: []string{conv.SessionID()}}

	s.jobs.update(id, func(j *Job) { j.Progress = "reading the session" })
	res, err := conv.Prime(context.Background(), ctx.Prompt())

	run.Items = 1
	run.EndedAt = time.Now()
	run.OK = err == nil
	s.db.PutRun(run)
	if err != nil {
		return nil, err
	}

	body := exec.CleanOutput(res.Output)
	if r := redact.Text(body); r.Redacted {
		body = r.Text
	}

	time.Sleep(1500 * time.Millisecond)
	s.settle(id, run.UID)
	return map[string]any{"depth": depth, "body": body}, nil
}

// doAsk answers a question from the compressed picture.
func (s *Server) doAsk(id string, req actionRequest) (any, error) {
	q := strings.TrimSpace(req.Question)
	if q == "" {
		return nil, fmt.Errorf("a question is required")
	}

	s.jobs.update(id, func(j *Job) { j.Progress = "assembling evidence" })
	st := s.guideState()
	brief := s.buildBrief(st)
	brief.RedactAll()
	brief.Trim(q)

	raw := brief.EstTokens(q)
	stats, _ := s.db.CalibrationFor("ask")
	est := cost.Predict("ask", raw, stats)
	s.jobs.update(id, func(j *Job) { j.Estimate = &est })

	if !req.Apply {
		return map[string]any{
			"preview": true, "estimate": est, "estimate_text": est.String(),
			"nuggets": len(brief.Nuggets), "findings": len(brief.Findings),
		}, nil
	}

	be, err := exec.Detect(req.Backend)
	if err != nil {
		return nil, err
	}
	runner := &exec.Runner{Backend: be, Model: req.Model, Pure: true, Timeout: 10 * time.Minute}
	conv := runner.NewConversation()
	run := cost.Run{UID: index.NewUID(), Op: "ask", Scope: truncate(q, 40),
		Backend: string(be), EstTokens: raw, StartedAt: time.Now(),
		CLISessions: []string{conv.SessionID()}}

	s.jobs.update(id, func(j *Job) { j.Progress = "thinking" })
	res, err := conv.Prime(context.Background(), brief.Prompt(q))

	run.Items = 1
	run.EndedAt = time.Now()
	run.OK = err == nil
	s.db.PutRun(run)
	if err != nil {
		return nil, err
	}

	answer := exec.CleanOutput(res.Output)
	if r := redact.Text(answer); r.Redacted {
		answer = r.Text
	}

	time.Sleep(1500 * time.Millisecond)
	s.settle(id, run.UID)
	return map[string]any{"question": q, "answer": answer}, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// doBrief harvests a session into a paste-able handoff.
//
// This is the remedy for the one problem the tool ranks above everything
// else — a transcript past the resume cliff — and it was reachable only by
// copying a command into a terminal. The most urgent action in the product
// was the one action the product could not perform.
//
// It is free, deterministic and read-only, so there is no estimate step and
// no apply gate: previewing it and running it are the same operation.
func (s *Server) doBrief(id string, req actionRequest) (any, error) {
	sess, err := req.exactSession()
	if err != nil {
		return nil, err
	}

	h, ok := adapter.Find(sess.Tool).(core.Harvester)
	if !ok {
		return nil, fmt.Errorf("%s sessions cannot be harvested yet", sess.Tool)
	}

	// Large transcripts are exactly the ones that need this, so say what is
	// happening rather than appearing to stall.
	s.jobs.update(id, func(j *Job) {
		j.Progress = fmt.Sprintf("reading %s transcript", render.Bytes(sess.Bytes))
	})

	turns := req.Records
	if turns <= 0 {
		turns = 10
	}
	hv, err := h.Harvest(sess, turns)
	if err != nil {
		return nil, fmt.Errorf("harvest: %w", err)
	}

	body := handoff.Text(sess, hv, handoff.DefaultClip)

	// Keep it. A rescue that lives only in the clipboard is lost the moment
	// the tab closes — the one artifact that saves work would be the one the
	// tool does not keep. Failing to save is not failing to rescue, so a
	// write error degrades to clipboard-only rather than losing the brief.
	saved, warning := "", ""
	if scan := redact.Scan(body); len(scan) > 0 {
		body = redact.Text(body).Text
		warning = redact.Summary(scan)
	}
	dir := filepath.Join(index.Dir(), "artifacts")
	if os.MkdirAll(dir, 0o755) == nil {
		name := fmt.Sprintf("handoff-%s-%s.md", string(sess.Tool), shortID(sess.ID))
		path := filepath.Join(dir, name)
		header := fmt.Sprintf("<!-- midden handoff · %s %s · %s · rescued %s -->\n\n",
			sess.Tool, shortID(sess.ID), sess.Dir, time.Now().Format("2006-01-02 15:04"))
		if os.WriteFile(path, []byte(header+body), 0o644) == nil {
			saved = path
			s.db.PutArtifact(index.Artifact{
				Kind:  "handoff",
				Title: core.Truncate(sess.Title, 80),
				Path:  path,
				Scope: sess.Dir,
			})
		}
	}

	return map[string]any{
		"free":       true,
		"session":    shortID(sess.ID),
		"title":      sess.Title,
		"dir":        sess.Dir,
		"user_turns": hv.UserTurns,
		"records":    hv.TotalRecords,
		"body":       body,
		"saved":      saved,
		"warning":    warning,
	}, nil
}
