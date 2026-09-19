package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowInitializationIncludesTheInstalledProcessContract(t *testing.T) {
	var output bytes.Buffer
	handleRPCWithOptions([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`), json.NewEncoder(&output), mcpOptions{Workflow: true, Home: t.TempDir()})
	var reply struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	for _, concept := range []string{"Inspect before asserting outcomes", "A saved draft", "results.inspect"} {
		if !strings.Contains(reply.Result.Instructions, concept) {
			t.Errorf("initialization omitted %q", concept)
		}
	}
	if len(reply.Result.Instructions) > 8192 {
		t.Fatal("bootstrap became an unbounded context dump")
	}
}
