package module

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/create"
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

	// SessionID narrows evidence to ONE session.
	//
	// Everything upstream is session-addressed -- sessions.list takes ids,
	// sessions.assay takes an id, seed.create documents an exact session scope
	// -- and this step dropped to workspace only. An agent scoping to a single
	// session got a workspace filter instead, and reported it precisely: "it
	// is a workspace filter wearing a session filter's clothes".
	//
	// The isolation it observed held BY COINCIDENCE: the other sessions in
	// that workspace had no mined evidence yet. Mine one, re-run, and the
	// scope silently widens with no error and no warning. The store already
	// supported this -- NuggetQuery.SessionID existed and nothing offered it.
	SessionID string `json:"session_id,omitempty"`

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

	// Empty reports that the document was written and has no content. A kind
	// can be inapplicable to the evidence available — a preference pack needs
	// dead_end items to pair against — and a caller must be able to tell that
	// from a finished output without inspecting the file.
	Empty bool `json:"empty"`

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
	q := index.NuggetQuery{
		Workspace: strings.TrimSpace(req.Workspace),
		SessionID: strings.TrimSpace(req.SessionID),
	}
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
			if driver := getNativeDriver(); driver != nil {
				grant = ModelGrant{
					Backend:      exec.Backend("native"),
					Path:         "in-process",
					StageDir:     dir,
					NativeDriver: driver,
				}
			} else {
				return nil, nil, fmt.Errorf("%s", ErrSubprocessDenied)
			}
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

	// An output whose body is empty is a REAL result — a preference pack needs
	// dead_end evidence to pair against, and a corpus without any yields no
	// pairs. But reporting it as a plain success hands back a document with
	// nothing in it and no indication why, which is the same misleading-answer
	// shape as a filtered count stated alone.
	if strings.TrimSpace(body) == "" {
		warnings = append(warnings, fmt.Sprintf(
			"%s produced an EMPTY document: the %d evidence items in scope contain nothing this "+
				"kind can use. It is written and valid, but has no content — widen the scope or "+
				"extract more evidence rather than treating this as a finished output.",
			kind, len(nuggets)))
	}

	wrapped := refinery.WrapOutput(spec, recipe, body, modelName, len(nuggets))

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = refinery.Slug(kind)
	}
	if !validSeedName(name) {
		return nil, nil, fmt.Errorf("output name %q is not a single safe path segment", req.Name)
	}

	// Run-scoped, so a second output of the same kind does not replace the
	// first. The flat layout wrote content/<kind>.<ext> and silently destroyed
	// earlier work on the second use of a capability.
	run := create.NewRunID(time.Now())
	rel, err := create.OutputPath(run, name, refinery.FileExtension(spec))
	if err != nil {
		return nil, nil, err
	}
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(abs, []byte(wrapped), 0o644); err != nil {
		return nil, nil, fmt.Errorf("write output: %w", err)
	}

	// Record the artifact so every face can see what was produced.
	//
	// The CLI (brief, refine) and the standalone UI both write an artifact row;
	// the module face wrote the FILE and nothing else, so content produced
	// through facet-studio was invisible to `midden ui` -- the same output
	// reachable or not depending on which face made it. The nugget ids are the
	// provenance link back to the evidence the document was written from.
	//
	// A failure here is reported, not fatal: the document exists and the caller
	// already holds its path, so losing the work over a bookkeeping error would
	// be the wrong trade -- but a silent miss is how a library quietly stops
	// matching the disk.
	var warnArtifact []string
	nuggetIDs := make([]string, 0, len(nuggets))
	for _, n := range nuggets {
		nuggetIDs = append(nuggetIDs, n.UID)
	}
	if err := db.PutArtifact(index.Artifact{
		Kind:  kind,
		Title: title,
		Path:  abs,
		// Scope records WHERE this was produced. Every one of the first seven
		// artifacts recorded an empty scope because no caller passed a
		// workspace -- the field was right and nothing filled it, so "what did
		// I produce in this project" had no answer. The run id is recorded
		// unconditionally so origin survives a caller that says nothing.
		Scope:     scopeOrRun(req.Workspace, run),
		NuggetIDs: nuggetIDs,
		Model:     modelName,
		CreatedAt: time.Now(),
	}); err != nil {
		warnArtifact = append(warnArtifact, "the document was written but not recorded in the library: "+
			err.Error()+". `midden ui` will not list it.")
	}
	warnings = append(warnings, warnArtifact...)

	return &ContentProduceResult{
		Kind:          kind,
		Title:         title,
		Format:        spec.Format,
		MediaType:     mediaTypeFor(spec.Format),
		Root:          RootMiddenHome,
		Path:          rel,
		Bytes:         int64(len(wrapped)),
		EvidenceCount: len(nuggets),
		Empty:         strings.TrimSpace(body) == "",
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
	Backend      exec.Backend
	Path         string
	Timeout      time.Duration
	StageDir     string
	NativeDriver func(ctx context.Context, prompt string) (string, error)
}

