// Package refinery turns mined evidence into reviewable multi-output
// productions. It is deterministic until a production explicitly invokes a
// model, and every output retains the exact evidence ids behind it.
package refinery

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
)

const (
	RecipeDraft          = "draft"
	RecipeEvidenceReview = "evidence_review"
	RecipeApproved       = "approved"
	RecipeRunning        = "running"
	RecipeReview         = "review"
	RecipeComplete       = "complete"
	RecipeFailed         = "failed"
	RecipeArchived       = "archived"

	OutputDraft    = "draft"
	OutputReviewed = "reviewed"
	OutputRejected = "rejected"
	OutputExported = "exported"
)

var outputTemplates = []index.RecipeOutputSpec{
	{Kind: "tutorial", Title: "Deep technical tutorial", Audience: "practitioners learning the complete workflow", Maker: "Midden + Pandoc", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "adr", Title: "Architecture decision record", Audience: "engineers maintaining the project later", Maker: "Midden", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "release_pack", Title: "Release and launch pack", Audience: "users, maintainers, and launch channels", Maker: "Midden", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "slides", Title: "Presentation deck", Audience: "an engineering review or workshop", Maker: "Marp", Format: "marp", CostClass: "spends", RequiresModel: true},
	{Kind: "diagram", Title: "Architecture diagram", Audience: "technical readers who need the system shape quickly", Maker: "D2", Format: "d2", CostClass: "spends", RequiresModel: true},
	{Kind: "video_brief", Title: "Video production brief", Audience: "a producer creating a concise product demonstration", Maker: "Midden / OpenMontage handoff", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "handbook", Title: "Project field guide", Audience: "the owner returning to the project later", Maker: "Midden + Quarto/Pandoc", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "flashcards", Title: "Spaced-repetition deck", Audience: "the owner retaining commands, concepts, and gotchas", Maker: "Midden / Anki export", Format: "tsv", CostClass: "spends", RequiresModel: true},
	{Kind: "quiz", Title: "Scenario quiz", Audience: "a learner testing applied understanding", Maker: "Midden / H5P handoff", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "notebook_pack", Title: "Research notebook source pack", Audience: "a local research or retrieval workspace", Maker: "Midden / Open Notebook", Format: "markdown", CostClass: "free", RequiresModel: false},
	{Kind: "skill", Title: "Agent skill proposal", Audience: "an operator reviewing a reusable agent behavior", Maker: "Agent Skills", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "instruction_patch", Title: "Instruction-file patch proposal", Audience: "an operator reviewing a scoped behavior rule", Maker: "Midden", Format: "diff", CostClass: "spends", RequiresModel: true},
	{Kind: "agent_profile", Title: "Specialist agent proposal", Audience: "an operator reviewing a bounded specialist", Maker: "Midden", Format: "markdown", CostClass: "spends", RequiresModel: true},
	{Kind: "eval_pack", Title: "Evidence-derived evaluation pack", Audience: "an operator comparing current and proposed behavior", Maker: "Midden / Promptfoo", Format: "jsonl", CostClass: "free", RequiresModel: false},
	{Kind: "retrieval_pack", Title: "Retrieval memory pack", Audience: "a local RAG or durable-memory system", Maker: "Midden", Format: "jsonl", CostClass: "free", RequiresModel: false},
	{Kind: "sft_pack", Title: "Supervised examples pack", Audience: "an expert reviewing possible training examples", Maker: "Midden", Format: "jsonl", CostClass: "free", RequiresModel: false},
	{Kind: "preference_pack", Title: "Preference-pair pack", Audience: "an expert reviewing accepted versus rejected approaches", Maker: "Midden", Format: "jsonl", CostClass: "free", RequiresModel: false},
	{Kind: "privacy_manifest", Title: "Privacy and licensing manifest", Audience: "the owner auditing a data export", Maker: "Midden", Format: "json", CostClass: "free", RequiresModel: false},
	{Kind: "provenance_manifest", Title: "Bundle provenance manifest", Audience: "the owner auditing every derived claim", Maker: "Midden", Format: "json", CostClass: "free", RequiresModel: false},
}

// Templates returns a copy of the supported refinery deliverables.
func Templates() []index.RecipeOutputSpec {
	return append([]index.RecipeOutputSpec(nil), outputTemplates...)
}

// FindTemplate resolves a deliverable by its stable kind.
func FindTemplate(kind string) (index.RecipeOutputSpec, bool) {
	for _, template := range outputTemplates {
		if template.Kind == strings.ToLower(strings.TrimSpace(kind)) {
			return template, true
		}
	}
	return index.RecipeOutputSpec{}, false
}

// DefaultBundle is the product's recommended first multi-output production.
func DefaultBundle() []string {
	return []string{"tutorial", "slides", "diagram", "video_brief", "skill", "provenance_manifest"}
}

// WorkspaceSummary is a source scope that can feed a production.
type WorkspaceSummary struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Repo     string    `json:"repo,omitempty"`
	Sessions int       `json:"sessions"`
	Nuggets  int       `json:"nuggets"`
	Bytes    int64     `json:"bytes"`
	Updated  time.Time `json:"updated,omitempty"`
}

