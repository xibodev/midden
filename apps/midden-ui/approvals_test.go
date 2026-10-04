package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xibodev/compa/pkg/agent"
)

func approvalHost(t *testing.T) (*App, *kernelHost) {
	t.Helper()
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app, &kernelHost{app: app}
}

func eventsOfType(app *App, kind string) []Event {
	app.mu.Lock()
	defer app.mu.Unlock()
	out := []Event{}
	for _, event := range app.events {
		if event.Type == kind {
			out = append(out, event)
		}
	}
	return out
}

func pendingPermission(t *testing.T, app *App) *permission {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		for _, p := range app.permissions {
			app.mu.Unlock()
			return p
		}
		app.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no permission request appeared")
	return nil
}

type approvalOutcome struct {
	decision agent.ApprovalDecision
	err      error
}

func approveAsync(host *kernelHost, tool string, arguments map[string]any) chan approvalOutcome {
	done := make(chan approvalOutcome, 1)
	go func() {
		decision, err := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: tool, Arguments: arguments})
		done <- approvalOutcome{decision, err}
	}()
	return done
}

func awaitApproval(t *testing.T, done chan approvalOutcome) approvalOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatal("approval did not finish")
		return approvalOutcome{}
	}
}

func TestMiddenReadsAndCacheWritesRunWithoutAPermissionCard(t *testing.T) {
	app, host := approvalHost(t)
	cases := []struct {
		args   any
		effect coreEffect
	}{
		{[]any{"ls", "--json"}, effectReadOnly},
		{[]string{"collection", "inspect", "sources", "--json"}, effectReadOnly},
		{[]any{"resume", "0b5c2d1e"}, effectReadOnly},
		{[]any{"read", "--tool", "claude", "--session", "s", "--json"}, effectWritesCache},
		{[]any{"brief", "--tool", "claude", "--session", "s", "--handoff"}, effectWritesCache},
	}
	for _, tc := range cases {
		decision, err := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "midden", Arguments: map[string]any{"args": tc.args}})
		if err != nil || !decision.Approved {
			t.Fatalf("%v was not approved: %+v %v", tc.args, decision, err)
		}
	}
	if permissions := eventsOfType(app, "permission"); len(permissions) != 0 {
		t.Fatalf("deterministic data calls asked the operator: %+v", permissions)
	}
	pending := eventsOfType(app, "tool")
	if len(pending) != len(cases) {
		t.Fatalf("got %d tool events, want %d", len(pending), len(cases))
	}
	for i, event := range pending {
		if event.Status != "pending" || event.Effect != string(cases[i].effect) || event.CallID == "" {
			t.Fatalf("pending event %d = %+v, want effect %s with a call id", i, event, cases[i].effect)
		}
	}
}

func TestMiddenWorkspaceWritesUseTheOperatorCard(t *testing.T) {
	app, host := approvalHost(t)
	arguments := map[string]any{"args": []any{"collect", "--view", "v-synthetic", "--out", "sources", "--json"}}
	done := approveAsync(host, "midden", arguments)
	p := pendingPermission(t, app)
	cards := eventsOfType(app, "permission")
	pending := eventsOfType(app, "tool")
	if len(cards) != 1 || len(pending) != 1 {
		t.Fatalf("want one card and one pending event, got %+v and %+v", cards, pending)
	}
	if cards[0].Effect != string(effectWritesWorkspace) || pending[0].Effect != string(effectWritesWorkspace) {
		t.Fatalf("workspace write not labelled: %+v %+v", cards[0], pending[0])
	}
	if cards[0].CallID == "" || cards[0].CallID != pending[0].CallID {
		t.Fatalf("card and pending event do not share a call id: %q %q", cards[0].CallID, pending[0].CallID)
	}
	if err := app.Decide(p.ID, true); err != nil {
		t.Fatal(err)
	}
	outcome := awaitApproval(t, done)
	if outcome.err != nil || !outcome.decision.Approved {
		t.Fatalf("allowed write was not approved: %+v", outcome)
	}

	done = approveAsync(host, "midden", map[string]any{"args": []any{"collection", "export", "sources", "--format", "markdown", "--out", "notes.md"}})
	p = pendingPermission(t, app)
	if err := app.Decide(p.ID, false); err != nil {
		t.Fatal(err)
	}
	outcome = awaitApproval(t, done)
	if outcome.decision.Approved || !strings.Contains(outcome.decision.Reason, "Denied by operator") {
		t.Fatalf("denied write was approved: %+v", outcome)
	}
	tools := eventsOfType(app, "tool")
	last := tools[len(tools)-1]
	if last.Status != "failed" || !strings.Contains(last.Result, "Denied by operator") || last.CallID != tools[len(tools)-2].CallID {
		t.Fatalf("denied call was not closed with the model's text: %+v", last)
	}
}

