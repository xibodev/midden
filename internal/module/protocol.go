// Package module implements Midden's side of the detached-module protocol:
// the wire contract between a module host and this binary.
//
// The host speaks exactly two verbs:
//
//	midden module describe --json
//	midden module invoke <capability> --input <request.json>
//
// Midden's human CLI is unaffected; the host never calls it.
//
// These types are deliberately a SEPARATE DECLARATION of the same wire shape
// the host defines, not a shared Go package. Modules are detached local
// processes, never imported packages, so the contract is the JSON on the wire.
// Coupling the two repos through a Go import would defeat the point and make
// Midden unbuildable without the host.
//
// Wire rules:
//
//   - stdout carries exactly one bounded JSON envelope and nothing else;
//   - stderr is advisory only and never load-bearing for correctness;
//   - every path is supplied by the host, canonicalized, and root-confined;
//   - unknown cost stays unknown; it never silently becomes zero;
//   - collections marshal as [] or {}, never null.
package module

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/mekjr1/midden/internal/confirmation"
)

// ProtocolID is the operator-ruled protocol identifier. Vendor-neutral because
// Midden is as much a first-class module as any other: the shared namespace
// root is xibodev, matching the xibodev.midden.seed/v1 seed contract.
//
// Kept as a single constant so adopting a new protocol version is one edit.
const ProtocolID = "xibodev.module/v1"

// ModuleID is Midden's stable module identifier and capability namespace root.
const ModuleID = "midden"

// DigestPrefix is the mandatory algorithm prefix on every protocol digest.
// The prefix makes a future hash change unambiguous rather than a silent
// reinterpretation of existing bare hex.
const DigestPrefix = "sha256:"

// DigestSHA256 formats content as a protocol digest: "sha256:<lowercase-hex>".
func DigestSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// ValidDigest reports whether s is a well-formed protocol digest. Shape only;
// it does not verify the digest against any content.
func ValidDigest(s string) bool {
	rest, ok := strings.CutPrefix(s, DigestPrefix)
	if !ok || len(rest) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

// Descriptor is the complete result of `module describe --json`.
//
// The field set is closed: all three lane prompts specify this exact list.
type Descriptor struct {
	Module  string `json:"module"`
	Name    string `json:"name"`
	Version string `json:"version"`

	// Build names the artifact variant ("standalone", "headless"). Two builds
	// of the same version can legitimately include different faces, so a
	// consumer pinning a surface must be able to tell them apart from the
	// descriptor rather than by calling something and finding it missing.
	Build            string                     `json:"build,omitempty"`
	ProtocolVersions []string                   `json:"protocol_versions"`
	Capabilities     []Capability               `json:"capabilities"`
	RequestSchemas   map[string]json.RawMessage `json:"request_schemas"`
	ResultSchemas    map[string]json.RawMessage `json:"result_schemas"`
	ArtifactSchemas  map[string]json.RawMessage `json:"artifact_schemas"`
	AgentOverlays    []Overlay                  `json:"agent_overlays"`
	Skills           []Skill                    `json:"skills"`
	Permissions      Permissions                `json:"permissions"`
	Requirements     []Requirement              `json:"requirements"`
}

// Capability is one invocable operation.
type Capability struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Summary         string   `json:"summary"`
	RequestSchema   string   `json:"request_schema"`
	ResultSchema    string   `json:"result_schema"`
	ArtifactSchemas []string `json:"artifact_schemas"`
	Effects         Effects  `json:"effects"`
	Skills          []string `json:"skills"`
	LongRunning     bool     `json:"long_running"`
	PollCapability  string   `json:"poll_capability,omitempty"`
}

// Effects is the pre-declared effect profile of a capability, mirroring
// Execution, which reports what an invocation actually did.
//
// It must describe what the CODE PATH does, not what the capability name
// suggests: a capability that reads like a status check can still be
// destructive through a constructor side effect.
type Effects struct {
	Local          bool   `json:"local"`
	Network        bool   `json:"network"`
	ExternalWrites bool   `json:"external_writes"`
	Provider       string `json:"provider"`
	CostKnown      bool   `json:"cost_known"`
}

// Overlay is a module-authored agent instruction document.
type Overlay struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Tokens int    `json:"tokens"`
}

// Skill is progressively loadable module knowledge.
type Skill struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Tokens  int    `json:"tokens"`
}

// Permissions is what Midden declares it MAY need. It is a request, never a
// grant: installing a module grants nothing, and authority arrives per
// invocation in Grants.
type Permissions struct {
	FilesystemRead  []string `json:"filesystem_read"`
	FilesystemWrite []string `json:"filesystem_write"`
	Network         []string `json:"network"`
	Credentials     []string `json:"credentials"`
	PaidProviders   []string `json:"paid_providers"`
	Publish         bool     `json:"publish"`

	// Subprocess is distinct from Network on purpose. Midden holds no API key
	// and never calls a model API directly; model-backed capabilities shell
	// out to an already-authenticated copilot/claude/opencode CLI. That is
	// subprocess authority, not network or credential authority.
	Subprocess []string `json:"subprocess"`
}

