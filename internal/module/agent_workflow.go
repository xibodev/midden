package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

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
	{"handoffs.create", "Package reviewed project outputs with editorial context and provenance for Markdown, Quarto, Pandoc or D2. Local source files only; never renders, transfers or publishes.", "create_editorial_handoff", true, shape[editorial.HandoffRequest](), shape[create.HandoffResult]()},
	{"evidence.prepare", "Prepare bounded, redacted source excerpts for the host agent. Exact source required; no model or writes.", "prepare_host_evidence", false, shape[EvidencePrepareInput](), shape[EvidencePacket]()},
	{"evidence.compose", "Validate source-record citations and store host-authored extraction. Requires the current packet digest; never calls a model.", "compose_host_evidence", true, shape[EvidenceComposeInput](), shape[EvidenceComposed]()},
	{"projects.create", "Create an exact multi-session editorial corpus from stored evidence. Human review and publication remain separate.", "create_editorial_project", true, shape[editorial.CreateRequest](), shape[editorial.Project]()},
	{"projects.update", "Replace a corpus or goal using a revision fence. Preserves history and marks previous analysis stale.", "update_editorial_project", true, shape[editorial.UpdateRequest](), shape[editorial.Project]()},
	{"projects.list", "List bounded editorial project summaries with total, offset and limit.", "", false, shape[pageInput](), shape[projectPage]()},
	{"projects.inspect", "Inspect a project or historical revision, including opportunities, chapter plans and recipe links.", "", false, shape[projectInput](), shape[editorial.Project]()},
	{"editorial.prepare", "Prepare bounded evidence and investigative guidance for the host agent. Reports scope and estimated input tokens; no nested model call.", "prepare_editorial_analysis", false, shape[projectInput](), shape[editorial.Prepared]()},
	{"editorial.analyze", "Store host-authored editorial analysis after validating claims, source references, reversals and chapter dependencies. Not editorial approval.", "analyze_editorial_evidence", true, shape[analyzeInput](), shape[editorial.Project]()},
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
			props[name] = s
			if !strings.Contains(tag, ",omitempty") {
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
	case *editorial.HandoffRequest:
		result, err = w.Handoff(*c)
	case *EvidenceComposeInput:
		result, err = composeHostEvidence(*c, req, db)
	case *editorial.CreateRequest:
		result, err = w.Create(*c)
	case *editorial.UpdateRequest:
		result, err = w.Update(*c)
	case *editorial.SelectRequest:
		result, err = w.Select(*c)
	case *analyzeInput:
		if c.Analysis == nil {
			err = fmt.Errorf("analysis object is required")
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
