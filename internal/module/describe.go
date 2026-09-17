package module

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

// Version is Midden's module version, independent of the protocol version.
const Version = core.Version

// Capability IDs, namespaced by module.
const (
	CapSessionsList  = "sessions.list"
	CapSessionsAssay = "sessions.assay"
)

// Schema IDs referenced by capabilities.
const (
	SchemaSessionsListRequest = "xibodev.midden.sessions.list.request/v1"

	// SchemaContentTypesRequest is an EMPTY request. content.types reads no
	// input at all, so it previously borrowed sessions.list's schema — which
	// told a consumer to send scope fields the capability silently ignores.
	// A declared surface that does not match behaviour is worse than none: it
	// is the same class of defect as the seed digest disagreement, found by
	// reading the descriptor rather than by anything failing.
	SchemaContentTypesRequest  = "xibodev.midden.content.types.request/v1"
	SchemaSessionsListResult   = "xibodev.midden.sessions.list.result/v1"
	SchemaSessionsAssayRequest = "xibodev.midden.sessions.assay.request/v1"
	SchemaSessionsAssayResult  = "xibodev.midden.sessions.assay.result/v1"
)

// Logical root names. These are NAMES, not paths: the host supplies the
// canonicalized absolute path for each one per invocation, and Midden never
// resolves a root itself.
const (
	RootCopilot    = "copilot_store"
	RootClaude     = "claude_store"
	RootOpencode   = "opencode_store"
	RootMiddenHome = "midden_home"
)

