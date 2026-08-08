package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/cost"
	agentexec "github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/refine"
	"github.com/mekjr1/midden/internal/refinery"
)

type workItemSummary struct {
	Recipe       index.Recipe    `json:"recipe"`
	OutputCount  int             `json:"output_count"`
	MessageCount int             `json:"message_count"`
	LastMessage  string          `json:"last_message,omitempty"`
	Thread       *workThreadView `json:"thread,omitempty"`
}

type workThreadView struct {
	RecipeID       string    `json:"recipe_id"`
	Backend        string    `json:"backend,omitempty"`
	Model          string    `json:"model,omitempty"`
	BudgetTokens   int       `json:"budget_tokens"`
	EstimatedSpent int       `json:"estimated_spent"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

func publicWorkThread(thread index.WorkThread) workThreadView {
	return workThreadView{
		RecipeID: thread.RecipeID, Backend: thread.Backend, Model: thread.Model,
		BudgetTokens: thread.BudgetTokens, EstimatedSpent: thread.EstimatedSpent,
		UpdatedAt: thread.UpdatedAt,
	}
}

func (s *Server) handleWorkItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	recipes, err := s.db.Recipes(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]workItemSummary, 0, len(recipes))
	for _, recipe := range recipes {
		if recipe.Status == refinery.RecipeArchived {
			continue
		}
		outputs, _ := s.db.RefineryOutputs(recipe.UID, 0)
		messages, _ := s.db.WorkMessages(recipe.UID, 100)
		item := workItemSummary{
			Recipe: recipe, OutputCount: len(outputs),
			MessageCount: len(messages),
		}
		if len(messages) > 0 {
			item.LastMessage = core.Truncate(messages[len(messages)-1].Body, 120)
		}
		if thread, err := s.db.WorkThread(recipe.UID); err == nil {
			view := publicWorkThread(thread)
			item.Thread = &view
		}
		items = append(items, item)
	}
	writeJSON(w, items)
}

func (s *Server) handleWorkItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	recipeID := strings.TrimSpace(r.URL.Query().Get("id"))
	recipe, err := s.db.Recipe(recipeID)
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
	outputs, _ := s.db.RefineryOutputs(recipe.UID, 100)
	runs, _ := s.db.RefineryRuns(recipe.UID, 30)
	messages, _ := s.db.WorkMessages(recipe.UID, 200)
	thread, err := s.workThread(recipe.UID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	estimate, report := s.productionEstimate(recipe, evidence)
	writeJSON(w, map[string]any{
		"recipe": recipe, "evidence": evidence, "outputs": outputs, "runs": runs,
		"messages": messages, "thread": publicWorkThread(thread), "evidence_report": report,
		"estimate": estimate, "estimate_text": productionEstimateText(recipe, estimate),
	})
}

func (s *Server) workThread(recipeID string) (index.WorkThread, error) {
	thread, err := s.db.WorkThread(recipeID)
	if err == nil {
		return thread, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return thread, err
	}
	return index.WorkThread{RecipeID: recipeID, BudgetTokens: 1_200_000}, nil
}

// doWorkChat continues one evidence-grounded AI CLI session for a work item.
// The budget is approved once for the thread rather than before every turn.
func (s *Server) doWorkChat(jobID string, req actionRequest) (any, error) {
	question := strings.TrimSpace(req.Question)
	if req.RecipeID == "" || question == "" {
		return nil, fmt.Errorf("work item and message are required")
	}
	threadLock := s.workChatLock(req.RecipeID)
	threadLock.Lock()
	defer threadLock.Unlock()
	recipe, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		return nil, err
	}
	evidence, err := s.db.NuggetsByIDs(recipe.EvidenceIDs)
	if err != nil {
		return nil, err
	}
	if len(evidence) == 0 {
		return nil, fmt.Errorf("this work item has no available evidence")
	}
	thread, err := s.workThread(recipe.UID)
	if err != nil {
		return nil, err
	}
	if req.BudgetTokens > 0 {
		thread.BudgetTokens = req.BudgetTokens
	}
	preferredBackend := valueOr(req.Backend, thread.Backend)
	model := valueOr(req.Model, thread.Model)
	firstTurn := thread.CLISessionID == ""
	rawTokens := len(question)/4 + 500
	if firstTurn {
		rawTokens += len(refinery.EvidencePreamble(recipe, evidence)) / 4
	}
	stats, _ := s.db.CalibrationFor("work_chat")
	estimate := cost.Predict("work_chat", rawTokens, stats)
	s.jobs.update(jobID, func(job *Job) { job.Estimate = &estimate })
	if thread.EstimatedSpent+int(estimate.Mid) > thread.BudgetTokens {
		return nil, fmt.Errorf(
			"this turn would exceed the work-item budget (%d of %d estimated tokens used)",
			thread.EstimatedSpent, thread.BudgetTokens)
	}
	backend, err := agentexec.Detect(preferredBackend)
	if err != nil {
		return nil, err
	}

	userMessage := index.WorkMessage{
		RecipeID: recipe.UID, Role: "user", Body: question, JobID: jobID,
	}
	if err := s.db.PutWorkMessage(&userMessage); err != nil {
		return nil, err
	}

	runner := &agentexec.Runner{
		Backend: backend, Model: model, Pure: true,
		Dir: index.Dir(), Timeout: 10 * time.Minute,
	}
	var conversation *agentexec.Conversation
	if firstTurn {
		conversation = runner.NewConversation()
	} else {
		conversation = runner.ResumeConversation(thread.CLISessionID)
	}
	run := cost.Run{
		UID: index.NewUID(), Op: "work_chat", Scope: recipe.Title,
		Backend: string(backend), EstTokens: rawTokens, StartedAt: time.Now(),
	}
	s.jobs.update(jobID, func(job *Job) { job.Progress = "thinking in the persistent work session" })

	var result *agentexec.Result
	if firstTurn {
		prompt := refinery.EvidencePreamble(recipe, evidence) +
			"\n\nYou are the persistent assistant for this Midden work item. " +
			"Answer the operator from the approved evidence. Do not run tools, " +
			"change files, or invent facts. If a requested change belongs in an " +
			"output, describe the exact revision for the operator to approve.\n\n" +
			"Operator message:\n" + question
		result, err = conversation.Prime(context.Background(), prompt)
	} else {
		result, err = conversation.Ask(context.Background(), question)
	}
	run.Items = 1
	run.CLISessions = []string{conversation.SessionID()}
	run.EndedAt = time.Now()
	run.OK = err == nil
	if err != nil {
		run.Note = err.Error()
	}
	_ = s.db.PutRun(run)
	if err != nil {
		return nil, err
	}

	answer := refine.CleanOutput(result.Output)
	if scan := redact.Text(answer); scan.Redacted {
		answer = scan.Text
	}
	if strings.TrimSpace(answer) == "" {
		return nil, fmt.Errorf("the work session returned an empty answer")
	}
	agentMessage := index.WorkMessage{
		RecipeID: recipe.UID, Role: "agent", Body: answer, JobID: jobID,
	}
	if err := s.db.PutWorkMessage(&agentMessage); err != nil {
		return nil, err
	}
	thread.Backend = string(backend)
	thread.Model = model
	thread.CLISessionID = conversation.SessionID()
	thread.EstimatedSpent += int(estimate.Mid)
	if err := s.db.PutWorkThread(&thread); err != nil {
		return nil, err
	}

	s.jobs.update(jobID, func(job *Job) { job.Progress = "settling work-session usage" })
	time.Sleep(1500 * time.Millisecond)
	s.settle(jobID, run.UID)
	return map[string]any{
		"message": agentMessage, "thread": publicWorkThread(thread), "estimate": estimate,
	}, nil
}

func (s *Server) workChatLock(recipeID string) *sync.Mutex {
	value, _ := s.workChatLocks.LoadOrStore(recipeID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

type consoleRequest struct {
	RecipeID string `json:"recipe_id"`
	Command  string `json:"command"`
}

// handleWorkConsole exposes a deliberately small diagnostic console. It is
// not a host shell; every accepted command is implemented below.
func (s *Server) handleWorkConsole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !requireExplicitMiddenRequest(w, r) {
		return
	}
	var req consoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	recipe, err := s.db.Recipe(req.RecipeID)
	if err != nil {
		http.Error(w, "work item not found", http.StatusNotFound)
		return
	}
	command := strings.ToLower(strings.TrimSpace(req.Command))
	var output string
	switch command {
	case "help", "?":
		output = "available commands:\n  status\n  files\n  evidence\n  runs\n  openmontage status"
	case "status":
		output = fmt.Sprintf(
			"work item: %s\nstatus: %s\nworkspace: %s\nevidence: %d\noutputs: %d",
			recipe.Title, recipe.Status, valueOr(recipe.Workspace, "all evidence"),
			len(recipe.EvidenceIDs), len(recipe.Outputs))
	case "files":
		outputs, _ := s.db.RefineryOutputs(recipe.UID, 100)
		if len(outputs) == 0 {
			output = "no generated files"
			break
		}
		var lines []string
		for _, item := range outputs {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s",
				item.Status, item.Format, filepath.Base(item.Path)))
		}
		output = strings.Join(lines, "\n")
	case "evidence":
		evidence, _ := s.db.NuggetsByIDs(recipe.EvidenceIDs)
		counts := map[string]int{}
		for _, nugget := range evidence {
			counts[nugget.Kind]++
		}
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var lines []string
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("%s\t%d", key, counts[key]))
		}
		output = strings.Join(lines, "\n")
	case "runs":
		runs, _ := s.db.RefineryRuns(recipe.UID, 20)
		if len(runs) == 0 {
			output = "no production runs"
			break
		}
		var lines []string
		for _, run := range runs {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s",
				run.Status, run.StartedAt.Format(time.RFC3339), run.Backend))
		}
		output = strings.Join(lines, "\n")
	case "openmontage status":
		config, err := s.integrationViews()
		if err != nil {
			output = "OpenMontage status unavailable: " + err.Error()
			break
		}
		output = "OpenMontage is not configured"
		for _, item := range config {
			if item.ID == "openmontage" {
				output = fmt.Sprintf("OpenMontage: %s\n%s", item.State, item.StateDetail)
				break
			}
		}
	default:
		http.Error(w, "unsupported console command; use help", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"command": req.Command, "output": output})
}

func (s *Server) handleOutputDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	output, err := s.db.RefineryOutput(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "output not found", http.StatusNotFound)
		return
	}
	path, err := safeRefineryFile(output.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "output file not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "output unavailable", http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	name := filepath.Base(path)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// handleOutputRendered serves an inline, type-appropriate preview. Renderers
// are invoked directly with fixed arguments, never through a shell.
func (s *Server) handleOutputRendered(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	output, err := s.db.RefineryOutput(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "output not found", http.StatusNotFound)
		return
	}
	source, err := safeRefineryFile(output.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	preview := source
	if output.Format == "d2" {
		preview, err = renderD2Preview(source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotImplemented)
			return
		}
	}
	extension := strings.ToLower(filepath.Ext(preview))
	allowed := map[string]string{
		".svg": "image/svg+xml",
		".png": "image/png",
		".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".gif": "image/gif", ".webp": "image/webp",
		".mp4": "video/mp4", ".webm": "video/webm",
		".pdf": "application/pdf",
	}
	contentType, ok := allowed[extension]
	if !ok {
		http.Error(w, "this output has no inline renderer", http.StatusNotImplemented)
		return
	}
	file, err := os.Open(preview)
	if err != nil {
		http.Error(w, "rendered output unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "rendered output unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, filepath.Base(preview), info.ModTime(), file)
}

func renderD2Preview(source string) (string, error) {
	binary, err := exec.LookPath("d2")
	if err != nil {
		return "", fmt.Errorf("D2 is not installed; connect it under Tools to render this source")
	}
	target := source + ".preview.svg"
	target, err = safeRefineryFile(target)
	if err != nil {
		return "", fmt.Errorf("unsafe D2 preview target: %w", err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("inspect D2 source: %w", err)
	}
	if targetInfo, err := os.Stat(target); err == nil &&
		!targetInfo.ModTime().Before(sourceInfo.ModTime()) {
		return target, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, source, target)
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("D2 preview timed out")
		}
		return "", fmt.Errorf("D2 preview failed: %s", strings.TrimSpace(string(output)))
	}
	return target, nil
}

type cleanupGate struct {
	Key    string `json:"key"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

type cleanupCandidate struct {
	Session         sessionView   `json:"session"`
	Decision        string        `json:"decision"`
	DormantDays     int           `json:"dormant_days"`
	Evidence        int           `json:"evidence"`
	Outputs         int           `json:"outputs"`
	ReviewedOutputs int           `json:"reviewed_outputs"`
	ActiveRefs      int           `json:"active_refs"`
	NewerSessions   int           `json:"newer_sessions"`
	SourceFresh     bool          `json:"source_fresh"`
	Gates           []cleanupGate `json:"gates"`
}

func (s *Server) handleCleanupCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	candidates, eligibleBytes, err := s.cleanupCandidates()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"candidates": candidates, "eligible_bytes": eligibleBytes,
	})
}

