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

func (w Workflow) validateAnalysis(p Project, a Analysis) error {
	if len(a.Arcs) > 32 || len(a.Decisions) > 100 || len(a.Claims) > 100 || len(a.Assets) > 100 ||
		len(a.Gaps) > 100 || len(a.Opportunities) > 20 || len(a.Chapters) > 100 {
		return fmt.Errorf("analysis exceeds bounded collection limits")
	}
	groups := map[string]map[string]bool{}
	register := func(group, id string) error {
		if !localID.MatchString(id) {
			return fmt.Errorf("invalid %s id %q", group, id)
		}
		if groups[group] == nil {
			groups[group] = map[string]bool{}
		}
		if groups[group][id] {
			return fmt.Errorf("duplicate %s id %q", group, id)
		}
		groups[group][id] = true
		return nil
	}
	for _, id := range p.EvidenceIDs {
		if groups["evidence"] == nil {
			groups["evidence"] = map[string]bool{}
		}
		groups["evidence"][id] = true
	}
	for _, v := range a.Arcs {
		if err := register("arc", v.ID); err != nil {
			return err
		}
	}
	for _, v := range a.Decisions {
		if err := register("decision", v.ID); err != nil {
			return err
		}
	}
	for _, v := range a.Claims {
		if err := register("claim", v.ID); err != nil {
			return err
		}
	}
	for _, v := range a.Assets {
		if err := register("asset", v.ID); err != nil {
			return err
		}
	}
	for _, v := range a.Gaps {
		if err := register("gap", v.ID); err != nil {
			return err
		}
	}
	for _, v := range a.Opportunities {
		if err := register("opportunity", v.ID); err != nil {
			return err
		}
	}
	for _, v := range a.Chapters {
		if err := register("chapter", v.ID); err != nil {
			return err
		}
	}
	refs := func(group string, ids []string, required bool) error {
		if required && len(ids) == 0 {
			return fmt.Errorf("%s references are required", group)
		}
		if len(unique(ids)) != len(ids) {
			return fmt.Errorf("duplicate %s reference", group)
		}
		for _, id := range ids {
			if !groups[group][id] {
				return fmt.Errorf("unknown %s reference %q", group, id)
			}
		}
		return nil
	}
	for _, v := range a.Arcs {
		if !text(v.Title) || !text(v.Summary) {
			return fmt.Errorf("arc %s needs a title and summary", v.ID)
		}
		if err := refs("evidence", v.EvidenceIDs, true); err != nil {
			return err
		}
	}
	decisions := map[string]Decision{}
	for _, v := range a.Decisions {
		decisions[v.ID] = v
	}
	edges := map[string][]string{}
	for _, v := range a.Decisions {
		if !text(v.Statement) || !text(v.Rationale) || !contains([]string{"proposed", "accepted", "superseded", "rejected", "uncertain"}, v.Status) {
			return fmt.Errorf("invalid decision %s", v.ID)
		}
		if err := refs("evidence", v.EvidenceIDs, true); err != nil {
			return err
		}
		if err := refs("decision", v.Supersedes, false); err != nil {
			return err
		}
		for _, id := range v.Supersedes {
			if decisions[id].Status != "superseded" {
				return fmt.Errorf("decision %s must be marked superseded", id)
			}
		}
		edges[v.ID] = v.Supersedes
	}
	if err := acyclic(edges); err != nil {
		return fmt.Errorf("decision supersession: %w", err)
	}
	for _, v := range a.Claims {
		if !text(v.Text) || !contains([]string{"supported", "contested", "unverified"}, v.Status) {
			return fmt.Errorf("invalid claim %s", v.ID)
		}
		if err := refs("evidence", v.SupportingIDs, v.Status != "unverified"); err != nil {
			return err
		}
		if err := refs("evidence", v.ContradictingIDs, v.Status == "contested"); err != nil {
			return err
		}
		if v.Status == "supported" && len(v.ContradictingIDs) > 0 {
			return fmt.Errorf("claim %s has contradictions; mark contested", v.ID)
		}
		for _, id := range v.SupportingIDs {
			if contains(v.ContradictingIDs, id) {
				return fmt.Errorf("claim %s uses the same evidence on both sides", v.ID)
			}
		}
	}
	for _, v := range a.Assets {
		if !text(v.Label) || !contains([]string{"image", "code", "document", "diagram", "video", "other"}, v.Kind) ||
			!contains([]string{"referenced", "missing"}, v.Availability) {
			return fmt.Errorf("invalid asset %s", v.ID)
		}
		if err := refs("evidence", []string{v.EvidenceID}, true); err != nil {
			return err
		}
		path := strings.ReplaceAll(v.Locator, `\`, "/")
		if !text(path) || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.Contains(path, ":") || contains(strings.Split(path, "/"), "..") {
			return fmt.Errorf("asset %s locator must be a relative source reference, not an external path", v.ID)
		}
	}
	for _, v := range a.Gaps {
		if !text(v.Detail) || !contains([]string{"open", "disclosed", "resolved"}, v.Status) {
			return fmt.Errorf("invalid gap %s", v.ID)
		}
		if err := refs("claim", v.ClaimIDs, false); err != nil {
			return err
		}
		if err := refs("evidence", v.EvidenceIDs, v.Status == "resolved"); err != nil {
			return err
		}
	}
	for _, v := range a.Opportunities {
		if !text(v.Title) || !text(v.Hook) || !text(v.Audience) || !text(v.Purpose) || !text(v.Rationale) ||
			!contains([]string{"small", "medium", "large"}, v.Effort) {
			return fmt.Errorf("opportunity %s needs a reasoned audience, purpose, hook and effort", v.ID)
		}
		if len(v.Formats) == 0 || len(v.Formats) > 8 || len(unique(v.Formats)) != len(v.Formats) {
			return fmt.Errorf("opportunity %s needs 1..8 distinct formats", v.ID)
		}
		for _, kind := range v.Formats {
			if _, ok := content.Find(kind); !ok {
				return fmt.Errorf("unknown format %q", kind)
			}
		}
		for _, check := range []struct {
			group    string
			ids      []string
			required bool
		}{
			{"arc", v.ArcIDs, true}, {"claim", v.ClaimIDs, false}, {"decision", v.DecisionIDs, false}, {"asset", v.AssetIDs, false}, {"gap", v.GapIDs, false},
		} {
			if err := refs(check.group, check.ids, check.required); err != nil {
				return err
			}
		}
		for _, risk := range v.Risks {
			if !text(risk) {
				return fmt.Errorf("invalid risk in %s", v.ID)
			}
		}
	}
	edges = map[string][]string{}
	for _, v := range a.Chapters {
		if !text(v.Title) || !contains([]string{"planned", "drafting", "review", "complete"}, v.Status) {
			return fmt.Errorf("invalid chapter %s", v.ID)
		}
		if err := refs("opportunity", []string{v.OpportunityID}, true); err != nil {
			return err
		}
		if err := refs("chapter", v.DependsOn, false); err != nil {
			return err
		}
		edges[v.ID] = v.DependsOn
		if v.Status == "complete" && v.OutputID == "" {
			return fmt.Errorf("complete chapter %s needs a reviewed output", v.ID)
		}
		if v.OutputID != "" {
			o, err := w.DB.RefineryOutput(v.OutputID)
			if err != nil {
				return fmt.Errorf("chapter output: %w", err)
			}
			linked := false
			for _, s := range p.Selections {
				if s.RecipeID == o.RecipeID && s.OpportunityID == v.OpportunityID {
					linked = true
				}
			}
			if !linked {
				return fmt.Errorf("chapter output must belong to its selected opportunity")
			}
			if err := w.validateOutputEvidence(p, v.OutputID); err != nil {
				return err
			}
			if v.Status == "complete" && o.Status != "reviewed" && o.Status != "exported" {
				return fmt.Errorf("chapter output is not reviewed")
			}
			if v.Status == "complete" {
				if _, err := (create.Workflow{DB: w.DB}).ReviewedSource(v.OutputID); err != nil {
					return err
				}
			}
		}
	}
	return acyclic(edges)
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
	// Close the claim/gap relation so an omitted convenience link cannot hide
	// a known caveat. Related claims may themselves bring additional gaps.
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