// Workspaces combines indexed sessions and reclaimed evidence into selectable
// production scopes.
func Workspaces(sessions []core.Session, nuggets []index.Nugget) []WorkspaceSummary {
	byID := map[string]*WorkspaceSummary{}
	get := func(id string) *WorkspaceSummary {
		id = strings.TrimSpace(id)
		if id == "" {
			id = "all evidence"
		}
		if existing := byID[id]; existing != nil {
			return existing
		}
		name := filepath.Base(id)
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = id
		}
		item := &WorkspaceSummary{ID: id, Name: name}
		byID[id] = item
		return item
	}
	for _, session := range sessions {
		if session.Noise {
			continue
		}
		item := get(session.Dir)
		item.Sessions++
		item.Bytes += session.Bytes
		if item.Repo == "" {
			item.Repo = session.Repo
		}
		if session.Updated.After(item.Updated) {
			item.Updated = session.Updated
		}
	}
	for _, nugget := range nuggets {
		id := nugget.Workspace
		if id == "" {
			id = nugget.Repo
		}
		item := get(id)
		item.Nuggets++
		if item.Repo == "" {
			item.Repo = nugget.Repo
		}
		if nugget.CreatedAt.After(item.Updated) {
			item.Updated = nugget.CreatedAt
		}
	}
	out := make([]WorkspaceSummary, 0, len(byID))
	for _, item := range byID {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Nuggets != out[j].Nuggets {
			return out[i].Nuggets > out[j].Nuggets
		}
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out
}

// Design creates a transparent recipe from a natural-language request.
func Design(prompt, workspace string, requested []string, nuggets []index.Nugget, now time.Time) (index.Recipe, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if len(nuggets) == 0 {
		return index.Recipe{}, fmt.Errorf("no reclaimed evidence matches this scope")
	}
	kinds := requested
	if len(kinds) == 0 {
		kinds = inferKinds(prompt)
	}
	if len(kinds) == 0 {
		kinds = DefaultBundle()
	}
	seen := map[string]bool{}
	var outputs []index.RecipeOutputSpec
	for _, kind := range kinds {
		template, ok := FindTemplate(kind)
		if !ok {
			return index.Recipe{}, fmt.Errorf("unknown refinery output %q", kind)
		}
		if seen[template.Kind] {
			continue
		}
		seen[template.Kind] = true
		outputs = append(outputs, template)
	}
	if len(outputs) == 0 {
		return index.Recipe{}, fmt.Errorf("choose at least one output")
	}
	title := recipeTitle(workspace, outputs)
	return index.Recipe{
		Title:       title,
		Workspace:   workspace,
		Request:     strings.TrimSpace(prompt),
		Status:      RecipeDraft,
		Outputs:     outputs,
		EvidenceIDs: DefaultEvidence(nuggets, workspace, outputs, 120),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func recipeTitle(workspace string, outputs []index.RecipeOutputSpec) string {
	scope := strings.TrimSpace(workspace)
	if scope == "" {
		scope = "Evidence"
	} else {
		scope = filepath.Base(scope)
		if scope == "." || scope == string(filepath.Separator) || scope == "" {
			scope = "Evidence"
		}
	}
	names := map[string]string{
		"tutorial":            "tutorial",
		"adr":                 "decision record",
		"release_pack":        "release pack",
		"slides":              "deck",
		"diagram":             "diagram",
		"video_brief":         "video brief",
		"handbook":            "field guide",
		"flashcards":          "learning deck",
		"quiz":                "quiz",
		"notebook_pack":       "notebook pack",
		"skill":               "skill proposal",
		"instruction_patch":   "instruction proposal",
		"agent_profile":       "agent proposal",
		"eval_pack":           "evaluation pack",
		"retrieval_pack":      "retrieval pack",
		"sft_pack":            "SFT pack",
		"preference_pack":     "preference pack",
		"privacy_manifest":    "privacy manifest",
		"provenance_manifest": "provenance manifest",
	}
	var parts []string
	for _, output := range outputs {
		name := names[output.Kind]
		if name == "" || containsString(parts, name) {
			continue
		}
		parts = append(parts, name)
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return scope + " production"
	}
	title := scope + " " + strings.Join(parts, " and ")
	if len(outputs) > len(parts) {
		title += fmt.Sprintf(" + %d more", len(outputs)-len(parts))
	}
	return title
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func inferKinds(prompt string) []string {
	prompt = strings.ToLower(prompt)
	type rule struct {
		kind  string
		terms []string
	}
	rules := []rule{
		{"tutorial", []string{"tutorial", "how-to", "how to", "article", "blog"}},
		{"adr", []string{"adr", "architecture decision", "decision record"}},
		{"release_pack", []string{"release", "launch", "newsletter", "social campaign"}},
		{"slides", []string{"slide", "deck", "presentation", "workshop"}},
		{"diagram", []string{"diagram", "architecture visual", "infographic", "visual"}},
		{"video_brief", []string{"video", "storyboard", "demo brief", "demo script"}},
		{"handbook", []string{"handbook", "field guide", "book", "ebook", "course notes"}},
		{"flashcards", []string{"flashcard", "anki", "spaced repetition"}},
		{"quiz", []string{"quiz", "assessment", "learning path"}},
		{"notebook_pack", []string{"notebook", "research pack", "knowledge base", "rag"}},
		{"skill", []string{"skill", "skill.md", "reusable agent behavior"}},
		{"instruction_patch", []string{"agents.md", "claude.md", "instruction", "rule patch"}},
		{"agent_profile", []string{"subagent", "specialist agent", "agent profile"}},
		{"eval_pack", []string{"eval", "regression case", "promptfoo"}},
		{"retrieval_pack", []string{"retrieval", "memory pack", "rag pack"}},
		{"sft_pack", []string{"sft", "supervised", "fine-tune", "finetune"}},
		{"preference_pack", []string{"preference", "dpo", "accepted versus rejected"}},
		{"privacy_manifest", []string{"privacy manifest", "licensing manifest", "privacy review"}},
		{"provenance_manifest", []string{"provenance manifest", "source manifest", "audit manifest", "provenance"}},
	}
	type matched struct {
		kind string
		at   int
	}
	var matches []matched
	for _, rule := range rules {
		first := -1
		for _, term := range rule.terms {
			if at := strings.Index(prompt, term); at >= 0 && (first < 0 || at < first) {
				first = at
			}
		}
		if first >= 0 {
			matches = append(matches, matched{kind: rule.kind, at: first})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].at < matches[j].at })
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.kind)
	}
	return out
}

