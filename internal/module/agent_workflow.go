package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/confirmation"
	"github.com/mekjr1/midden/internal/create"
	"github.com/mekjr1/midden/internal/editorial"
	"github.com/mekjr1/midden/internal/index"
)

type projectInput struct {
	ProjectID string `json:"project_id"`
	Revision  int    `json:"revision,omitempty"`
}
type analyzeInput struct {
	ProjectID        string              `json:"project_id"`
	ExpectedRevision int                 `json:"expected_revision"`
	Analysis         *editorial.Analysis `json:"analysis"`
}
type pageInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}
type projectSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Goal          string `json:"goal"`
	Revision      int    `json:"revision"`
	SourceCount   int    `json:"source_count"`
	EvidenceCount int    `json:"evidence_count"`
	AnalysisStale bool   `json:"analysis_stale"`
}
type projectPage struct {
	Projects []projectSummary `json:"projects"`
	Total    int              `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
}
type agentCapability struct {
	ID, Summary, Operation string
	Write                  bool
	Input, Output          reflect.Type
}

func shape[T any]() reflect.Type { return reflect.TypeFor[T]() }

var agentCapabilities = []agentCapability{
	{"host.status", "Inspect the host confirmation channel. Optional probe_confirmation tests one operator dialog without changing evidence, approvals, drafts or exports.", "", false, shape[HostStatusInput](), shape[HostStatus]()},
	{"results.inspect", "Read bounded pages of a detailed result using its _view.result_id and returned JSON-pointer paths. No shell parsing is needed. The result cache retains the latest 128 distinct views; durable projects and packets are separate.", "", false, shape[ResultInspectInput](), shape[ResultPage]()},
	{"handoffs.create", "Package reviewed project outputs with editorial context and provenance for Markdown, Quarto, Pandoc or D2. Local source files only; never renders, transfers or publishes.", "create_editorial_handoff", true, shape[editorial.HandoffRequest](), shape[create.HandoffResult]()},
	{"evidence.prepare", "Orient across an exact session, including its beginning and end. Persist a bounded redacted packet with chronology and cumulative read budget. No model; source stores remain read-only.", "prepare_host_evidence", true, shape[EvidencePrepareInput](), shape[EvidencePacket]()},
	{"evidence.read", "Read fuller context around 1..4 records from a stored packet, within the same source snapshot and cumulative budget. Metadata is not treated as conversation.", "prepare_host_evidence", true, shape[EvidenceReadInput](), shape[EvidencePacket]()},
	{"evidence.search", "Search an exact packet's source snapshot for a literal phrase. Return representative early and late matches within a cumulative read budget; no full transcript dump.", "prepare_host_evidence", true, shape[EvidenceSearchInput](), shape[EvidencePacket]()},
	{"evidence.validate", "Validate a proposed extraction against a stored packet without storing evidence. Use for diagnostics instead of inserting test items.", "", false, shape[EvidenceComposeInput](), shape[EvidenceComposed]()},
	{"evidence.extend_budget", "Request an operator-confirmed increase of this investigation's cumulative source-text budget, at most 1 MiB. Unsupported or cancelled host confirmation leaves it unchanged.", "prepare_host_evidence", true, shape[EvidenceBudgetInput](), shape[index.ReadingBudget]()},
	{"evidence.compose", "Validate source-record citations and exact quotations, then store host-authored extraction from packet_id. No reconstruction of read parameters or source rescan; no model call.", "compose_host_evidence", true, shape[EvidenceComposeInput](), shape[EvidenceComposed]()},
	{"projects.create", "Create an exact multi-session editorial corpus from stored evidence. Human review and publication remain separate.", "create_editorial_project", true, shape[editorial.CreateRequest](), shape[editorial.Project]()},
	{"projects.update", "Replace a corpus or goal using a revision fence. Preserves history and marks previous analysis stale.", "update_editorial_project", true, shape[editorial.UpdateRequest](), shape[editorial.Project]()},
	{"projects.list", "List bounded editorial project summaries with total, offset and limit.", "", false, shape[pageInput](), shape[projectPage]()},
	{"projects.inspect", "Inspect a project or historical revision, including opportunities, chapter plans and recipe links.", "", false, shape[projectInput](), shape[editorial.Project]()},
	{"editorial.prepare", "Prepare bounded evidence and investigative guidance for the host agent. Reports scope and estimated input tokens; no nested model call.", "prepare_editorial_analysis", false, shape[projectInput](), shape[editorial.Prepared]()},
	{"editorial.analyze", "Store host-authored editorial analysis after validating claims, source references, reversals and chapter dependencies. Not editorial approval.", "analyze_editorial_evidence", true, shape[analyzeInput](), shape[editorial.Project]()},
	{"editorial.validate", "Check the complete proposed analysis without saving. Returns all field-path issues and preserves the meaning of refuting evidence.", "", false, shape[analyzeInput](), shape[editorial.ValidationReport]()},
	{"editorial.select", "Select one opportunity into a draft recipe. Does not approve its evidence or produce content. Unresolved gaps need explicit disclosure.", "select_editorial_opportunity", true, shape[editorial.SelectRequest](), shape[editorial.SelectionResult]()},
}

func agentCapabilityByID(id string) (agentCapability, bool) {
	for _, c := range agentCapabilities {
		if c.ID == id {
			return c, true
		}
	}
	return agentCapability{}, false
}

// AgentCapabilityIDs is shared by the CLI and opt-in MCP workflow adapter.
func AgentCapabilityIDs() []string {
	out := make([]string, 0, len(agentCapabilities))
	for _, c := range agentCapabilities {
		out = append(out, c.ID)
	}
	return out
}

func addAgentCapabilities(d *Descriptor) {
	for _, c := range agentCapabilities {
		requestID := "xibodev.midden." + c.ID + ".request/v1"
		resultID := "xibodev.midden." + c.ID + ".result/v1"
		capability := Capability{ID: c.ID, Title: c.ID, Summary: c.Summary, RequestSchema: requestID,
			ResultSchema: resultID, Effects: Effects{Local: true, CostKnown: true}, Skills: []string{SkillEditorial, SkillEvidenceSelection}}
		if c.ID == "handoffs.create" {
			capability.ArtifactSchemas = []string{create.HandoffSchema}
			d.ArtifactSchemas[create.HandoffSchema] = typedSchema(create.HandoffSchema, shape[create.HandoffManifest]())
		}
		d.Capabilities = append(d.Capabilities, capability)
		d.RequestSchemas[requestID] = typedSchema(requestID, c.Input)
		d.ResultSchemas[resultID] = typedSchema(resultID, c.Output)
	}
}

func typedSchema(id string, t reflect.Type) json.RawMessage {
	s := typeSchema(t)
	s["$id"] = id
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	raw, err := json.Marshal(s)
	if err != nil {
		panic("invalid static agent schema: " + err.Error())
	}
	return raw
}

func typeSchema(t reflect.Type) map[string]any {
	if t == reflect.TypeFor[time.Time]() {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return map[string]any{"anyOf": []any{typeSchema(t.Elem()), map[string]any{"type": "null"}}}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": typeSchema(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8, reflect.Uint, reflect.Uint64, reflect.Uint32:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": typeSchema(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "-" || f.PkgPath != "" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			s := typeSchema(f.Type)
			if values := f.Tag.Get("enum"); values != "" {
				s["enum"] = strings.Split(values, ",")
			}
			for _, rule := range []struct{ tag, key string }{{"min", "minimum"}, {"max", "maximum"}, {"default", "default"}} {
				if value := f.Tag.Get(rule.tag); value != "" {
					n, err := strconv.Atoi(value)
					if err != nil {
						panic("invalid static schema constraint")
					}
					key := rule.key
					if f.Type.Kind() == reflect.String && rule.tag != "default" {
						if rule.tag == "min" {
							key = "minLength"
						} else {
							key = "maxLength"
						}
					}
					if f.Type.Kind() == reflect.Slice && rule.tag != "default" {
						if rule.tag == "min" {
							key = "minItems"
						} else {
							key = "maxItems"
						}
					}
					s[key] = n
				}
			}
			props[name] = s
			if !strings.Contains(tag, ",omitempty") && !strings.Contains(tag, ",omitzero") {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
	default:
		return map[string]any{}
	}
}

func decodeAgentInput(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > editorial.MaxDocumentBytes {
		return fmt.Errorf("request exceeds %d byte bound", editorial.MaxDocumentBytes)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("input must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("input must contain exactly one JSON object")
	}
	return nil
}

func invokeAgent(req Request, cap agentCapability) Envelope {
	if cap.ID == "host.status" {
		var input HostStatusInput
		if err := decodeAgentInput(req.Input, &input); err != nil {
			return invalidRequest(req, err)
		}
		return successEnvelope(req, hostStatus(req, input), nil)
	}
	root, ok := req.Roots[RootMiddenHome]
	if cap.ID == "evidence.prepare" {
		var input EvidencePrepareInput
		if err := decodeAgentInput(req.Input, &input); err != nil {
			return invalidRequest(req, err)
		}
		packet, _, err := prepareHostEvidence(input, req)
		if err != nil {
			return invalidRequest(req, err)
		}
		return successEnvelope(req, packet, packet.Warnings)
	}
	if !ok || root.Path == "" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrMissingRoot, Message: "explicit midden_home state root is required"}, LocalFree())
	}
	if !filepath.IsAbs(root.Path) {
		return invalidRequest(req, fmt.Errorf("midden_home must be absolute"))
	}
	if cap.Write && root.Mode != "rw" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{Code: ErrPermissionDenied, Message: "operation requires writable midden_home"}, LocalFree())
	}
	// Validate the closed input shape before opening or creating state.
	input := reflect.New(cap.Input).Interface()
	if err := decodeAgentInput(req.Input, input); err != nil {
		return invalidRequest(req, err)
	}
	var db *index.DB
	var err error
	if cap.Write {
		db, err = index.OpenAt(root.Path)
	} else {
		db, err = index.OpenReadOnly(root.Path)
	}
	if err != nil {
		return invalidRequest(req, fmt.Errorf("open Midden state (initialize with projects.create or evidence.compose): %w", err))
	}
	defer db.Close()
	w := editorial.Workflow{DB: db}
	var result any
	switch c := input.(type) {
	case *ResultInspectInput:
		result, err = inspectResult(req, db, *c)
	case *EvidenceBudgetInput:
		if req.ConfirmOperator == nil {
			return pendingOperator(req, "Increasing the cumulative read budget requires a trusted operator confirmation")
		}
		stored, e := loadReadingPacket(db, c.PacketID, "")
		if e != nil {
			return invalidRequest(req, e)
		}
		budget := stored.Packet.Budget
		if c.LimitBytes <= budget.LimitBytes || c.LimitBytes > 1048576 {
			return invalidRequest(req, fmt.Errorf("limit_bytes must increase the current %d byte limit and remain at most 1048576", budget.LimitBytes))
		}
		proposal := confirmation.Request{Action: "extend_reading", SubjectID: stored.Packet.InvestigationID,
			Digest:  DigestSHA256([]byte(fmt.Sprintf("%s:%d:%d", stored.Packet.InvestigationID, budget.LimitBytes, c.LimitBytes))),
			Message: fmt.Sprintf("Allow this exact session investigation to expose up to %d source-text bytes instead of %d? Host reasoning uses the host's model budget; no new source scope is authorized.", c.LimitBytes, budget.LimitBytes)}
		accepted, e := confirmedByHost(req, proposal)
		if e != nil || !accepted {
			return pendingOperator(req, "Read budget increase was not confirmed")
		}
		err = db.ExtendReadingBudget(stored.Packet.InvestigationID, budget.LimitBytes, c.LimitBytes)
		if err == nil {
			result, err = db.ReadingBudget(stored.Packet.InvestigationID)
		}
	case *editorial.HandoffRequest:
		for _, id := range c.OutputIDs {
			if blocked := requireStoredReview(req, db, "outputs.export", create.Change{OutputID: id}); blocked != nil {
				return *blocked
			}
		}
		result, err = w.Handoff(*c)
	case *EvidenceComposeInput:
		result, err = composeHostEvidence(*c, req, db)
	case *EvidenceReadInput:
		result, err = readHostEvidence(*c, req, db)
	case *EvidenceSearchInput:
		result, err = searchHostEvidence(*c, req, db)
	case *editorial.CreateRequest:
		result, err = w.Create(*c)
	case *editorial.UpdateRequest:
		result, err = w.Update(*c)
	case *editorial.SelectRequest:
		result, err = w.Select(*c)
	case *analyzeInput:
		if c.Analysis == nil {
			err = fmt.Errorf("analysis object is required")
		} else if cap.ID == "editorial.validate" {
			result, err = w.Validate(c.ProjectID, c.ExpectedRevision, *c.Analysis)
		} else {
			result, err = w.Analyze(c.ProjectID, c.ExpectedRevision, *c.Analysis)
		}
	case *projectInput:
		if cap.ID == "editorial.prepare" {
			if c.Revision != 0 {
				err = fmt.Errorf("editorial.prepare only accepts the current project revision")
			} else {
				result, err = w.Prepare(c.ProjectID)
			}
		} else {
			result, err = w.InspectRevision(c.ProjectID, c.Revision)
		}
	case *pageInput:
		if c.Limit == 0 {
			c.Limit = 20
		}
		var rows []json.RawMessage
		var total int
		rows, total, err = db.EditorialList(c.Limit, c.Offset)
		page := projectPage{Projects: []projectSummary{}, Total: total, Limit: c.Limit, Offset: c.Offset}
		if err == nil {
			for _, row := range rows {
				var p editorial.Project
				if err = json.Unmarshal(row, &p); err != nil {
					break
				}
				page.Projects = append(page.Projects, projectSummary{p.ID, p.Title, p.Goal, p.Revision, len(p.Sources), len(p.EvidenceIDs), p.AnalysisStale})
			}
		}
		result = page
	default:
		err = fmt.Errorf("unimplemented agent operation %s", cap.ID)
	}
	if err != nil {
		return invalidRequest(req, err)
	}
	env := successEnvelope(req, result, nil)
	if handoff, ok := result.(create.HandoffResult); ok {
		info, statErr := os.Stat(filepath.Join(root.Path, filepath.FromSlash(handoff.ManifestPath)))
		if statErr != nil {
			return invalidRequest(req, statErr)
		}
		env.Execution.Artifacts = append(env.Execution.Artifacts, Artifact{
			ID: "editorial-handoff", Kind: create.HandoffSchema, Path: handoff.ManifestPath, Root: RootMiddenHome,
			MediaType: "application/json", Bytes: info.Size(), Digest: handoff.ManifestDigest,
		})
	}
	return env
}
