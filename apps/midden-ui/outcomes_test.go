package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type outcomeEngine struct {
	process func(context.Context, string, string) (string, error)
}

func (e outcomeEngine) Process(ctx context.Context, text, id string) (string, error) {
	return e.process(ctx, text, id)
}
func (outcomeEngine) Close() {}

type observedOutcome struct {
	TurnID       string `json:"turnId"`
	Status       string `json:"status"`
	Error        string `json:"error"`
	At           string `json:"at"`
	MessageIndex int    `json:"messageIndex"`
}

func observedOutcomes(t *testing.T, app *App, id string) []observedOutcome {
	t.Helper()
	s, err := app.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Outcomes []observedOutcome `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	return saved.Outcomes
}

func TestFailedTurnOutcomeSurvivesRestart(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	app.model = Model{Provider: "openai", Model: "fixture"}
	app.runtime = outcomeEngine{process: func(context.Context, string, string) (string, error) {
		return "", errors.New("synthetic provider refused the request")
	}}
	s, _ := app.NewSession("failure")
	turn, err := app.StartTurn(s.ID, "Write a synthetic note.")
	if err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	app.Close()
	reopened, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	outcomes := observedOutcomes(t, reopened, s.ID)
	if len(outcomes) != 1 || outcomes[0].TurnID != turn || outcomes[0].Status != "failed" ||
		!strings.Contains(outcomes[0].Error, "synthetic provider refused") || outcomes[0].MessageIndex != 0 || outcomes[0].At == "" {
		t.Fatalf("failed outcome was not preserved with its request: %+v", outcomes)
	}
	history, _ := reopened.Session(s.ID)
	if len(history.Messages) != 1 || history.Messages[0].Role != "user" {
		t.Fatal("host failure must not masquerade as assistant-authored prose")
	}
}

func TestCancelledTurnHasADurableOutcome(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.model = Model{Provider: "openai", Model: "fixture"}
	app.runtime = outcomeEngine{process: func(ctx context.Context, _, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	s, _ := app.NewSession("cancel")
	turn, err := app.StartTurn(s.ID, "Wait for my decision.")
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Cancel(turn); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	outcomes := observedOutcomes(t, app, s.ID)
	if len(outcomes) != 1 || outcomes[0].Status != "cancelled" || !strings.Contains(outcomes[0].Error, "not undone") {
		t.Fatalf("cancel outcome is missing: %+v", outcomes)
	}
}

func TestRestartMarksUnfinishedTurnInterrupted(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	raw := `{"s":{"id":"s","title":"Interrupted work","updated":"2026-01-01T12:00:00Z","messages":[{"role":"user","content":"Write a note.","at":"2026-01-01T12:00:00Z"}],"outcomes":[{"turnId":"unfinished","status":"running","at":"2026-01-01T12:00:00Z","messageIndex":0}]}}`
	if err := os.WriteFile(filepath.Join(opts.State, "sessions.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	outcomes := observedOutcomes(t, reopened, "s")
	if len(outcomes) != 1 || outcomes[0].Status != "interrupted" || outcomes[0].Error == "" {
		t.Fatalf("unfinished turn was silently treated as ready: %+v", outcomes)
	}
	persisted, err := os.ReadFile(filepath.Join(opts.State, "sessions.json"))
	if err != nil || !strings.Contains(string(persisted), `"interrupted"`) {
		t.Fatal("interrupted outcome was not saved", err)
	}
}

func TestWorkspaceIdentityIsStableAndStateScoped(t *testing.T) {
	opts := testOptions(t)
	a, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := a.Status()["workspaceId"].(string)
	a.Close()
	b, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := b.Status()["workspaceId"].(string)
	b.Close()
	opts.State += "-other"
	c, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	other, _ := c.Status()["workspaceId"].(string)
	if id == "" || id != same || id == other || strings.Contains(id, opts.Workspace) {
		t.Fatal("browser drafts cannot be scoped to a stable opaque workspace/state identity")
	}
}
