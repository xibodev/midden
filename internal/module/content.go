package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

// Content production capabilities.
//
// These close the gap between "mine a session" and "create content from it".
// sessions.assay measures, seed.create packages — neither produces a document
// a person would read.
const (
	CapContentTypes   = "content.types"
	CapContentProduce = "content.produce"

	SchemaContentTypesResult    = "xibodev.midden.content.types.result/v1"
	SchemaContentProduceRequest = "xibodev.midden.content.produce.request/v1"
	SchemaContentProduceResult  = "xibodev.midden.content.produce.result/v1"

	// ArtifactContentOutput is the produced document.
	ArtifactContentOutput = "xibodev.midden.content.output/v1"
)

// ErrNoEvidence is returned when production is asked for with nothing to
// ground it.
//
// Midden will not write a document from nothing: an output invented rather
// than derived would carry the same provenance fields as a real one and be
// indistinguishable from it. Refusing is the honest answer.
const ErrNoEvidence = "no_evidence"

// ContentType describes one producible output.
type ContentType struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Audience string `json:"audience"`
	Format   string `json:"format"`

	// MediaType is what the host should render it as. Every output is source
	// text; Midden renders nothing.
	MediaType string `json:"media_type"`

	// RequiresModel reports whether producing this costs a model call through
	// an already-authenticated CLI. Free outputs are derived deterministically
	// from stored evidence.
	RequiresModel bool `json:"requires_model"`

	// Maker names the tool a person would run NEXT on this source — Marp for a
	// deck, D2 for a diagram. It is a handoff target, not a dependency Midden
	// invokes.
	Maker string `json:"maker,omitempty"`
}

// ContentTypesResult lists what can be produced.
type ContentTypesResult struct {
	Types []ContentType `json:"types"`

	// EvidenceCount is how much stored evidence exists to draw on. Zero means
	// nothing can be produced yet regardless of which type is asked for, which
	// is worth knowing before choosing one.
	EvidenceCount int `json:"evidence_count"`

	// FreeKinds is the subset producible with no model call at all.
	FreeKinds []string `json:"free_kinds"`
}

// Normalize satisfies Normalizer.
func (r *ContentTypesResult) Normalize() {
	r.Types = emptySlice(r.Types)
	r.FreeKinds = emptySlice(r.FreeKinds)
}

// ContentProduceRequest asks for one output.
type ContentProduceRequest struct {
	// Kind is the output type, from content.types.
	Kind string `json:"kind"`

	// Title overrides the generated title.
	Title string `json:"title,omitempty"`

	// Workspace narrows evidence to one project.
	Workspace string `json:"workspace,omitempty"`

	// Tags and Kinds narrow which stored evidence is used.
	EvidenceKinds []string `json:"evidence_kinds,omitempty"`

	// MaxEvidence bounds how much evidence feeds the output.
	MaxEvidence int `json:"max_evidence,omitempty"`

	// Name is the output filename stem, a single safe path segment.
	Name string `json:"name,omitempty"`
}

// Bounds on production.
const (
	DefaultProduceEvidence = 40
	MaxProduceEvidence     = 300
)

// ContentProduceResult reports the produced document.
type ContentProduceResult struct {
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Format        string `json:"format"`
	MediaType     string `json:"media_type"`
	Root          string `json:"root"`
	Path          string `json:"path"`
	Bytes         int64  `json:"bytes"`
	EvidenceCount int    `json:"evidence_count"`

	// ModelUsed is false for every deterministic output. It is a fact about
	// this production, not a promise about the capability.
	ModelUsed bool `json:"model_used"`

	// Review is always "draft". Midden does not mark its own output accepted;
	// producing a document is not approving it.
	Review string `json:"review"`
}

// Normalize satisfies Normalizer.
func (r *ContentProduceResult) Normalize() {}

// mediaTypeFor maps a refinery format to what the host should render.
//
// Every Midden output is SOURCE TEXT. A .d2 file is a diagram's source, not a
// diagram; a marp deck is markdown with front matter. Declaring these as text
// shapes rather than as rendered media is what keeps the host from needing a
// renderer per format.
func mediaTypeFor(format string) string {
	switch format {
	case "markdown", "marp":
		return "text/markdown"
	case "d2":
		return "text/plain"
	case "diff":
		return "text/x-diff"
	case "json":
		return "application/json"
	case "jsonl":
		return "application/x-ndjson"
	case "tsv":
		return "text/tab-separated-values"
	}
	return "text/plain"
}

