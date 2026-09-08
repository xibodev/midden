package module

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/exec"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/reclaim"
)

// Evidence extraction.
//
// This is the step between measuring a session and writing something from it.
// sessions.assay reports what a transcript is made of; content.produce needs
// stored evidence to draw on; nothing connected the two, so a host with a fresh
// midden_home could mine and measure and then produce nothing at all.
const (
	CapEvidenceExtract = "evidence.extract"

	SchemaEvidenceExtractRequest = "xibodev.midden.evidence.extract.request/v1"
	SchemaEvidenceExtractResult  = "xibodev.midden.evidence.extract.result/v1"
)

// EvidenceExtractRequest selects sessions to mine.
type EvidenceExtractRequest struct {
	AssayRequest

	// MaxRecords bounds the candidate slice sent to the model PER SESSION.
	// This is the number that governs real cost, not the session count.
	MaxRecords int `json:"max_records,omitempty"`
}

// Bounds on extraction. The slice is what reaches a model, so it is bounded
// tightly: an unbounded slice over a large transcript is the runaway this
// contract exists to prevent.
const (
	DefaultExtractSessions = 3
	MaxExtractSessions     = 25
	DefaultExtractRecords  = 40
	MaxExtractRecords      = 200
)

// ExtractedSession reports one session's yield.
type ExtractedSession struct {
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Nuggets   int    `json:"nuggets"`
	Redacted  int    `json:"redacted"`

	// SliceBytes and EstSliceTokens describe what was actually sent to the
	// model, which is what a cost question is really about.
	SliceBytes     int64 `json:"slice_bytes"`
	EstSliceTokens int64 `json:"est_slice_tokens"`
}

// EvidenceExtractResult is the evidence.extract payload.
type EvidenceExtractResult struct {
	Sessions []ExtractedSession `json:"sessions"`

	Extracted int `json:"extracted"`
	Failed    int `json:"failed"`
	Stored    int `json:"stored"`

	// ByKind counts what was found: decision, gotcha, error_fix, command,
	// artifact, dead_end. A reader deciding whether extraction was worthwhile
	// needs the composition, not just a total.
	ByKind map[string]int `json:"by_kind"`

	// ModelUsed is always true for a successful extraction: there is no
	// deterministic path to evidence. Stated as a fact rather than implied.
	ModelUsed    bool   `json:"model_used"`
	ModelBackend string `json:"model_backend,omitempty"`
}

// Normalize satisfies Normalizer.
func (r *EvidenceExtractResult) Normalize() {
	r.Sessions = emptySlice(r.Sessions)
	r.ByKind = emptyIntMap(r.ByKind)
}

func emptyIntMap(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

// EvidenceExtract mines sessions into stored, redacted evidence.
//
// It always calls a model: turning a transcript into evidence is a judgement,
// and there is no deterministic path to it. The slice sent is bounded and
// redacted before it leaves the machine, and what comes back is redacted again
// on the way into storage.
func EvidenceExtract(db *index.DB, req EvidenceExtractRequest, roots adapter.Roots, grant ModelGrant) (*EvidenceExtractResult, []string, error) {
	if grant.Backend == "" {
		return nil, nil, fmt.Errorf("%s", ErrSubprocessDenied)
	}
	if db == nil {
		return nil, nil, fmt.Errorf("the evidence index is unavailable")
	}

	sc, err := scopeFromAssayRequest(req.AssayRequest)
	if err != nil {
		return nil, nil, err
	}
	maxSessions := clamp(req.MaxSessions, DefaultExtractSessions, MaxExtractSessions)
	maxRecords := clamp(req.MaxRecords, DefaultExtractRecords, MaxExtractRecords)

	sessions, errs := adapter.CollectWithRoots(sc, roots)
	var warnings []string
	for _, e := range errs {
		warnings = append(warnings, "source store: "+e.Error())
	}
	if len(sessions) == 0 {
		return nil, warnings, fmt.Errorf("no session matches that scope")
	}
	if len(sessions) > maxSessions {
		warnings = append(warnings, fmt.Sprintf(
			"scope matched %d sessions; extracted the first %d. Each session costs a model call.",
			len(sessions), maxSessions))
		sessions = sessions[:maxSessions]
	}

	result := &EvidenceExtractResult{
		ByKind:       map[string]int{},
		ModelUsed:    true,
		ModelBackend: string(grant.Backend),
	}

	// Open the ledger entry BEFORE any model runs: a call that fails or is
	// killed must still leave evidence that it happened and cost something.
	run := modelRun("evidence.extract",
		scopeLabel(req.Tool, req.Workspace, fmt.Sprintf("%d sessions", len(sessions))),
		string(grant.Backend), 0)

	for _, s := range sessions {
		a := adapter.FindWithRoots(s.Tool, roots)
		as, ok := a.(adapter.Assayer)
		if !ok {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("tool %q cannot be assayed", s.Tool))
			continue
		}
		manifest, err := as.Assay(s, maxRecords)
		if err != nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("assay %s: %v", shortID(s.ID), err))
			continue
		}

		slice := reclaim.BuildSlice(s, manifest, maxRecords)
		if len(slice.Candidates) == 0 {
			warnings = append(warnings, fmt.Sprintf(
				"%s has no signal records worth extracting", shortID(s.ID)))
			continue
		}

		out, err := runExtraction(slice, grant)
		if err != nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("extract %s: %v", shortID(s.ID), err))
			continue
		}

		nuggets, err := reclaim.Parse(out, s, string(grant.Backend))
		if err != nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("parse %s: %v", shortID(s.ID), err))
			continue
		}
		if len(nuggets) == 0 {
			warnings = append(warnings, fmt.Sprintf(
				"%s yielded no evidence; the model found nothing reusable", shortID(s.ID)))
			continue
		}

		if err := db.PutNuggets(nuggets); err != nil {
			result.Failed++
			warnings = append(warnings, fmt.Sprintf("store %s: %v", shortID(s.ID), err))
			continue
		}

		redacted := 0
		for _, n := range nuggets {
			result.ByKind[n.Kind]++
			if n.Redacted {
				redacted++
			}
		}

		result.Sessions = append(result.Sessions, ExtractedSession{
			SessionID:      s.ID,
			Tool:           string(s.Tool),
			Nuggets:        len(nuggets),
			Redacted:       redacted,
			SliceBytes:     manifest.SliceBytes(),
			EstSliceTokens: manifest.EstSliceTokens(),
		})
		result.Extracted++
		result.Stored += len(nuggets)
	}

	sort.SliceStable(result.Sessions, func(i, j int) bool {
		return result.Sessions[i].SessionID < result.Sessions[j].SessionID
	})

	if result.Stored == 0 && result.Failed == 0 {
		warnings = append(warnings,
			"nothing was stored: the sessions in scope carried no reusable evidence")
	}

	// Every session that reached the model cost something, whether or not it
	// yielded evidence. Estimated tokens come from the slices actually sent.
	var est int64
	for _, s := range result.Sessions {
		est += s.EstSliceTokens
	}
	run.EstTokens = int(est)
	warnings = append(warnings, recordRun(db, run, result.Stored, result.Extracted > 0,
		fmt.Sprintf("%d sessions mined, %d evidence items stored", result.Extracted, result.Stored))...)

	return result, warnings, nil
}