// Requirement is an external precondition the host surfaces when unmet.
type Requirement struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

// ---------------------------------------------------------------------------
// Invocation
// ---------------------------------------------------------------------------

// Request is the JSON document the host supplies via --input.
type Request struct {
	ConfirmOperator confirmation.Handler                          `json:"-"`
	Context         context.Context                               `json:"-"`
	NativeDriver    func(context.Context, string) (string, error) `json:"-"`
	Protocol        string                                        `json:"protocol"`
	Capability      string                                        `json:"capability"`
	RequestID       string                                        `json:"request_id"`
	Input           json.RawMessage                               `json:"input"`
	Roots           map[string]Root                               `json:"roots"`
	Grants          Grants                                        `json:"grants"`

	// ExplicitSourceRoots disables ambient fallback to user-profile paths,
	// requiring all source stores to be explicitly provided in Roots.
	ExplicitSourceRoots bool `json:"explicit_source_roots,omitempty"`

	// Binaries maps a declared subprocess NAME to the absolute path the host
	// resolved for it.
	//
	// A module never searches PATH: under the host's empty environment there
	// is none, and a PATH a module searches is ambient authority while an
	// absolute path the host supplies is a grant. An unresolvable binary is
	// ABSENT from this map rather than present-and-empty, so "not installed"
	// and "not permitted" stay distinguishable.
	Binaries map[string]string `json:"binaries"`

	DeadlineMS     int `json:"deadline_ms"`
	MaxOutputBytes int `json:"max_output_bytes"`
}

// Root is one canonicalized filesystem root with its access mode.
type Root struct {
	Path string `json:"path"`
	Mode string `json:"mode"` // "ro" or "rw"
}

// Grants is the per-invocation authorization set.
type Grants struct {
	Network       []string `json:"network"`
	Credentials   []string `json:"credentials"`
	PaidProviders []string `json:"paid_providers"`
	Publish       bool     `json:"publish"`
	Subprocess    []string `json:"subprocess"`
}

// Envelope is the single JSON document written to stdout, for both describe
// and invoke. Nothing else may appear on stdout.
type Envelope struct {
	Protocol  string          `json:"protocol"`
	Module    string          `json:"module"`
	Operation string          `json:"operation"`
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *Error          `json:"error,omitempty"`
	Warnings  []string        `json:"warnings"`
	Execution Execution       `json:"execution"`
}

// Error is a structured, bounded failure.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

// Execution is what actually happened, present even on failure because a
// failed call may still have cost money or written something.
type Execution struct {
	Local          bool   `json:"local"`
	Network        bool   `json:"network"`
	ExternalWrites bool   `json:"external_writes"`
	Provider       string `json:"provider"`

	// EstimatedCost and ActualCost are pointers on purpose: null means
	// genuinely unknown, 0 means genuinely free. Collapsing the two would let
	// an unpriced call render as free and slip past cost approval.
	EstimatedCost *float64 `json:"estimated_cost"`
	ActualCost    *float64 `json:"actual_cost"`

	Artifacts []Artifact `json:"artifacts"`
}

