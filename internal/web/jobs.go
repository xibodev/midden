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
	mu   sync.RWMutex
	jobs map[string]*Job
	seq  int
}

func NewJobs() *Jobs { return &Jobs{jobs: map[string]*Job{}} }

func (j *Jobs) create(op, scope string) *Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	job := &Job{
		ID:      fmt.Sprintf("job-%d-%d", time.Now().Unix(), j.seq),
		Op:      op,
		Scope:   scope,
		Status:  Queued,
		Started: time.Now(),
	}
	j.jobs[job.ID] = job
	return job
}

func (j *Jobs) update(id string, fn func(*Job)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job, ok := j.jobs[id]; ok {
		fn(job)
	}
}

func (j *Jobs) get(id string) (*Job, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	job, ok := j.jobs[id]
	if !ok {
		return nil, false
	}
	c := *job
	return &c, true
}

func (j *Jobs) list() []*Job {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]*Job, 0, len(j.jobs))
	for _, job := range j.jobs {
		c := *job
		out = append(out, &c)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Started.After(out[b].Started) })
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// actionRequest is the body every action endpoint accepts.
type actionRequest struct {
	Op                   string   `json:"op"`
	SessionID            string   `json:"session_id"`
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
	case a.SessionID != "":
		return "session " + shortID(a.SessionID)
	case a.Workspace != "":
		return "workspace " + a.Workspace
	case a.Days > 0:
		return fmt.Sprintf("last %dd", a.Days)
	}
	return "all"
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
		"refresh", "mine", "production", "open_notebook_push":
	default:
		http.Error(w, "unknown op: "+req.Op, http.StatusBadRequest)
		return
	}

	job := s.jobs.create(req.Op, req.label())
	go s.runJob(job.ID, req)
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
	writeJSON(w, s.jobs.list())
}

// runJob dispatches to the operation implementations.
func (s *Server) runJob(id string, req actionRequest) {
	s.jobs.update(id, func(j *Job) {
		j.Status = Running
		j.Progress = "starting"
	})

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
	}

	s.jobs.update(id, func(j *Job) {
		j.Ended = time.Now()
		if err != nil {
			j.Status = Failed
			j.Error = err.Error()
			return
		}
		j.Status = Done
		j.Progress = "complete"
		j.Result = result
	})
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

	for i, x := range targets {
		s.jobs.update(id, func(j *Job) {
			j.Progress = fmt.Sprintf("%d/%d  %s", i+1, len(targets), core.Truncate(x.Title, 40))
		})

		if !req.Apply {
			a, ok := adapter.Find(x.Tool).(adapter.Assayer)
			if !ok {
				continue
			}
			m, err := a.Assay(x, 0)
			if err != nil {
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
			continue
		}
		v, verr := dispose.Verify(x.TranscriptPath, target, kindOfLine)
		rw := row{Session: toView(x), Before: p.BeforeBytes, After: p.AfterBytes,
			Saved: p.Saved(), Applied: true}
		if verr == nil && v != nil {
			rw.Verified = v.OK
			rw.Failures = v.Failures
		}
		rows = append(rows, rw)
		totalSaved += p.Saved()

		s.db.RecordOp("prune", string(x.Tool), x.ID, p.BeforeBytes, p.AfterBytes,
			fmt.Sprintf("verified=%v via ui", rw.Verified), true)
	}

	return map[string]any{
		"applied": req.Apply, "rows": rows, "total_saved": totalSaved,
		"output_dir": workDir,
	}, nil
}

// doArchive moves transcripts out of a tool's active path.
func (s *Server) doArchive(id string, req actionRequest) (any, error) {
	// Irreversible enough to demand a second acknowledgement.
	if req.Apply && !req.Confirm {
		return nil, fmt.Errorf("archive requires explicit confirmation")
	}

	sessions, _ := adapter.Collect(req.scope())
	root := index.Dir()

	var moved int64
	var rows []map[string]any

	for _, x := range sessions {
		if x.TranscriptPath == "" || !fileExists(x.TranscriptPath) {
			continue
		}
		if x.Live != nil {
			continue // never touch a session that is open right now
		}
		dir := dispose.ArchivePath(root, string(x.Tool), x.ID)
		rows = append(rows, map[string]any{
			"session": x.ID, "tool": string(x.Tool), "bytes": x.Bytes, "target": dir,
		})
		if !req.Apply {
			continue
		}

		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		man := dispose.ArchiveManifest{
			Tool: string(x.Tool), SessionID: x.ID, Title: x.Title,
			Workspace: x.Dir, SourcePath: x.TranscriptPath, Bytes: x.Bytes,
			ArchivedAt: time.Now(),
			Note:       "Archived by midden. Move the transcript back to source_path to restore.",
		}
		mb, _ := json.MarshalIndent(man, "", "  ")
		os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o644)

		dst := filepath.Join(dir, filepath.Base(x.TranscriptPath))
		if os.Rename(x.TranscriptPath, dst) == nil {
			moved += x.Bytes
			s.db.RecordOp("archive", string(x.Tool), x.ID, x.Bytes, 0, dir+" (ui)", true)
		}
	}
	return map[string]any{"applied": req.Apply, "rows": rows, "moved": moved}, nil
}

// doReclaim mines sessions for nuggets.
func (s *Server) doReclaim(id string, req actionRequest) (any, error) {
	sessions, _ := adapter.Collect(req.scope())
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions in scope")
	}
	if len(sessions) > 8 {
		sessions = sessions[:8]
	}

	records := req.Records
	if records <= 0 {
		records = 100
	}

	type job struct {
		session core.Session
		slice   reclaim.Slice
	}
	var jobs []job
	var raw int
	for _, x := range sessions {
		a, ok := adapter.Find(x.Tool).(adapter.Assayer)
		if !ok {
			continue
		}
		m, err := a.Assay(x, records)
		if err != nil {
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
			"estimated_seconds": maxInt(30, len(jobs)*75),
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
			continue
		}
		model := req.Model
		if model == "" {
			model = string(be) + ":default"
		}
		ns, err := reclaim.Parse(res.Output, jb.session, model)
		if err != nil || len(ns) == 0 {
			continue
		}
		if s.db.PutNuggets(ns) == nil {
			stored = append(stored, ns...)
		}
	}

	run.Items = len(stored)
	run.EndedAt = time.Now()
	run.OK = len(stored) > 0
	s.db.PutRun(run)

	s.jobs.update(id, func(j *Job) { j.Progress = "settling cost" })
	time.Sleep(1500 * time.Millisecond)
	s.settle(id, run.UID)

	return map[string]any{"nuggets": stored, "count": len(stored)}, nil
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
	if req.SessionID == "" {
		return nil, fmt.Errorf("a session is required")
	}
	matches, _ := adapter.Collect(core.Scope{IDPrefix: req.SessionID, IncludeNoise: true})
	if len(matches) == 0 {
		return nil, fmt.Errorf("session not found")
	}
	sess := matches[0]

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
	if req.SessionID == "" {
		return nil, fmt.Errorf("a session is required")
	}

	matches, _ := adapter.Collect(core.Scope{IDPrefix: req.SessionID, IncludeNoise: true})
	if len(matches) == 0 {
		return nil, fmt.Errorf("session not found")
	}
	sess := matches[0]

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
