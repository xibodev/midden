package refinery_test

import (
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refine"
	"github.com/mekjr1/midden/internal/refinery"
)

func TestLegacyContentKindsCanEnterReviewedRecipeLifecycle(t *testing.T) {
	for _, legacy := range refine.Templates {
		t.Run(legacy.Name, func(t *testing.T) {
			recipe, err := refinery.Design("Create a useful document", "", []string{legacy.Name},
				[]index.Nugget{{UID: "decision-1", Kind: "decision", Body: "Use a compatibility boundary.", Confidence: 1}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(recipe.Outputs) != 1 || recipe.Outputs[0].Kind != legacy.Name || !recipe.Outputs[0].RequiresModel {
				t.Fatalf("legacy kind did not resolve to a narrative output: %+v", recipe.Outputs)
			}
		})
	}
}
