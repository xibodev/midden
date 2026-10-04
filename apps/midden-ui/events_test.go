package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xibodev/compa/pkg/agent"
	"github.com/xibodev/compa/pkg/tools"
)

func finishTool(t *testing.T, host *kernelHost, tool string, arguments map[string]any, result *tools.ToolResult) Event {
	t.Helper()
	if _, _, err := host.AfterTool(context.Background(), &agent.ToolResultHookResponse{Tool: tool, Arguments: arguments, Result: result}); err != nil {
		t.Fatal(err)
	}
	events := eventsOfType(host.app, "tool")
	return events[len(events)-1]
}

func TestToolEventsCarryCallIDEffectAndResult(t *testing.T) {
	app, host := approvalHost(t)
	arguments := map[string]any{"args": []any{"ls", "--json"}}
	if decision, err := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "midden", Arguments: arguments}); err != nil || !decision.Approved {
		t.Fatalf("read-only call denied: %+v %v", decision, err)
	}
	pending := eventsOfType(app, "tool")[0]
	done := finishTool(t, host, "midden", arguments, tools.SilentResult(`{"sessions":[],"total":0}`))
	if done.Status != "completed" || done.CallID == "" || done.CallID != pending.CallID {
		t.Fatalf("completion does not pair with its pending event: %+v %+v", pending, done)
	}
	if done.Effect != string(effectReadOnly) || done.Result != `{"sessions":[],"total":0}` || done.ResultTruncated {
		t.Fatalf("completion lost effect or result: %+v", done)
	}
	raw, err := json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["callId"] != done.CallID || wire["effect"] != "read-only" || wire["result"] != done.Result {
		t.Fatalf("event wire fields: %s", raw)
	}
	if _, present := wire["resultTruncated"]; present {
		t.Fatalf("untruncated result reported truncation: %s", raw)
	}
}

func TestToolEventResultsAreBoundedAndFailuresMarked(t *testing.T) {
	app, host := approvalHost(t)
	if err := os.WriteFile(filepath.Join(app.opts.Workspace, "notes.md"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"path": "notes.md"}
	if decision, _ := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "read_file", Arguments: arguments}); !decision.Approved {
		t.Fatal("workspace read denied")
	}
	large := strings.Repeat("€", eventResultLimit)
	done := finishTool(t, host, "read_file", arguments, tools.SilentResult(large))
	if !done.ResultTruncated || len(done.Result) > eventResultLimit || len(done.Result) < eventResultLimit-4 || !utf8.ValidString(done.Result) {
		t.Fatalf("result not clipped at a character boundary: %d bytes, truncated=%t", len(done.Result), done.ResultTruncated)
	}
	if done.Effect != "" || done.CallID == "" {
		t.Fatalf("file tool event: effect %q, call id %q", done.Effect, done.CallID)
	}

	host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "read_file", Arguments: arguments})
	failed := finishTool(t, host, "read_file", arguments, tools.ErrorResult("synthetic read failure"))
	if failed.Status != "failed" || failed.Result != "synthetic read failure" {
		t.Fatalf("failure not reported with the model's text: %+v", failed)
	}
}

func TestCompletedMiddenWriteKeepsItsEffectAfterTheDestinationExists(t *testing.T) {
	app, host := approvalHost(t)
	arguments := map[string]any{"args": []any{"collect", "--view", "v-synthetic", "--out", "sources", "--json"}}
	done := approveAsync(host, "midden", arguments)
	p := pendingPermission(t, app)
	card := eventsOfType(app, "permission")[0]
	if err := app.Decide(p.ID, true); err != nil {
		t.Fatal(err)
	}
	awaitApproval(t, done)
	if err := os.Mkdir(filepath.Join(app.opts.Workspace, "sources"), 0700); err != nil {
		t.Fatal(err)
	}
	event := finishTool(t, host, "midden", arguments, tools.SilentResult(`{"record_count":1}`))
	if event.Effect != string(effectWritesWorkspace) || event.CallID == "" || event.CallID != card.CallID {
		t.Fatalf("completed write event: %+v (card %+v)", event, card)
	}
}

func TestResultsWithoutAMatchingApprovalHaveNoCallID(t *testing.T) {
	_, host := approvalHost(t)
	arguments := map[string]any{"args": []any{"ls", "--json"}}
	host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "midden", Arguments: arguments})
	if event := finishTool(t, host, "read_file", map[string]any{"path": "notes.md"}, tools.SilentResult("text")); event.CallID != "" {
		t.Fatalf("another tool's result took the call id: %+v", event)
	}
	if event := finishTool(t, host, "midden", arguments, tools.SilentResult("{}")); event.CallID != "" {
		t.Fatalf("a released call id was reused: %+v", event)
	}
}