// DefaultEvidence chooses the strongest relevant evidence once so every
// deliverable in a bundle shares the same claim graph.
func DefaultEvidence(nuggets []index.Nugget, workspace string, outputs []index.RecipeOutputSpec, limit int) []string {
	if limit <= 0 {
		limit = 120
	}
	relevantKinds := map[string]bool{}
	for _, output := range outputs {
		for _, kind := range evidenceKinds(output.Kind) {
			relevantKinds[kind] = true
		}
	}
	type candidate struct {
		nugget index.Nugget
		score  float64
	}
	var candidates []candidate
	for _, nugget := range nuggets {
		if !matchesWorkspace(nugget, workspace) || nugget.Confidence < 0.55 {
			continue
		}
		score := nugget.Confidence
		if relevantKinds[nugget.Kind] {
			score += 0.35
		}
		if nugget.TurnRef != "" {
			score += 0.03
		}
		candidates = append(candidates, candidate{nugget: nugget, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].nugget.CreatedAt.After(candidates[j].nugget.CreatedAt)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.nugget.UID)
	}
	return ids
}

func matchesWorkspace(nugget index.Nugget, workspace string) bool {
	workspace = strings.TrimSpace(strings.ToLower(workspace))
	if workspace == "" || workspace == "all evidence" {
		return true
	}
	return strings.Contains(strings.ToLower(nugget.Workspace), workspace) ||
		strings.Contains(strings.ToLower(nugget.Repo), workspace)
}

func evidenceKinds(output string) []string {
	switch output {
	case "tutorial", "handbook", "slides", "video_brief", "release_pack":
		return []string{"decision", "error_fix", "command", "gotcha", "dead_end", "artifact"}
	case "adr", "diagram":
		return []string{"decision", "dead_end", "artifact"}
	case "flashcards", "quiz", "notebook_pack", "retrieval_pack":
		return []string{"decision", "error_fix", "command", "gotcha"}
	case "skill", "instruction_patch", "agent_profile", "eval_pack":
		return []string{"error_fix", "gotcha", "dead_end", "command", "decision"}
	case "sft_pack":
		return []string{"decision", "error_fix", "command"}
	case "preference_pack":
		return []string{"dead_end", "error_fix", "decision"}
	default:
		return append([]string(nil), index.NuggetKinds...)
	}
}

// EvidenceReport explains whether a selected claim graph is fit to generate.
type EvidenceReport struct {
	Selected       int            `json:"selected"`
	MeanConfidence float64        `json:"mean_confidence"`
	Coverage       float64        `json:"coverage"`
	Quality        float64        `json:"quality"`
	ByKind         map[string]int `json:"by_kind"`
	NeedsReview    []string       `json:"needs_review"`
	Warnings       []string       `json:"warnings"`
	Blocked        bool           `json:"blocked"`
}

// AssessEvidence scores confidence separately from claim coverage.
func AssessEvidence(recipe index.Recipe, nuggets []index.Nugget) EvidenceReport {
	report := EvidenceReport{Selected: len(nuggets), ByKind: map[string]int{}}
	if len(nuggets) == 0 {
		report.Blocked = true
		report.Warnings = []string{"No evidence is selected. Mine or select evidence before running this recipe."}
		return report
	}
	var confidence float64
	duplicateTitles := map[string]string{}
	for _, nugget := range nuggets {
		report.ByKind[nugget.Kind]++
		confidence += clamp(nugget.Confidence, 0, 1)
		if nugget.Confidence < 0.75 {
			report.NeedsReview = append(report.NeedsReview, nugget.UID)
		}
		key := normalizedKey(nugget.Title)
		if previous, ok := duplicateTitles[key]; ok && key != "" &&
			strings.TrimSpace(previous) != strings.TrimSpace(nugget.Body) {
			report.Warnings = appendUnique(report.Warnings,
				"Evidence with the same subject has different wording; review it for a possible conflict.")
		} else if key != "" {
			duplicateTitles[key] = nugget.Body
		}
	}
	report.MeanConfidence = round(confidence/float64(len(nuggets))*100, 1)

	needed := map[string]bool{}
	for _, output := range recipe.Outputs {
		for _, kind := range evidenceKinds(output.Kind) {
			needed[kind] = true
		}
	}
	covered := 0
	for kind := range needed {
		if report.ByKind[kind] > 0 {
			covered++
		}
	}
	if len(needed) > 0 {
		report.Coverage = round(float64(covered)/float64(len(needed))*100, 1)
	} else {
		report.Coverage = 100
	}
	report.Quality = round(report.MeanConfidence*0.78+report.Coverage*0.22, 1)
	if len(report.NeedsReview) > 0 {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("%d selected item(s) are below 75%% confidence.", len(report.NeedsReview)))
	}
	if report.Coverage < 35 {
		report.Warnings = append(report.Warnings,
			"The selected evidence covers fewer than half of the claim types this bundle needs.")
	}
	return report
}

