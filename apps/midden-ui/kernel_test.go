package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for midden-ui kernel-hook when the
// real kernel starts it.
func TestMain(m *testing.M) {
	if os.Getenv("MIDDEN_UI_TEST_KERNEL_HOOK") == "1" {
		if err := runKernelHook(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// lockedBuffer is a buffer the hook's replies and the test share.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// recordingObserver records what the relay hands on and answers approvals
// with answer.
type recordingObserver struct {
	mu       sync.Mutex
	events   []kernelEvent
	requests []kernelApprovalRequest
	answer   kernelApprovalAnswer
}

func (o *recordingObserver) kernelEvent(event kernelEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingObserver) kernelApproval(_ context.Context, request kernelApprovalRequest) kernelApprovalAnswer {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, request)
	return o.answer
}

func testKernelProcess(t *testing.T, observer kernelObserver) *kernelProcess {
	t.Helper()
	home := t.TempDir()
	kernel, err := newKernelProcess(kernelSetup{Home: home, Workspace: t.TempDir(), Log: home + "/kernel.log"})
	if err != nil {
		t.Fatal(err)
	}
	kernel.observer = observer
	t.Cleanup(kernel.Close)
	return kernel
}

func relayPost(t *testing.T, kernel *kernelProcess, secret, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, kernel.relayURL(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestRelayHandsEventsAndApprovalsToTheObserver(t *testing.T) {
	observer := &recordingObserver{answer: kernelApprovalAnswer{Approved: true, Reason: "fine"}}
	kernel := testKernelProcess(t, observer)
	event := `{"jsonrpc":"2.0","method":"hook.runtime_event","params":{"kind":"agent.turn.end","scope":{"chat_id":"web:s","message_id":"t"},"payload":{"Status":"completed","FinalContent":"done"}}}`
	if response := relayPost(t, kernel, "wrong", event); response.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong secret: %d", response.StatusCode)
	}
	if response := relayPost(t, kernel, kernel.relaySecret, event); response.StatusCode != http.StatusOK {
		t.Fatalf("event: %d", response.StatusCode)
	}
	approval := `{"jsonrpc":"2.0","id":3,"method":"hook.approve_tool","params":{"tool":"exec","arguments":{"command":"ls"},
		"context":{"inbound":{"chat_id":"web:s","message_id":"t"}}}}`
	response := relayPost(t, kernel, kernel.relaySecret, approval)
	var answer kernelApprovalAnswer
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil || !answer.Approved || answer.Reason != "fine" {
		t.Fatalf("approval answer = %+v %v", answer, err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) != 1 || observer.events[0].Kind != "agent.turn.end" || observer.events[0].ChatID != "web:s" ||
		observer.events[0].MessageID != "t" || observer.events[0].Payload["FinalContent"] != "done" {
		t.Fatalf("events = %+v", observer.events)
	}
	if len(observer.requests) != 1 || observer.requests[0].Tool != "exec" || observer.requests[0].MessageID != "t" ||
		observer.requests[0].Arguments["command"] != "ls" {
		t.Fatalf("requests = %+v", observer.requests)
	}
}

func TestKernelHookRelaysInOrderAndAnswersHelloItself(t *testing.T) {
	var mu sync.Mutex
	var received []string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hook-secret" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var message struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &message)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		if message.Method == "hook.approve_tool" {
			w.Write([]byte(`{"approved":true,"reason":"yes"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer relay.Close()
	t.Setenv(envKernelRelay, relay.URL+"/relay")
	t.Setenv(envKernelRelaySecret, "hook-secret")
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"name":"midden","version":1}}`,
		`{"jsonrpc":"2.0","method":"hook.runtime_event","params":{"kind":"one"}}`,
		`{"jsonrpc":"2.0","method":"hook.runtime_event","params":{"kind":"two"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"hook.approve_tool","params":{"tool":"exec"}}`,
		`not json`,
	}, "\n") + "\n"
	reader, writer := io.Pipe()
	var output lockedBuffer
	done := make(chan error, 1)
	go func() { done <- runKernelHook(reader, &output) }()
	io.WriteString(writer, input)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		count := len(received)
		mu.Unlock()
		if count == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 || !strings.Contains(received[0]+received[1], `"kind":"one"`) || strings.Contains(strings.Join(received, ""), "hook.hello") {
		t.Fatalf("relayed = %v", received)
	}
	one, two := strings.Index(strings.Join(received, "|"), `"one"`), strings.Index(strings.Join(received, "|"), `"two"`)
	if one < 0 || two < one {
		t.Fatalf("events lost their order: %v", received)
	}
	answers := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(output.String()))
	for scanner.Scan() {
		var reply struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			t.Fatalf("the hook wrote something other than a reply: %q", scanner.Text())
		}
		answers[string(reply.ID)] = string(reply.Result)
	}
	if answers["1"] != "{}" || answers["2"] != `{"approved":true,"reason":"yes"}` || len(answers) != 2 {
		t.Fatalf("answers = %v", answers)
	}
}

func TestKernelHookDeniesWhenMiddenIsUnreachable(t *testing.T) {
	t.Setenv(envKernelRelay, "http://127.0.0.1:1/relay")
	t.Setenv(envKernelRelaySecret, "hook-secret")
	var output lockedBuffer
	input := `{"jsonrpc":"2.0","id":7,"method":"hook.approve_tool","params":{"tool":"exec"}}` + "\n"
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runKernelHook(reader, &output) }()
	io.WriteString(writer, input)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(output.String(), `"id":7`) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	writer.Close()
	<-done
	if !strings.Contains(output.String(), `"result":{"approved":false,"reason":"Midden is not reachable"}`) {
		t.Fatalf("output = %s", output.String())
	}
	os.Unsetenv(envKernelRelay)
	if err := runKernelHook(strings.NewReader(""), io.Discard); err == nil {
		t.Fatal("the hook ran without its relay")
	}
}

func TestKernelSettingsAddOnlyTheWebChatAndTheHook(t *testing.T) {
	home := t.TempDir()
	original := `{"channel_list":{"telegram":{"enabled":true,"type":"telegram"},"web":{"enabled":false,"type":"web"}},
"hooks":{"enabled":false,"defaults":{"approval_timeout_ms":300000},"processes":{"theirs":{"enabled":true,"command":["x"],"observe":["*"]}}},
"tools":{"exec":{"timeout_seconds":90}},"agents":{"defaults":{"model_name":"a/b"}}}`
	if err := os.WriteFile(kernelConfigPath(home), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	setup := kernelSetup{Home: home, Hook: []string{"/install/midden-ui", "kernel-hook"}}
	timeout, err := prepareKernelSettings(setup, "http://127.0.0.1:5/relay", "relay-secret")
	if err != nil {
		t.Fatal(err)
	}
	if timeout != 5*time.Minute {
		t.Fatalf("approval timeout = %s, want the configured 5m", timeout)
	}
	raw, _ := os.ReadFile(kernelConfigPath(home))
	var saved struct {
		Channels map[string]map[string]any `json:"channel_list"`
		Hooks    struct {
			Enabled   bool                      `json:"enabled"`
			Defaults  map[string]any            `json:"defaults"`
			Processes map[string]map[string]any `json:"processes"`
		} `json:"hooks"`
		Tools  map[string]any `json:"tools"`
		Agents map[string]any `json:"agents"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	web := saved.Channels["web"]
	settings, _ := web["settings"].(map[string]any)
	streaming, _ := settings["streaming"].(map[string]any)
	if web["enabled"] != true || web["type"] != "web" || streaming["enabled"] != true || saved.Channels["telegram"]["enabled"] != true {
		t.Fatalf("channels = %v", saved.Channels)
	}
	midden := saved.Hooks.Processes["midden"]
	env, _ := midden["env"].(map[string]any)
	if !saved.Hooks.Enabled || midden["enabled"] != true || fmt.Sprint(midden["command"]) != "[/install/midden-ui kernel-hook]" ||
		env[envKernelRelay] != "http://127.0.0.1:5/relay" || env[envKernelRelaySecret] != "relay-secret" ||
		fmt.Sprint(midden["intercept"]) != "[approve_tool]" || saved.Hooks.Processes["theirs"] == nil || saved.Hooks.Defaults == nil {
		t.Fatalf("hooks = %+v", saved.Hooks)
	}
	if saved.Tools["exec"] == nil || saved.Agents["defaults"] == nil {
		t.Fatal("settings Midden does not own changed")
	}
	if strings.Contains(string(raw), "token") {
		t.Fatal("the web chat token was written to config.json")
	}
}

func TestKernelVersionIsReadFromItsOutput(t *testing.T) {
	if _, err := probeKernelVersion("/nonexistent/compa-kernel", t.TempDir()); err == nil {
		t.Fatal("a missing kernel reported a version")
	}
	if path := os.Getenv("MIDDEN_TEST_KERNEL"); path != "" {
		version, err := probeKernelVersion(path, t.TempDir())
		if err != nil || version != "3.0.0" {
			t.Fatalf("version = %q %v", version, err)
		}
	}
}
