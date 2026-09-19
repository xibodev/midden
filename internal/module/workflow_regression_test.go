package module

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestWorkflowComposeSupportsDeterministicOnlyPlan(t *testing.T) {
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutNuggets([]index.Nugget{{UID: "scope", Tool: "copilot", SessionID: "synthetic", Kind: "decision", Title: "Scope", Body: "Select exact source evidence.", Confidence: 1}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	call := func(cap string, input any) Envelope {
		t.Helper()
		raw, _ := json.Marshal(input)
		return Invoke(Request{Capability: cap, Input: raw, ConfirmOperator: testOperatorConsent, Roots: map[string]Root{RootMiddenHome: {Path: home, Mode: "rw"}}})
	}
	env := call("recipes.design", map[string]any{"output_kinds": []string{"notebook_pack"}, "evidence_ids": []string{"scope"}})
	if !env.OK {
		t.Fatal(env.Error)
	}
	var designed struct {
		Recipe index.Recipe `json:"recipe"`
	}
	if err = json.Unmarshal(env.Result, &designed); err != nil {
		t.Fatal(err)
	}
	env = call("recipes.evidence", map[string]any{"recipe_id": designed.Recipe.UID, "decision": "approved", "evidence_ids": []string{"scope"}})
	if !env.OK {
		t.Fatal(env.Error)
	}
	env = call("recipes.compose", map[string]any{"recipe_id": designed.Recipe.UID, "drafts": map[string]string{}})
	if !env.OK {
		t.Fatalf("deterministic-only composition failed: %+v", env.Error)
	}
	if !strings.Contains(string(env.Result), `"notebook_pack"`) {
		t.Fatal("missing deterministic output")
	}
}

func checkCollectionShapes(t *testing.T, schema map[string]any, value any, path string) {
	t.Helper()
	switch schema["type"] {
	case "array":
		items, ok := value.([]any)
		if !ok {
			t.Errorf("%s: expected array, got %T", path, value)
			return
		}
		itemSchema, _ := schema["items"].(map[string]any)
		for i, item := range items {
			checkCollectionShapes(t, itemSchema, item, fmt.Sprintf("%s[%d]", path, i))
		}
	case "object":
		values, ok := value.(map[string]any)
		if !ok {
			t.Errorf("%s: expected object, got %T", path, value)
			return
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, property := range properties {
			if v, present := values[key]; present {
				p, _ := property.(map[string]any)
				checkCollectionShapes(t, p, v, path+"."+key)
			}
		}
	}
}

func TestWorkflowSuccessfulResponsesHonorCollectionSchemas(t *testing.T) {
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutNuggets([]index.Nugget{{UID: "e", Tool: "claude", SessionID: "fixture", Kind: "decision", Title: "Decision", Body: "Use reviewed evidence.", Confidence: 1}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	call := func(cap string, input any) Envelope {
		t.Helper()
		raw, _ := json.Marshal(input)
		env := Invoke(Request{Capability: cap, Input: raw, ConfirmOperator: testOperatorConsent, Roots: map[string]Root{RootMiddenHome: {Path: home, Mode: "rw"}}})
		if !env.OK {
			t.Fatalf("%s: %v", cap, env.Error)
		}
		var schema map[string]any
		var value any
		json.Unmarshal(Describe().ResultSchemas["xibodev.midden."+cap+".result/v1"], &schema)
		json.Unmarshal(env.Result, &value)
		checkCollectionShapes(t, schema, value, cap)
		return env
	}
	env := call("recipes.design", map[string]any{"output_kinds": []string{"post"}, "evidence_ids": []string{"e"}})
	var designed struct {
		Recipe index.Recipe `json:"recipe"`
	}
	json.Unmarshal(env.Result, &designed)
	call("recipes.evidence", map[string]any{"recipe_id": designed.Recipe.UID, "decision": "approved", "evidence_ids": []string{"e"}})
	call("recipes.compose", map[string]any{"recipe_id": designed.Recipe.UID, "drafts": map[string]string{"post": "# Evidence\nUse reviewed evidence. [E1]"}})
	call("recipes.inspect", map[string]any{"recipe_id": designed.Recipe.UID})
}