// YieldCategory is an honest opportunity estimate, not a generated count.
type YieldCategory struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Count   int      `json:"count"`
	Unit    string   `json:"unit"`
	Quality float64  `json:"quality"`
	Details []string `json:"details"`
}

// RecommendedBundle is the highest-value grounded first production.
type RecommendedBundle struct {
	Workspace string   `json:"workspace"`
	Title     string   `json:"title"`
	Why       string   `json:"why"`
	Outputs   []string `json:"outputs"`
	Evidence  int      `json:"evidence"`
}

// YieldMap describes what the current evidence can support before generation.
type YieldMap struct {
	Total       int               `json:"total"`
	Evidence    int               `json:"evidence"`
	Estimated   bool              `json:"estimated"`
	Ready       bool              `json:"ready"`
	Assayed     int64             `json:"assayed"`
	SignalBytes int64             `json:"signal_bytes"`
	Compression float64           `json:"compression"`
	Message     string            `json:"message"`
	Categories  []YieldCategory   `json:"categories"`
	Recommended RecommendedBundle `json:"recommended"`
}

// AssessYield calculates opportunity counts from stored evidence. When no
// nuggets exist yet it uses assayed signal volume only and marks the result as
// estimated rather than pretending specific assets are already supported.
func AssessYield(nuggets []index.Nugget, sessions []core.Session, totals index.Totals) YieldMap {
	counts := map[string]int{}
	var confidence float64
	for _, nugget := range nuggets {
		counts[nugget.Kind]++
		confidence += clamp(nugget.Confidence, 0, 1)
	}
	quality := 0.0
	if len(nuggets) > 0 {
		quality = confidence / float64(len(nuggets)) * 100
	} else if totals.Assayed > 0 {
		quality = 55
	}
	workspaces := Workspaces(sessions, nuggets)
	evidenceWorkspaces := map[string]bool{}
	for _, nugget := range nuggets {
		key := nugget.Workspace
		if key == "" {
			key = nugget.Repo
		}
		if key != "" {
			evidenceWorkspaces[strings.ToLower(key)] = true
		}
	}

	content := ceilDiv(counts["command"]+counts["error_fix"]+counts["gotcha"], 4) +
		ceilDiv(counts["decision"], 3) + ceilDiv(counts["dead_end"]+counts["error_fix"], 6)
	visual := ceilDiv(counts["decision"]+counts["artifact"], 5)
	if content > 0 {
		visual++
	}
	knowledge := len(evidenceWorkspaces) + ceilDiv(counts["command"]+counts["gotcha"]+counts["decision"], 3)
	proposals := AgentProposals(nuggets)
	improvement := len(proposals) + ceilDiv(counts["error_fix"]+counts["gotcha"], 8)

	estimated := len(nuggets) == 0
	if estimated {
		content, visual, knowledge, improvement = 0, 0, 0, 0
		quality = 0
	}
	categories := []YieldCategory{
		{ID: "content", Title: "Publishable content", Count: content, Unit: "assets", Quality: round(quality, 1), Details: []string{"tutorials, ADRs, release packs, and long-form drafts"}},
		{ID: "visual", Title: "Visual and media", Count: visual, Unit: "assets", Quality: round(quality*0.88, 1), Details: []string{"slides, diagrams, infographics, and video briefs"}},
		{ID: "knowledge", Title: "Knowledge and learning", Count: knowledge, Unit: "assets", Quality: round(quality*0.96, 1), Details: []string{"handbooks, flashcards, quizzes, and notebook packs"}},
		{ID: "improvement", Title: "Agent and model improvement", Count: improvement, Unit: "packs", Quality: round(quality*0.8, 1), Details: []string{"skills, instruction proposals, evals, and private data packs"}},
	}
	yield := YieldMap{
		Evidence: len(nuggets), Estimated: estimated, Ready: len(nuggets) > 0,
		Assayed: totals.Assayed, SignalBytes: totals.Signal,
		Compression: round(totals.Compression(), 1), Categories: categories,
	}
	if estimated {
		switch {
		case totals.Assayed == 0:
			yield.Message = "Run the free mine to measure your session signal."
		default:
			yield.Message = fmt.Sprintf("Assay measured %d session(s). Extract evidence before Midden recommends any asset.", totals.Assayed)
		}
	}
	for _, category := range categories {
		yield.Total += category.Count
	}
	for _, best := range workspaces {
		if best.Nuggets == 0 {
			continue
		}
		yield.Recommended = RecommendedBundle{
			Workspace: best.ID,
			Title:     "Turn the strongest lessons into a reusable launch and learning bundle.",
			Why:       fmt.Sprintf("%d reclaimed evidence item(s) across %d session(s).", best.Nuggets, best.Sessions),
			Outputs:   DefaultBundle(),
			Evidence:  best.Nuggets,
		}
		break
	}
	return yield
}

