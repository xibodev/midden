package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// drive sends one JSON-RPC line through the handler and returns the response,
// or nil when the message was a notification.
func drive(t *testing.T, line string) *rpcResponse {
	t.Helper()

	var buf bytes.Buffer
	handleRPC([]byte(line), json.NewEncoder(&buf))

	if buf.Len() == 0 {
		return nil
	}
	var resp rpcResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("response was not valid JSON: %v\n%s", err, buf.String())
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("missing jsonrpc version: %q", resp.JSONRPC)
	}
	return &resp
}

func TestInitializeAdvertisesTools(t *testing.T) {
	resp := drive(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}

	res, _ := json.Marshal(resp.Result)
	var parsed struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	json.Unmarshal(res, &parsed)

	if parsed.ProtocolVersion == "" {
		t.Error("protocolVersion is required")
	}
	if _, ok := parsed.Capabilities["tools"]; !ok {
		t.Error("server must advertise the tools capability")
	}
	if parsed.ServerInfo.Name != "midden" {
		t.Errorf("serverInfo.name = %q", parsed.ServerInfo.Name)
	}
}

func TestNotificationsGetNoResponse(t *testing.T) {
	// Replying to a notification is a protocol violation and confuses clients.
	if resp := drive(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp != nil {
		t.Errorf("notification must not be answered, got %+v", resp)
	}
}

func TestMalformedJSONReturnsParseError(t *testing.T) {
	resp := drive(t, `{not json`)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected a parse error response")
	}
	if resp.Error.Code != errParse {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errParse)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	resp := drive(t, `{"jsonrpc":"2.0","id":9,"method":"nope/nope"}`)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected an error response")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, errMethodNotFound)
	}
}

func TestUnknownToolIsAToolErrorNotAProtocolError(t *testing.T) {
	// An unknown tool is a normal result with isError set, so the model can
	// read and recover from it rather than the transport failing.
	resp := drive(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"does_not_exist","arguments":{}}}`)
	if resp == nil || resp.Error != nil {
		t.Fatalf("should be a tool-level error, got protocol error: %+v", resp)
	}

	res, _ := json.Marshal(resp.Result)
	var tr toolResult
	json.Unmarshal(res, &tr)
	if !tr.IsError {
		t.Error("isError should be true for an unknown tool")
	}
}

func TestToolsListIsWellFormed(t *testing.T) {
	resp := drive(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list failed: %+v", resp)
	}

	res, _ := json.Marshal(resp.Result)
	var parsed struct {
		Tools []mcpTool `json:"tools"`
	}
	json.Unmarshal(res, &parsed)

	if len(parsed.Tools) < 5 {
		t.Fatalf("expected at least 5 tools, got %d", len(parsed.Tools))
	}

	seen := map[string]bool{}
	for _, tool := range parsed.Tools {
		if !strings.HasPrefix(tool.Name, "midden_") {
			t.Errorf("tool %q should be namespaced", tool.Name)
		}
		if seen[tool.Name] {
			t.Errorf("duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = true

		if len(tool.Description) < 40 {
			t.Errorf("tool %q needs a description a model can act on", tool.Name)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q schema must be an object", tool.Name)
		}
		if _, ok := tool.InputSchema["properties"]; !ok {
			t.Errorf("tool %q schema missing properties", tool.Name)
		}
	}

	for _, required := range []string{
		"midden_health", "midden_list_sessions", "midden_search",
		"midden_session_brief", "midden_resume_command",
	} {
		if !seen[required] {
			t.Errorf("missing tool %q", required)
		}
	}
}

func TestRequiredArgsAreEnforced(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"midden_session_brief", `{}`},
		{"midden_resume_command", `{}`},
		{"midden_search", `{}`},
	} {
		got := callTool(tc.name, json.RawMessage(tc.args))
		if !got.IsError {
			t.Errorf("%s should reject missing required arguments", tc.name)
		}
	}
}
