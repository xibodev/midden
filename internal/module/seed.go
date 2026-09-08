package module

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/redact"
)

// SeedSchemaID is the Midden content-seed contract.
//
// A seed is a portable DIRECTORY, not a single file:
//
//	<seed-root>/
//	  manifest.json     entry file; the machine-readable facts
//	  brief.md          prose for the host agent to read
//	  evidence.jsonl    selected evidence, one record per line
//	  provenance.json   source identities and revisions
//	  attachments/      referenced files
//
// It must be readable with no Midden binary, no Midden index, and no
// MIDDEN_HOME present: the host stages a seed into its own artifact store and
// hands a consumer a read-only root, so the path a consumer sees is NOT the
// path Midden wrote. Every internal reference is therefore relative to the seed
// root, and the DIGEST rather than the path is the stable identity.
const SeedSchemaID = "xibodev.midden.seed/v1"

// Seed file names. Fixed by the contract: a consumer resolves the entry file
// by name relative to the seed root.
const (
	SeedManifestFile   = "manifest.json"
	SeedBriefFile      = "brief.md"
	SeedEvidenceFile   = "evidence.jsonl"
	SeedProvenanceFile = "provenance.json"
	SeedAttachmentsDir = "attachments"
)

// SeedManifest is the seed's entry file.
//
// The consuming lane reads exactly these fields, so the shape is theirs rather
// than mine. Everything except Schema is optional: a seed carrying only a goal
// is valid, and a consumer degrades rather than failing.
type SeedManifest struct {
	Schema string `json:"schema"`

	Goal      string   `json:"goal,omitempty"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	KeyPoints []string `json:"key_points,omitempty"`

	// Attachments are paths RELATIVE to the seed root, never absolute and
	// never referencing Midden's own layout.
	Attachments []string `json:"attachments,omitempty"`

	// SuggestedOutputTypes is an advisory hint. It is deliberately a plain
	// string list rather than an enum: the creative lane owns this vocabulary,
	// so enumerating it here would make every new output type a breaking change
	// in Midden's schema. An unrecognised value degrades to "no suggestion".
	SuggestedOutputTypes []string `json:"suggested_output_types,omitempty"`

	// EvidenceDigest is sha256 over evidence.jsonl, bare lowercase hex.
	//
	// It is scoped to the EVIDENCE SET, not the whole bundle, so regenerating
	// brief.md prose does not invalidate a consumer's provenance claim. It is
	// a different value from any digest the host computes over the seed for
	// transfer integrity: two questions, two values. Conflating them would make
	// a prose edit look like evidence tampering.
	//
	// Bare hex inside the manifest; the "sha256:" prefix is a module-boundary
	// convention and is applied when the seed is reported as an Artifact.
	EvidenceDigest string `json:"evidence_digest"`

	// EvidenceCount is how many records evidence.jsonl holds. Zero is valid:
	// a hand-written goal with no recovered material is a legitimate seed.
	EvidenceCount int `json:"evidence_count"`

	CreatedAt string `json:"created_at,omitempty"`
}

// Normalize satisfies Normalizer.
func (m *SeedManifest) Normalize() {
	m.KeyPoints = emptySlice(m.KeyPoints)
	m.Attachments = emptySlice(m.Attachments)
	m.SuggestedOutputTypes = emptySlice(m.SuggestedOutputTypes)
}

// SeedEvidence is one evidence record, one JSON object per line.
//
// It carries a POINTER plus a bounded excerpt, never a whole transcript: the
// source material is private, and a seed that copied transcripts wholesale
// would defeat both the privacy boundary and the bounded-size property.
type SeedEvidence struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Kind      string `json:"kind,omitempty"`
	Role      string `json:"role,omitempty"`
	Time      string `json:"time,omitempty"`

	// Excerpt is redacted and length-bounded.
	Excerpt string `json:"excerpt"`

	// Redacted reports whether secret-shaped content was replaced. It is a
	// fact about this record, not a promise that the excerpt is safe to
	// publish: redaction matches credential shapes, not private prose.
	Redacted bool `json:"redacted"`
}

// SeedProvenance records where a seed came from, and what was NOT done to it.
type SeedProvenance struct {
	Schema      string       `json:"schema"`
	Module      string       `json:"module"`
	ModuleVer   string       `json:"module_version"`
	CreatedAt   string       `json:"created_at"`
	Sources     []SeedSource `json:"sources"`
	Redaction   string       `json:"redaction"`
	ModelUsed   bool         `json:"model_used"`
	ReviewState string       `json:"review_state"`
}

// SeedTitleLimit bounds a source title.
//
// A session's title is DERIVED FROM ITS FIRST PROMPT, so it is unbounded and
// is private source material rather than a label. Left unbounded it made a
// provenance file 5.7 KB of verbatim prompt text for two sessions — both a
// privacy leak across the module boundary and a violation of the bounded-size
// property the seed contract rests on.
const SeedTitleLimit = 120

// SeedSource identifies one source session and its revision.
//
// Bytes and ModifiedAt are the revision: Midden has no content digest over
// source transcripts, so (size, mtime) is the honest freshness signal rather
// than a hash it does not compute.
type SeedSource struct {
	SessionID  string `json:"session_id"`
	Tool       string `json:"tool"`
	Title      string `json:"title,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
}