// Describe returns Midden's module descriptor.
//
// It is entirely deterministic and calls no model: discovery must never cost
// anything or depend on a provider being reachable.
func Describe() Descriptor {
	d := Descriptor{
		Module:           ModuleID,
		Name:             "Midden",
		Version:          Version,
		Build:            BuildVariant,
		ProtocolVersions: []string{ProtocolID},
		Capabilities: []Capability{
			{
				ID:            CapSessionsList,
				Title:         "List sessions",
				Summary:       "Inventory past agentic-CLI sessions from read-only source stores, filtered by an exact scope. Automated and trivial sessions are EXCLUDED by default: the result reports excluded_noise and matched alongside total, and total is a filtered count rather than everything on disk. Pass include_noise for the full set.",
				RequestSchema: SchemaSessionsListRequest,
				ResultSchema:  SchemaSessionsListResult,
				Effects: Effects{
					Local:     true,
					CostKnown: true,
				},
				Skills: []string{SkillSessionRecovery},
			},
			{
				ID:            CapSessionsAssay,
				Title:         "Assay sessions",
				Summary:       "Classify a session's records into signal, exhaust, artifact and bookkeeping, and report reclaimable yield. Deterministic; never calls a model.",
				RequestSchema: SchemaSessionsAssayRequest,
				ResultSchema:  SchemaSessionsAssayResult,
				Effects: Effects{
					Local:     true,
					CostKnown: true,
				},
				Skills: []string{SkillSessionRecovery, SkillEvidenceSelection},
				// Synchronous: the assay path holds no job layer and returns a
				// completed result. Bounded by the scope plus the host deadline.
				LongRunning: false,
			},
			{
				ID:              CapSeedCreate,
				Title:           "Create a content seed",
				Summary:         "Build a portable xibodev.midden.seed/v1 bundle from an exact session scope: goal, redacted evidence with provenance, and an evidence digest. Deterministic; never calls a model.",
				RequestSchema:   SchemaSeedCreateRequest,
				ResultSchema:    SchemaSeedCreateResult,
				ArtifactSchemas: []string{SeedSchemaID},
				Skills:          []string{SkillContentSeed, SkillEvidenceSelection},
				Effects: Effects{
					Local:     true,
					CostKnown: true,
					// ExternalWrites is FALSE: the seed is written inside the
					// host-granted midden_home root. The flag means "wrote
					// outside what the host granted", not "wrote a file".
					ExternalWrites: false,
				},
				LongRunning: false,
			},
			{
				ID:            CapEvidenceExtract,
				Title:         "Extract evidence from sessions",
				Summary:       "Mine sessions into stored, redacted evidence: decisions, gotchas, error fixes, commands. This is the step between measuring a session and writing anything from it — content.produce draws on what this stores. Always calls a model through an AI CLI the user is already signed in to; there is no deterministic path to evidence.",
				RequestSchema: SchemaEvidenceExtractRequest,
				ResultSchema:  SchemaEvidenceExtractResult,
				// CostKnown is FALSE because the AMOUNT is unknowable: the
				// spend happens inside someone's own subscription and Midden
				// never sees a bill, so it cannot price the call.
				//
				// Not because "this may spend money" -- that is a separate
				// fact (operator ruling 4) with no field on the v1 wire. The
				// two coincide here and diverge on content.produce, where
				// seven of nineteen kinds spend nothing while the amount stays
				// unknowable for the rest.
				Effects: Effects{Local: false, CostKnown: false},
				Skills:  []string{SkillEvidenceSelection, SkillSessionRecovery},
			},
			{
				ID:            CapContentTypes,
				Title:         "List producible content types",
				Summary:       "List the document types Midden can produce from mined evidence, which need a model and which are free, and how much stored evidence exists. Call this before content.produce.",
				RequestSchema: SchemaContentTypesRequest,
				ResultSchema:  SchemaContentTypesResult,
				Effects:       Effects{Local: true, CostKnown: true},
				Skills:        []string{SkillEvidenceSelection},
			},
			{
				ID:              CapContentProduce,
				Title:           "Produce content from mined evidence",
				Summary:         "Write a document from mined evidence: tutorials, ADRs, slide decks, diagrams, video briefs, handbooks, and deterministic packs and manifests. Seven kinds are free and model-free; twelve are written by a model through an AI CLI the user is already signed in to, and those need subprocess authority granted for the invocation. Call content.types first for the split.",
				RequestSchema:   SchemaContentProduceRequest,
				ResultSchema:    SchemaContentProduceResult,
				ArtifactSchemas: []string{ArtifactContentOutput},
				// Local is FALSE because this code path can spawn an AI CLI.
				//
				// It declared Local:true while being able to drive a model
				// through subprocess, which is the declaration this type's own
				// doc forbids: Effects must describe what the CODE PATH does,
				// not what the capability name suggests. Runtime reporting was
				// already honest (contentExecution sets Local: !r.ModelUsed),
				// so a host saw the truth AFTER the fact and a wrong answer
				// BEFORE it -- exactly when a pre-flight decision is made.
				//
				// Seven of nineteen kinds are deterministic and spend nothing.
				// A capability-level declaration cannot express "depends on the
				// argument", so it declares the WIDER effect and the narrower
				// truth is reported per kind by content.types. Over-declaring
				// costs an unnecessary approval; under-declaring spends a
				// user's subscription without one.
				//
				// CostKnown is FALSE because the amount is unknowable before
				// the kind is chosen, NOT as a proxy for "may spend money":
				// those are separate facts (operator ruling 4).
				Effects: Effects{Local: false, CostKnown: false},
				Skills:  []string{SkillEvidenceSelection, SkillContentSeed},
			},
		},
		RequestSchemas: map[string]json.RawMessage{
			SchemaSessionsListRequest:    json.RawMessage(scopeRequestSchema),
			SchemaContentTypesRequest:    json.RawMessage(noInputRequestSchema),
			SchemaSessionsAssayRequest:   json.RawMessage(assayRequestSchema),
			SchemaSeedCreateRequest:      json.RawMessage(seedCreateRequestSchema),
			SchemaContentProduceRequest:  json.RawMessage(contentProduceRequestSchema),
			SchemaEvidenceExtractRequest: json.RawMessage(evidenceExtractRequestSchema),
		},
		ResultSchemas: map[string]json.RawMessage{
			SchemaSessionsListResult:    json.RawMessage(sessionsListResultSchema),
			SchemaSessionsAssayResult:   json.RawMessage(assayResultSchema),
			SchemaSeedCreateResult:      json.RawMessage(seedCreateResultSchema),
			SchemaContentTypesResult:    json.RawMessage(contentTypesResultSchema),
			SchemaContentProduceResult:  json.RawMessage(contentProduceResultSchema),
			SchemaEvidenceExtractResult: json.RawMessage(evidenceExtractResultSchema),
		},
		ArtifactSchemas: map[string]json.RawMessage{
			SeedSchemaID:          json.RawMessage(seedManifestSchema),
			ArtifactContentOutput: json.RawMessage(contentOutputSchema),
		},
		Permissions: Permissions{
			// Read-only source stores. Midden opens these read-only; the host
			// additionally supplies them as "ro" roots so confinement is
			// enforced rather than trusted.
			FilesystemRead: []string{RootCopilot, RootClaude, RootOpencode},
			// Midden's own state is the only thing it writes.
			FilesystemWrite: []string{RootMiddenHome},
			// Model-backed content shells out to an AI CLI the user is
			// already signed in to. Midden holds no API key and never calls a
			// provider directly, so this is SUBPROCESS authority rather than
			// network or credential authority — modelling it as network would
			// be both wrong and insufficient.
			//
			// Declaring a binary is a request, not a grant: the host decides
			// which of these it authorizes per invocation, and supplies the
			// absolute path. Naming all three lets the host grant whichever
			// the user actually has.
			Subprocess: []string{"copilot", "claude", "opencode"},
		},
		AgentOverlays: overlays(),
		Skills:        skills(),
		Requirements:  []Requirement{},
	}
	addWorkflowCapabilities(&d)
	d.Normalize()
	return d
}

