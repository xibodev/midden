package module

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/exec"
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

// ErrSubprocessDenied is returned when a model-backed output is asked for
// without the subprocess authority that producing it requires.
//
// It is distinct from missing_root: the caller supplied everything Midden
// needs to read, and the host simply did not grant the right to run an AI CLI.
// Reporting that as a generic failure would leave a host unable to tell a
// setup problem from a policy decision.
const ErrSubprocessDenied = "subprocess_denied"

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

	// ModelUsed is a fact about THIS production, not a promise about the
	// capability: the same capability produces free packs and model-backed
	// documents depending on the kind asked for.
	ModelUsed bool `json:"model_used"`

	// ModelBackend names the AI CLI that was invoked, empty when none was.
	ModelBackend string `json:"model_backend,omitempty"`

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
// ContentProduce writes one output from stored evidence.
//
// Deterministic kinds are assembled locally and cost nothing. Model-backed
// kinds shell out to an already-authenticated AI CLI, which requires the host
// to have granted subprocess authority and supplied that binary's absolute
// path. Midden holds no API key and never calls a provider directly.
func ContentProduce(db *index.DB, req ContentProduceRequest, dir string, grant ModelGrant) (*ContentProduceResult, []string, error) {
	kind := strings.TrimSpace(strings.ToLower(req.Kind))
	if kind == "" {
		return nil, nil, fmt.Errorf("kind is required; call content.types for the list")
	}
	spec, ok := refinery.FindTemplate(kind)
	if !ok {
		return nil, nil, fmt.Errorf("unknown content kind %q; call content.types for the list", req.Kind)
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

	var (
		body      string
		modelUsed bool
		modelName string
	)

	if spec.RequiresModel {
		if grant.Backend == "" {
			return nil, nil, fmt.Errorf("%s", ErrSubprocessDenied)
		}
		// Ledger entry opened before the call, so a failed or killed
		// invocation still leaves a record that it happened.
		run := modelRun("content.produce",
			scopeLabel(kind, req.Workspace, fmt.Sprintf("%d evidence", len(nuggets))),
			string(grant.Backend), 0)

		out, sid, err := runModelOutput(spec, recipe, nuggets, grant)
		if sid != "" {
			run.CLISessions = []string{sid}
		}
		if err != nil {
			// Record the failure too: a model that was invoked and errored may
			// still have billed, and a ledger that only shows successes hides
			// exactly the runs a user is trying to account for.
			_ = recordRun(db, run, 0, false, "model call failed: "+err.Error())
			return nil, nil, err
		}
		body, modelUsed, modelName = out, true, string(grant.Backend)

		// Roughly four bytes per token, matching how the rest of Midden
		// estimates. Crude and stated as an estimate rather than a charge.
		run.EstTokens = len(body) / 4
		warnings = append(warnings, recordRun(db, run, 1, true,
			fmt.Sprintf("produced %s from %d evidence items", kind, len(nuggets)))...)
	} else {
		var produced bool
		var err error
		body, produced, err = refinery.DeterministicOutput(spec, recipe, nuggets)
		if err != nil {
			return nil, nil, fmt.Errorf("produce %s: %w", kind, err)
		}
		if !produced {
			return nil, nil, fmt.Errorf("%q declares no model requirement but produced nothing deterministically", kind)
		}
	}

	wrapped := refinery.WrapOutput(spec, recipe, body, modelName, len(nuggets))

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
		ModelUsed:     modelUsed,
		ModelBackend:  modelName,
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

	result, warnings, err := ContentProduce(db, in, root.Path, modelGrantFrom(req))
	if err != nil {
		if err.Error() == ErrSubprocessDenied {
			return NewErrorEnvelope(OpInvoke, req.RequestID, Error{
				Code: ErrSubprocessDenied,
				Message: "this output needs a model, and no AI CLI was granted for this invocation. " +
					"Midden holds no API key and never calls a provider directly: it shells out to a CLI " +
					"the user is already signed in to, so the host must grant subprocess authority and " +
					"supply that binary's absolute path.",
				Retryable: true,
				Details: map[string]any{
					"required_subprocess": []string{"copilot", "claude", "opencode"},
					"free_kinds":          freeKinds(),
					"note":                "the free kinds above need no grant and produce real documents now",
				},
			}, UnknownCost())
		}
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
//
// Cost depends on HOW this document was produced, not on the capability. A
// deterministic pack is genuinely free and says so with an explicit zero. A
// model-backed document spent real money through the user's own AI CLI
// subscription, and Midden never sees a bill — so its cost is UNKNOWN and both
// pointers stay nil. Reporting zero there would render a paid call as free in
// a cockpit and slip it past cost approval, which is the exact failure the
// pointer types exist to prevent.
func contentExecution(rootPath string, r *ContentProduceResult) Execution {
	exec := Execution{
		Local:     !r.ModelUsed,
		Provider:  r.ModelBackend,
		Artifacts: []Artifact{},
	}
	if !r.ModelUsed {
		zero := 0.0
		exec.EstimatedCost = &zero
		exec.ActualCost = &zero
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

// ModelGrant is the authority to invoke one AI CLI, as granted by the host.
//
// Midden never resolves this itself. The host declares which binaries a module
// may run, resolves each to an absolute path, and supplies it per invocation —
// so a module cannot be tricked into executing a different `claude` that
// happens to appear earlier on a search path. A zero value means no authority
// was granted, and a model-backed output must be refused rather than attempted.
type ModelGrant struct {
	Backend exec.Backend
	Path    string
	Timeout time.Duration

	// StageDir is a writable directory for staging an oversized prompt. It is
	// the host-granted root: under an empty environment the OS temp directory
	// resolves to a path the process cannot write.
	StageDir string
}

// modelGrantFrom reads the subprocess grant out of a request.
//
// It honours the host's grant list rather than the module's declaration: a
// module declares what it MAY need, and only what the host actually authorized
// for this invocation may be run.
func modelGrantFrom(req Request) ModelGrant {
	granted := map[string]bool{}
	for _, g := range req.Grants.Subprocess {
		granted[strings.ToLower(strings.TrimSpace(g))] = true
	}

	// Preference order matches the human CLI's, so a module run and a terminal
	// run choose the same backend when both are available.
	for _, b := range []exec.Backend{exec.Copilot, exec.Claude, exec.Opencode} {
		name := string(b)
		if !granted[name] {
			continue
		}
		path := strings.TrimSpace(req.Binaries[name])
		if path == "" {
			// Granted but unresolved. The host reports an unresolvable binary
			// as ABSENT rather than empty, so this means the CLI is not
			// installed — a different problem from not being permitted.
			continue
		}
		return ModelGrant{Backend: b, Path: path, Timeout: deadlineOf(req), StageDir: writableRootOf(req)}
	}
	return ModelGrant{}
}

// writableRootOf returns a directory the host granted for writing, used to
// stage prompts that are too large to pass as an argument.
func writableRootOf(req Request) string {
	if r, ok := req.Roots[RootMiddenHome]; ok && r.Mode == "rw" {
		return strings.TrimSpace(r.Path)
	}
	return ""
}

func deadlineOf(req Request) time.Duration {
	if req.DeadlineMS > 0 {
		// Leave headroom so Midden returns a structured envelope before the
		// host kills the process tree: a timeout the module reports is far
		// more useful than one the host infers from a dead process.
		d := time.Duration(req.DeadlineMS) * time.Millisecond
		if d > 5*time.Second {
			return d - 3*time.Second
		}
		return d
	}
	return 5 * time.Minute
}

// runModelOutput produces one narrative output through an authenticated CLI.
//
// The prompt carries the evidence and the output's own requirements, both from
// the refinery that the human `midden refine` path already uses — so a module
// run and a terminal run produce the same shape from the same evidence.
func runModelOutput(spec index.RecipeOutputSpec, recipe index.Recipe, nuggets []index.Nugget, grant ModelGrant) (string, string, error) {
	prompt := refinery.EvidencePreamble(recipe, nuggets) + "\n" + refinery.OutputRequest(spec)

	runner := &exec.Runner{
		Backend:    grant.Backend,
		BinaryPath: grant.Path,
		StageDir:   grant.StageDir,
		Timeout:    grant.Timeout,
		Pure:       true,
	}
	// A conversation assigns the CLI session id up front, which is what
	// reconciliation needs to resolve real usage afterwards.
	conv := runner.NewConversation()

	ctx, cancel := context.WithTimeout(context.Background(), grant.Timeout)
	defer cancel()

	res, err := conv.Prime(ctx, prompt)
	if err != nil {
		return "", conv.SessionID(), fmt.Errorf("%s could not produce %s: %w", grant.Backend, spec.Kind, err)
	}
	out := strings.TrimSpace(exec.CleanOutput(res.Output))
	if out == "" {
		return "", conv.SessionID(), fmt.Errorf("%s returned no content for %s", grant.Backend, spec.Kind)
	}
	return out, conv.SessionID(), nil
}
