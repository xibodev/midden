package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
)

// SessionSummary is one session on the wire.
//
// It carries identity and measurement, never transcript content: the source
// material is private and is not returned merely to simplify host integration.
type SessionSummary struct {
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Title     string `json:"title,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Created   string `json:"created,omitempty"`
	Updated   string `json:"updated,omitempty"`
	Turns     int    `json:"turns,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	Risk      string `json:"risk,omitempty"`

	// WorkspaceExists reports whether the session's directory is still on
	// disk. A dead workspace is why resume fails, so it is worth stating.
	WorkspaceExists bool `json:"workspace_exists"`
}

// ListResult is the sessions.list payload.
type ListResult struct {
	Sessions []SessionSummary `json:"sessions"`
	// RiskCounts covers the complete filtered scope before the bounded page.
	RiskCounts map[string]int `json:"risk_counts"`

	// Total is how many sessions the scope matched AFTER filtering.
	Total int `json:"total"`

	// ExcludedNoise is how many sessions the scope matched but the noise
	// filter removed, and Matched is the two combined.
	//
	// These exist because `total` alone is a number that cannot carry its own
	// caveat. Midden hides automated and trivial sessions by default, so a
	// user who counts 218 files on disk and is told "93" concludes Midden lost
	// 125 of them. The count was right and the ANSWER was misleading, which is
	// worse than being wrong: nothing about it invites checking.
	//
	// Reporting the denominator alongside the figure means an agent cannot
	// state the total without the context that qualifies it.
	ExcludedNoise int `json:"excluded_noise"`
	Matched       int `json:"matched"`

	// NoiseFilterApplied states plainly that a default did work here, so an
	// agent does not have to infer it from two numbers differing.
	NoiseFilterApplied bool `json:"noise_filter_applied"`

	// StoresRead and StoresUnavailable make the counts self-qualifying.
	//
	// A store that is present but LOCKED is skipped: the run succeeds, a
	// warning names the store and the remedy, and every count is computed over
	// the stores that opened. That is correct behaviour and a misleading
	// number -- matched is right about what was read and silent about what was
	// not, so a consumer cannot tell a partial inventory from a complete one.
	//
	// Reported as a FIELD rather than left to the warning, for the reason
	// excluded_noise exists: prose is not machine-readable, and an agent
	// summarising this result will quote the figure and drop the caveat. A
	// count that cannot carry its own denominator invites exactly the wrong
	// conclusion, and nothing about it prompts a check.
	StoresRead        []string `json:"stores_read"`
	StoresUnavailable []string `json:"stores_unavailable"`

	// PartialInventory is true when at least one store could not be read, so
	// the fact survives even if a consumer ignores the lists.
	PartialInventory bool `json:"partial_inventory"`

	Truncated bool `json:"truncated"`
}

// Normalize satisfies Normalizer.
func (r *ListResult) Normalize() {
	r.Sessions = emptySlice(r.Sessions)
	r.StoresRead = emptySlice(r.StoresRead)
	r.StoresUnavailable = emptySlice(r.StoresUnavailable)
}

