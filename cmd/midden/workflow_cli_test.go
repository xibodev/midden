package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/module"
)

func TestDetachedCLIWorkflowUsesExplicitStore(t *testing.T) {
	bin := buildModuleBinary(t)
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.PutNuggets([]index.Nugget{{UID: "cli-evidence", Tool: "claude", SessionID: "fixture", Kind: "decision", Title: "Preserve scope", Body: "Explicit evidence scopes preserve provenance.", Confidence: 1, Workspace: "fixture", CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	call := func(cap string, input any) map[string]json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(input)
		req := module.Request{Protocol: module.ProtocolID, Capability: cap, Input: raw, Roots: map[string]module.Root{module.RootMiddenHome: {Path: home, Mode: "rw"}}, ExplicitSourceRoots: true}
		body, _ := json.Marshal(req)
		path := filepath.Join(t.TempDir(), "request.json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		out, err := commandWithEmptyEnv(bin, "module", "invoke", cap, "--input", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		var env module.Envelope
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatal(err)
		}
		if !env.OK {
			t.Fatal(env.Error)
		}
		var result map[string]json.RawMessage
		json.Unmarshal(env.Result, &result)
		return result
	}
	design := call("recipes.design", map[string]any{"workspace": "fixture", "evidence_ids": []string{"cli-evidence"}, "output_kinds": []string{"provenance_manifest"}})
	var recipe index.Recipe
	json.Unmarshal(design["recipe"], &recipe)
	call("recipes.evidence", map[string]any{"recipe_id": recipe.UID, "evidence_ids": []string{"cli-evidence"}, "decision": "approved"})
	produced := call("recipes.produce", map[string]any{"recipe_id": recipe.UID})
	var outputs []index.RefineryOutput
	json.Unmarshal(produced["outputs"], &outputs)
	if len(outputs) != 1 {
		t.Fatal("detached CLI produced no output")
	}
	call("outputs.inspect", map[string]any{"output_id": outputs[0].UID})
}
