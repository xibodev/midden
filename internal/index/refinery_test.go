package index

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/cost"
)

func TestRefineryStateRoundTrip(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	recipe := Recipe{
		Title:     "Payments bundle",
		Workspace: "payments",
		Request:   "Create a tutorial and diagram",
		Status:    "approved",
		Outputs: []RecipeOutputSpec{
			{Kind: "tutorial", Title: "Tutorial", Maker: "Midden", Format: "markdown", RequiresModel: true},
		},
		EvidenceIDs: []string{"e-1", "e-2"},
		ApprovedAt:  time.Now(),
	}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	if recipe.UID == "" {
		t.Fatal("recipe uid was not assigned")
	}

	run := RefineryRun{
		RecipeID: recipe.UID,
		Status:   "running",
		Estimate: cost.Estimate{Op: "refinery", RawTokens: 120, Mid: 10_800},
		Stages: []RefineryRunStage{
			{Key: "evidence", Label: "Load evidence", Status: "done"},
		},
	}
	if err := db.PutRefineryRun(&run); err != nil {
		t.Fatal(err)
	}
	output := RefineryOutput{
		RecipeID:       recipe.UID,
		RunID:          run.UID,
		Kind:           "tutorial",
		Title:          "Tutorial",
		Maker:          "Midden",
		Format:         "markdown",
		Status:         "draft",
		Path:           filepath.Join("artifacts", "refinery", "tutorial.md"),
		ProvenancePath: filepath.Join("artifacts", "refinery", "tutorial.md.provenance.json"),
		EvidenceIDs:    []string{"e-1", "e-2"},
		Quality:        91.5,
	}
	if err := db.PutRefineryOutput(&output); err != nil {
		t.Fatal(err)
	}

	gotRecipe, err := db.Recipe(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRecipe.Title != recipe.Title || len(gotRecipe.Outputs) != 1 ||
		len(gotRecipe.EvidenceIDs) != 2 || gotRecipe.ApprovedAt.IsZero() {
		t.Fatalf("recipe round trip = %#v", gotRecipe)
	}

	runs, err := db.RefineryRuns(recipe.UID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Estimate.Mid != 10_800 ||
		len(runs[0].Stages) != 1 {
		t.Fatalf("run round trip = %#v", runs)
	}

	outputs, err := db.RefineryOutputs(recipe.UID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Quality != 91.5 ||
		len(outputs[0].EvidenceIDs) != 2 {
		t.Fatalf("output round trip = %#v", outputs)
	}
}

func TestNuggetsByIDsIsExactAndOrdered(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	nuggets := []Nugget{
		{UID: "literal-percent-%", Tool: "claude", SessionID: "s1", Kind: "decision", Body: "first"},
		{UID: "literal-underscore_", Tool: "claude", SessionID: "s2", Kind: "gotcha", Body: "second"},
		{UID: "unselected", Tool: "claude", SessionID: "s3", Kind: "command", Body: "third"},
	}
	if err := db.PutNuggets(nuggets); err != nil {
		t.Fatal(err)
	}

	got, err := db.NuggetsByIDs([]string{"literal-underscore_", "literal-percent-%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UID != "literal-underscore_" || got[1].UID != "literal-percent-%" {
		t.Fatalf("ordered exact lookup = %#v", got)
	}
}

func TestClaimRecipeForRunIsAtomic(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	recipe := Recipe{Title: "One run", Status: "approved"}
	if err := db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	first, err := db.ClaimRecipeForRun(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.ClaimRecipeForRun(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("claims = first:%v second:%v, want true then false", first, second)
	}
	got, err := db.Recipe(recipe.UID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" {
		t.Fatalf("status=%q, want running", got.Status)
	}
}

func TestEmptyRefineryCollectionsEncodeAsArrays(t *testing.T) {
	t.Setenv("MIDDEN_HOME", t.TempDir())
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	recipes, err := db.Recipes(0)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := db.RefineryOutputs("", 0)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := db.RefineryRuns("", 0)
	if err != nil {
		t.Fatal(err)
	}
	nuggets, err := db.Nuggets(NuggetQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if recipes == nil || outputs == nil || runs == nil || nuggets == nil {
		t.Fatalf("nil collection: recipes=%#v outputs=%#v runs=%#v nuggets=%#v",
			recipes, outputs, runs, nuggets)
	}
}
