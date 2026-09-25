package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xibodev/facet-studio/pkg/config"
)

func kernelApp(t *testing.T) (*App, *httptest.Server, *atomic.Int32) {
	t.Helper()
	opts := testOptions(t)
	t.Setenv(config.EnvHome, filepath.Join(opts.State, "kernel"))
	bundle, err := filepath.Abs(filepath.Join("..", "..", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	opts.Bundle = bundle
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.Error(w, "unexpected path", 404)
			return
		}
		var body struct {
			Messages []struct{ Role, Content string }
			Tools    []any
			Stream   bool
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		boundWorkspace := false
		for _, message := range body.Messages {
			if message.Role == "system" && strings.Contains(message.Content, opts.Workspace) {
				boundWorkspace = true
			}
		}
		if !boundWorkspace {
			t.Error("actual kernel request lost the artifact workspace binding")
		}
		calls.Add(1)
		hasResult := false
		for _, m := range body.Messages {
			if m.Role == "tool" {
				hasResult = true
			}
		}
		message := map[string]any{"role": "assistant"}
		finish := "stop"
		if !hasResult {
			message["tool_calls"] = []any{map[string]any{"id": "fixture-call", "type": "function", "function": map[string]any{"name": "write_file", "arguments": `{"path":"article.md","content":"# Synthetic article\n\nZero is a value, not absence.\n"}`}}}
			finish = "tool_calls"
		} else {
			message["content"] = "Saved the synthetic article in article.md."
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
		} else {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
		}
	}))
	app, err := NewApp(opts)
	if err != nil {
		provider.Close()
		t.Fatal(err)
	}
	if err = app.SetModel(ModelInput{Provider: "openai", Model: "fixture", Endpoint: provider.URL}); err != nil {
		app.Close()
		provider.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close(); provider.Close() })
	return app, provider, &calls
}

func awaitPermission(t *testing.T, app *App) *permission {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		for _, p := range app.permissions {
			app.mu.Unlock()
			return p
		}
		active := app.active != nil
		app.mu.Unlock()
		if !active {
			t.Fatal("turn ended before the expected permission request")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("permission request timed out")
	return nil
}
func awaitIdle(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		idle := app.active == nil
		app.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("turn did not finish")
}
func TestRealKernelRequiresApprovalBeforeSavingAnArtifact(t *testing.T) {
	app, _, calls := kernelApp(t)
	s, err := app.NewSession("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.StartTurn(s.ID, "Write an article from the installed bundle."); err != nil {
		t.Fatal(err)
	}
	p := awaitPermission(t, app)
	if _, err = os.Stat(filepath.Join(app.opts.Workspace, "article.md")); !os.IsNotExist(err) {
		t.Fatal("file written before permission")
	}
	if err = app.Decide(p.ID, true); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, app)
	raw, err := os.ReadFile(filepath.Join(app.opts.Workspace, "article.md"))
	if err != nil || !strings.Contains(string(raw), "Zero is a value") {
		t.Fatal("kernel did not execute the approved tool", err)
	}
	saved, err := app.Session(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 2 || calls.Load() < 2 {
		t.Fatal("kernel tool-result loop/history was not exercised")
	}
}
func TestCancellingAPermissionWaitDoesNotWriteTheFile(t *testing.T) {
	app, _, _ := kernelApp(t)
	s, _ := app.NewSession("cancel")
	turn, err := app.StartTurn(s.ID, "Write an article.")
	if err != nil {
		t.Fatal(err)
	}
	awaitPermission(t, app)
	if err = app.Cancel(turn); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, app)
	if _, err = os.Stat(filepath.Join(app.opts.Workspace, "article.md")); !os.IsNotExist(err) {
		t.Fatal("cancelled tool wrote a file")
	}
}
func TestDeniedOperationDoesNotWriteTheFile(t *testing.T) {
	app, _, _ := kernelApp(t)
	s, _ := app.NewSession("deny")
	if _, err := app.StartTurn(s.ID, "Write an article."); err != nil {
		t.Fatal(err)
	}
	p := awaitPermission(t, app)
	if err := app.Decide(p.ID, false); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, app)
	if _, err := os.Stat(filepath.Join(app.opts.Workspace, "article.md")); !os.IsNotExist(err) {
		t.Fatal("denied tool wrote a file")
	}
}
