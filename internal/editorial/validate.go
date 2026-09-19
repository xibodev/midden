package editorial

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mekjr1/midden/internal/content"
	"github.com/mekjr1/midden/internal/create"
)

var localID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)

func text(value string) bool { return strings.TrimSpace(value) != "" && len(value) <= 8192 }

type ValidationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}
type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues"`
}
type ValidationError struct{ Report ValidationReport }

func (e ValidationError) Error() string {
	parts := make([]string, 0, len(e.Report.Issues))
	for _, issue := range e.Report.Issues {
		parts = append(parts, issue.Path+": "+issue.Message)
	}
	return strings.Join(parts, "; ")
}
func (e ValidationError) ErrorDetails() map[string]any {
	return map[string]any{"issues": e.Report.Issues}
}

func (w Workflow) Validate(id string, expected int, a Analysis) (ValidationReport, error) {
	p, err := w.current(id, expected)
	if err != nil {
		return ValidationReport{}, err
	}
	_, digest, err := w.evidence(p)
	if err != nil {
		return ValidationReport{}, err
	}
	if digest != p.EvidenceDigest {
		return ValidationReport{}, fmt.Errorf("evidence changed; refresh the project first")
	}
	return w.analysisReport(p, canonicalFormats(a)), nil
}

func (w Workflow) validateAnalysis(p Project, a Analysis) error {
	report := w.analysisReport(p, a)
	if !report.Valid {
		return ValidationError{report}
	}
	return nil
}