// ---------------------------------------------------------------------------
// sessions.assay
// ---------------------------------------------------------------------------

// AssayRequest is the input to sessions.assay.
//
// Scope is mandatory in spirit: an unbounded assay over every store is exactly
// the runaway this contract exists to prevent, so MaxSessions is clamped.
type AssayRequest struct {
	Tool         string   `json:"tool,omitempty"`
	Days         int      `json:"days,omitempty"`
	Workspace    string   `json:"workspace,omitempty"`
	Repo         string   `json:"repo,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	IDPrefix     string   `json:"id_prefix,omitempty"`
	IncludeNoise bool     `json:"include_noise,omitempty"`

	// MaxSessions bounds how many sessions are assayed. Clamped to
	// MaxSessionsCeiling; 0 means DefaultMaxSessions.
	MaxSessions int `json:"max_sessions,omitempty"`

	// MaxCandidates bounds the candidate record list per session.
	MaxCandidates int `json:"max_candidates,omitempty"`
}

// Bounds applied to every assay request. A host deadline is a backstop, not a
// substitute for the module bounding its own work.
const (
	DefaultMaxSessions   = 25
	MaxSessionsCeiling   = 200
	DefaultMaxCandidates = 40
	MaxCandidatesCeiling = 500
)

// AssaySessionResult is the per-session outcome.
//
// It reports the manifest's derived measures rather than the raw candidate
// records: previews are drawn from private transcripts, so they are not
// returned merely to simplify host integration.
type AssaySessionResult struct {
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Title     string `json:"title,omitempty"`

	TotalRecords int64 `json:"total_records"`
	TotalBytes   int64 `json:"total_bytes"`

	Counts map[string]int64 `json:"counts"`
	Bytes  map[string]int64 `json:"bytes"`
	ByKind map[string]int64 `json:"by_kind"`

	SignalBytes      int64   `json:"signal_bytes"`
	ReclaimableBytes int64   `json:"reclaimable_bytes"`
	SignalShare      float64 `json:"signal_share"`
	Compression      float64 `json:"compression"`

	CandidateCount int64 `json:"candidate_count"`
	SliceBytes     int64 `json:"slice_bytes"`
	EstSliceTokens int64 `json:"est_slice_tokens"`

	DuplicateReads int64 `json:"duplicate_reads"`
	DuplicateBytes int64 `json:"duplicate_bytes"`
	ImageCount     int64 `json:"image_count"`
	ImageClusters  int64 `json:"image_clusters"`
}

// AssayResult is the sessions.assay payload.
type AssayResult struct {
	Sessions  []AssaySessionResult `json:"sessions"`
	Assayed   int                  `json:"assayed"`
	Failed    int                  `json:"failed"`
	Truncated bool                 `json:"truncated"`

	TotalBytes       int64 `json:"total_bytes"`
	SignalBytes      int64 `json:"signal_bytes"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
}

// Normalize satisfies Normalizer so nested collections never marshal as null.
func (r *AssayResult) Normalize() {
	r.Sessions = emptySlice(r.Sessions)
	for i := range r.Sessions {
		r.Sessions[i].Counts = emptyMap(r.Sessions[i].Counts)
		r.Sessions[i].Bytes = emptyMap(r.Sessions[i].Bytes)
		r.Sessions[i].ByKind = emptyMap(r.Sessions[i].ByKind)
	}
}

