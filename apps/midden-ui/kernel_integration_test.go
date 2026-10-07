package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kernelModel answers like an OpenAI-compatible model for the real kernel:
// a turn whose text asks for a tool calls exec once, a "slow" turn waits, and
// anything else answers at once.
func kernelModel(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"fixture","object":"model"}]}`)
			return
		}
		var request struct {
			Messages []map[string]any `json:"messages"`
			Stream   bool             `json:"stream"`
			Tools    []any            `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		last := 0
		for i, message := range request.Messages {
			if message["role"] == "user" {
				last = i
			}
		}
		user, results := fmt.Sprint(request.Messages[last]["content"]), []string{}
		for _, message := range request.Messages[last:] {
			if message["role"] == "tool" {
				results = append(results, fmt.Sprint(message["content"]))
			}
		}
		if strings.Contains(user, "slow") {
			select {
			case <-time.After(20 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		message := map[string]any{"role": "assistant", "content": "Plain answer."}
		finish := "stop"
		if _, path, ok := strings.Cut(user, "READ:"); ok && len(results) == 0 && len(request.Tools) > 0 {
			arguments, _ := json.Marshal(map[string]string{"path": strings.TrimSpace(path)})
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call-read", "type": "function", "index": 0,
				"function": map[string]any{"name": "read_file", "arguments": string(arguments)}}}}
			finish = "tool_calls"
		} else if strings.Contains(user, "tool") && len(results) == 0 && len(request.Tools) > 0 {
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call-1", "type": "function", "index": 0,
				"function": map[string]any{"name": "exec", "arguments": `{"action":"run","command":"echo kernel-check"}`}}}}
			finish = "tool_calls"
		} else if len(results) > 0 {
			message["content"] = "Tool said: " + strings.TrimSpace(results[0])
		}
		choice := map[string]any{"index": 0, "finish_reason": finish}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			choice["delta"] = message
			raw, _ := json.Marshal(map[string]any{"choices": []any{choice}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			return
		}
		choice["message"] = message
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{choice}})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTheAppDrivesTheRealKernel(t *testing.T) {
	executable := os.Getenv("MIDDEN_TEST_KERNEL")
	if executable == "" {
		t.Skip("set MIDDEN_TEST_KERNEL to a compa-kernel 3.0.0 executable to run the App against it")
	}
	model := kernelModel(t)
	opts := testOptions(t)
	opts.Kernel, opts.KernelVersion = executable, "3.0.0"
	opts.Skills = filepath.Join(t.TempDir(), "skills")
	writeTestFile(t, filepath.Join(opts.Skills, "midden-check", "SKILL.md"), "---\nname: midden-check\ndescription: Use when checking.\n---\n# Check\n")
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	storeTestModel(t, app, model.URL+"/v1", "")
	// The person's own policy asks before exec, so the approval relay runs.
	cfg, err := loadKernelConfig(app.paths.Kernel)
	if err != nil {
		t.Fatal(err)
	}
	cfg.other["tools"] = json.RawMessage(`{"approval":{"rules":[{"tool":"exec","action":"ask"}]}}`)
	if err := saveKernelConfig(app.paths.Kernel, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIDDEN_UI_TEST_KERNEL_HOOK", "1")
	setup := app.kernelSetup(os.Args[0])
	setup.Hook = []string{os.Args[0]}
	kernel, err := newKernelProcess(setup)
	if err != nil {
		t.Fatal(err)
	}
	app.attachKernel(kernel)
	kernel.Start()
	ctx, cancel := context.WithTimeout(context.Background(), kernelStartTimeout)
	defer cancel()
	if err := kernel.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	events := watchEvents(app)

	s, _ := app.NewSession("real kernel")
	if _, err := app.StartTurn(s.ID, "please run a tool"); err != nil {
		t.Fatal(err)
	}
	asked := waitEvent(t, events, func(e Event) bool { return e.Type == "permission" })
	if asked.Tool != "exec" {
		t.Fatalf("permission = %+v", asked)
	}
	if err := app.Decide(asked.PermissionID, true); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, func(e Event) bool { return e.Type == "tool" && e.Status == "completed" && e.Tool == "exec" })
	waitEvent(t, events, func(e Event) bool { return e.Type == "turn_done" })
	session, _ := app.Session(s.ID)
	if session.Outcomes[0].Status != "completed" || !strings.Contains(session.Messages[len(session.Messages)-1].Content, "kernel-check") {
		t.Fatalf("tool turn = %+v %+v", session.Outcomes, session.Messages)
	}

	if _, err := app.StartTurn(s.ID, "slow question"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	app.mu.Lock()
	turn := app.active.ID
	app.mu.Unlock()
	stopped := time.Now()
	if err := app.Cancel(turn); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, func(e Event) bool { return e.Type == "turn_done" && e.TurnID == turn })
	if elapsed := time.Since(stopped); elapsed > 10*time.Second {
		t.Fatalf("stopping took %s", elapsed)
	}
	if session, _ = app.Session(s.ID); session.Outcomes[1].Status != "cancelled" {
		t.Fatalf("stopped turn = %+v", session.Outcomes[1])
	}

	if err := app.setDefaultModel(context.Background(), "fixture/fixture"); err != nil {
		t.Fatalf("a model change did not reach the kernel: %v", err)
	}
	if _, err := app.StartTurn(s.ID, "plain question"); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, func(e Event) bool { return e.Type == "turn_done" })
	if session, _ = app.Session(s.ID); session.Outcomes[2].Status != "completed" || session.Messages[len(session.Messages)-1].Content != "Plain answer." {
		t.Fatalf("turn after reload = %+v", session.Outcomes[2])
	}
	// The agent reads the installed skills, but not the kernel's own settings.
	read := func(path string) string {
		t.Helper()
		if _, err := app.StartTurn(s.ID, "please READ:"+path); err != nil {
			t.Fatal(err)
		}
		waitEvent(t, events, func(e Event) bool { return e.Type == "turn_done" })
		session, _ := app.Session(s.ID)
		return session.Messages[len(session.Messages)-1].Content
	}
	if answer := read(filepath.Join(opts.Skills, "midden-check", "SKILL.md")); !strings.Contains(answer, "name: midden-check") {
		t.Fatalf("the agent could not read an installed skill: %q", answer)
	}
	if answer := read(kernelConfigPath(app.paths.Kernel)); strings.Contains(answer, "provider_instances") {
		t.Fatalf("the agent read the kernel's settings: %q", answer)
	}
	if text := readTestFile(t, filepath.Join(app.paths.Workspace, "AGENT.md")); !strings.Contains(text, "name: Midden") {
		t.Fatal("the kernel's workspace lacks Midden's instructions")
	}
	app.Close()
	if _, err := readKernelPid(app.paths.Kernel); !os.IsNotExist(err) {
		t.Fatalf("the kernel did not stop with the App: %v", err)
	}
}
