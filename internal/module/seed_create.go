package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

// Capability and schema IDs for seed creation.
const (
	CapSeedCreate = "seed.create"

	SchemaSeedCreateRequest = "xibodev.midden.seed.create.request/v1"
	SchemaSeedCreateResult  = "xibodev.midden.seed.create.result/v1"
)

// ErrMissingRoot is returned when a write-requiring capability is invoked
// without the root it needs.
//
// Operator ruling: seed.create resolves its write location ONLY from
// Request.Roots["midden_home"]. It never falls back to the MIDDEN_HOME
// environment variable or the user profile.
//
// The reason is measured, not theoretical. The host runs modules with an empty
// environment, and under one Midden's own Dir() resolves to the RELATIVE path
// ".midden" — so a fallback would silently write a seed into whatever directory
// the host happened to launch from, with no error anywhere. Requiring the root
// makes that failure structurally impossible instead of merely guarded.
const ErrMissingRoot = "missing_root"

// SeedCreateRequest asks for a seed built from an exact session scope.
type SeedCreateRequest struct {
	// Scope selects the source sessions. Exact IDs are strongly preferred
	// over an inferred scope: a seed built from the wrong sessions is worse
	// than no seed.
	AssayRequest

	Goal      string   `json:"goal,omitempty"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	KeyPoints []string `json:"key_points,omitempty"`

	// SuggestedOutputTypes is advisory and owned by the consuming lane.
	SuggestedOutputTypes []string `json:"suggested_output_types,omitempty"`

	// Brief overrides the generated prose. Optional.
	Brief string `json:"brief,omitempty"`

	// Name is the seed directory name under <midden_home>/seeds/. Generated
	// when empty. It is validated as a single path segment: a seed must not be
	// able to name a location outside the granted root.
	Name string `json:"name,omitempty"`

	// Attach names produced documents to copy into the seed, as paths
	// RELATIVE to the midden_home root — the same `path` content.produce
	// returns.
	//
	// This is what makes the richer journey possible: mine sessions, write a
	// video brief from the evidence, then hand a consumer the brief AND the
	// evidence behind it rather than evidence alone. Without it a seed carries
	// a goal and raw evidence, and the document written for the consumer is
	// left behind.
	//
	// Paths are confined to the granted root and copied by basename into
	// attachments/, so a seed can never reference a location outside it.
	Attach []string `json:"attach,omitempty"`

	// MaxEvidence bounds how many evidence records the seed carries.
	MaxEvidence int `json:"max_evidence,omitempty"`
}

// Bounds for seed construction.
const (
	DefaultMaxEvidence = 40
	MaxEvidenceCeiling = 500
)

// SeedCreateResult reports the created seed.
//
// Path is RELATIVE to the midden_home root, because an absolute path would be
// unverifiable by the host and meaningless to a consumer reading a staged copy.
type SeedCreateResult struct {
	Schema         string `json:"schema"`
	Root           string `json:"root"`
	Path           string `json:"path"`
	ManifestPath   string `json:"manifest_path"`
	EvidenceDigest string `json:"evidence_digest"`
	EvidenceCount  int    `json:"evidence_count"`
	SessionCount   int    `json:"session_count"`
}

// Normalize satisfies Normalizer. The result carries no collections today;
// the method exists so adding one cannot silently reintroduce a null.
func (r *SeedCreateResult) Normalize() {}

// pathEscapesRoot reports whether an absolute path resolves outside a root.
//
// Both sides are symlink-resolved before comparison. A lexical check alone is
// defeated by a link that is relative, traversal-free, and inside the root by
// name while pointing elsewhere — which is exactly how a confined path becomes
// unconfined.
func pathEscapesRoot(abs, root string) bool {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(root)+string(filepath.Separator))
}

// validSeedName reports whether name is a single safe path segment.
//
// A seed name arrives from a caller and becomes a directory, so it is exactly
// the input that must not be able to escape the granted root.
func validSeedName(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// invokeSeedCreate builds a seed under the host-supplied write root.
func invokeSeedCreate(req Request) Envelope {
	var in SeedCreateRequest
	if err := decodeInput(req.Input, &in); err != nil {
		return invalidRequest(req, err)
	}

	// The write root is mandatory. No ambient fallback: see ErrMissingRoot.
	root, ok := req.Roots[RootMiddenHome]
	if !ok || strings.TrimSpace(root.Path) == "" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:    ErrMissingRoot,
			Message: fmt.Sprintf("seed.create requires the %q write root; none was supplied", RootMiddenHome),
			// Retryable: the same request succeeds once the host grants the
			// root, so this is a setup problem rather than a permanent failure.
			Retryable: true,
			Details: map[string]any{
				"required_root": RootMiddenHome,
				"required_mode": "rw",
			},
		}, LocalFree())
	}
	if root.Mode != "rw" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrPermissionDenied,
			Message:   fmt.Sprintf("seed.create needs write access to %q, but it was supplied as %q", RootMiddenHome, root.Mode),
			Retryable: false,
			Details:   map[string]any{"required_mode": "rw", "supplied_mode": root.Mode},
		}, LocalFree())
	}
	if !filepath.IsAbs(root.Path) {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrInvalidRequest,
			Message:   "root paths must be absolute and canonicalized by the host",
			Retryable: false,
		}, LocalFree())
	}

	name := in.Name
	if name == "" {
		name = "seed-" + strings.ReplaceAll(req.RequestID, ":", "-")
		if !validSeedName(name) {
			name = "seed-unnamed"
		}
	}
	if !validSeedName(name) {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrInvalidRequest,
			Message:   fmt.Sprintf("seed name %q is not a single safe path segment", in.Name),
			Retryable: false,
		}, LocalFree())
	}

	// Source stores must be visible: a seed built from an unreadable store
	// would be an empty seed that looks deliberate.
	roots := sourceRootsFrom(req)
	if !sourceStoresVisible(roots) {
		return noSourceStoresEnvelope(req)
	}

	sc, err := scopeFromAssayRequest(in.AssayRequest)
	if err != nil {
		return invalidRequest(req, err)
	}
	maxSessions := clamp(in.MaxSessions, DefaultMaxSessions, MaxSessionsCeiling)
	maxEvidence := clamp(in.MaxEvidence, DefaultMaxEvidence, MaxEvidenceCeiling)

	sessions, errs := adapter.CollectWithRoots(sc, roots)
	var warnings []string
	for _, e := range errs {
		warnings = append(warnings, "source store: "+e.Error())
	}
	if len(sessions) > maxSessions {
		sessions = sessions[:maxSessions]
		warnings = append(warnings, fmt.Sprintf("scope matched more sessions than the bound; used the first %d", maxSessions))
	}

	// Attachments are resolved against the granted root and confined to it.
	// A caller supplies a relative path — the one content.produce returned —
	// and anything escaping the root is refused rather than silently clamped,
	// because a silently-corrected path hides a caller that believed it could
	// reach outside.
	var attach []string
	for _, rel := range in.Attach {
		clean := filepath.Clean(strings.TrimSpace(rel))
		if clean == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
				Code:      ErrInvalidRequest,
				Message:   fmt.Sprintf("attachment %q must be a relative path inside the %q root", rel, RootMiddenHome),
				Retryable: false,
			}, LocalFree())
		}
		abs := filepath.Join(root.Path, clean)
		// Resolve symlinks before the confinement check: a lexical check alone
		// is defeated by a link pointing outside the root.
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if pathEscapesRoot(abs, root.Path) {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
				Code:      ErrPathOutsideRoot,
				Message:   fmt.Sprintf("attachment %q resolves outside the %q root", rel, RootMiddenHome),
				Retryable: false,
			}, LocalFree())
		}
		if _, err := os.Stat(abs); err != nil {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
				Code:      ErrInvalidRequest,
				Message:   fmt.Sprintf("attachment %q does not exist under the %q root", rel, RootMiddenHome),
				Retryable: true,
			}, LocalFree())
		}
		attach = append(attach, abs)
	}

	input := SeedInput{
		Attach:               attach,
		Goal:                 in.Goal,
		Title:                in.Title,
		Summary:              in.Summary,
		KeyPoints:            in.KeyPoints,
		SuggestedOutputTypes: in.SuggestedOutputTypes,
		Brief:                in.Brief,
	}

	for _, s := range sessions {
		input.Sources = append(input.Sources, SeedSource{
			SessionID:  s.ID,
			Tool:       string(s.Tool),
			Title:      s.Title,
			Bytes:      s.Bytes,
			ModifiedAt: rfc3339OrEmpty(s),
		})
		if len(input.Evidence) >= maxEvidence {
			continue
		}
		ev, w := evidenceFrom(s, maxEvidence-len(input.Evidence), roots)
		input.Evidence = append(input.Evidence, ev...)
		warnings = append(warnings, w...)
	}

	seedDir := filepath.Join(root.Path, "seeds", name)
	m, err := WriteSeed(seedDir, input)
	if err != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrInternal,
			Message:   "seed could not be written: " + err.Error(),
			Retryable: false,
		}, LocalFree())
	}

	rel := filepath.ToSlash(filepath.Join("seeds", name))
	result := &SeedCreateResult{
		Schema:         SeedSchemaID,
		Root:           RootMiddenHome,
		Path:           rel,
		ManifestPath:   rel + "/" + SeedManifestFile,
		EvidenceDigest: m.EvidenceDigest,
		EvidenceCount:  m.EvidenceCount,
		SessionCount:   len(sessions),
	}

	env, err := NewResultEnvelope(OpInvoke, req.RequestID, result, seedExecution(seedDir, rel, m))
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

// seedExecution reports the seed as an artifact pointer.
//
// ExternalWrites stays FALSE: the seed is written inside Midden's own granted
// root, not to a store outside the module's boundary. Reporting true would be
// as dishonest as under-reporting — the flag means "wrote somewhere the host
// did not grant me", not "wrote a file".
func seedExecution(seedDir, rel string, m *SeedManifest) Execution {
	zero := 0.0
	exec := Execution{
		Local:         true,
		EstimatedCost: &zero,
		ActualCost:    &zero,
		Artifacts:     []Artifact{},
	}

	manifestPath := filepath.Join(seedDir, SeedManifestFile)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return exec
	}
	exec.Artifacts = append(exec.Artifacts, Artifact{
		ID:        "seed-manifest",
		Kind:      SeedSchemaID,
		Path:      rel + "/" + SeedManifestFile,
		Root:      RootMiddenHome,
		MediaType: "application/json",
		Bytes:     int64(len(raw)),
		Digest:    DigestSHA256(raw),
		Title:     strings.TrimSpace(m.Title),
	})
	return exec
}

func rfc3339OrEmpty(s core.Session) string {
	if s.Updated.IsZero() {
		return ""
	}
	return s.Updated.UTC().Format("2006-01-02T15:04:05Z")
}

// evidenceFrom selects bounded evidence from a session's assay candidates.
//
// It reuses the deterministic assay rather than re-reading transcripts: the
// candidate set is precisely "signal records worth handing to a model", which
// is what a seed should carry. No model is invoked.
func evidenceFrom(s core.Session, limit int, roots adapter.Roots) ([]SeedEvidence, []string) {
	if limit <= 0 {
		return nil, nil
	}
	a := adapter.FindWithRoots(s.Tool, roots)
	if a == nil {
		return nil, []string{fmt.Sprintf("no adapter for tool %q", s.Tool)}
	}
	as, ok := a.(adapter.Assayer)
	if !ok {
		return nil, []string{fmt.Sprintf("tool %q cannot be assayed", s.Tool)}
	}
	m, err := as.Assay(s, limit)
	if err != nil {
		return nil, []string{fmt.Sprintf("assay %s: %v", s.ID, err)}
	}

	var out []SeedEvidence
	for i, c := range m.Candidates {
		if len(out) >= limit {
			break
		}
		if c.Class != assay.Signal {
			continue
		}
		out = append(out, SeedEvidence{
			ID:        fmt.Sprintf("%s-%04d", shortID(s.ID), i+1),
			SessionID: s.ID,
			Tool:      string(s.Tool),
			Kind:      c.Kind,
			Role:      c.Role,
			Time:      timeOrEmpty(c),
			Excerpt:   c.Preview,
		})
	}
	return out, nil
}

func timeOrEmpty(r assay.Record) string {
	if r.Time.IsZero() {
		return ""
	}
	return r.Time.UTC().Format("2006-01-02T15:04:05Z")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