func canonicalFormats(a Analysis) Analysis {
	a.Opportunities = append([]Opportunity(nil), a.Opportunities...)
	for i, o := range a.Opportunities {
		kinds := []string{}
		for _, kind := range o.Formats {
			if t, ok := content.Find(kind); ok {
				kind = t.Name
			}
			if !contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
		a.Opportunities[i].Formats = kinds
	}
	return a
}

func (w Workflow) analysisReport(p Project, a Analysis) ValidationReport {
	report := ValidationReport{Valid: true, Issues: []ValidationIssue{}}
	add := func(path, message string) {
		report.Valid = false
		if len(report.Issues) < 256 {
			report.Issues = append(report.Issues, ValidationIssue{path, message})
		}
	}
	if len(a.Arcs) > 32 || len(a.Decisions) > 100 || len(a.Claims) > 100 || len(a.Assets) > 100 || len(a.Gaps) > 100 || len(a.Opportunities) > 20 || len(a.Chapters) > 100 {
		add("analysis", "collection limits: arcs 32, opportunities 20, all other collections 100")
		return report
	}
	groups := map[string]map[string]bool{"evidence": {}}
	for _, id := range p.EvidenceIDs {
		groups["evidence"][id] = true
	}
	register := func(group, id, path string) {
		if groups[group] == nil {
			groups[group] = map[string]bool{}
		}
		if !localID.MatchString(id) {
			add(path, "use a nonempty alphanumeric identifier of at most 80 characters")
		}
		if groups[group][id] {
			add(path, "duplicate identifier")
		}
		groups[group][id] = true
	}
	for i, v := range a.Arcs {
		register("arc", v.ID, fmt.Sprintf("analysis.arcs[%d].id", i))
	}
	for i, v := range a.Decisions {
		register("decision", v.ID, fmt.Sprintf("analysis.decisions[%d].id", i))
	}
	for i, v := range a.Claims {
		register("claim", v.ID, fmt.Sprintf("analysis.claims[%d].id", i))
	}
	for i, v := range a.Assets {
		register("asset", v.ID, fmt.Sprintf("analysis.assets[%d].id", i))
	}
	for i, v := range a.Gaps {
		register("gap", v.ID, fmt.Sprintf("analysis.gaps[%d].id", i))
	}
	for i, v := range a.Opportunities {
		register("opportunity", v.ID, fmt.Sprintf("analysis.opportunities[%d].id", i))
	}
	for i, v := range a.Chapters {
		register("chapter", v.ID, fmt.Sprintf("analysis.chapters[%d].id", i))
	}
	refs := func(path, group string, ids []string, required bool) {
		if required && len(ids) == 0 {
			add(path, "at least one "+group+" reference is required")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !groups[group][id] {
				add(path, fmt.Sprintf("unknown %s reference %q", group, id))
			}
			if seen[id] {
				add(path, "duplicate reference "+id)
			}
			seen[id] = true
		}
	}
	required := func(path, value string) {
		if !text(value) {
			add(path, "nonempty text of at most 8192 bytes is required")
		}
	}
	for i, v := range a.Arcs {
		path := fmt.Sprintf("analysis.arcs[%d]", i)
		required(path+".title", v.Title)
		required(path+".summary", v.Summary)
		refs(path+".evidence_ids", "evidence", v.EvidenceIDs, true)
	}
	decisions := map[string]Decision{}
	edges := map[string][]string{}
	for _, v := range a.Decisions {
		decisions[v.ID] = v
	}
	for i, v := range a.Decisions {
		path := fmt.Sprintf("analysis.decisions[%d]", i)
		required(path+".statement", v.Statement)
		required(path+".rationale", v.Rationale)
		if !contains([]string{"proposed", "accepted", "superseded", "rejected", "uncertain"}, v.Status) {
			add(path+".status", "use proposed, accepted, superseded, rejected or uncertain")
		}
		refs(path+".evidence_ids", "evidence", v.EvidenceIDs, true)
		refs(path+".supersedes", "decision", v.Supersedes, false)
		for _, id := range v.Supersedes {
			if target, ok := decisions[id]; ok && target.Status != "superseded" {
				add(path+".supersedes", "decision "+id+" must be marked superseded")
			}
		}
		edges[v.ID] = v.Supersedes
	}
	if err := acyclic(edges); err != nil {
		add("analysis.decisions", err.Error())
	}
	for i, v := range a.Claims {
		path := fmt.Sprintf("analysis.claims[%d]", i)
		required(path+".text", v.Text)
		if !contains([]string{"supported", "contested", "refuted", "unverified"}, v.Status) {
			add(path+".status", "use supported, contested, refuted or unverified")
		}
		refs(path+".supporting_ids", "evidence", v.SupportingIDs, v.Status == "supported" || v.Status == "contested")
		refs(path+".contradicting_ids", "evidence", v.ContradictingIDs, v.Status == "contested" || v.Status == "refuted")
		if v.Status == "supported" && len(v.ContradictingIDs) > 0 {
			add(path+".status", "contradictions require contested/refuted status, not supported")
		}
		if v.Status == "contested" && len(v.SupportingIDs) == 0 && len(v.ContradictingIDs) > 0 {
			add(path+".status", "use refuted for evidence only against a claim; do not relabel counterevidence as support")
		}
		for _, id := range v.SupportingIDs {
			if contains(v.ContradictingIDs, id) {
				add(path+".supporting_ids", "the same item cannot support and contradict this claim; separate claims or split the evidence")
			}
		}
	}
	for i, v := range a.Assets {
		path := fmt.Sprintf("analysis.assets[%d]", i)
		required(path+".label", v.Label)
		if !contains([]string{"image", "code", "document", "diagram", "video", "other"}, v.Kind) {
			add(path+".kind", "unknown asset kind")
		}
		if !contains([]string{"referenced", "missing"}, v.Availability) {
			add(path+".availability", "use referenced or missing; discovery is not visual inspection")
		}
		refs(path+".evidence_id", "evidence", []string{v.EvidenceID}, true)
		locator := strings.ReplaceAll(v.Locator, `\`, "/")
		if !text(locator) || filepath.IsAbs(locator) || strings.HasPrefix(locator, "/") || strings.Contains(locator, ":") || contains(strings.Split(locator, "/"), "..") {
			add(path+".locator", "use a relative source reference, not an external path")
		}
	}
	for i, v := range a.Gaps {
		path := fmt.Sprintf("analysis.gaps[%d]", i)
		required(path+".detail", v.Detail)
		if !contains([]string{"open", "disclosed", "resolved"}, v.Status) {
			add(path+".status", "use open, disclosed or resolved")
		}
		refs(path+".claim_ids", "claim", v.ClaimIDs, false)
		refs(path+".evidence_ids", "evidence", v.EvidenceIDs, v.Status == "resolved")
	}
	for i, v := range a.Opportunities {
		path := fmt.Sprintf("analysis.opportunities[%d]", i)
		for field, value := range map[string]string{"title": v.Title, "hook": v.Hook, "audience": v.Audience, "purpose": v.Purpose, "rationale": v.Rationale} {
			required(path+"."+field, value)
		}
		if !contains([]string{"small", "medium", "large"}, v.Effort) {
			add(path+".effort", "use small, medium or large")
		}
		if len(v.Formats) == 0 || len(v.Formats) > 8 || len(unique(v.Formats)) != len(v.Formats) {
			add(path+".formats", "choose 1..8 distinct content kinds")
		}
		for _, kind := range v.Formats {
			if _, ok := content.Find(kind); !ok {
				add(path+".formats", fmt.Sprintf("unknown kind %q; use content.types, for example post, lessons, handbook or slides", kind))
			}
		}
		refs(path+".arc_ids", "arc", v.ArcIDs, true)
		refs(path+".claim_ids", "claim", v.ClaimIDs, false)
		refs(path+".decision_ids", "decision", v.DecisionIDs, false)
		refs(path+".asset_ids", "asset", v.AssetIDs, false)
		refs(path+".gap_ids", "gap", v.GapIDs, false)
		for j, risk := range v.Risks {
			required(fmt.Sprintf("%s.risks[%d]", path, j), risk)
		}
	}
	edges = map[string][]string{}
	for i, v := range a.Chapters {
		path := fmt.Sprintf("analysis.chapters[%d]", i)
		required(path+".title", v.Title)
		if !contains([]string{"planned", "drafting", "review", "complete"}, v.Status) {
			add(path+".status", "use planned, drafting, review or complete")
		}
		refs(path+".opportunity_id", "opportunity", []string{v.OpportunityID}, true)
		refs(path+".depends_on", "chapter", v.DependsOn, false)
		edges[v.ID] = v.DependsOn
		if v.Status == "complete" && v.OutputID == "" {
			add(path+".output_id", "a complete chapter requires a reviewed output")
		}
		if v.OutputID == "" {
			continue
		}
		o, err := w.DB.RefineryOutput(v.OutputID)
		if err != nil {
			add(path+".output_id", err.Error())
			continue
		}
		linked := false
		for _, s := range p.Selections {
			if s.RecipeID == o.RecipeID && s.OpportunityID == v.OpportunityID {
				linked = true
			}
		}
		if !linked {
			add(path+".output_id", "output must belong to this chapter's selected opportunity")
		}
		if err = w.validateOutputEvidence(p, v.OutputID); err != nil {
			add(path+".output_id", err.Error())
		}
		if v.Status == "complete" {
			if _, err = (create.Workflow{DB: w.DB}).ReviewedSource(v.OutputID); err != nil {
				add(path+".output_id", err.Error())
			}
		}
	}
	if err := acyclic(edges); err != nil {
		add("analysis.chapters", err.Error())
	}
	return report
}

func acyclic(edges map[string][]string) error {
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("cycle at %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, next := range edges[id] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range edges {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func subset(a Analysis, o Opportunity) Analysis {
	out := Analysis{Arcs: []Arc{}, Decisions: []Decision{}, Claims: []Claim{}, Assets: []Asset{}, Gaps: []Gap{}, Opportunities: []Opportunity{o}, Chapters: []Chapter{}}
	for _, v := range a.Arcs {
		if contains(o.ArcIDs, v.ID) {
			out.Arcs = append(out.Arcs, v)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, v := range a.Gaps {
			linked := contains(o.GapIDs, v.ID)
			for _, id := range v.ClaimIDs {
				linked = linked || contains(o.ClaimIDs, id)
			}
			if !linked {
				continue
			}
			if !contains(o.GapIDs, v.ID) {
				o.GapIDs = append(o.GapIDs, v.ID)
				changed = true
			}
			for _, id := range v.ClaimIDs {
				if !contains(o.ClaimIDs, id) {
					o.ClaimIDs = append(o.ClaimIDs, id)
					changed = true
				}
			}
		}
	}
	for _, v := range a.Gaps {
		if contains(o.GapIDs, v.ID) {
			out.Gaps = append(out.Gaps, v)
		}
	}
	for _, v := range a.Claims {
		if contains(o.ClaimIDs, v.ID) {
			out.Claims = append(out.Claims, v)
		}
	}
	for _, v := range a.Assets {
		if contains(o.AssetIDs, v.ID) {
			out.Assets = append(out.Assets, v)
		}
	}
	ids := unique(o.DecisionIDs)
	for changed := true; changed; {
		changed = false
		for _, v := range a.Decisions {
			if contains(ids, v.ID) {
				for _, id := range v.Supersedes {
					if !contains(ids, id) {
						ids = append(ids, id)
						changed = true
					}
				}
			}
		}
	}
	for _, v := range a.Decisions {
		if contains(ids, v.ID) {
			out.Decisions = append(out.Decisions, v)
		}
	}
	return out
}

func evidenceIDs(a Analysis) []string {
	var ids []string
	for _, v := range a.Arcs {
		ids = append(ids, v.EvidenceIDs...)
	}
	for _, v := range a.Decisions {
		ids = append(ids, v.EvidenceIDs...)
	}
	for _, v := range a.Claims {
		ids = append(ids, v.SupportingIDs...)
		ids = append(ids, v.ContradictingIDs...)
	}
	for _, v := range a.Assets {
		ids = append(ids, v.EvidenceID)
	}
	for _, v := range a.Gaps {
		ids = append(ids, v.EvidenceIDs...)
	}
	return unique(ids)
}