// ContentTypes lists producible outputs and how much evidence exists.
func ContentTypes(db *index.DB) (*ContentTypesResult, []string, error) {
	res := &ContentTypesResult{}
	for _, t := range refinery.Templates() {
		res.Types = append(res.Types, ContentType{
			Kind:          t.Kind,
			Title:         t.Title,
			Audience:      t.Audience,
			Format:        t.Format,
			MediaType:     mediaTypeFor(t.Format),
			RequiresModel: t.RequiresModel,
			Maker:         t.Maker,
		})
		if !t.RequiresModel {
			res.FreeKinds = append(res.FreeKinds, t.Kind)
		}
	}
	sort.SliceStable(res.Types, func(i, j int) bool { return res.Types[i].Kind < res.Types[j].Kind })
	sort.Strings(res.FreeKinds)

	var warnings []string
	if db != nil {
		nuggets, err := db.Nuggets(index.NuggetQuery{})
		if err != nil {
			warnings = append(warnings, "stored evidence could not be read: "+err.Error())
		} else {
			res.EvidenceCount = len(nuggets)
		}
	}
	if res.EvidenceCount == 0 {
		warnings = append(warnings,
			"no stored evidence yet: run evidence extraction before producing content, or every output would be invented rather than derived")
	}
	return res, warnings, nil
}

// ContentProduce writes one output from stored evidence.
//
// It produces only DETERMINISTIC outputs. Model-backed kinds are refused with a
// clear reason rather than silently producing something weaker, because a
// tutorial that looks produced but was assembled without a model would be
// indistinguishable from one that was.
func ContentProduce(db *index.DB, req ContentProduceRequest, dir string) (*ContentProduceResult, []string, error) {
	kind := strings.TrimSpace(strings.ToLower(req.Kind))
	if kind == "" {
		return nil, nil, fmt.Errorf("kind is required; call content.types for the list")
	}
	spec, ok := refinery.FindTemplate(kind)
	if !ok {
		return nil, nil, fmt.Errorf("unknown content kind %q; call content.types for the list", req.Kind)
	}
	if spec.RequiresModel {
		return nil, nil, fmt.Errorf(
			"%q requires a model and is not yet exposed as a module capability; "+
				"deterministic kinds available now: %s",
			kind, strings.Join(freeKinds(), ", "))
	}
	if db == nil {
		return nil, nil, fmt.Errorf("the evidence index is unavailable")
	}

	limit := clamp(req.MaxEvidence, DefaultProduceEvidence, MaxProduceEvidence)
	q := index.NuggetQuery{Workspace: strings.TrimSpace(req.Workspace)}
	nuggets, err := db.Nuggets(q)
	if err != nil {
		return nil, nil, fmt.Errorf("stored evidence could not be read: %w", err)
	}

	if len(req.EvidenceKinds) > 0 {
		want := map[string]bool{}
		for _, k := range req.EvidenceKinds {
			want[strings.ToLower(strings.TrimSpace(k))] = true
		}
		filtered := nuggets[:0:0]
		for _, n := range nuggets {
			if want[strings.ToLower(n.Kind)] {
				filtered = append(filtered, n)
			}
		}
		nuggets = filtered
	}

	var warnings []string
	if len(nuggets) == 0 {
		return nil, nil, fmt.Errorf("%s", ErrNoEvidence)
	}
	if len(nuggets) > limit {
		warnings = append(warnings, fmt.Sprintf(
			"%d evidence items matched; the output uses the first %d", len(nuggets), limit))
		nuggets = nuggets[:limit]
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = spec.Title
	}

	recipe := index.Recipe{
		UID:       "module-" + refinery.Slug(kind),
		Title:     title,
		Workspace: strings.TrimSpace(req.Workspace),
		CreatedAt: time.Now().UTC(),
	}

	body, produced, err := refinery.DeterministicOutput(spec, recipe, nuggets)
	if err != nil {
		return nil, nil, fmt.Errorf("produce %s: %w", kind, err)
	}
	if !produced {
		return nil, nil, fmt.Errorf("%q declares no model requirement but produced nothing deterministically", kind)
	}

	wrapped := refinery.WrapOutput(spec, recipe, body, "", len(nuggets))

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = refinery.Slug(kind)
	}
	if !validSeedName(name) {
		return nil, nil, fmt.Errorf("output name %q is not a single safe path segment", req.Name)
	}

	outDir := filepath.Join(dir, "content")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create output directory: %w", err)
	}
	rel := filepath.ToSlash(filepath.Join("content", name+refinery.FileExtension(spec)))
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, []byte(wrapped), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write output: %w", err)
	}

	return &ContentProduceResult{
		Kind:          kind,
		Title:         title,
		Format:        spec.Format,
		MediaType:     mediaTypeFor(spec.Format),
		Root:          RootMiddenHome,
		Path:          rel,
		Bytes:         int64(len(wrapped)),
		EvidenceCount: len(nuggets),
		ModelUsed:     false,
		Review:        "draft",
	}, warnings, nil
}

