package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWorkflowModeUsesOnlyCanonicalWorkflowTools(t *testing.T) {
	var buffer bytes.Buffer
	handleRPCWithOptions([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), json.NewEncoder(&buffer), mcpOptions{Workflow: true, Home: t.TempDir()})
	var response struct {
		Result struct {
			Tools []mcpTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buffer.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range response.Result.Tools {
		if tool.Name == "midden_sessions_list" {
			found = true
		}
		if tool.Name == "midden_session_brief" || tool.Name == "midden_list_sessions" {
			t.Fatal("duplicate legacy discovery remained in the agent workflow catalog")
		}
	}
	if !found {
		t.Fatal("canonical discovery tool absent")
	}
}