// runExtraction sends one bounded slice to the granted CLI.
func runExtraction(slice reclaim.Slice, grant ModelGrant) (string, error) {
	runner := &exec.Runner{
		Backend:    grant.Backend,
		BinaryPath: grant.Path,
		StageDir:   grant.StageDir,
		Timeout:    grant.Timeout,
		Pure:       true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), grant.Timeout)
	defer cancel()
	res, err := runner.Run(ctx, slice.Prompt())
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(exec.CleanOutput(res.Output))
	if out == "" {
		return "", fmt.Errorf("%s returned no output", grant.Backend)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

func invokeEvidenceExtract(req Request) Envelope {
	var in EvidenceExtractRequest
	if err := decodeInput(req.Input, &in); err != nil {
		return invalidRequest(req, err)
	}

	root, ok := req.Roots[RootMiddenHome]
	if !ok || strings.TrimSpace(root.Path) == "" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrMissingRoot,
			Message:   fmt.Sprintf("evidence.extract stores evidence and requires the %q root; none was supplied", RootMiddenHome),
			Retryable: true,
			Details:   map[string]any{"required_root": RootMiddenHome, "required_mode": "rw"},
		}, LocalFree())
	}
	if root.Mode != "rw" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrPermissionDenied,
			Message:   fmt.Sprintf("evidence.extract writes, but %q was supplied as %q", RootMiddenHome, root.Mode),
			Retryable: false,
		}, LocalFree())
	}

	roots := sourceRootsFrom(req)
	if !sourceStoresVisible(roots) {
		return noSourceStoresEnvelope(req)
	}

	db, closeDB, err := openIndex(req)
	if err != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code: ErrMissingRoot, Message: err.Error(), Retryable: true,
		}, LocalFree())
	}
	defer closeDB()

	result, warnings, err := EvidenceExtract(db, in, roots, modelGrantFrom(req))
	if err != nil {
		if err.Error() == ErrSubprocessDenied {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
				Code: ErrSubprocessDenied,
				Message: "extracting evidence requires a model, and no AI CLI was granted for this " +
					"invocation. Midden holds no API key: it shells out to a CLI the user is already " +
					"signed in to, so the host must grant subprocess authority and supply that " +
					"binary's absolute path. There is no deterministic path to evidence.",
				Retryable: true,
				Details: map[string]any{
					"required_subprocess": []string{"copilot", "claude", "opencode"},
				},
			}, UnknownCost())
		}
		return invalidRequest(req, err)
	}

	// Extraction always spends: a model was invoked through someone's own
	// subscription and Midden never sees a bill.
	spent := Execution{
		Local:     false,
		Provider:  result.ModelBackend,
		Artifacts: []Artifact{},
	}
	env, buildErr := NewResultEnvelope(OpInvoke, req.RequestID, result, spent)
	if buildErr != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code: ErrInternal, Message: buildErr.Error(),
		}, UnknownCost())
	}
	env.Warnings = emptySlice(warnings)
	return env
}
