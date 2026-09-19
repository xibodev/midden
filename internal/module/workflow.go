package module

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/confirmation"
	"github.com/mekjr1/midden/internal/content"
	"github.com/mekjr1/midden/internal/create"
	"github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
)

type workflowCapability struct {
	ID, Title, Summary string
	ReadOnly, Model    bool
}

var workflowCapabilities = []workflowCapability{
	{"recipes.compose", "Submit authored drafts", "Store explicitly requested local drafts against selected evidence without requiring or claiming human approval. No additional model call. Outputs remain unreviewed; review/export still require positive confirmation.", false, false},
	{"evidence.list", "List stored evidence", "Inspect existing mined evidence before planning or re-extracting. Results are bounded and include evidence IDs.", true, false},
	{"recipes.list", "List recovery plans", "List saved recovery production plans and their review status.", true, false},
	{"recipes.inspect", "Inspect a recovery plan", "Read a recipe, its exact evidence, review findings, citation keys, outputs and production runs.", true, false},
	{"recipes.preview", "Preview a recovery plan", "Design an evidence-grounded recipe without saving or generating content.", true, false},
	{"recipes.design", "Save a recovery plan", "Save a draft recipe from intent, output kinds and evidence. Does not approve evidence or call a model.", false, false},
	{"recipes.update", "Revise a recovery plan", "Revise the purpose or outputs of a saved plan. Invalidates prior evidence approval.", false, false},
	{"recipes.evidence", "Select plan evidence", "Save an exact evidence set. An approved decision requires a trusted host operator-confirmation channel; plain CLI/model assertions leave review pending. Approval does not produce outputs.", false, false},
	{"recipes.produce", "Produce approved plan", "Generate drafts from an approved recipe. Deterministic packs need no model; narrative outputs use the host-authorized model. Drafts still require review.", false, true},
	{"outputs.inspect", "Inspect output and provenance", "Read draft bytes, provenance and content digest before review or revision.", true, false},
	{"outputs.audit", "Audit draft source support", "Check citation scope, passage coverage and exact quotations without changing the draft. Valid references are not semantic proof; inspect the actual source support before requesting operator review.", true, false},
	{"outputs.review", "Revise or review an output", "Revise a draft or request host-confirmed operator review. Supply expected_digest and, for approval, review_notes after outputs.audit. Omitting body reviews existing content. Unsupported confirmation leaves it pending.", false, false},
	{"outputs.export", "Export a reviewed output", "Copy reviewed, digest-verified content and provenance to the local vault. Never publishes remotely.", false, false},
	{"outputs.render", "Render a delivery file", "Render current Markdown to standalone HTML or slide source to editable PPTX with Pandoc. Tracks source and rendered digests; does not approve or publish.", false, false},
}

