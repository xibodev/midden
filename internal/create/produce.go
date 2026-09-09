package create

import (
	"fmt"

	"github.com/mekjr1/midden/internal/cost"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

// Producer turns an approved plan into artifacts.
//
// Split from Planner deliberately: planning is deterministic and free,
// production invokes a model and spends. Keeping them in one type would make
// the guarantee "planning cannot spend" a matter of reading the code rather
// than of holding no executor.
type Producer struct {
	DB *index.DB

	// Progress reports movement. Never nil in practice -- use NopProgress.
	Progress Progress
}

// Preview reports what a run WOULD do, without running it.
//
// This is the natural seam in the existing orchestration: everything before the
// approval gate is free and answers "what will this cost me", everything after
// spends. A driver shows the preview, takes approval, and only then runs -- so
// the boundary is a product affordance rather than an implementation detail.
type Preview struct {
	Recipe   index.Recipe
	Report   refinery.EvidenceReport
	Estimate cost.Estimate

	// ModelOutputs is how many planned outputs require a model. Zero means the
	// whole run is free, which a driver should say plainly rather than asking
	// for approval to spend nothing.
	ModelOutputs int
}

// Preview assembles the pre-run picture. Deterministic and free.
func (p Producer) Preview(recipeID string) (Preview, error) {
	recipe, err := p.DB.Recipe(recipeID)
	if err != nil {
		return Preview{}, err
	}
	evidence, err := p.DB.NuggetsByIDs(recipe.EvidenceIDs)
	if err != nil {
		return Preview{}, err
	}
	estimate, report := Estimate(p.DB, recipe, evidence)

	var modelOutputs int
	for _, out := range recipe.Outputs {
		if out.RequiresModel {
			modelOutputs++
		}
	}
	return Preview{
		Recipe: recipe, Report: report, Estimate: estimate, ModelOutputs: modelOutputs,
	}, nil
}

// CheckRunnable applies the gates that must hold before anything spends.
//
// Extracted as its own step because each refusal is a DIFFERENT product
// statement and a caller should be able to say which one fired. Collapsing them
// into a generic "cannot run" would leave a user unable to tell an unapproved
// recipe from evidence that vanished underneath it.
func (p Producer) CheckRunnable(recipe index.Recipe, evidence []index.Nugget, report refinery.EvidenceReport) error {
	if recipe.Status != refinery.RecipeApproved && recipe.Status != refinery.RecipeFailed {
		return fmt.Errorf("approve the evidence before running this recipe")
	}
	if report.Blocked {
		return fmt.Errorf("the approved evidence is no longer available")
	}
	if len(evidence) != len(recipe.EvidenceIDs) {
		return fmt.Errorf("the evidence set changed; review it again before running")
	}
	return nil
}

// Estimate prices a run and assesses the evidence behind it.
//
// Free of any face: it reads the plan and the evidence and computes. A run with
// no model outputs is genuinely free and reports a zero estimate rather than an
// unknown one -- unknown and zero are different claims, and a user asked to
// approve a spend that cannot occur learns to ignore the prompt.
func Estimate(db *index.DB, recipe index.Recipe, evidence []index.Nugget) (cost.Estimate, refinery.EvidenceReport) {
	report := refinery.AssessEvidence(recipe, evidence)

	modelOutputs := 0
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			modelOutputs++
		}
	}
	if modelOutputs == 0 {
		return cost.Estimate{Op: "refinery"}, report
	}

	raw := len(refinery.EvidencePreamble(recipe, evidence))/4 + 400
	for _, output := range recipe.Outputs {
		if output.RequiresModel {
			raw += len(refinery.OutputRequest(output))/4 + 350
		}
	}
	// Calibration is applied here rather than by the caller: an estimate that
	// ignores what past runs actually cost is a prediction nobody should act
	// on, and every face would otherwise have to remember to apply it.
	stats, _ := db.CalibrationFor("refinery")
	return cost.Predict("refinery", raw, stats), report
}