// SessionsList inventories sessions over a bounded scope. Deterministic and
// read-only: source stores are opened read-only and never written.
func SessionsList(req AssayRequest, roots adapter.Roots) (*ListResult, []string, error) {
	sc, err := scopeFromAssayRequest(req)
	if err != nil {
		return nil, nil, err
	}
	max := clamp(req.MaxSessions, DefaultMaxSessions, MaxSessionsCeiling)

	// Collect WIDE, then filter here, so the number excluded is known rather
	// than invisible. Asking the adapter for a filtered set would make the
	// denominator unrecoverable.
	wide := sc
	wide.IncludeNoise = true
	all, errs := adapter.CollectWithRoots(wide, roots)

	var warnings []string
	// Coverage is derived from the SAME errors that produce the warnings, so
	// the prose and the structured fields cannot disagree.
	var unavailable []string
	for _, e := range errs {
		warnings = append(warnings, "source store: "+e.Error())
		unavailable = append(unavailable, storeNameOf(e))
	}

	var read []string
	for _, a := range adapter.AvailableWithRoots(roots) {
		name := string(a.Tool())
		if !containsName(unavailable, name) {
			read = append(read, name)
		}
	}
	sort.Strings(read)
	sort.Strings(unavailable)

	sessions := all
	excluded := 0
	if !req.IncludeNoise {
		sessions = sessions[:0:0]
		for _, s := range all {
			if s.Noise {
				excluded++
				continue
			}
			sessions = append(sessions, s)
		}
	}

	result := &ListResult{
		RiskCounts:         map[string]int{},
		Total:              len(sessions),
		Matched:            len(all),
		ExcludedNoise:      excluded,
		NoiseFilterApplied: !req.IncludeNoise,
		StoresRead:         read,
		StoresUnavailable:  unavailable,
		PartialInventory:   len(unavailable) > 0,
	}
	for _, session := range sessions {
		result.RiskCounts[session.Risk().String()]++
	}
	if len(unavailable) > 0 {
		// Said in prose as well, because the numbers above are the ones an
		// agent will quote and this is the caveat that qualifies every one.
		warnings = append(warnings, fmt.Sprintf(
			"every count here covers %v only: %v could not be read, so this is a "+
				"PARTIAL inventory and the totals are lower bounds",
			read, unavailable))
	}
	if excluded > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d automated or trivial sessions were excluded; %d of %d matched the scope. Pass include_noise to see them all.",
			excluded, len(sessions), len(all)))
	}
	if len(sessions) > max {
		sessions = sessions[:max]
		result.Truncated = true
	}

	for _, s := range sessions {
		result.Sessions = append(result.Sessions, summaryFrom(s))
	}

	sort.SliceStable(result.Sessions, func(i, j int) bool {
		if result.Sessions[i].Tool != result.Sessions[j].Tool {
			return result.Sessions[i].Tool < result.Sessions[j].Tool
		}
		return result.Sessions[i].SessionID < result.Sessions[j].SessionID
	})

	return result, warnings, nil
}