func freeKinds() []string {
	var out []string
	for _, t := range refinery.Templates() {
		if !t.RequiresModel {
			out = append(out, t.Kind)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

func invokeContentTypes(req Request) Envelope {
	db, closeDB, err := openIndex(req)
	if err != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrMissingRoot,
			Message:   err.Error(),
			Retryable: true,
			Details:   map[string]any{"required_root": RootMiddenHome, "required_mode": "rw"},
		}, LocalFree())
	}
	defer closeDB()

	result, warnings, err := ContentTypes(db)
	if err != nil {
		return invalidRequest(req, err)
	}
	return successEnvelope(req, result, warnings)
}

func invokeContentProduce(req Request) Envelope {
	var in ContentProduceRequest
	if err := decodeInput(req.Input, &in); err != nil {
		return invalidRequest(req, err)
	}

	root, ok := req.Roots[RootMiddenHome]
	if !ok || strings.TrimSpace(root.Path) == "" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrMissingRoot,
			Message:   fmt.Sprintf("content.produce requires the %q root; none was supplied", RootMiddenHome),
			Retryable: true,
			Details:   map[string]any{"required_root": RootMiddenHome, "required_mode": "rw"},
		}, LocalFree())
	}
	if root.Mode != "rw" {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code:      ErrPermissionDenied,
			Message:   fmt.Sprintf("content.produce writes, but %q was supplied as %q", RootMiddenHome, root.Mode),
			Retryable: false,
		}, LocalFree())
	}

	db, closeDB, err := openIndex(req)
	if err != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code: ErrMissingRoot, Message: err.Error(), Retryable: true,
		}, LocalFree())
	}
	defer closeDB()

	result, warnings, err := ContentProduce(db, in, root.Path)
	if err != nil {
		if err.Error() == ErrNoEvidence {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
				Code: ErrNoEvidence,
				Message: "no stored evidence matches that scope. Midden will not write a document " +
					"from nothing: an invented output would carry the same provenance fields as a derived one.",
				Retryable: true,
				Details:   map[string]any{"remedy": "extract evidence first, or widen the scope"},
			}, LocalFree())
		}
		return invalidRequest(req, err)
	}

	env, buildErr := NewResultEnvelope(OpInvoke, req.RequestID, result, contentExecution(root.Path, result))
	if buildErr != nil {
		return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
			Code: ErrInternal, Message: buildErr.Error(),
		}, LocalFree())
	}
	env.Warnings = emptySlice(warnings)
	return env
}

// contentExecution reports the produced document as an artifact pointer.
//
// ExternalWrites stays false: the document is written inside the granted root.
// Costs are explicit zeroes because deterministic production is genuinely free,
// not of unknown price.
func contentExecution(rootPath string, r *ContentProduceResult) Execution {
	zero := 0.0
	exec := Execution{
		Local:         true,
		EstimatedCost: &zero,
		ActualCost:    &zero,
		Artifacts:     []Artifact{},
	}
	raw, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(r.Path)))
	if err != nil {
		return exec
	}
	exec.Artifacts = append(exec.Artifacts, Artifact{
		ID:        "content-" + r.Kind,
		Kind:      ArtifactContentOutput,
		Path:      r.Path,
		Root:      RootMiddenHome,
		MediaType: r.MediaType,
		Bytes:     int64(len(raw)),
		Digest:    DigestSHA256(raw),
		Title:     r.Title,
	})
	return exec
}

// openIndex opens Midden's own index under the host-supplied write root.
//
// It never falls back to the environment or the user profile: under the host's
// empty environment that would resolve to a relative path and read an index
// that is not the user's.
func openIndex(req Request) (*index.DB, func(), error) {
	root, ok := req.Roots[RootMiddenHome]
	if !ok || strings.TrimSpace(root.Path) == "" {
		return nil, func() {}, fmt.Errorf(
			"this capability reads Midden's evidence index and requires the %q root; none was supplied", RootMiddenHome)
	}
	if !filepath.IsAbs(root.Path) {
		return nil, func() {}, fmt.Errorf("root paths must be absolute and canonicalized by the host")
	}
	db, err := index.OpenAt(root.Path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("evidence index could not be opened: %w", err)
	}
	return db, func() { db.Close() }, nil
}

var _ = json.Marshal