// AgentProposal is a repeated pattern strong enough to evaluate as a future
// agent behavior. It is never an installation instruction by itself.
type AgentProposal struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Workspace   string   `json:"workspace"`
	Support     int      `json:"support"`
	EvalCases   int      `json:"eval_cases"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// AgentProposals groups repeated failure/success evidence into bounded,
// reviewable proposals. Single observations never become agent rules.
func AgentProposals(nuggets []index.Nugget) []AgentProposal {
	type group struct {
		key       string
		workspace string
		items     []index.Nugget
	}
	groups := map[string]*group{}
	for _, nugget := range nuggets {
		switch nugget.Kind {
		case "error_fix", "gotcha", "dead_end", "command":
		default:
			continue
		}
		key := proposalKey(nugget)
		if key == "" {
			continue
		}
		full := strings.ToLower(nugget.Workspace) + "|" + key
		if groups[full] == nil {
			groups[full] = &group{key: key, workspace: nugget.Workspace}
		}
		groups[full].items = append(groups[full].items, nugget)
	}
	var out []AgentProposal
	for _, group := range groups {
		if len(group.items) < 2 {
			continue
		}
		kind := "skill"
		if strings.Contains(group.key, "complete") || strings.Contains(group.key, "verif") ||
			strings.Contains(group.key, "test") {
			kind = "instruction_patch"
		}
		title := strings.TrimSpace(group.items[0].Title)
		if title == "" {
			title = strings.Title(group.key) //nolint:staticcheck // local UI label, not language-aware text.
		}
		ids := make([]string, 0, len(group.items))
		for _, item := range group.items {
			ids = append(ids, item.UID)
		}
		out = append(out, AgentProposal{
			ID:          "proposal-" + shortHash(group.workspace+"|"+group.key),
			Kind:        kind,
			Title:       title,
			Summary:     fmt.Sprintf("Observed %d times. Generate a proposal and regression cases before installing anything.", len(group.items)),
			Workspace:   group.workspace,
			Support:     len(group.items),
			EvalCases:   max(6, len(group.items)*2),
			EvidenceIDs: ids,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Support != out[j].Support {
			return out[i].Support > out[j].Support
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func proposalKey(nugget index.Nugget) string {
	for _, tag := range nugget.Tags {
		if key := normalizedKey(tag); key != "" && len(key) > 3 {
			return key
		}
	}
	return normalizedKey(nugget.Title)
}

// ReadinessGate is one explicit quality or volume condition.
type ReadinessGate struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Current int    `json:"current"`
	Target  int    `json:"target"`
	Detail  string `json:"detail"`
}

// PersonalizationReport separates immediately useful packs from guarded
// training readiness.
type PersonalizationReport struct {
	Retrieval       int             `json:"retrieval"`
	SFT             int             `json:"sft"`
	PreferencePairs int             `json:"preference_pairs"`
	Evals           int             `json:"evals"`
	NeedsReview     int             `json:"needs_review"`
	TrainingReady   bool            `json:"training_ready"`
	Recommendation  string          `json:"recommendation"`
	Gates           []ReadinessGate `json:"gates"`
}

// AssessPersonalization applies the conservative product gates from the
// discovery research.
func AssessPersonalization(nuggets []index.Nugget) PersonalizationReport {
	var report PersonalizationReport
	for _, nugget := range nuggets {
		if nugget.Confidence >= 0.65 {
			report.Retrieval++
		}
		if nugget.Confidence < 0.75 {
			report.NeedsReview++
		}
		if nugget.Confidence >= 0.8 {
			switch nugget.Kind {
			case "decision", "error_fix", "command":
				report.SFT++
			}
		}
		if nugget.Confidence >= 0.75 && (nugget.Kind == "error_fix" || nugget.Kind == "gotcha") {
			report.Evals++
		}
	}
	report.PreferencePairs = len(preferencePairs(nuggets))
	report.TrainingReady = report.SFT >= 500 && report.PreferencePairs >= 200 &&
		report.Evals >= 50 && report.NeedsReview == 0
	if report.TrainingReady {
		report.Recommendation = "The evidence volume passes the conservative product gates. Establish a baseline and review licensing before any training handoff."
	} else {
		report.Recommendation = "Export retrieval and eval packs now. Keep fine-tuning locked until volume, privacy review, and a held-out baseline all pass."
	}
	report.Gates = []ReadinessGate{
		gate("retrieval", "Retrieval memory", report.Retrieval, 1, "Useful from the first approved evidence item."),
		gate("sft", "Clean SFT examples", report.SFT, 500, "Conservative minimum before recommending a personal LoRA/SFT handoff."),
		gate("preference", "Matched preference pairs", report.PreferencePairs, 200, "Accepted and rejected approaches must be explicitly paired."),
		gate("evals", "Held-out evaluation cases", report.Evals, 50, "A meaningful baseline must exist before changing model weights."),
		gate("privacy", "Items awaiting review", report.NeedsReview, 0, "Every candidate needs provenance and privacy review."),
	}
	if report.NeedsReview == 0 {
		report.Gates[len(report.Gates)-1].Status = "pass"
	} else {
		report.Gates[len(report.Gates)-1].Status = "review"
	}
	return report
}

func gate(id, label string, current, target int, detail string) ReadinessGate {
	status := "locked"
	if current >= target {
		status = "pass"
	} else if current > 0 {
		status = "progress"
	}
	return ReadinessGate{ID: id, Label: label, Status: status, Current: current, Target: target, Detail: detail}
}

// Update is a return trigger grounded in evidence newer than an accepted
// output or saved recipe.
type Update struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	RecipeID  string `json:"recipe_id,omitempty"`
	OutputID  string `json:"output_id,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// Updates finds content refreshes and reusable recipes without claiming