// Artifact is a pointer to something the module produced. Never an inline
// payload: large results belong here so the envelope stays bounded.
//
// Root names a root supplied to THIS invocation and is not portable to another
// module. Crossing a module boundary is the host's job, via staging.
type Artifact struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Root      string `json:"root"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	Digest    string `json:"digest"`
	Title     string `json:"title,omitempty"`
}

// Stable error codes a module may return.
const (
	ErrUnsupportedProtocol = "unsupported_protocol"
	ErrUnknownCapability   = "unknown_capability"
	ErrInvalidRequest      = "invalid_request"
	ErrMissingRequirement  = "missing_requirement"
	ErrPermissionDenied    = "permission_denied"
	ErrPathOutsideRoot     = "path_outside_root"
	ErrProviderFailure     = "provider_failure"
	ErrTimeout             = "timeout"
	ErrCancelled           = "cancelled"
	ErrInternal            = "internal"
)

// ---------------------------------------------------------------------------
// Empty-collection normalization
// ---------------------------------------------------------------------------
//
// Go marshals a nil slice and a nil map as null. The protocol requires [] and
// {}, so every collection is normalized before marshalling. Doing this in one
// place means a capability cannot ship nulls by forgetting a guard.

func emptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func emptyMap[T any](m map[string]T) map[string]T {
	if m == nil {
		return map[string]T{}
	}
	return m
}

// Normalize replaces every nil collection in the descriptor, recursively.
func (d *Descriptor) Normalize() {
	d.ProtocolVersions = emptySlice(d.ProtocolVersions)
	d.Capabilities = emptySlice(d.Capabilities)
	d.RequestSchemas = emptyMap(d.RequestSchemas)
	d.ResultSchemas = emptyMap(d.ResultSchemas)
	d.ArtifactSchemas = emptyMap(d.ArtifactSchemas)
	d.AgentOverlays = emptySlice(d.AgentOverlays)
	d.Skills = emptySlice(d.Skills)
	d.Requirements = emptySlice(d.Requirements)
	for i := range d.Capabilities {
		d.Capabilities[i].ArtifactSchemas = emptySlice(d.Capabilities[i].ArtifactSchemas)
		d.Capabilities[i].Skills = emptySlice(d.Capabilities[i].Skills)
	}
	d.Permissions.Normalize()
}

// Normalize replaces every nil slice in the permission set.
func (p *Permissions) Normalize() {
	p.FilesystemRead = emptySlice(p.FilesystemRead)
	p.FilesystemWrite = emptySlice(p.FilesystemWrite)
	p.Network = emptySlice(p.Network)
	p.Credentials = emptySlice(p.Credentials)
	p.PaidProviders = emptySlice(p.PaidProviders)
	p.Subprocess = emptySlice(p.Subprocess)
}

// Normalize replaces every nil slice in the grant set.
func (g *Grants) Normalize() {
	g.Network = emptySlice(g.Network)
	g.Credentials = emptySlice(g.Credentials)
	g.PaidProviders = emptySlice(g.PaidProviders)
	g.Subprocess = emptySlice(g.Subprocess)
}

// Normalize replaces every nil collection in the envelope.
//
// It deliberately does NOT touch EstimatedCost or ActualCost: a nil cost is
// meaningful (unknown) and must never be normalized into zero.
//
// It also cannot reach inside Result, which is already opaque bytes by the
// time this runs. Payloads must therefore be normalized BEFORE being marshalled
// into Result — which is why constructing an envelope goes through
// NewResultEnvelope rather than by hand.
func (e *Envelope) Normalize() {
	e.Warnings = emptySlice(e.Warnings)
	e.Execution.Artifacts = emptySlice(e.Execution.Artifacts)
	if e.Error != nil && e.Error.Details == nil {
		e.Error.Details = map[string]any{}
	}
}

// Normalize replaces every nil collection in the request.
func (r *Request) Normalize() {
	if r.Roots == nil {
		r.Roots = map[string]Root{}
	}
	if r.Binaries == nil {
		r.Binaries = map[string]string{}
	}
	r.Grants.Normalize()
}

// Normalizer is implemented by result payloads that carry their own nested
// collections. NewResultEnvelope calls it before marshalling, so a payload
// normalizes itself rather than relying on the envelope to reach inside it.
type Normalizer interface{ Normalize() }

// ---------------------------------------------------------------------------
// Envelope construction
// ---------------------------------------------------------------------------
//
// These constructors exist so the ORDER is always right: normalize the payload,
// marshal it into Result, then normalize the envelope. Building an envelope by
// hand is the one way to reintroduce nulls, so nothing in this package does.

// NewResultEnvelope builds a success envelope around a payload.
func NewResultEnvelope(operation, requestID string, payload any, exec Execution) (Envelope, error) {
	if n, ok := payload.(Normalizer); ok {
		n.Normalize()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	env := Envelope{
		Protocol:  ProtocolID,
		Module:    ModuleID,
		Operation: operation,
		RequestID: requestID,
		OK:        true,
		Result:    raw,
		Execution: exec,
	}
	env.Normalize()
	return env, nil
}

// NewErrorEnvelope builds a failure envelope.
//
// Execution is required rather than defaulted: a failed invocation may still
// have cost money or written something. A zero Execution leaves both cost
// pointers nil, which is the safe "unknown" default.
func NewErrorEnvelope(operation, requestID string, e Error, exec Execution) Envelope {
	env := Envelope{
		Protocol:  ProtocolID,
		Module:    ModuleID,
		Operation: operation,
		RequestID: requestID,
		OK:        false,
		Error:     &e,
		Execution: exec,
	}
	env.Normalize()
	return env
}

// LocalFree is the Execution for deterministic local work that costs nothing.
//
// Costs are EXPLICIT ZEROES, not nil: this work is genuinely free, not of
// unknown price. That distinction is the whole reason the fields are pointers.
func LocalFree() Execution {
	zero := 0.0
	return Execution{
		Local:         true,
		EstimatedCost: &zero,
		ActualCost:    &zero,
		Artifacts:     []Artifact{},
	}
}

// UnknownCost is the Execution for work whose price is not known. Both cost
// pointers stay nil, so the host treats it as requiring approval.
func UnknownCost() Execution {
	return Execution{Local: true, Artifacts: []Artifact{}}
}