func runWorkflowModel(ctx context.Context, prompt string, grant ModelGrant) (string, error) {
	r := &exec.Runner{Backend: grant.Backend, BinaryPath: grant.Path, StageDir: grant.StageDir, Timeout: grant.Timeout, Pure: true}
	res, err := r.NewConversation().Prime(ctx, prompt)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

func workflowCapabilityByID(id string) (workflowCapability, bool) {
	for _, c := range workflowCapabilities {
		if c.ID == id {
			return c, true
		}
	}
	return workflowCapability{}, false
}

func addWorkflowCapabilities(d *Descriptor) {
	kinds := []string{}
	for _, kind := range content.Templates() {
		kinds = append(kinds, kind.Name)
	}
	for _, c := range workflowCapabilities {
		requestID := "xibodev.midden." + c.ID + ".request/v1"
		resultID := "xibodev.midden." + c.ID + ".result/v1"
		d.Capabilities = append(d.Capabilities, Capability{ID: c.ID, Title: c.Title, Summary: c.Summary, RequestSchema: requestID, ResultSchema: resultID, Effects: Effects{Local: !c.Model, CostKnown: !c.Model}, Skills: []string{SkillEvidenceSelection, SkillContentSeed}})
		schema := map[string]any{"$id": requestID, "type": "object", "additionalProperties": false, "properties": map[string]any{
			"recipe_id": map[string]any{"type": "string"}, "output_id": map[string]any{"type": "string"},
			"tool":       map[string]any{"type": "string", "enum": []string{"copilot", "claude", "opencode"}},
			"session_id": map[string]any{"type": "string", "description": "Exact session ID, not a prefix."},
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			"offset":     map[string]any{"type": "integer", "minimum": 0},
			"workspace":  map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"},
			"decision": map[string]any{"type": "string"}, "destination": map[string]any{"type": "string", "enum": []string{"local_vault"}},
			"body": map[string]any{"type": "string"}, "expected_digest": map[string]any{"type": "string"},
			"review_notes": map[string]any{"type": "string", "description": "Host agent's source-support review, including unresolved limitations. Not operator approval or proof of truth."},
			"format":       map[string]any{"type": "string", "enum": []string{"pptx", "html"}},
			"drafts":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"output_kinds": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": kinds}},
			"evidence_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}}
		properties := schema["properties"].(map[string]any)
		fields := map[string][]string{
			"recipes.compose": {"recipe_id", "drafts"},
			"evidence.list":   {"workspace", "tool", "session_id", "limit", "offset"}, "recipes.list": {}, "recipes.inspect": {"recipe_id"},
			"recipes.preview":  {"workspace", "prompt", "title", "output_kinds", "evidence_ids"},
			"recipes.design":   {"workspace", "prompt", "title", "output_kinds", "evidence_ids"},
			"recipes.update":   {"recipe_id", "prompt", "title", "output_kinds"},
			"recipes.evidence": {"recipe_id", "evidence_ids", "decision"}, "recipes.produce": {"recipe_id"},
			"outputs.inspect": {"output_id"}, "outputs.review": {"output_id", "body", "decision", "evidence_ids", "expected_digest", "review_notes"},
			"outputs.audit":  {"output_id", "body", "evidence_ids", "expected_digest"},
			"outputs.export": {"output_id", "destination"},
			"outputs.render": {"output_id", "format"},
		}
		selected := map[string]any{}
		for _, field := range fields[c.ID] {
			selected[field] = properties[field]
		}
		schema["properties"] = selected
		if strings.HasPrefix(c.ID, "outputs.") {
			schema["required"] = []string{"output_id"}
		}
		if c.ID == "recipes.inspect" || c.ID == "recipes.update" || c.ID == "recipes.evidence" || c.ID == "recipes.produce" {
			schema["required"] = []string{"recipe_id"}
		}
		switch c.ID {
		case "recipes.compose":
			schema["required"] = []string{"recipe_id", "drafts"}
		case "recipes.evidence":
			schema["required"] = []string{"recipe_id", "evidence_ids", "decision"}
			selected["decision"] = map[string]any{"type": "string", "enum": []string{"saved", "approved"}}
		case "outputs.review":
			schema["required"] = []string{"output_id", "decision", "expected_digest"}
			selected["decision"] = map[string]any{"type": "string", "enum": []string{"draft", "reviewed", "rejected"}}
		case "outputs.render":
			schema["required"] = []string{"output_id", "format"}
		}
		raw, _ := json.Marshal(schema)
		d.RequestSchemas[requestID] = raw
		d.ResultSchemas[resultID] = workflowResultSchema(resultID, c.ID)
	}
}

func invokeWorkflow(req Request, cap workflowCapability) Envelope {
	var c create.Change
	var fields map[string]json.RawMessage
	if len(req.Input) > 0 {
		if err := json.Unmarshal(req.Input, &fields); err != nil {
			return invalidRequest(req, err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(Describe().RequestSchemas["xibodev.midden."+cap.ID+".request/v1"], &schema); err != nil {
			return invalidRequest(req, err)
		}
		for field := range fields {
			if _, ok := schema.Properties[field]; !ok {
				return invalidRequest(req, fmt.Errorf("%s does not accept %s", cap.ID, field))
			}
		}
		for _, field := range schema.Required {
			raw, ok := fields[field]
			if !ok || strings.TrimSpace(string(raw)) == "null" {
				return invalidRequest(req, fmt.Errorf("%s is required", field))
			}
			var property struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(schema.Properties[field], &property); err != nil {
				return invalidRequest(req, err)
			}
			if property.Type == "string" {
				var value string
				if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
					return invalidRequest(req, fmt.Errorf("%s must be a nonempty string", field))
				}
			}
		}
	}
	dec := json.NewDecoder(strings.NewReader(string(req.Input)))
	dec.DisallowUnknownFields()
	if len(req.Input) > 0 {
		if err := dec.Decode(&c); err != nil {
			return invalidRequest(req, err)
		}
	}
	root, ok := req.Roots[RootMiddenHome]
	if !ok || root.Path == "" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrMissingRoot, Message: "workflow requires explicit midden_home state root"}, LocalFree())
	}
	if !filepath.IsAbs(root.Path) {
		return invalidRequest(req, fmt.Errorf("midden_home root must be absolute"))
	}
	if !cap.ReadOnly && root.Mode != "rw" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrPermissionDenied, Message: "workflow change requires writable midden_home"}, LocalFree())
	}
	var db *index.DB
	var err error
	if cap.ReadOnly {
		db, err = index.OpenReadOnly(root.Path)
	} else {
		db, err = index.OpenAt(root.Path)
	}
	if err != nil {
		if cap.Model {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrInvalidRequest, Message: err.Error()}, UnknownCost())
		}
		return invalidRequest(req, err)
	}
	defer db.Close()
	if blocked := requireStoredReview(req, db, cap.ID, c); blocked != nil {
		return *blocked
	}
	var proposal confirmation.Request
	needsConfirmation := (cap.ID == "recipes.evidence" && c.Decision == "approved") || (cap.ID == "outputs.review" && c.Decision == "reviewed")
	if needsConfirmation {
		if cap.ID == "outputs.review" {
			audit, auditErr := (create.Workflow{DB: db}).AuditOutput(c)
			if auditErr != nil {
				return invalidRequest(req, auditErr)
			}
			if audit.Blocked {
				return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: "draft_audit_failed", Message: "Resolve the draft's citation/quotation findings before review", Details: map[string]any{"audit": audit}}, LocalFree())
			}
			if strings.TrimSpace(c.ReviewNotes) == "" {
				return invalidRequest(req, fmt.Errorf("review_notes are required: examine whether each assertion is supported, and state the remaining limits; references alone do not prove truth"))
			}
		}
		if req.ConfirmOperator == nil {
			return pendingOperator(req, "This transport cannot confirm an operator decision. Use a host-confirmed MCP/native interaction, or leave the review pending.")
		}
		if cap.ID == "recipes.evidence" {
			proposal, err = recipeConfirmation(db, c.RecipeID, c.EvidenceIDs)
		} else {
			proposal, err = outputConfirmation(db, c)
		}
		if err != nil {
			return invalidRequest(req, err)
		}
		accepted, confirmErr := confirmedByHost(req, proposal)
		if confirmErr != nil || !accepted {
			return confirmationPending(req, accepted, confirmErr)
		}
		var current confirmation.Request
		if cap.ID == "recipes.evidence" {
			current, err = recipeConfirmation(db, c.RecipeID, c.EvidenceIDs)
		} else {
			current, err = outputConfirmation(db, c)
		}
		if err != nil {
			return invalidRequest(req, err)
		}
		if current.Digest != proposal.Digest {
			return invalidRequest(req, fmt.Errorf("content changed during operator confirmation; inspect again"))
		}
	}
	grant := modelGrantFrom(req)
	w := create.Workflow{DB: db, Generate: grant.NativeDriver, Model: string(grant.Backend)}
	if w.Generate == nil && grant.Backend != "" {
		w.Generate = func(ctx context.Context, prompt string) (string, error) { return runWorkflowModel(ctx, prompt, grant) }
	}
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var result any
	switch cap.ID {
	case "recipes.compose":
		if c.Drafts == nil {
			err = fmt.Errorf("drafts keyed by output kind are required")
		} else {
			w.Drafts = c.Drafts
			result, err = w.Produce(ctx, c.RecipeID)
		}
	case "evidence.list":
		var items []index.Nugget
		if c.Limit == 0 {
			c.Limit = 40
		}
		if c.Limit < 1 || c.Limit > 200 || c.Offset < 0 {
			err = fmt.Errorf("limit must be 1..200 and offset nonnegative")
		} else if c.Tool != "" && c.Tool != "copilot" && c.Tool != "claude" && c.Tool != "opencode" {
			err = fmt.Errorf("unknown source tool")
		} else {
			items, err = db.Nuggets(index.NuggetQuery{Workspace: c.Workspace, Tool: c.Tool, ExactSessionID: c.SessionID, Limit: c.Limit, Offset: c.Offset})
		}
		result = map[string]any{"evidence": items, "limit": c.Limit, "offset": c.Offset, "possibly_truncated": len(items) == c.Limit}
	case "recipes.list":
		var items []index.Recipe
		items, err = db.Recipes(100)
		result = map[string]any{"recipes": items, "limit": 100, "possibly_truncated": len(items) == 100}
	case "recipes.inspect":
		result, err = w.Inspect(c.RecipeID)
	case "recipes.preview":
		result, err = w.Design(c, false)
	case "recipes.design":
		result, err = w.Design(c, true)
	case "recipes.update":
		result, err = w.Update(c)
	case "recipes.evidence":
		if c.Decision != "" && c.Decision != "saved" && c.Decision != "approved" {
			err = fmt.Errorf("decision must be saved or approved")
		} else {
			result, err = w.SelectEvidence(c, c.Decision == "approved")
		}
	case "recipes.produce":
		result, err = w.Produce(ctx, c.RecipeID)
	case "outputs.inspect":
		result, err = w.ReadOutput(c.OutputID)
	case "outputs.audit":
		result, err = w.AuditOutput(c)
	case "outputs.review":
		result, err = w.Review(c)
	case "outputs.export":
		result, err = w.Export(c)
	case "outputs.render":
		result, err = w.Render(ctx, c.OutputID, c.Format)
	}
	if err != nil {
		if cap.Model {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrInvalidRequest, Message: err.Error()}, UnknownCost())
		}
		return invalidRequest(req, err)
	}
	if needsConfirmation {
		if err = db.RecordHostReview(proposal.Action, proposal.SubjectID, proposal.Digest); err != nil {
			return invalidRequest(req, err)
		}
	}
	wire, wireErr := workflowWireResult(result)
	if wireErr != nil {
		return invalidRequest(req, wireErr)
	}
	if cap.Model {
		if payload, ok := result.(map[string]any); ok {
			if recipe, ok := payload["recipe"].(index.Recipe); ok {
				usesModel := false
				for _, spec := range recipe.Outputs {
					usesModel = usesModel || spec.RequiresModel
				}
				if !usesModel {
					return successEnvelope(req, wire, nil)
				}
			}
		}
		env, e := NewResultEnvelope(OpInvoke, req.RequestID, wire, UnknownCost())
		if e != nil {
			return invalidRequest(req, e)
		}
		return env
	}
	return successEnvelope(req, wire, nil)
}