// behavior improvements that the evidence cannot measure.
func Updates(recipes []index.Recipe, outputs []index.RefineryOutput, nuggets []index.Nugget) []Update {
	latest := map[string]time.Time{}
	counts := map[string]int{}
	for _, nugget := range nuggets {
		key := strings.ToLower(nugget.Workspace)
		counts[key]++
		if nugget.CreatedAt.After(latest[key]) {
			latest[key] = nugget.CreatedAt
		}
	}
	var updates []Update
	for _, output := range outputs {
		if output.Status != OutputReviewed && output.Status != OutputExported {
			continue
		}
		recipe, ok := findRecipe(recipes, output.RecipeID)
		if !ok {
			continue
		}
		key := strings.ToLower(recipe.Workspace)
		if latest[key].After(output.UpdatedAt) {
			updates = append(updates, Update{
				ID: "refresh-" + output.UID, Kind: "content_refresh",
				Title:    output.Title + " has newer evidence",
				Detail:   fmt.Sprintf("%d evidence item(s) are now available in this workspace. Preview an evidence-only refresh before replacing edits.", counts[key]),
				RecipeID: recipe.UID, OutputID: output.UID, Workspace: recipe.Workspace,
			})
		}
	}
	for _, recipe := range recipes {
		if recipe.Status != RecipeComplete {
			continue
		}
		key := strings.ToLower(recipe.Workspace)
		if latest[key].After(recipe.UpdatedAt) {
			updates = append(updates, Update{
				ID: "rerun-" + recipe.UID, Kind: "saved_recipe",
				Title:    "New evidence can reuse " + recipe.Title,
				Detail:   "The saved recipe can run against only the changed evidence after review.",
				RecipeID: recipe.UID, Workspace: recipe.Workspace,
			})
		}
	}
	if len(recipes) == 0 && len(nuggets) >= 3 {
		updates = append(updates, Update{
			ID: "first-opportunity", Kind: "new_opportunity",
			Title:  "Your first evidence bundle is ready to design",
			Detail: fmt.Sprintf("%d reclaimed items can now feed multiple reviewed outputs.", len(nuggets)),
		})
	}
	if len(updates) > 8 {
		updates = updates[:8]
	}
	return updates
}

func findRecipe(recipes []index.Recipe, id string) (index.Recipe, bool) {
	for _, recipe := range recipes {
		if recipe.UID == id {
			return recipe, true
		}
	}
	return index.Recipe{}, false
}

// EvidencePreamble is the stable context loaded once for every model-backed
// output in a production.
func EvidencePreamble(recipe index.Recipe, nuggets []index.Nugget) string {
	var b strings.Builder
	b.WriteString("You are producing reviewed assets from evidence mined out of real AI work.\n")
	b.WriteString("The evidence below is the only source you may use.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Never invent commands, versions, dates, outcomes, screenshots, or decisions.\n")
	b.WriteString("- Keep unresolved conflicts unresolved and name the missing fact.\n")
	b.WriteString("- Preserve placeholders such as <API_KEY — ask operator> exactly.\n")
	b.WriteString("- Every important claim must end with a compact citation like [evidence:UID].\n")
	b.WriteString("- Output only the requested artifact, with no task commentary.\n\n")
	fmt.Fprintf(&b, "RECIPE: %s\nWORKSPACE: %s\nREQUEST: %s\nEVIDENCE ITEMS: %d\n\n",
		recipe.Title, recipe.Workspace, recipe.Request, len(nuggets))
	for _, nugget := range nuggets {
		fmt.Fprintf(&b, "## [%s] %s\n", nugget.UID, nugget.Title)
		fmt.Fprintf(&b, "kind: %s\nsource: %s:%s\nconfidence: %.0f%%\n",
			nugget.Kind, nugget.Tool, shortSession(nugget.SessionID), nugget.Confidence*100)
		if nugget.TurnRef != "" {
			fmt.Fprintf(&b, "turn: %s\n", nugget.TurnRef)
		}
		b.WriteString(strings.TrimSpace(nugget.Body))
		b.WriteString("\n\n")
	}
	b.WriteString("Reply with READY and wait for the first deliverable request.")
	return b.String()
}

