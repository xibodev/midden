package create

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/refinery"
)

// TestEachRefusalSaysWhichGateFired covers the pre-spend gates, which NO web
// test reaches.
//
// Found by mutation: making CheckRunnable always refuse left every web test
// green, because the only test touching production takes the preview path and
// returns before the gates. The gates decide whether a user is about to spend
// money, and nothing exercised them.
//
// Each refusal is a DIFFERENT product statement. Collapsing them into a generic
// "cannot run" would leave a user unable to tell an unapproved recipe from
// evidence that vanished underneath one.
func TestEachRefusalSaysWhichGateFired(t *testing.T) {
	p := Producer{Progress: NopProgress{}}

	approved := index.Recipe{
		Status: refinery.RecipeApproved, EvidenceIDs: []string{"a", "b"},
	}
	two := []index.Nugget{{UID: "a"}, {UID: "b"}}

	cases := []struct {
		name   string
		recipe index.Recipe
		ev     []index.Nugget
		report refinery.EvidenceReport
		want   string
	}{
		{
			name:   "not approved",
			recipe: index.Recipe{Status: refinery.RecipeReview, EvidenceIDs: []string{"a"}},
			ev:     []index.Nugget{{UID: "a"}},
			want:   "approve the evidence",
		},
		{
			name:   "evidence blocked",
			recipe: approved,
			ev:     two,
			report: refinery.EvidenceReport{Blocked: true},
			want:   "no longer available",
		},
		{
			name:   "evidence set shrank",
			recipe: approved,
			ev:     []index.Nugget{{UID: "a"}},
			want:   "evidence set changed",
		},
	}

	for _, tc := range cases {
		err := p.CheckRunnable(tc.recipe, tc.ev, tc.report)
		if err == nil {
			t.Errorf("%s: the run was allowed", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refusal says %q, which does not name the gate that fired",
				tc.name, err)
		}
	}

	// An approved recipe whose evidence is intact must RUN. A gate that refuses
	// everything passes every refusal test above and is useless.
	if err := p.CheckRunnable(approved, two, refinery.EvidenceReport{}); err != nil {
		t.Errorf("an approved, intact recipe was refused: %v", err)
	}
}

// TestAFreeRunIsPricedAtZeroNotUnknown keeps an approval prompt meaningful.
//
// Unknown cost and zero cost are different claims. A user asked to approve a
// spend that cannot occur learns to click through the prompt, which is how a
// real spend gets approved without being read.
func TestAFreeRunIsPricedAtZeroNotUnknown(t *testing.T) {
	db := testDB(t)
	recipe := index.Recipe{
		Outputs: []index.RecipeOutputSpec{{Kind: "retrieval_pack", RequiresModel: false}},
	}
	est, _ := Estimate(db, recipe, []index.Nugget{{UID: "a"}})
	if est.RawTokens != 0 || est.Mid != 0 {
		t.Errorf("a run with no model outputs estimated raw=%d mid=%d; it spends nothing",
			est.RawTokens, est.Mid)
	}
}
