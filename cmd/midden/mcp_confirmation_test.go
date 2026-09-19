package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestMCPConfirmationComesFromClientElicitationNotToolArguments(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "declined", true: "accepted"}[accept], func(t *testing.T) {
			home := t.TempDir()
			db, err := index.OpenAt(home)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.PutNuggets([]index.Nugget{{UID: "e", Tool: "copilot", SessionID: "fixture", Kind: "decision", Title: "Deployment", Body: "Inspect the deployed version first.", Confidence: .9}}); err != nil {
				t.Fatal(err)
			}
			recipe := index.Recipe{UID: "r", Title: "Fixture", Status: "draft", EvidenceIDs: []string{"e"}, Outputs: []index.RecipeOutputSpec{{Kind: "post", Title: "Post", Format: "markdown", RequiresModel: true}}}
			if err = db.PutRecipe(&recipe); err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			serverIn, clientOut := io.Pipe()
			clientIn, serverOut := io.Pipe()
			done := make(chan error, 1)
			go func() {
				done <- serveMCP(serverIn, serverOut, mcpOptions{Workflow: true, Home: home})
				serverOut.Close()
			}()
			write := json.NewEncoder(clientOut)
			read := bufio.NewReader(clientIn)
			next := func() map[string]json.RawMessage {
				t.Helper()
				line, e := read.ReadBytes('\n')
				if e != nil {
					t.Fatal(e)
				}
				var value map[string]json.RawMessage
				if e = json.Unmarshal(line, &value); e != nil {
					t.Fatal(e)
				}
				return value
			}
			write.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"elicitation": map[string]any{}}}})
			next()
			write.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "midden_recipes_evidence", "arguments": map[string]any{"recipe_id": "r", "evidence_ids": []string{"e"}, "decision": "approved"}}})
			form := next()
			if string(form["method"]) != `"elicitation/create"` {
				t.Fatalf("no host operator form: %s", form["method"])
			}
			current, err := db.Recipe("r")
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != "draft" {
				t.Fatal("state changed before the operator replied")
			}
			action := "decline"
			if accept {
				action = "accept"
			}
			write.Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(form["id"]), "result": map[string]any{"action": action, "content": map[string]bool{"approved": accept}}})
			response := next()
			var result toolResult
			json.Unmarshal(response["result"], &result)
			if result.IsError == accept {
				t.Fatalf("confirmation response mishandled: %+v", result)
			}
			current, err = db.Recipe("r")
			if err != nil {
				t.Fatal(err)
			}
			if (current.Status == "approved") != accept {
				t.Fatalf("unexpected approval: %s", current.Status)
			}
			clientOut.Close()
			clientIn.Close()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMCPWithoutElicitationLeavesApprovalPending(t *testing.T) {
	var output strings.Builder
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"capabilities\":{}}}\n")
	if err := serveMCP(input, &output, mcpOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "elicitation/create") {
		t.Fatal("unsolicited confirmation without client support")
	}
}