func summaryFrom(s core.Session) SessionSummary {
	sum := SessionSummary{
		SessionID: s.ID,
		Tool:      string(s.Tool),
		Title:     s.Title,
		Workspace: s.Dir,
		Repo:      s.Repo,
		Turns:     s.Turns,
		Bytes:     s.Bytes,
		// Risk is an int type with a String method: string(r) would yield a
		// rune, not the label.
		Risk:            s.Risk().String(),
		WorkspaceExists: s.DirExists(),
	}
	if !s.Created.IsZero() {
		sum.Created = s.Created.UTC().Format(time.RFC3339)
	}
	if !s.Updated.IsZero() {
		sum.Updated = s.Updated.UTC().Format(time.RFC3339)
	}
	return sum
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// ErrNoSourceStores is the detail returned when Midden cannot locate any
// source store to read.
//
// This exists because of a specific silent-failure mode. The host runs modules
// with an EMPTY environment (cmd.Env = []string{}), which is deliberate: it is
// what makes "everything arrives in the request" enforceable. But Midden's
// adapters resolve source stores from the user profile via os.UserHomeDir(),
// and adapter.home() swallows the error and returns "" — so every store path
// becomes relative, matches nothing, and a scope that should have found
// sessions returns ok:true with zero results and zero warnings.
//
// An empty success is indistinguishable from "you genuinely have no sessions",
// which is the worst possible answer: the host agent would report to a user
// that their recovery scope is empty when in fact the module could not see.
// Better to fail loudly with a code the host can route on.
const ErrNoSourceStores = "no_source_stores"

// sourceRootsFrom maps the host-supplied roots onto the adapter layer.
//
// The host runs modules with an empty environment and passes every path in the
// request, so these are the authoritative locations. A root the host did not
// supply is left empty, and the adapter falls back to user-profile resolution —
// which is what the human-facing CLI has always done and must keep doing.
//
// Paths are NORMALIZED because a logical root name does not pin a layout. A
// host reasonably reads "claude_store" as the directory holding the sessions
// (~/.claude/projects) while the adapter expects the store root (~/.claude) and
// appends "projects" itself. Both readings are defensible, and the failure was
// silent: the wrong depth produced ok:true with zero sessions, which is
// indistinguishable from "you have no sessions". Accepting either spelling
// costs one Stat and removes a whole class of interop bug.
func sourceRootsFrom(req Request) adapter.Roots {
	var r adapter.Roots
	if v, ok := req.Roots[RootCopilot]; ok {
		r.Copilot = strings.TrimSpace(v.Path)
	}
	if v, ok := req.Roots[RootClaude]; ok {
		r.Claude = normalizeClaudeRoot(strings.TrimSpace(v.Path))
	}
	if v, ok := req.Roots[RootOpencode]; ok {
		r.Opencode = strings.TrimSpace(v.Path)
	}
	r.Strict = req.ExplicitSourceRoots
	return r
}

// normalizeClaudeRoot accepts either the store root or its projects directory.
//
// It steps up only when the supplied path is named "projects" AND its parent
// carries another marker of a real Claude store, so a directory that merely
// happens to be called projects is left alone.
//
// The obvious check — "does <parent>/projects exist" — is circular: it is
// trivially true of the input itself and would rewrite any path ending in
// projects. The parent must show independent evidence.
func normalizeClaudeRoot(p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if !strings.EqualFold(filepath.Base(clean), "projects") {
		return p
	}
	parent := filepath.Dir(clean)

	// Independent evidence that the parent is a Claude store: either it is
	// named .claude, or it holds a sibling a bare projects directory would not.
	if strings.EqualFold(filepath.Base(parent), ".claude") {
		return parent
	}
	for _, sibling := range []string{"settings.json", "statsig", "todos", "shell-snapshots"} {
		if _, err := os.Stat(filepath.Join(parent, sibling)); err == nil {
			return parent
		}
	}
	return p
}

// sourceStoresVisible reports whether the source stores can be located at all.
//
// A root supplied by the host is sufficient on its own: that is the whole point
// of explicit roots, and requiring an environment as well would defeat them.
// Only when NO root is supplied does this fall back to asking whether a home
// directory resolves — because in that case the adapters will resolve from the
// user profile, and under an empty environment they would silently produce
// relative paths that match nothing.
//
// It is a precondition check, not a "do you have sessions" check: zero sessions
// from a visible store is a legitimate empty result.
func sourceStoresVisible(r adapter.Roots) bool {
	if r.Copilot != "" || r.Claude != "" || r.Opencode != "" {
		return true
	}
	if r.Strict {
		return false
	}
	h, err := os.UserHomeDir()
	return err == nil && strings.TrimSpace(h) != ""
}

// noSourceStoresEnvelope reports the precondition failure with a stable code.
//
// Retryable is TRUE: the same request succeeds once the caller supplies the
// environment or root the module needs, so the host should surface it as a
// setup problem rather than a permanent failure.
func noSourceStoresEnvelope(req Request) Envelope {
	return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
		Code: ErrNoSourceStores,
		Message: "no source store is reachable: Midden could not resolve a home directory. " +
			"Supply the source-store roots in the request, or run with an environment that sets HOME/USERPROFILE.",
		Retryable: true,
		Details: map[string]any{
			"expected_roots": []string{RootCopilot, RootClaude, RootOpencode},
			"reason":         "home directory unresolved; source-store paths would silently match nothing",
		},
	}, LocalFree())
}

// Operation values. These mirror the two verbs the host speaks, NOT the
// capability: the capability already travels in Request.Capability and is
// correlated by request_id. Putting the capability here would make the field
// mean different things depending on which module answered.
//
// Pinned by the host's canonical fixtures, which are authoritative over chat.
const (
	OpDescribe = "describe"
	OpInvoke   = "invoke"
)