func (s *Server) cleanupCandidates() ([]cleanupCandidate, int64, error) {
	snap := s.cache.get(s)
	nuggets, err := s.db.Nuggets(index.NuggetQuery{Limit: 10000})
	if err != nil {
		return nil, 0, err
	}
	outputs, _ := s.db.RefineryOutputs("", 10000)
	recipes, _ := s.db.Recipes(1000)

	nuggetsBySession := map[string][]index.Nugget{}
	nuggetSession := map[string]string{}
	for _, nugget := range nuggets {
		key := sessionIdentity(nugget.Tool, nugget.SessionID)
		nuggetsBySession[key] = append(nuggetsBySession[key], nugget)
		nuggetSession[nugget.UID] = key
	}
	outputCount := map[string]int{}
	reviewedCount := map[string]int{}
	for _, output := range outputs {
		seen := map[string]bool{}
		for _, evidenceID := range output.EvidenceIDs {
			sessionID := nuggetSession[evidenceID]
			if sessionID == "" || seen[sessionID] {
				continue
			}
			seen[sessionID] = true
			outputCount[sessionID]++
			if output.Status == refinery.OutputReviewed ||
				output.Status == refinery.OutputExported {
				reviewedCount[sessionID]++
			}
		}
	}
	activeRefs := map[string]int{}
	for _, recipe := range recipes {
		if recipe.Status == refinery.RecipeComplete ||
			recipe.Status == refinery.RecipeArchived {
			continue
		}
		seen := map[string]bool{}
		for _, evidenceID := range recipe.EvidenceIDs {
			sessionID := nuggetSession[evidenceID]
			if sessionID == "" || seen[sessionID] {
				continue
			}
			seen[sessionID] = true
			activeRefs[sessionID]++
		}
	}

	candidates := make([]cleanupCandidate, 0)
	var eligibleBytes int64
	now := time.Now()
	for _, session := range snap.Sessions {
		if session.Noise || session.TranscriptPath == "" || session.Bytes == 0 {
			continue
		}
		dormant := int(now.Sub(session.Updated).Hours() / 24)
		sourceBytes, sourceMtime := index.SourceStamp(session)
		fresh := s.db.ManifestFresh(string(session.Tool), session.ID,
			sourceBytes, sourceMtime)
		key := sessionIdentity(string(session.Tool), session.ID)
		evidence := len(nuggetsBySession[key])
		used := outputCount[key]
		reviewed := reviewedCount[key]
		active := activeRefs[key]
		newer := 0
		for _, other := range snap.Sessions {
			if other.ID != session.ID && other.Dir == session.Dir &&
				other.Updated.After(session.Updated) {
				newer++
			}
		}
		gates := []cleanupGate{
			{Key: "closed", Pass: session.Live == nil, Detail: "session is not open"},
			{Key: "dormant", Pass: dormant >= 90, Detail: fmt.Sprintf("%d day(s) dormant", dormant)},
			{Key: "fingerprint", Pass: fresh, Detail: "source unchanged since assay"},
			{Key: "evidence", Pass: evidence > 0, Detail: fmt.Sprintf("%d evidence item(s)", evidence)},
			{Key: "outputs", Pass: used > 0 && reviewed == used, Detail: fmt.Sprintf("%d of %d output reference(s) reviewed", reviewed, used)},
			{Key: "references", Pass: active == 0, Detail: fmt.Sprintf("%d active work-item reference(s)", active)},
		}
		decision := "held"
		switch {
		case session.Live != nil || dormant < 30 || active > 0:
			decision = "protected"
		case dormant >= 90 && fresh && evidence > 0 && used > 0 &&
			reviewed == used && active == 0:
			decision = "eligible"
			eligibleBytes += session.Bytes
		}
		candidates = append(candidates, cleanupCandidate{
			Session: toView(session), Decision: decision, DormantDays: dormant,
			Evidence: evidence, Outputs: used, ReviewedOutputs: reviewed,
			ActiveRefs: active, NewerSessions: newer, SourceFresh: fresh, Gates: gates,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		rank := map[string]int{"eligible": 0, "held": 1, "protected": 2}
		if rank[candidates[i].Decision] != rank[candidates[j].Decision] {
			return rank[candidates[i].Decision] < rank[candidates[j].Decision]
		}
		return candidates[i].Session.Bytes > candidates[j].Session.Bytes
	})
	return candidates, eligibleBytes, nil
}

func sessionIdentity(tool, id string) string {
	return strings.ToLower(strings.TrimSpace(tool)) + ":" + strings.TrimSpace(id)
}