func TestInvalidMiddenArgumentsAreDeniedWithTheValidationMessage(t *testing.T) {
	app, host := approvalHost(t)
	cases := []struct {
		arguments map[string]any
		message   string
	}{
		{map[string]any{"args": "ls --json"}, "args must be a string array"},
		{map[string]any{}, "args must be a string array"},
		{map[string]any{"args": []any{"ls", 7}}, "args must contain strings"},
		{map[string]any{"args": []any{"prune", "--execute"}}, "command is not available"},
		{map[string]any{"args": []any{"read", "--state", "elsewhere"}}, "controlled by the host"},
		{map[string]any{"args": []any{"collection", "read", "../outside"}}, "inside the workspace"},
		{map[string]any{"args": []any{"collect", "--view", "v", "--out", "../outside"}}, "inside the workspace"},
	}
	for _, tc := range cases {
		decision, err := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "midden", Arguments: tc.arguments})
		if err != nil || decision.Approved || !strings.Contains(decision.Reason, tc.message) {
			t.Fatalf("%v: decision %+v, %v; want a denial mentioning %q", tc.arguments, decision, err, tc.message)
		}
	}
	if permissions := eventsOfType(app, "permission"); len(permissions) != 0 {
		t.Fatalf("invalid calls reached the operator: %+v", permissions)
	}
	events := eventsOfType(app, "tool")
	if len(events) != 2*len(cases) {
		t.Fatalf("got %d tool events, want a pending and a failed event per call", len(events))
	}
	for i, tc := range cases {
		pending, failed := events[2*i], events[2*i+1]
		if pending.Status != "pending" || failed.Status != "failed" || pending.CallID == "" || pending.CallID != failed.CallID {
			t.Fatalf("call %d events do not pair: %+v %+v", i, pending, failed)
		}
		if !strings.Contains(failed.Result, tc.message) || failed.Effect != "" {
			t.Fatalf("call %d failure does not carry the validation message: %+v", i, failed)
		}
	}
}

func TestFileToolApprovalsAreUnchanged(t *testing.T) {
	app, host := approvalHost(t)
	if err := os.WriteFile(filepath.Join(app.opts.Workspace, "notes.md"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	decision, err := host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "read_file", Arguments: map[string]any{"path": "notes.md"}})
	if err != nil || !decision.Approved {
		t.Fatalf("workspace read needs no card: %+v %v", decision, err)
	}
	if len(eventsOfType(app, "permission")) != 0 {
		t.Fatal("workspace read asked the operator")
	}
	done := approveAsync(host, "write_file", map[string]any{"path": "draft.md", "content": "synthetic"})
	p := pendingPermission(t, app)
	cards := eventsOfType(app, "permission")
	if len(cards) != 1 || cards[0].Tool != "write_file" || cards[0].Effect != "" {
		t.Fatalf("file write card changed: %+v", cards)
	}
	if err = app.Decide(p.ID, true); err != nil {
		t.Fatal(err)
	}
	if outcome := awaitApproval(t, done); !outcome.decision.Approved {
		t.Fatalf("allowed file write denied: %+v", outcome)
	}
	decision, _ = host.ApproveTool(context.Background(), &agent.ToolApprovalRequest{Tool: "write_file", Arguments: map[string]any{"path": "../outside.md"}})
	if decision.Approved {
		t.Fatal("file write outside the workspace approved")
	}
}