// Invoke routes a request to its capability and returns the response envelope.
//
// It returns an envelope rather than an error for capability-level failures:
// a structured error envelope IS the protocol's failure mode, and the host
// routes on the stable code rather than on message text.
func Invoke(req Request) Envelope {
	if req.Protocol != "" && !speaksProtocol(req.Protocol) {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code: ErrUnsupportedProtocol,
			Message: fmt.Sprintf("unsupported protocol %q, this module speaks %v",
				req.Protocol, Describe().ProtocolVersions),
			Retryable: false,
			Details:   map[string]any{"supported": Describe().ProtocolVersions},
		}, UnknownCost())
	}

	if capability, ok := agentCapabilityByID(req.Capability); ok {
		return invokeAgent(req, capability)
	}
	if capability, ok := workflowCapabilityByID(req.Capability); ok {
		return invokeWorkflow(req, capability)
	}
	// Precondition: the read capabilities need visible source stores.
	// Reporting an empty success when the stores are merely invisible would be
	// a lie the host cannot detect. seed.create runs its own checks in order:
	// it must report a missing write root before it reports missing stores.
	needsStores := req.Capability != CapSeedCreate &&
		req.Capability != CapContentTypes &&
		req.Capability != CapContentProduce &&
		req.Capability != CapEvidenceExtract
	if needsStores && !sourceStoresVisible(sourceRootsFrom(req)) {
		return noSourceStoresEnvelope(req)
	}

	switch req.Capability {
	case CapSessionsList:
		return invokeList(req)
	case CapSessionsAssay:
		return invokeAssay(req)
	case CapSeedCreate:
		return invokeSeedCreate(req)
	case CapContentTypes:
		return invokeContentTypes(req)
	case CapContentProduce:
		return invokeContentProduce(req)
	case CapEvidenceExtract:
		return invokeEvidenceExtract(req)
	default:
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrUnknownCapability,
			Message:   fmt.Sprintf("unknown capability %q", req.Capability),
			Retryable: false,
			Details:   map[string]any{"known": []string{CapSessionsList, CapSessionsAssay, CapSeedCreate, CapContentTypes, CapContentProduce, CapEvidenceExtract}},
		}, UnknownCost())
	}
}

func decodeInput(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func invokeList(req Request) Envelope {
	var in AssayRequest
	if err := decodeInput(req.Input, &in); err != nil {
		return invalidRequest(req, err)
	}
	result, warnings, err := SessionsList(in, sourceRootsFrom(req))
	if err != nil {
		return invalidRequest(req, err)
	}
	return successEnvelope(req, result, warnings)
}

func invokeAssay(req Request) Envelope {
	var in AssayRequest
	if err := decodeInput(req.Input, &in); err != nil {
		return invalidRequest(req, err)
	}
	result, warnings, err := SessionsAssay(in, sourceRootsFrom(req))
	if err != nil {
		return invalidRequest(req, err)
	}
	return successEnvelope(req, result, warnings)
}

// successEnvelope wraps a deterministic local result. Costs are explicit
// zeroes because this work is genuinely free, not of unknown price.
func successEnvelope(req Request, payload any, warnings []string) Envelope {
	env, err := NewResultEnvelope(OpInvoke, req.RequestID, payload, LocalFree())
	if err != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrInternal,
			Message:   "result could not be encoded: " + err.Error(),
			Retryable: false,
		}, LocalFree())
	}
	env.Warnings = emptySlice(warnings)
	return env
}

func invalidRequest(req Request, err error) Envelope {
	return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
		Code:      ErrInvalidRequest,
		Message:   err.Error(),
		Retryable: false,
	}, LocalFree())
}

// speaksProtocol reports whether a protocol id is one this module declares.
//
// The descriptor advertises ProtocolVersions as a LIST, but validation compared
// against a single constant: the module declared a negotiable surface and then
// implemented exact match. Today the list holds one entry so the two agree, and
// the divergence would appear only when a second version exists -- at which
// point a host offering v2 while still supporting v1 would be refused with
// "unsupported protocol", which reads as a protocol error rather than a
// negotiation miss.
//
// This checks MEMBERSHIP of the declared list, so the declaration and the check
// cannot drift. It adds no v1 guarantee and negotiates nothing: the host still
// selects, and this module still speaks exactly what it advertises.
func speaksProtocol(id string) bool {
	for _, v := range Describe().ProtocolVersions {
		if v == id {
			return true
		}
	}
	return false
}

// storeNameOf extracts the tool name from a source-store error.
//
// The errors are formatted "<tool>: <cause>", so the name is the prefix. A
// malformed error yields the whole string rather than an empty name: an
// unnamed unavailable store is worse than an oddly named one, because the
// coverage list would silently shrink.
func storeNameOf(e error) string {
	msg := e.Error()
	if i := strings.Index(msg, ":"); i > 0 {
		return strings.TrimSpace(msg[:i])
	}
	return msg
}

func containsName(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