// OutputRequest describes one deliverable without reloading the evidence.
func OutputRequest(output index.RecipeOutputSpec) string {
	base := fmt.Sprintf("Create the %s for %s. Use only the evidence already loaded.",
		output.Title, output.Audience)
	switch output.Kind {
	case "tutorial":
		return base + "\nUse Markdown. Include prerequisites, numbered steps, exact verified commands, expected results, failure modes, and a verification section."
	case "adr":
		return base + "\nUse Markdown sections: Context, Decision, Alternatives considered, Consequences, Evidence."
	case "release_pack":
		return base + "\nProduce one Markdown bundle containing release notes, a launch summary, a demo script, and channel-ready short copy. Keep every claim consistent."
	case "slides":
		return base + "\nProduce Marp-compatible Markdown with exactly 12 slides, YAML front matter, concise speaker notes, and evidence citations."
	case "diagram":
		return base + "\nOutput valid D2 source only. Model the evidenced architecture or journey; use comments for citations. Do not wrap it in Markdown fences."
	case "video_brief":
		return base + "\nUse Markdown. Include a 60-second script, six scene beats, source assets, generated-metaphor disclosures, captions, and an approval checklist. Do not render video."
	case "handbook":
		return base + "\nUse Markdown. Include architecture, decisions, operating commands, troubleshooting, glossary, and evidence gaps."
	case "flashcards":
		return base + "\nOutput tab-separated rows only: front<TAB>back<TAB>tags. Keep each card atomic and cite the evidence id in the tags field."
	case "quiz":
		return base + "\nUse Markdown. Include scenario questions, answer choices, the correct answer, rationale, and evidence citation."
	case "skill":
		return base + "\nProduce a proposed Agent Skills-compatible SKILL.md. Clearly label it PROPOSAL, define triggers, safe workflow, stop conditions, and evidence. Do not claim it is installed."
	case "instruction_patch":
		return base + "\nOutput a unified diff proposal for an instruction file. Keep the scope reversible and cite the repeated evidence behind each added rule."
	case "agent_profile":
		return base + "\nProduce a read-only specialist-agent proposal with objective, bounded tools, model guidance, stop conditions, and evaluation criteria."
	default:
		return base
	}
}

// DeterministicOutput renders packs that do not need a model. It returns
// false for narrative outputs that require a reviewed model invocation.
func DeterministicOutput(output index.RecipeOutputSpec, recipe index.Recipe, nuggets []index.Nugget) (string, bool, error) {
	switch output.Kind {
	case "notebook_pack":
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", recipe.Title)
		b.WriteString("Prepared from stored, redacted nuggets. Raw transcripts are not included.\n\n")
		for _, nugget := range nuggets {
			fmt.Fprintf(&b, "## %s\n\n- kind: %s\n- source: %s:%s\n- evidence: %s\n\n%s\n\n",
				nugget.Title, nugget.Kind, nugget.Tool, shortSession(nugget.SessionID),
				nugget.UID, strings.TrimSpace(nugget.Body))
		}
		return b.String(), true, nil
	case "retrieval_pack":
		return jsonLines(nuggets, func(n index.Nugget) any {
			return map[string]any{
				"id": n.UID, "text": n.Body,
				"metadata": map[string]any{
					"title": n.Title, "kind": n.Kind, "workspace": n.Workspace,
					"repo": n.Repo, "tool": n.Tool, "session_id": n.SessionID,
					"turn_ref": n.TurnRef, "confidence": n.Confidence,
				},
			}
		}), true, nil
	case "sft_pack":
		var eligible []index.Nugget
		for _, nugget := range nuggets {
			if nugget.Confidence >= 0.8 &&
				(nugget.Kind == "decision" || nugget.Kind == "error_fix" || nugget.Kind == "command") {
				eligible = append(eligible, nugget)
			}
		}
		return jsonLines(eligible, func(n index.Nugget) any {
			return map[string]any{
				"instruction": n.Title,
				"response":    n.Body,
				"provenance":  map[string]any{"evidence_id": n.UID, "session_id": n.SessionID, "turn_ref": n.TurnRef},
			}
		}), true, nil
	case "preference_pack":
		pairs := preferencePairs(nuggets)
		var b strings.Builder
		for _, pair := range pairs {
			row, err := json.Marshal(pair)
			if err != nil {
				return "", true, err
			}
			b.Write(row)
			b.WriteByte('\n')
		}
		return b.String(), true, nil
	case "eval_pack":
		var eligible []index.Nugget
		for _, nugget := range nuggets {
			if nugget.Confidence >= 0.75 && (nugget.Kind == "error_fix" || nugget.Kind == "gotcha" || nugget.Kind == "decision") {
				eligible = append(eligible, nugget)
			}
		}
		return jsonLines(eligible, func(n index.Nugget) any {
			return map[string]any{
				"description": n.Title,
				"prompt":      "Handle this situation while preserving the evidenced constraint: " + n.Title,
				"expected":    n.Body,
				"evidence_id": n.UID,
			}
		}), true, nil
	case "privacy_manifest":
		report := AssessPersonalization(nuggets)
		body, err := json.MarshalIndent(map[string]any{
			"recipe_id": recipe.UID, "workspace": recipe.Workspace,
			"generated_at":             time.Now().UTC().Format(time.RFC3339),
			"raw_transcripts_included": false,
			"evidence_items":           len(nuggets),
			"redacted_items":           countRedacted(nuggets),
			"items_needing_review":     report.NeedsReview,
			"source_tools":             sourceTools(nuggets),
			"licensing_review":         "required before external training or registry upload",
		}, "", "  ")
		return string(body) + "\n", true, err
	case "provenance_manifest":
		items := make([]map[string]any, 0, len(nuggets))
		for _, nugget := range nuggets {
			items = append(items, map[string]any{
				"evidence_id": nugget.UID, "tool": nugget.Tool,
				"session_id": nugget.SessionID, "turn_ref": nugget.TurnRef,
				"kind": nugget.Kind, "confidence": nugget.Confidence,
				"redacted": nugget.Redacted, "model": nugget.Model,
			})
		}
		body, err := json.MarshalIndent(map[string]any{
			"recipe_id": recipe.UID, "title": recipe.Title,
			"workspace": recipe.Workspace, "generated_at": time.Now().UTC().Format(time.RFC3339),
			"evidence": items,
		}, "", "  ")
		return string(body) + "\n", true, err
	default:
		return "", false, nil
	}
}