var (
	nativeDriverMu     sync.RWMutex
	globalNativeDriver func(ctx context.Context, prompt string) (string, error)
)

// SetNativeDriver configures an in-process prompt executor for model-backed operations.
func SetNativeDriver(driver func(ctx context.Context, prompt string) (string, error)) {
	nativeDriverMu.Lock()
	defer nativeDriverMu.Unlock()
	globalNativeDriver = driver
}

func getNativeDriver() func(ctx context.Context, prompt string) (string, error) {
	nativeDriverMu.RLock()
	defer nativeDriverMu.RUnlock()
	return globalNativeDriver
}

// modelGrantFrom reads the subprocess grant out of a request.
//
// It honours the host's grant list rather than the module's declaration: a
// module declares what it MAY need, and only what the host actually authorized
// for this invocation may be run.
func modelGrantFrom(req Request) ModelGrant {
	if driver := getNativeDriver(); driver != nil {
		return ModelGrant{
			Backend:      exec.Backend("native"),
			Path:         "in-process",
			Timeout:      deadlineOf(req),
			StageDir:     writableRootOf(req),
			NativeDriver: driver,
		}
	}

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

// envelopeHeadroom is how long Midden reserves to write a structured envelope
// before the host kills the process tree. A timeout the module REPORTS is far
// more useful than one a host infers from a dead process.
const envelopeHeadroom = 3 * time.Second

func deadlineOf(req Request) time.Duration {
	if req.DeadlineMS > 0 {
		// Leave headroom so Midden returns a structured envelope before the
		// host kills the process tree: a timeout the module reports is far
		// more useful than one the host infers from a dead process.
		d := time.Duration(req.DeadlineMS) * time.Millisecond
		// The guard is DERIVED from the margin rather than being a second
		// independent literal. They were 5s and 3s: correct together, and a
		// margin raised past the guard would have returned a NEGATIVE deadline
		// silently, because both halves stayed internally consistent while
		// disagreeing. Deriving it means the two cannot drift apart.
		if d > envelopeHeadroom+time.Second {
			return d - envelopeHeadroom
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
		Backend:      grant.Backend,
		BinaryPath:   grant.Path,
		StageDir:     grant.StageDir,
		Timeout:      grant.Timeout,
		Pure:         true,
		NativeDriver: grant.NativeDriver,
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

// scopeOrRun records where an artifact came from.
//
// A caller-supplied workspace is the better answer and is preferred. When a
// caller says nothing the run id is recorded instead, because an artifact with
// no origin at all cannot be attributed later and the question "what did I make
// here" becomes unanswerable from the store.
//
// It deliberately does NOT invent a session identity. No driver passes one --
// the module protocol carries request_id, which is per-call -- and claiming to
// know which session produced something would be a well-formed wrong answer.
func scopeOrRun(workspace string, run create.RunID) string {
	if w := strings.TrimSpace(workspace); w != "" {
		return w
	}
	return "run:" + string(run)
}