// SessionsAssay runs the deterministic assay over a bounded scope.
//
// It calls adapter.Assayer directly and never constructs Midden's web job
// manager: doing so would fire stale-job reconciliation and force-fail live
// jobs belonging to a running Midden UI. A capability that looks read-only must
// be read-only in the code path, not merely in name.
//
// No model is invoked. internal/assay imports only stdlib and performs pure
// classification; the adapters do file and read-only SQLite reads.
func SessionsAssay(req AssayRequest, roots adapter.Roots) (*AssayResult, []string, error) {
	sc, err := scopeFromAssayRequest(req)
	if err != nil {
		return nil, nil, err
	}

	maxSessions := clamp(req.MaxSessions, DefaultMaxSessions, MaxSessionsCeiling)
	maxCandidates := clamp(req.MaxCandidates, DefaultMaxCandidates, MaxCandidatesCeiling)

	sessions, errs := adapter.CollectWithRoots(sc, roots)

	var warnings []string
	for _, e := range errs {
		// One tool's format drift must not blind the others, and it must not
		// silently vanish either.
		warnings = append(warnings, "source store: "+e.Error())
	}

	result := &AssayResult{}
	if len(sessions) > maxSessions {
		sessions = sessions[:maxSessions]
		result.Truncated = true
		warnings = append(warnings, fmt.Sprintf(
			"scope matched more sessions than the bound; assayed the first %d", maxSessions))
	}

	for _, s := range sessions {
		a := adapter.FindWithRoots(s.Tool, roots)
		if a == nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("no adapter for tool %q", s.Tool))
			continue
		}
		as, ok := a.(adapter.Assayer)
		if !ok {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("tool %q cannot be assayed", s.Tool))
			continue
		}

		m, err := as.Assay(s, maxCandidates)
		if err != nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("assay %s: %v", s.ID, err))
			continue
		}

		result.Sessions = append(result.Sessions, sessionResultFrom(m))
		result.Assayed++
		result.TotalBytes += m.TotalBytes
		result.SignalBytes += m.SignalBytes()
		result.ReclaimableBytes += m.ReclaimableBytes()
	}

	// Stable ordering: map iteration and adapter concurrency must not make an
	// otherwise deterministic capability return different bytes per run.
	sort.SliceStable(result.Sessions, func(i, j int) bool {
		if result.Sessions[i].Tool != result.Sessions[j].Tool {
			return result.Sessions[i].Tool < result.Sessions[j].Tool
		}
		return result.Sessions[i].SessionID < result.Sessions[j].SessionID
	})

	return result, warnings, nil
}

// sessionResultFrom projects a manifest into the wire result.
//
// Manifest.Elapsed is deliberately dropped: it is wall-clock and would make an
// otherwise deterministic result differ on every run.
func sessionResultFrom(m *assay.Manifest) AssaySessionResult {
	return AssaySessionResult{
		SessionID:        m.SessionID,
		Tool:             m.Tool,
		Title:            m.Title,
		TotalRecords:     m.TotalRecords,
		TotalBytes:       m.TotalBytes,
		Counts:           m.Counts,
		Bytes:            m.Bytes,
		ByKind:           m.ByKind,
		SignalBytes:      m.SignalBytes(),
		ReclaimableBytes: m.ReclaimableBytes(),
		SignalShare:      m.SignalShare(),
		Compression:      m.Compression(),
		CandidateCount:   int64(len(m.Candidates)),
		SliceBytes:       m.SliceBytes(),
		EstSliceTokens:   m.EstSliceTokens(),
		DuplicateReads:   m.DuplicateReads,
		DuplicateBytes:   m.DuplicateBytes,
		ImageCount:       m.ImageCount,
		ImageClusters:    m.ImageClusters,
	}
}

func scopeFromAssayRequest(req AssayRequest) (core.Scope, error) {
	ids := make([]string, 0, len(req.IDs))
	for _, id := range req.IDs {
		if tool, bare, ok := strings.Cut(id, ":"); ok {
			if req.Tool != "" && req.Tool != tool {
				return core.Scope{}, fmt.Errorf("mixed tool identities require separate scoped requests")
			}
			req.Tool = tool
			id = bare
		}
		if strings.TrimSpace(id) == "" {
			return core.Scope{}, fmt.Errorf("session id cannot be empty")
		}
		ids = append(ids, id)
	}
	sc := core.Scope{
		Days:         req.Days,
		Workspace:    req.Workspace,
		Repo:         req.Repo,
		IDs:          ids,
		IDPrefix:     req.IDPrefix,
		IncludeNoise: req.IncludeNoise,
	}
	if t := strings.TrimSpace(strings.ToLower(req.Tool)); t != "" {
		tool := core.Tool(t)
		switch tool {
		case core.ToolCopilot, core.ToolClaude, core.ToolOpencode:
			sc.Tools = []core.Tool{tool}
		default:
			return sc, fmt.Errorf("unknown tool %q (want copilot, claude or opencode)", req.Tool)
		}
	}
	return sc, nil
}

func clamp(v, def, ceiling int) int {
	if v <= 0 {
		v = def
	}
	if v > ceiling {
		return ceiling
	}
	return v
}
