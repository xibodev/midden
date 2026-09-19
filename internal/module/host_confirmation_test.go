package module

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mekjr1/midden/internal/confirmation"
	"github.com/mekjr1/midden/internal/index"
)

func testOperatorConsent(ctx context.Context, proposal confirmation.Request) (bool, error) {
	return proposal.SubjectID != "" && proposal.Digest != "" && proposal.Message != "", ctx.Err()
}

func TestOperatorDeclineAndChangedProposalStayUnapproved(t *testing.T) {
	for _, change := range []bool{false, true} {
		req, recipe := operatorFixture(t)
		req.Capability = "recipes.evidence"
		req.Input, _ = json.Marshal(map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e"}, "decision": "approved"})
		req.ConfirmOperator = func(context.Context, confirmation.Request) (bool, error) {
			if !change {
				return false, nil
			}
			db, err := index.OpenAt(req.Roots[RootMiddenHome].Path)
			if err != nil {
				return false, err
			}
			defer db.Close()
			r, err := db.Recipe(recipe.UID)
			if err != nil {
				return false, err
			}
			r.Title = "Changed while dialog was open"
			return true, db.PutRecipe(&r)
		}
		if Invoke(req).OK {
			t.Fatal("declined or stale confirmation accepted")
		}
	}
}
