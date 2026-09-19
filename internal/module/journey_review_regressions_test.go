package module

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestFailedLocalDraftCannotReviveRevokedDelegationApproval(t *testing.T) {
	req, recipe := operatorFixture(t)
	req.ConfirmOperator = testOperatorConsent
	db, err := index.OpenAt(req.Roots[RootMiddenHome].Path)
	if err != nil {
		t.Fatal(err)
	}
	recipe.Outputs = []index.RecipeOutputSpec{{Kind: "slides", Title: "Slides", Format: "marp", RequiresModel: true}}
	if err = db.PutRecipe(&recipe); err != nil {
		t.Fatal(err)
	}
	db.Close()
	call := func(cap string, input any) Envelope {
		req.Capability = cap
		req.Input, _ = json.Marshal(input)
		return Invoke(req)
	}
	if env := call("recipes.evidence", map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e"}, "decision": "approved"}); !env.OK {
		t.Fatal(env.Error)
	}
	if env := call("recipes.evidence", map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"e"}, "decision": "saved"}); !env.OK {
		t.Fatal(env.Error)
	}
	if call("recipes.compose", map[string]any{"recipe_id": recipe.UID, "drafts": map[string]string{"slides": "# Only one slide\n\nInvalid."}}).OK {
		t.Fatal("invalid slides accepted")
	}
	called := false
	req.NativeDriver = func(context.Context, string) (string, error) {
		called = true
		return "", fmt.Errorf("must not be called")
	}
	if call("recipes.produce", map[string]any{"recipe_id": recipe.UID}).OK || called {
		t.Fatal("failed local draft enabled unapproved delegated generation")
	}
}

func TestResultObjectPagingReachesEveryCitationKey(t *testing.T) {
	req, _ := operatorFixture(t)
	db, err := index.OpenAt(req.Roots[RootMiddenHome].Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	object := map[string]any{}
	for i := 0; i < 80; i++ {
		object[fmt.Sprintf("key-%03d", i)] = strings.Repeat("value", 50)
	}
	raw, _ := json.Marshal(object)
	if err = db.PutAgentView("large-object", "fixture", raw); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	offset := 0
	for attempts := 0; attempts < 30; attempts++ {
		page, err := inspectResult(req, db, ResultInspectInput{ResultID: "large-object", Offset: offset, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range page.Fields {
			seen[field.Path] = true
		}
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= offset {
			t.Fatal("object pagination did not advance")
		}
		offset = *page.NextOffset
	}
	if len(seen) != 80 {
		t.Fatalf("only %d of 80 fields were discoverable", len(seen))
	}
}
