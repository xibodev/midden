package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/module"
)

func TestAgentCLIReadsPayloadFromStdinInExplicitState(t *testing.T) {
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	var out bytes.Buffer
	err = runAgent([]string{"projects.list", "--home", home, "--input", "-"}, strings.NewReader(`{"limit":3}`), &out)
	if err != nil {
		t.Fatal(err)
	}
	var env module.Envelope
	if err = json.Unmarshal(out.Bytes(), &env); err != nil || !env.OK {
		t.Fatalf("bad response: %s %v", out.String(), err)
	}
	var result struct {
		Projects []any `json:"projects"`
		Limit    int   `json:"limit"`
	}
	if err = json.Unmarshal(env.Result, &result); err != nil || result.Limit != 3 || len(result.Projects) != 0 {
		t.Fatalf("wrong result: %s", env.Result)
	}
}

func TestAgentCLIDiscoversOneSchemaWithoutInvokingAnything(t *testing.T) {
	var out bytes.Buffer
	if err := runAgent([]string{"schema", "editorial.analyze"}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Input struct {
			Properties map[string]any `json:"properties"`
		} `json:"input_schema"`
	}
	if err := json.Unmarshal(out.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Input.Properties["analysis"] == nil || schema.Input.Properties["expected_revision"] == nil {
		t.Fatalf("schema missing inputs: %s", out.String())
	}
}

func TestMCPWorkflowRequiresOptInAndUsesSameState(t *testing.T) {
	const name = "midden_projects_list"
	for _, tool := range mcpTools() {
		if tool.Name == name {
			t.Fatal("default read-only MCP widened")
		}
	}
	home := t.TempDir()
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	var buf bytes.Buffer
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"midden_projects_list","arguments":{"limit":2}}}`)
	handleRPCWithOptions(request, json.NewEncoder(&buf), mcpOptions{Workflow: true, Home: home})
	var resp struct {
		Result toolResult `json:"result"`
	}
	if err = json.Unmarshal(buf.Bytes(), &resp); err != nil || resp.Result.IsError {
		t.Fatalf("workflow call failed: %s %v", buf.String(), err)
	}
	if len(resp.Result.Content) != 1 || !strings.Contains(resp.Result.Content[0].Text, `"limit":2`) {
		t.Fatalf("module result lost: %+v", resp.Result)
	}
}

func TestMCPWorkflowRejectsRootOverrideAndUnapprovedProduction(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	for _, args := range []string{`{"home":"elsewhere"}`, `{"root":"elsewhere"}`} {
		res := callWorkflowTool("midden_projects_list", json.RawMessage(args), mcpOptions{Workflow: true, Home: home})
		if !res.IsError {
			t.Fatal("client controlled state root")
		}
	}
	if !callWorkflowTool("midden_evidence_extract", json.RawMessage(`{}`), mcpOptions{Workflow: true, Home: home}).IsError {
		t.Fatal("model subprocess operation exposed through workflow MCP")
	}
}
