package create

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func testDB(t *testing.T) *index.DB {
	t.Helper()
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedEvidence(t *testing.T, db *index.DB, n int) []string {
	t.Helper()
	var ids []string
	var ns []index.Nugget
	for i := 0; i < n; i++ {
		uid := index.NewUID()
		ids = append(ids, uid)
		ns = append(ns, index.Nugget{
			UID: uid, Tool: "claude", SessionID: "s1", Kind: "decision",
			Title: "retry policy for the payments service",
			Body:  "Chose exponential backoff with a five attempt ceiling.",
		})
	}
	if err := db.PutNuggets(ns); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return ids
}

// TestPlanningNeverInvokesAModel is the product property that makes planning
// worth separating from production.
//
// A driver must be able to propose, revise and cost a plan BEFORE anything
// spends. The Planner holds no model executor at all, which is the strongest
// available form of that guarantee: it cannot spend, rather than choosing not
// to.
func TestPlanningNeverInvokesAModel(t *testing.T) {
	db := testDB(t)
	ids := seedEvidence(t, db, 4)

	recipe, err := Planner{DB: db}.Design(PlanRequest{
		Prompt:      "what did we decide about retry behaviour in the payments service",
		EvidenceIDs: ids,
	})
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	if len(recipe.Outputs) == 0 {
		t.Error("a plan proposed no outputs; a driver has nothing to show")
	}
	if len(recipe.EvidenceIDs) != len(ids) {
		t.Errorf("plan carries %d evidence ids, requested %d",
			len(recipe.EvidenceIDs), len(ids))
	}
}

// TestDesignDoesNotPersist keeps revision cheap.
//
// A driver shows a plan, takes a correction, and designs again. If designing
// wrote a row each time, an ordinary conversation would litter the store with
// abandoned plans.
func TestDesignDoesNotPersist(t *testing.T) {
	db := testDB(t)
	ids := seedEvidence(t, db, 3)

	if _, err := (Planner{DB: db}).Design(PlanRequest{
		Prompt: "summarise the outage debugging", EvidenceIDs: ids,
	}); err != nil {
		t.Fatalf("design: %v", err)
	}

	recipes, _, err := db.RecipesPage(10, 0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(recipes) != 0 {
		t.Errorf("designing persisted %d recipe(s); revision must be free", len(recipes))
	}
}

// TestMissingEvidenceIsRefusedNotSilentlyShrunk covers the branch the web tests
// never reach.
//
// A plan built from four items when five were named is a plan the user did not
// ask for, and nothing in the result would say so. Found by mutation: disabling
// this check left every web test green.
func TestMissingEvidenceIsRefusedNotSilentlyShrunk(t *testing.T) {
	db := testDB(t)
	ids := seedEvidence(t, db, 2)
	ids = append(ids, "does-not-exist")

	_, err := Planner{DB: db}.Design(PlanRequest{Prompt: "anything", EvidenceIDs: ids})
	if err == nil {
		t.Fatal("a plan naming missing evidence was accepted; it would be built " +
			"from fewer items than the user selected, silently")
	}
	if !strings.Contains(err.Error(), "no longer exist") {
		t.Errorf("error does not say what went wrong: %v", err)
	}
}

// TestPlanWithoutEvidenceIsRefused: producing from nothing yields a document
// with no provenance, which is worse than refusing.
func TestPlanWithoutEvidenceIsRefused(t *testing.T) {
	db := testDB(t)
	if _, err := (Planner{DB: db}).Design(PlanRequest{Prompt: "make me something"}); err == nil {
		t.Error("a plan was designed with no evidence at all")
	}
}
