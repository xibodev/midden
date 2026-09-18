package editorial

import (
	"encoding/json"
	"fmt"

	"github.com/mekjr1/midden/internal/create"
)

type HandoffRequest struct {
	ProjectID        string   `json:"project_id"`
	ExpectedRevision int      `json:"expected_revision"`
	Target           string   `json:"target" enum:"markdown,quarto,pandoc,d2"`
	OutputIDs        []string `json:"output_ids"`
}

func (w Workflow) Handoff(req HandoffRequest) (create.HandoffResult, error) {
	var result create.HandoffResult
	p, err := w.current(req.ProjectID, req.ExpectedRevision)
	if err != nil {
		return result, err
	}
	if p.Analysis == nil || p.AnalysisStale {
		return result, fmt.Errorf("current analysis is required before handoff")
	}
	_, digest, err := w.evidence(p)
	if err != nil {
		return result, err
	}
	if digest != p.EvidenceDigest {
		return result, fmt.Errorf("evidence changed; refresh and review before handoff")
	}
	if len(req.OutputIDs) < 1 || len(req.OutputIDs) > 100 || len(unique(req.OutputIDs)) != len(req.OutputIDs) {
		return result, fmt.Errorf("select 1..100 distinct project outputs")
	}
	type production struct {
		OutputID         string   `json:"output_id"`
		AnalysisRevision int      `json:"analysis_revision"`
		Analysis         Analysis `json:"analysis"`
	}
	productions := []production{}
	for _, id := range req.OutputIDs {
		o, e := w.DB.RefineryOutput(id)
		if e != nil {
			return result, e
		}
		if e = w.validateOutputEvidence(p, id); e != nil {
			return result, e
		}
		var selection *Selection
		for i := range p.Selections {
			if p.Selections[i].RecipeID == o.RecipeID {
				selection = &p.Selections[i]
				break
			}
		}
		if selection == nil {
			return result, fmt.Errorf("output %s does not belong to a selected project opportunity", id)
		}
		historical, e := w.InspectRevision(p.ID, selection.AnalysisRevision)
		if e != nil {
			return result, e
		}
		if historical.Analysis == nil {
			return result, fmt.Errorf("selected analysis revision is unavailable")
		}
		for _, op := range historical.Analysis.Opportunities {
			if op.ID == selection.OpportunityID {
				productions = append(productions, production{id, selection.AnalysisRevision, subset(*historical.Analysis, op)})
				break
			}
		}
	}
	if len(productions) != len(req.OutputIDs) {
		return result, fmt.Errorf("a selected editorial opportunity is missing")
	}
	if req.Target == "quarto" {
		positions := map[string]int{}
		for i, id := range req.OutputIDs {
			positions[id] = i
		}
		chapters := map[string]Chapter{}
		for _, c := range p.Analysis.Chapters {
			chapters[c.ID] = c
		}
		for _, c := range p.Analysis.Chapters {
			at, selected := positions[c.OutputID]
			if !selected {
				continue
			}
			for _, dep := range c.DependsOn {
				prior, ok := positions[chapters[dep].OutputID]
				if !ok || prior >= at {
					return result, fmt.Errorf("chapter %s needs reviewed dependency %s earlier in the handoff", c.ID, dep)
				}
			}
		}
	}
	raw, err := json.Marshal(struct {
		ProjectID      string       `json:"project_id"`
		Revision       int          `json:"revision"`
		EvidenceDigest string       `json:"evidence_digest"`
		Productions    []production `json:"productions"`
	}{p.ID, p.Revision, p.EvidenceDigest, productions})
	if err != nil {
		return result, err
	}
	return (create.Workflow{DB: w.DB}).BuildHandoff(p.ID, p.Revision, p.Title, req.Target, req.OutputIDs, raw)
}

func (w Workflow) validateOutputEvidence(p Project, id string) error {
	o, err := w.DB.RefineryOutput(id)
	if err != nil {
		return err
	}
	for _, s := range p.Selections {
		if s.RecipeID != o.RecipeID {
			continue
		}
		historical, err := w.InspectRevision(p.ID, s.AnalysisRevision)
		if err != nil {
			return err
		}
		if historical.Analysis == nil {
			return fmt.Errorf("historical analysis is missing")
		}
		for _, op := range historical.Analysis.Opportunities {
			if op.ID != s.OpportunityID {
				continue
			}
			allowed := evidenceIDs(subset(*historical.Analysis, op))
			if len(o.EvidenceIDs) == 0 {
				return fmt.Errorf("output has no supporting evidence")
			}
			for _, ev := range o.EvidenceIDs {
				if !contains(allowed, ev) {
					return fmt.Errorf("output evidence %s is outside its historical opportunity", ev)
				}
			}
			return w.DB.CheckEditorialRecipeScope(o.RecipeID, o.EvidenceIDs)
		}
	}
	return fmt.Errorf("output %s has no selected editorial opportunity", id)
}