func jsonLines(nuggets []index.Nugget, makeRow func(index.Nugget) any) string {
	var b strings.Builder
	for _, nugget := range nuggets {
		row, err := json.Marshal(makeRow(nugget))
		if err != nil {
			continue
		}
		b.Write(row)
		b.WriteByte('\n')
	}
	return b.String()
}

func preferencePairs(nuggets []index.Nugget) []map[string]any {
	type pairGroup struct {
		accepted []index.Nugget
		rejected []index.Nugget
	}
	groups := map[string]*pairGroup{}
	for _, nugget := range nuggets {
		if nugget.Kind != "dead_end" && nugget.Kind != "error_fix" {
			continue
		}
		subject := proposalKey(nugget)
		if subject == "" {
			continue
		}
		key := strings.ToLower(nugget.Workspace) + "|" + subject
		if groups[key] == nil {
			groups[key] = &pairGroup{}
		}
		if nugget.Kind == "dead_end" {
			groups[key].rejected = append(groups[key].rejected, nugget)
		} else {
			groups[key].accepted = append(groups[key].accepted, nugget)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []map[string]any
	for _, key := range keys {
		group := groups[key]
		sort.SliceStable(group.accepted, func(i, j int) bool {
			return group.accepted[i].UID < group.accepted[j].UID
		})
		sort.SliceStable(group.rejected, func(i, j int) bool {
			return group.rejected[i].UID < group.rejected[j].UID
		})
		for i := 0; i < min(len(group.rejected), len(group.accepted)); i++ {
			rejected, accepted := group.rejected[i], group.accepted[i]
			out = append(out, map[string]any{
				"prompt":    rejected.Title,
				"chosen":    accepted.Body,
				"rejected":  rejected.Body,
				"workspace": accepted.Workspace,
				"provenance": map[string]any{
					"chosen_evidence_id":   accepted.UID,
					"rejected_evidence_id": rejected.UID,
				},
			})
		}
	}
	return out
}

// WrapOutput adds the canonical human-editable metadata layer without
// corrupting formats that do not support front matter.
func WrapOutput(output index.RecipeOutputSpec, recipe index.Recipe, body, model string, evidence int) string {
	body = strings.TrimSpace(body)
	switch output.Format {
	case "markdown":
		return fmt.Sprintf("---\ntitle: %s\nmidden_recipe: %s\nmidden_status: draft\nmaker: %s\nmodel: %s\nevidence_items: %d\n---\n\n%s\n",
			yamlQuote(output.Title), yamlQuote(recipe.UID), yamlQuote(output.Maker),
			yamlQuote(model), evidence, body)
	case "marp":
		if strings.HasPrefix(body, "---") {
			return body + "\n"
		}
		return fmt.Sprintf("---\nmarp: true\ntitle: %s\nmidden_recipe: %s\nmidden_status: draft\nmodel: %s\nevidence_items: %d\n---\n\n%s\n",
			yamlQuote(output.Title), yamlQuote(recipe.UID), yamlQuote(model), evidence, body)
	default:
		return body + "\n"
	}
}

// FileExtension returns the native source extension for one output.
func FileExtension(output index.RecipeOutputSpec) string {
	switch output.Format {
	case "d2":
		return ".d2"
	case "jsonl":
		return ".jsonl"
	case "json":
		return ".json"
	case "tsv":
		return ".tsv"
	case "diff":
		return ".diff"
	default:
		return ".md"
	}
}

// Slug creates a stable filename-safe stem.
func Slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "refinery-output"
	}
	if len(out) > 72 {
		out = strings.Trim(out[:72], "-")
	}
	return out
}

func yamlQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func sourceTools(nuggets []index.Nugget) []string {
	seen := map[string]bool{}
	for _, nugget := range nuggets {
		seen[nugget.Tool] = true
	}
	var out []string
	for tool := range seen {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func countRedacted(nuggets []index.Nugget) int {
	count := 0
	for _, nugget := range nuggets {
		if nugget.Redacted {
			count++
		}
	}
	return count
}

func normalizedKey(value string) string {
	stop := map[string]bool{
		"the": true, "a": true, "an": true, "to": true, "of": true, "and": true,
		"for": true, "with": true, "in": true, "on": true, "from": true, "before": true,
		"after": true, "should": true, "must": true, "use": true, "using": true,
	}
	value = strings.ToLower(value)
	var words []string
	for _, word := range strings.FieldsFunc(value, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if len(word) < 3 || stop[word] {
			continue
		}
		words = append(words, word)
		if len(words) == 4 {
			break
		}
	}
	return strings.Join(words, "-")
}

func shortHash(value string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return fmt.Sprintf("%x", hash.Sum64())
}

func shortSession(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func ceilDiv(value, divisor int) int {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func round(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func clamp(value, low, high float64) float64 {
	return math.Max(low, math.Min(high, value))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
