package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func padOutcomeHistory(t *testing.T, app *App, id string, target int) {
	t.Helper()
	s := app.sessions[id]
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < target/64000; i++ {
		s.Messages = append(s.Messages, Message{Role: "user", Content: strings.Repeat("x", 60000), At: stamp})
	}
	length := func() int {
		raw, err := json.MarshalIndent(app.sessions, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return len(raw) + 1
	}
	for target-length() > 61000 {
		s.Messages = append(s.Messages, Message{Role: "user", Content: strings.Repeat("x", 60000), At: stamp})
	}
	s.Messages = append(s.Messages, Message{Role: "user", At: stamp})
	difference := target - length()
	if difference >= 0 {
		s.Messages[len(s.Messages)-1].Content = strings.Repeat("x", difference)
	} else {
		previous := &s.Messages[len(s.Messages)-2]
		previous.Content = previous.Content[:len(previous.Content)+difference]
	}
	for _, message := range s.Messages {
		if len(message.Content) > 64000 {
			t.Fatal("history fixture must respect individual request limits")
		}
	}
	if length() != target {
		t.Fatal("history fixture did not reach the exact byte target")
	}
}

func TestTurnAdmissionReservesTerminalOutcomeSpace(t *testing.T) {
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	s, _ := app.NewSession("near limit")
	padOutcomeHistory(t, app, s.ID, (8<<20)-1024)
	if err := app.saveSessionsLocked(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(app.opts.State, "sessions.json")
	before, _ := os.ReadFile(path)
	app.model = Model{Provider: "openai", Model: "fixture"}
	app.runtime = outcomeEngine{process: func(context.Context, string, string) (string, error) {
		return "", errors.New("synthetic failure")
	}}
	if _, err := app.StartTurn(s.ID, "Another request."); err == nil {
		t.Fatal("admitted a turn without capacity for its bounded terminal outcome")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected admission changed previously loadable history")
	}
}

func TestConcurrentMetadataCannotConsumePendingOutcomeReserve(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := app.NewSession("near limit")
	padOutcomeHistory(t, app, s.ID, (8<<20)-20000)
	release := make(chan struct{})
	app.model = Model{Provider: "openai", Model: "fixture"}
	app.runtime = outcomeEngine{process: func(ctx context.Context, _, _ string) (string, error) {
		select {
		case <-release:
			return "", errors.New(strings.Repeat("\x01", 2000))
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	defer app.Close()
	if _, err := app.StartTurn(s.ID, "Produce a result."); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := app.NewSession(strings.Repeat("\x01", 120)); err != nil {
			break
		}
	}
	close(release)
	app.wg.Wait()
	app.Close()
	reopened, err := NewApp(opts)
	if err != nil {
		t.Fatalf("accepted history became impossible to reopen: %v", err)
	}
	defer reopened.Close()
	outcomes := observedOutcomes(t, reopened, s.ID)
	if len(outcomes) != 1 || outcomes[0].Status != "failed" || len(outcomes[0].Error) != 2000 {
		t.Fatalf("bounded terminal error was not retained: %+v", outcomes)
	}
}
