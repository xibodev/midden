package create

import (
	"fmt"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

// Planner turns a request plus stored evidence into a reviewable Recipe.
//
// EVERY METHOD HERE IS DETERMINISTIC AND FREE. No model is invoked, nothing is
// charged, and the same inputs produce the same plan. That is a product
// property worth protecting rather than an implementation detail: it means a
// driver can propose, revise and cost a plan conversationally BEFORE anything
// spends, which is the whole reason planning is separate from production.
type Planner struct {
	// DB is the evidence and recipe store. Concrete rather than an interface:
	// there is one store, an interface here would buy nothing, and the
	// architecture-by-interface it invites costs more than it saves.
	DB *index.DB
}

// PlanRequest is what a face asks for. It is deliberately face-agnostic: no
// HTTP types, no job ids, no session state.
type PlanRequest struct {
	// Prompt is the user's own words. Empty is legal -- kinds may be given
	// explicitly instead.
	Prompt string

	// Workspace narrows which evidence is considered.
	Workspace string

	// OutputKinds requests specific content types. Empty means infer from the
	// prompt, which is what makes a bare natural-language request work.
	OutputKinds []string

	// EvidenceIDs pins an exact evidence set. Empty means select by workspace.
	EvidenceIDs []string

	// Title overrides the derived title.
	Title string
}

// Design builds a Recipe without persisting it.
//
// Not persisting is the point: a driver can show a plan, take a correction, and
// design again without leaving abandoned rows behind. Persistence is a separate
// decision made by Save.
func (p Planner) Design(req PlanRequest) (index.Recipe, error) {
	nuggets, err := p.evidenceFor(req)
	if err != nil {
		return index.Recipe{}, err
	}

	recipe, err := refinery.Design(req.Prompt, req.Workspace, req.OutputKinds, nuggets, time.Now())
	if err != nil {
		return index.Recipe{}, err
	}
	if t := strings.TrimSpace(req.Title); t != "" {
		recipe.Title = t
	}
	if strings.Contains(strings.ToLower(req.Prompt), "blog") {
		for i := range recipe.Outputs {
			if recipe.Outputs[i].Kind == "tutorial" {
				recipe.Outputs[i].Title = recipe.Title + " — Blog post"
				recipe.Outputs[i].Audience = "developers reading a publication-ready article"
			}
		}
	}
	if len(req.EvidenceIDs) > 0 {
		recipe.EvidenceIDs = uniqueStrings(req.EvidenceIDs)
	}
	return recipe, nil
}

// evidenceFor resolves the evidence a plan will be built from.
//
// An explicit id set is verified to still exist rather than silently shrinking:
// a plan built from four items when five were named is a plan the user did not
// ask for, and nothing about the result would say so.
func (p Planner) evidenceFor(req PlanRequest) ([]index.Nugget, error) {
	if len(req.EvidenceIDs) == 0 {
		return p.DB.Nuggets(index.NuggetQuery{Workspace: req.Workspace, Limit: 5000})
	}
	ids := uniqueStrings(req.EvidenceIDs)
	nuggets, err := p.DB.NuggetsByIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(nuggets) != len(ids) {
		return nil, fmt.Errorf("one or more selected evidence items no longer exist")
	}
	return nuggets, nil
}

// Assess reports whether the evidence behind a plan is worth producing from.
//
// Deterministic and free, and the reason it is exposed at all: a driver should
// be able to say "there is not much here" before a user pays for a document
// built on three weak items.
func (p Planner) Assess(recipe index.Recipe) (refinery.EvidenceReport, error) {
	nuggets, err := p.DB.NuggetsByIDs(recipe.EvidenceIDs)
	if err != nil {
		return refinery.EvidenceReport{}, err
	}
	return refinery.AssessEvidence(recipe, nuggets), nil
}

// Save persists a plan.
func (p Planner) Save(recipe *index.Recipe) error { return p.DB.PutRecipe(recipe) }

// Load returns a stored plan.
func (p Planner) Load(id string) (index.Recipe, error) { return p.DB.Recipe(id) }

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