// Normalize satisfies Normalizer.
func (p *SeedProvenance) Normalize() { p.Sources = emptySlice(p.Sources) }

// Review states. HumanApproved is never set by a module: technical processing
// is not editorial acceptance.
const (
	ReviewUnreviewed = "unreviewed"
)

// SeedExcerptLimit bounds a single evidence excerpt.
const SeedExcerptLimit = 1200

// ---------------------------------------------------------------------------
// Writing a seed
// ---------------------------------------------------------------------------

// SeedInput is what a caller supplies to build a seed.
type SeedInput struct {
	Goal                 string
	Title                string
	Summary              string
	KeyPoints            []string
	SuggestedOutputTypes []string
	Brief                string
	Evidence             []SeedEvidence
	Sources              []SeedSource

	// Attach are absolute paths to files copied into attachments/. The caller
	// resolves and authorizes them; WriteSeed copies by basename so nothing
	// in the manifest can point outside the seed.
	Attach []string
}

// WriteSeed materializes a seed bundle under dir and returns its manifest.
//
// dir must be an absolute path the caller is authorized to write. This function
// does not resolve it: resolution is the caller's responsibility precisely
// because ambient resolution is what makes a write path unauditable.
//
// Every excerpt is redacted on the way in. Redaction is applied HERE rather
// than left to callers because a seed leaves Midden's boundary — the CLI
// handoff path does not redact, and that asymmetry is a known hazard.
func WriteSeed(dir string, in SeedInput) (*SeedManifest, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("seed directory must be absolute, got %q", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, SeedAttachmentsDir), 0o755); err != nil {
		return nil, fmt.Errorf("create seed directory: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// attachments/ — copied by BASENAME so a manifest entry is always a
	// relative path inside the seed, whatever the source location was.
	var attached []string
	for _, src := range in.Attach {
		raw, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("attachment %s could not be read: %w", filepath.Base(src), err)
		}
		name := filepath.Base(src)
		if name == "." || name == string(filepath.Separator) {
			return nil, fmt.Errorf("attachment %q has no filename", src)
		}
		if err := os.WriteFile(filepath.Join(dir, SeedAttachmentsDir, name), raw, 0o644); err != nil {
			return nil, fmt.Errorf("write attachment %s: %w", name, err)
		}
		attached = append(attached, SeedAttachmentsDir+"/"+name)
	}
	sort.Strings(attached)

	// evidence.jsonl — written first, because the manifest carries its digest.
	//
	// An EMPTY evidence set is valid and its digest is the sha256 of zero
	// bytes. A seed with a goal and no recovered material is a legitimate
	// request, not an error.
	var buf strings.Builder
	for i := range in.Evidence {
		e := in.Evidence[i]
		res := redact.Text(e.Excerpt)
		e.Excerpt = clipExcerpt(res.Text, SeedExcerptLimit)
		e.Redacted = res.Redacted
		if e.ID == "" {
			e.ID = fmt.Sprintf("ev-%04d", i+1)
		}
		line, err := json.Marshal(e)
		if err != nil {
			return nil, fmt.Errorf("encode evidence %d: %w", i, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	evidenceBytes := []byte(buf.String())
	if err := os.WriteFile(filepath.Join(dir, SeedEvidenceFile), evidenceBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write evidence: %w", err)
	}
	sum := sha256.Sum256(evidenceBytes)

	// brief.md — prose, deliberately unstructured. The host agent reads it;
	// no mechanical consumer parses it, so imposing headings would be
	// structure nobody needs and Midden would have to maintain.
	brief := in.Brief
	if strings.TrimSpace(brief) == "" {
		brief = defaultBrief(in)
	}
	if err := os.WriteFile(filepath.Join(dir, SeedBriefFile), []byte(redact.Text(brief).Text), 0o644); err != nil {
		return nil, fmt.Errorf("write brief: %w", err)
	}

	// provenance.json
	sources := make([]SeedSource, len(in.Sources))
	copy(sources, in.Sources)
	for i := range sources {
		// Redact then clip: a title is first-prompt text and may carry both
		// secrets and unbounded private prose.
		sources[i].Title = clipExcerpt(redact.Text(sources[i].Title).Text, SeedTitleLimit)
	}

	prov := SeedProvenance{
		Schema:      SeedSchemaID,
		Module:      ModuleID,
		ModuleVer:   Version,
		CreatedAt:   now,
		Sources:     sources,
		Redaction:   "secret-shape redaction applied to excerpts and brief; not a privacy filter for prose",
		ModelUsed:   false,
		ReviewState: ReviewUnreviewed,
	}
	prov.Normalize()
	provRaw, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode provenance: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SeedProvenanceFile), append(provRaw, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("write provenance: %w", err)
	}

	// manifest.json — the entry file, written last so a partially built seed
	// is never mistaken for a complete one.
	m := &SeedManifest{
		Schema:               SeedSchemaID,
		Goal:                 in.Goal,
		Title:                in.Title,
		Summary:              in.Summary,
		KeyPoints:            in.KeyPoints,
		SuggestedOutputTypes: in.SuggestedOutputTypes,
		Attachments:          attached,
		EvidenceDigest:       hex.EncodeToString(sum[:]),
		EvidenceCount:        len(in.Evidence),
		CreatedAt:            now,
	}
	m.Normalize()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SeedManifestFile), append(raw, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	return m, nil
}

// ReadSeed loads a seed from a path, which may be the bundle root OR the
// manifest file itself.
//
// It takes a path and nothing else: no index, no database, no environment. A
// staged copy at an arbitrary absolute location must load identically to the
// original, which is what makes the seed portable rather than portable-by-luck.
func ReadSeed(path string) (*SeedManifest, error) {
	manifestPath := path
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		manifestPath = filepath.Join(path, SeedManifestFile)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("seed manifest could not be read: %w", err)
	}
	var m SeedManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("seed manifest is not valid JSON: %w", err)
	}
	if m.Schema != SeedSchemaID {
		return nil, fmt.Errorf("unsupported seed schema %q, expected %s", m.Schema, SeedSchemaID)
	}
	return &m, nil
}

