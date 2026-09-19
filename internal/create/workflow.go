package create

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
	"github.com/mekjr1/midden/internal/refinery"
)

// Workflow is the recovery production lifecycle shared by native tools, the
// detached module and HTTP. It owns no agent, provider or conversation engine.
type Workflow struct {
	DB       *index.DB
	Generate func(context.Context, string) (string, error)
	Model    string
	Drafts   map[string]string
}

type Change struct {
	Tool        string   `json:"tool,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Offset      int      `json:"offset,omitempty"`
	RecipeID    string   `json:"recipe_id,omitempty"`
	OutputID    string   `json:"output_id,omitempty"`
	Workspace   string   `json:"workspace,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Title       string   `json:"title,omitempty"`
	Decision    string   `json:"decision,omitempty"`
	Destination string   `json:"destination,omitempty"`
	Body        string   `json:"body,omitempty"`
	OutputKinds []string `json:"output_kinds,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// ExpectedDigest fences review against a draft changing after inspection.
	ExpectedDigest string            `json:"expected_digest,omitempty"`
	Format         string            `json:"format,omitempty"`
	Drafts         map[string]string `json:"drafts,omitempty"`
	ReviewNotes    string            `json:"review_notes,omitempty"`
}

func (w Workflow) Design(c Change, save bool) (any, error) {
	p := Planner{DB: w.DB}
	r, err := p.Design(PlanRequest{Prompt: c.Prompt, Workspace: c.Workspace, OutputKinds: c.OutputKinds, EvidenceIDs: c.EvidenceIDs, Title: c.Title})
	if err != nil {
		return nil, err
	}
	if save {
		if err = p.Save(&r); err != nil {
			return nil, err
		}
	}
	evidence, err := w.DB.NuggetsByIDs(r.EvidenceIDs)
	if err != nil {
		return nil, err
	}
	estimate, report := Estimate(w.DB, r, evidence)
	return map[string]any{"recipe": r, "evidence_report": report, "estimate": estimate, "preview": !save}, nil
}

func (w Workflow) Inspect(id string) (any, error) {
	r, err := w.DB.Recipe(id)
	if err != nil {
		return nil, err
	}
	evidence, err := w.DB.NuggetsByIDs(r.EvidenceIDs)
	if err != nil {
		return nil, err
	}
	outputs, err := w.DB.RefineryOutputs(id, 0)
	if err != nil {
		return nil, err
	}
	runs, err := w.DB.RefineryRuns(id, 30)
	if err != nil {
		return nil, err
	}
	estimate, report := Estimate(w.DB, r, evidence)
	return map[string]any{"recipe": r, "evidence": evidence, "citation_keys": CitationKeys(evidence), "outputs": outputs, "runs": runs, "estimate": estimate, "evidence_report": report}, nil
}

func (w Workflow) Update(c Change) (any, error) {
	r, err := w.DB.Recipe(c.RecipeID)
	if err != nil {
		return nil, err
	}
	if r.Status == refinery.RecipeRunning || r.Status == refinery.RecipeArchived {
		return nil, fmt.Errorf("a %s recipe cannot be edited", r.Status)
	}
	if strings.TrimSpace(c.Title) != "" {
		r.Title = strings.TrimSpace(c.Title)
	}
	if strings.TrimSpace(c.Prompt) != "" {
		r.Request = strings.TrimSpace(c.Prompt)
	}
	if len(c.OutputKinds) > 0 {
		r.Outputs = nil
		for _, kind := range uniqueStrings(c.OutputKinds) {
			spec, ok := refinery.FindTemplate(kind)
			if !ok {
				return nil, fmt.Errorf("unknown output %q", kind)
			}
			r.Outputs = append(r.Outputs, spec)
		}
	}
	r.Status = refinery.RecipeEvidenceReview
	r.ApprovedAt = time.Time{}
	if err = w.DB.PutRecipe(&r); err != nil {
		return nil, err
	}
	return map[string]any{"recipe": r}, nil
}

func (w Workflow) SelectEvidence(c Change, approve bool) (any, error) {
	r, err := w.DB.Recipe(c.RecipeID)
	if err != nil {
		return nil, err
	}
	if r.Status == refinery.RecipeRunning || r.Status == refinery.RecipeArchived {
		return nil, fmt.Errorf("evidence cannot be changed while recipe is %s", r.Status)
	}
	ids := uniqueStrings(c.EvidenceIDs)
	if err = w.DB.CheckEditorialRecipeScope(r.UID, ids); err != nil {
		return nil, err
	}
	evidence, err := w.DB.NuggetsByIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(ids) != len(evidence) {
		return nil, fmt.Errorf("one or more evidence items no longer exist")
	}
	r.EvidenceIDs = ids
	r.Status = refinery.RecipeEvidenceReview
	r.ApprovedAt = time.Time{}
	report := refinery.AssessEvidence(r, evidence)
	if approve {
		if report.Blocked {
			return nil, fmt.Errorf("the evidence set is blocked")
		}
		r.Status = refinery.RecipeApproved
		r.ApprovedAt = time.Now()
	}
	if err = w.DB.PutRecipe(&r); err != nil {
		return nil, err
	}
	return map[string]any{"recipe": r, "evidence_report": report}, nil
}

func (w Workflow) Produce(ctx context.Context, id string) (result any, err error) {
	r, e := w.DB.Recipe(id)
	if e != nil {
		return nil, e
	}
	if e = w.DB.CheckEditorialRecipeScope(r.UID, r.EvidenceIDs); e != nil {
		return nil, e
	}
	evidence, e := w.DB.NuggetsByIDs(r.EvidenceIDs)
	if e != nil {
		return nil, e
	}
	estimate, report := Estimate(w.DB, r, evidence)
	if w.Drafts != nil {
		estimate = cost.Estimate{Op: "compose"}
	}
	if w.Drafts != nil {
		if r.Status == refinery.RecipeRunning || r.Status == refinery.RecipeArchived {
			return nil, fmt.Errorf("cannot compose while recipe is %s", r.Status)
		}
		if len(evidence) == 0 || len(evidence) != len(r.EvidenceIDs) || report.Blocked {
			return nil, fmt.Errorf("select available evidence before composing a local draft")
		}
	} else {
		if e = (Producer{DB: w.DB}).CheckRunnable(r, evidence, report); e != nil {
			return nil, e
		}
	}
	for _, spec := range r.Outputs {
		if spec.RequiresModel && w.Drafts != nil && strings.TrimSpace(w.Drafts[spec.Kind]) == "" {
			return nil, fmt.Errorf("authored draft missing for %s", spec.Kind)
		}
		if spec.RequiresModel && w.Generate == nil && w.Drafts == nil {
			return nil, fmt.Errorf("model runtime unavailable for %s", spec.Kind)
		}
	}
	if w.Drafts != nil {
		for kind := range w.Drafts {
			found := false
			for _, spec := range r.Outputs {
				if spec.Kind == kind && spec.RequiresModel {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("draft %s is not a model-backed output in this recipe", kind)
			}
		}
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	var claimed bool
	if w.Drafts != nil {
		claimed, e = w.DB.ClaimRecipeForDraft(id)
	} else {
		claimed, e = w.DB.ClaimRecipeForRun(id)
	}
	if e != nil {
		return nil, e
	}
	if !claimed {
		return nil, fmt.Errorf("recipe already running or no longer approved")
	}
	run := index.RefineryRun{RecipeID: id, Status: "running", Model: w.Model, Backend: "host", Estimate: estimate}
	if e = w.DB.PutRefineryRun(&run); e != nil {
		if w.Drafts == nil {
			r.Status = refinery.RecipeFailed
		}
		_ = w.DB.PutRecipe(&r)
		return nil, e
	}
	defer func() {
		if err != nil {
			run.Status = "failed"
			run.Error = err.Error()
			run.EndedAt = time.Now()
			_ = w.DB.PutRefineryRun(&run)
			if w.Drafts == nil {
				r.Status = refinery.RecipeFailed
			}
			_ = w.DB.PutRecipe(&r)
		}
	}()
	modelOutputs := 0
	for _, spec := range r.Outputs {
		if spec.RequiresModel && w.Drafts == nil {
			modelOutputs++
		}
	}
	var ledger *cost.Run
	if modelOutputs > 0 {
		ledger = &cost.Run{UID: index.NewUID(), Op: "refinery", Scope: r.Title, Backend: w.Model, EstTokens: estimate.RawTokens, StartedAt: time.Now()}
		if err = w.DB.PutRun(*ledger); err != nil {
			return nil, err
		}
		defer func() {
			ledger.EndedAt = time.Now()
			ledger.OK = err == nil
			if err != nil {
				ledger.Note = err.Error()
			}
			if saveErr := w.DB.PutRun(*ledger); saveErr != nil && err == nil {
				err = fmt.Errorf("production ledger could not be saved: %w", saveErr)
			}
		}()
	}
	dir := filepath.Join(filepath.Dir(w.DB.Path()), "artifacts", "refinery", refinery.Slug(r.Title)+"-"+r.UID, run.UID)
	if err = w.checkWritePath(dir); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	outputs := []index.RefineryOutput{}
	for i, spec := range r.Outputs {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		body, deterministic, e := refinery.DeterministicOutput(spec, r, evidence)
		if e != nil {
			return nil, e
		}
		model := "deterministic"
		if !deterministic && w.Drafts != nil {
			body = w.Drafts[spec.Kind]
			model = "host-authored"
		} else if !deterministic {
			preamble := strings.TrimSuffix(refinery.EvidencePreamble(r, evidence), "Reply with READY and wait for the first deliverable request.")
			body, e = w.Generate(ctx, preamble+"\n"+refinery.OutputRequest(spec)+"\nIMPORTANT: This call produces ONLY the output kind "+spec.Kind+". The recipe mentions other deliverables for context; DO NOT include those other deliverables in this response. Do not reply READY. Return the finished single artifact now.")
			if e != nil {
				return nil, e
			}
			model = w.Model
		}
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("%s returned an empty draft", spec.Kind)
		}
		if spec.Kind == "slides" {
			if err = ValidateSlideSource(body); err != nil {
				return nil, fmt.Errorf("slide draft failed validation: %w", err)
			}
		}
		body = refinery.WrapOutput(spec, r, redact.Text(body).Text, model, len(evidence))
		path := filepath.Join(dir, fmt.Sprintf("%02d-%s%s", i+1, refinery.Slug(spec.Title), refinery.FileExtension(spec)))
		if err = w.checkWritePath(path); err != nil {
			return nil, err
		}
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for _, n := range evidence {
			items = append(items, map[string]any{"evidence_id": n.UID, "session_id": n.SessionID, "tool": n.Tool, "turn_ref": n.TurnRef, "kind": n.Kind, "title": n.Title, "confidence": n.Confidence, "extraction_model": n.Model})
		}
		manifest := map[string]any{"recipe_id": id, "run_id": run.UID, "output_kind": spec.Kind, "model": model, "status": "draft", "evidence": items, "citation_keys": CitationKeys(evidence), "semantic_verification": "not_proven", "content_digest": Digest([]byte(body)), "generation_digest": Digest([]byte(body)), "raw_transcripts_included": false}
		raw, e := json.MarshalIndent(manifest, "", "  ")
		if e != nil {
			return nil, e
		}
		if err = os.WriteFile(path+".provenance.json", raw, 0600); err != nil {
			return nil, err
		}
		out := index.RefineryOutput{RecipeID: id, RunID: run.UID, Kind: spec.Kind, Title: spec.Title, Maker: spec.Maker, Format: spec.Format, Status: refinery.OutputDraft, Path: path, ProvenancePath: path + ".provenance.json", EvidenceIDs: append([]string(nil), r.EvidenceIDs...), Quality: report.Quality}
		if err = w.DB.PutRefineryOutput(&out); err != nil {
			return nil, err
		}
		if err = w.DB.PutArtifact(index.Artifact{Kind: "refinery:" + spec.Kind, Title: spec.Title, Path: path, Scope: r.Workspace, NuggetIDs: r.EvidenceIDs, Model: model}); err != nil {
			return nil, err
		}
		outputs = append(outputs, out)
		if ledger != nil {
			ledger.Items = len(outputs)
		}
	}
	run.Status = "done"
	run.EndedAt = time.Now()
	if err = w.DB.PutRefineryRun(&run); err != nil {
		return nil, err
	}
	r.Status = refinery.RecipeReview
	if err = w.DB.PutRecipe(&r); err != nil {
		return nil, err
	}
	return map[string]any{"recipe": r, "run": run, "outputs": outputs, "review_required": true}, nil
}

func Digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// OwnedFile resolves symlinks before accepting an existing artifact path.
func (w Workflow) OwnedFile(path string) (string, error) {
	if err := w.checkWritePath(path); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(w.DB.Path()), "artifacts", "refinery"))
	if err != nil {
		return "", err
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, want)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("output is outside the owned refinery directory")
	}
	return want, nil
}

func (w Workflow) ReadOutput(id string) (any, error) {
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return nil, err
	}
	path, err := w.OwnedFile(o.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > 2<<20 {
		return nil, fmt.Errorf("output exceeds text inspection bound")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	prov, err := w.OwnedFile(o.ProvenancePath)
	if err != nil {
		return nil, err
	}
	p, err := os.ReadFile(prov)
	if err != nil {
		return nil, err
	}
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("output exceeds text inspection bound")
	}
	return map[string]any{"output": o, "body": string(raw), "provenance": string(p), "content_digest": Digest(raw)}, nil
}

func (w Workflow) Review(c Change) (any, error) {
	o, err := w.DB.RefineryOutput(c.OutputID)
	if err != nil {
		return nil, err
	}
	if c.Decision != "draft" && c.Decision != "reviewed" && c.Decision != "rejected" {
		return nil, fmt.Errorf("decision must be draft, reviewed, or rejected")
	}
	path, err := w.OwnedFile(o.Path)
	if err != nil {
		return nil, err
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if c.ExpectedDigest != "" && c.ExpectedDigest != Digest(old) {
		return nil, fmt.Errorf("output changed since inspection; read it again before review")
	}
	c.Body, c.EvidenceIDs, err = EffectiveReview(o, string(old), c)
	if err != nil {
		return nil, err
	}
	if c.Body == "" {
		c.Body = string(old)
	}
	c.Body = NormalizeSourceMetadata(c.Body)
	if c.Decision == "reviewed" && strings.TrimSpace(c.Body) == "" {
		return nil, fmt.Errorf("a reviewed output cannot be empty")
	}
	selected := c.EvidenceIDs
	if o.Format == "jsonl" && c.Body != "" {
		selected, err = EvidenceIDsFromJSONL(c.Body)
		if err != nil {
			return nil, err
		}
	}
	if selected != nil {
		allowed := map[string]bool{}
		for _, id := range o.EvidenceIDs {
			allowed[id] = true
		}
		selected = uniqueStrings(selected)
		for _, id := range selected {
			if !allowed[id] {
				return nil, fmt.Errorf("evidence %s is not part of this output", id)
			}
		}
		if c.Decision == "reviewed" && len(selected) == 0 {
			return nil, fmt.Errorf("a reviewed structured output must retain at least one evidence item")
		}
		o.EvidenceIDs = selected
	}
	prov, err := w.OwnedFile(o.ProvenancePath)
	if err != nil {
		return nil, err
	}
	p, err := os.ReadFile(prov)
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err = json.Unmarshal(p, &manifest); err != nil {
		return nil, err
	}
	if c.Body != "" {
		// Preserve the previous bytes for revision history before replacing a draft.
		if err = w.checkWritePath(filepath.Join(filepath.Dir(path), "revisions", filepath.Base(path)+"."+Digest(old))); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Join(filepath.Dir(path), "revisions"), 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(filepath.Dir(path), "revisions", filepath.Base(path)+"."+Digest(old)), old, 0600); err != nil {
			return nil, err
		}
		old = []byte(redact.Text(c.Body).Text)
		if err = os.WriteFile(path, old, 0600); err != nil {
			return nil, err
		}
	}
	allowed := map[string]bool{}
	for _, id := range o.EvidenceIDs {
		allowed[id] = true
	}
	if rows, ok := manifest["evidence"].([]any); ok {
		filtered := []any{}
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				if id, _ := m["evidence_id"].(string); allowed[id] {
					filtered = append(filtered, m)
				}
			}
		}
		manifest["evidence"] = filtered
	}
	manifest["status"] = c.Decision
	manifest["review_state"] = c.Decision
	manifest["delivery_state"] = "not_exported"
	manifest["review_notes"] = c.ReviewNotes
	manifest["content_digest"] = Digest(old)
	p, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(prov, p, 0600); err != nil {
		return nil, err
	}
	o.Status = c.Decision
	o.ReviewedAt = time.Time{}
	o.ExportedAt = time.Time{}
	if c.Decision == "reviewed" {
		o.ReviewedAt = time.Now()
	}
	if err = w.DB.PutRefineryOutput(&o); err != nil {
		return nil, err
	}
	w.CompleteIfReviewed(o.RecipeID)
	return map[string]any{"output": o, "content_digest": Digest(old)}, nil
}

func (w Workflow) Export(c Change) (any, error) {
	o, err := w.DB.RefineryOutput(c.OutputID)
	if err != nil {
		return nil, err
	}
	if o.Status != refinery.OutputReviewed && o.Status != refinery.OutputExported {
		return nil, fmt.Errorf("review this output before exporting it")
	}
	if c.Destination != "" && c.Destination != "local_vault" {
		return nil, fmt.Errorf("only reviewed local_vault export is supported")
	}
	r, err := w.DB.Recipe(o.RecipeID)
	if err != nil {
		return nil, err
	}
	path, err := w.OwnedFile(o.Path)
	if err != nil {
		return nil, err
	}
	prov, err := w.OwnedFile(o.ProvenancePath)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(prov)
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if digest, _ := manifest["content_digest"].(string); digest == "" || digest != Digest(body) {
		return nil, fmt.Errorf("reviewed bytes changed or digest is missing; review again before export")
	}
	manifest["status"] = refinery.OutputExported
	manifest["review_state"] = refinery.OutputReviewed
	manifest["delivery_state"] = "local_vault"
	raw, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	// Content-addressed exports keep older reviewed revisions intact.
	dir := filepath.Join(filepath.Dir(w.DB.Path()), "exports", refinery.Slug(r.Title), o.UID, Digest(body))
	if err = w.checkWritePath(dir); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, filepath.Base(path))
	pt := filepath.Join(dir, filepath.Base(prov))
	if err = w.checkWritePath(target); err != nil {
		return nil, err
	}
	if err = w.checkWritePath(pt); err != nil {
		return nil, err
	}
	if err = os.WriteFile(target, body, 0600); err != nil {
		return nil, err
	}
	if err = os.WriteFile(pt, raw, 0600); err != nil {
		return nil, err
	}
	if err = os.WriteFile(prov, raw, 0600); err != nil {
		return nil, err
	}
	o.Status = refinery.OutputExported
	o.ExportedAt = time.Now()
	if err = w.DB.PutRefineryOutput(&o); err != nil {
		return nil, err
	}
	w.CompleteIfReviewed(o.RecipeID)
	return map[string]any{"output": o, "path": target, "provenance_path": pt, "destination": "local_vault"}, nil
}

// Check the nearest existing ancestor before creating a destination so a
// symlinked artifacts/exports directory cannot redirect writes outside state.
func (w Workflow) checkWritePath(path string) error {
	root, err := filepath.EvalSymlinks(filepath.Dir(w.DB.Path()))
	if err != nil {
		return err
	}
	ancestor := path
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("destination has no existing ancestor")
		}
		ancestor = parent
	}
	real, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("destination escapes owned state")
	}
	return nil
}

func (w Workflow) CompleteIfReviewed(id string) {
	r, err := w.DB.Recipe(id)
	if err != nil {
		return
	}
	runs, err := w.DB.RefineryRuns(id, 1)
	if err != nil || len(runs) != 1 || runs[0].Status != "done" {
		return
	}
	outputs, err := w.DB.RefineryOutputs(id, 0)
	if err != nil {
		return
	}
	count := 0
	final := true
	for _, o := range outputs {
		if o.RunID != runs[0].UID {
			continue
		}
		count++
		if o.Status != "reviewed" && o.Status != "rejected" && o.Status != "exported" {
			final = false
		}
	}
	if count == len(r.Outputs) && final {
		r.Status = refinery.RecipeComplete
	} else {
		r.Status = refinery.RecipeReview
	}
	_ = w.DB.PutRecipe(&r)
}

func EvidenceIDsFromJSONL(body string) ([]string, error) {
	var ids []string
	for i, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("JSONL line %d is invalid: %w", i+1, err)
		}
		ids = append(ids, evidenceIDs(v)...)
	}
	return uniqueStrings(ids), nil
}

func evidenceIDs(v any) []string {
	var out []string
	switch v := v.(type) {
	case map[string]any:
		for _, key := range []string{"id", "evidence_id", "chosen_evidence_id", "rejected_evidence_id"} {
			if id, ok := v[key].(string); ok {
				out = append(out, id)
			}
		}
		keys := []string{}
		for key := range v {
			if key != "id" && key != "evidence_id" && key != "chosen_evidence_id" && key != "rejected_evidence_id" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, evidenceIDs(v[key])...)
		}
	case []any:
		for _, item := range v {
			out = append(out, evidenceIDs(item)...)
		}
	}
	return out
}