// VerifySeedEvidence recomputes the evidence digest and compares it to the
// manifest's claim.
//
// A seed whose evidence does not match its digest is REFUSED rather than
// consumed: a downstream artifact must never claim provenance from bytes that
// were not verified.
func VerifySeedEvidence(seedDir string) (*SeedManifest, error) {
	m, err := ReadSeed(seedDir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(seedDir, SeedEvidenceFile))
	if err != nil {
		return nil, fmt.Errorf("seed evidence could not be read: %w", err)
	}
	sum := sha256.Sum256(raw)
	actual := hex.EncodeToString(sum[:])

	// Accept a prefixed digest as well: the bare form is the manifest
	// convention, the prefixed form is the module-boundary convention, and a
	// consumer should not be broken by which one it was handed.
	expected := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(m.EvidenceDigest)), DigestPrefix)
	if expected != actual {
		return m, fmt.Errorf("seed evidence digest mismatch: manifest claims %s, computed %s", expected, actual)
	}
	return m, nil
}

func clipExcerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// defaultBrief writes readable prose from the structured fields, so a seed
// always carries something a human or an agent can read.
func defaultBrief(in SeedInput) string {
	var b strings.Builder
	title := in.Title
	if title == "" {
		title = "Recovered context"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	if in.Goal != "" {
		fmt.Fprintf(&b, "%s\n\n", in.Goal)
	}
	if in.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", in.Summary)
	}
	if len(in.KeyPoints) > 0 {
		for _, k := range in.KeyPoints {
			fmt.Fprintf(&b, "- %s\n", k)
		}
		b.WriteByte('\n')
	}
	if len(in.Sources) > 0 {
		sources := append([]SeedSource(nil), in.Sources...)
		sort.SliceStable(sources, func(i, j int) bool { return sources[i].SessionID < sources[j].SessionID })
		b.WriteString("Recovered from:\n")
		for _, s := range sources {
			fmt.Fprintf(&b, "- %s session %s\n", s.Tool, s.SessionID)
		}
	}
	return b.String()
}
