package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeGateway accepts the kernel's web chat socket as compa-kernel does and
// records what the App sends on it.
type fakeGateway struct {
	*httptest.Server
	token string
	sent  chan map[string]any
	conns chan *websocket.Conn
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	gateway := &fakeGateway{token: "web-token", sent: make(chan map[string]any, 16), conns: make(chan *websocket.Conn, 4)}
	upgrader := websocket.Upgrader{}
	gateway.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/web/ws" || r.Header.Get("Authorization") != "Bearer "+gateway.token || r.URL.Query().Get("session_id") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		gateway.conns <- conn
		for {
			var frame map[string]any
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			gateway.sent <- frame
		}
	}))
	t.Cleanup(gateway.Close)
	return gateway
}

// readyKernel is a kernel the chat bridge sees running at gateway.
func readyKernel(t *testing.T, gateway *fakeGateway) *kernelProcess {
	t.Helper()
	kernel := testKernelProcess(t, nil)
	closed := make(chan struct{})
	close(closed)
	kernel.mu.Lock()
	kernel.state, kernel.ready, kernel.exited = "ready", closed, make(chan struct{})
	kernel.port, kernel.webToken = gateway.Listener.Addr().(*net.TCPAddr).Port, gateway.token
	kernel.mu.Unlock()
	return kernel
}

func chatApp(t *testing.T) (*App, *kernelChat, *fakeGateway) {
	t.Helper()
	app := newTestApp(t)
	storeTestModel(t, app, "http://127.0.0.1:9/v1", "")
	gateway := newFakeGateway(t)
	app.attachKernel(readyKernel(t, gateway))
	return app, app.kernel.observer.(*kernelChat), gateway
}

func watchEvents(app *App) chan Event {
	events := make(chan Event, 256)
	app.mu.Lock()
	app.watchers[events] = true
	app.mu.Unlock()
	return events
}

func waitEvent(t *testing.T, events chan Event, match func(Event) bool) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if match(event) {
				return event
			}
		case <-deadline:
			t.Fatal("the expected event did not come")
		}
	}
}

func waitFrame(t *testing.T, gateway *fakeGateway) map[string]any {
	t.Helper()
	select {
	case frame := <-gateway.sent:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("the App sent nothing to the kernel")
	}
	return nil
}

func TestChatTurnStreamsTheReplyAndEndsOnTheKernelsWord(t *testing.T) {
	app, chat, gateway := chatApp(t)
	events := watchEvents(app)
	s, _ := app.NewSession("chat")
	turnID, err := app.StartTurn(s.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	frame := waitFrame(t, gateway)
	payload, _ := frame["payload"].(map[string]any)
	if frame["type"] != "message.send" || frame["id"] != turnID || frame["session_id"] != s.ID || payload["content"] != "hello" {
		t.Fatalf("sent = %v", frame)
	}
	conn := <-gateway.conns
	for _, out := range []map[string]any{
		{"type": "typing.start"},
		{"type": "message.create", "payload": map[string]any{"kind": "tool_calls", "content": "", "message_id": "a"}},
		{"type": "message.create", "payload": map[string]any{"content": "Hel", "message_id": "b"}},
		{"type": "message.update", "payload": map[string]any{"content": "Hello", "message_id": "b"}},
	} {
		if err := conn.WriteJSON(out); err != nil {
			t.Fatal(err)
		}
	}
	waitEvent(t, events, func(e Event) bool { return e.Type == "delta" && e.Text == "Hel" && e.TurnID == turnID })
	waitEvent(t, events, func(e Event) bool { return e.Type == "delta" && e.Text == "Hello" })
	chatID := "web:" + s.ID
	chat.kernelEvent(kernelEvent{Kind: "agent.tool.exec_start", ChatID: chatID, MessageID: turnID,
		Payload: map[string]any{"Tool": "exec", "Arguments": map[string]any{"command": "midden ls --json"}}})
	started := waitEvent(t, events, func(e Event) bool { return e.Type == "tool" })
	chat.kernelEvent(kernelEvent{Kind: "agent.tool.exec_end", ChatID: chatID, MessageID: turnID,
		Payload: map[string]any{"Tool": "exec", "IsError": false, "Duration": 1.5e9}})
	ended := waitEvent(t, events, func(e Event) bool { return e.Type == "tool" })
	if started.Status != "pending" || started.CallID == "" || ended.Status != "completed" || ended.CallID != started.CallID ||
		ended.Result != "Finished after 1.5 s." || started.Arguments == nil {
		t.Fatalf("tool events = %+v / %+v", started, ended)
	}
	chat.kernelEvent(kernelEvent{Kind: "agent.turn.end", ChatID: chatID, MessageID: "another-turn", Payload: map[string]any{"Status": "completed"}})
	chat.kernelEvent(kernelEvent{Kind: "agent.turn.end", ChatID: "web:another", MessageID: turnID, Payload: map[string]any{"Status": "completed"}})
	chat.kernelEvent(kernelEvent{Kind: "agent.turn.end", ChatID: chatID, MessageID: turnID,
		Payload: map[string]any{"Status": "completed", "FinalContent": "Hello there"}})
	waitEvent(t, events, func(e Event) bool { return e.Type == "message" && e.Text == "Hello there" })
	app.wg.Wait()
	session, _ := app.Session(s.ID)
	if len(session.Messages) != 2 || session.Messages[1].Content != "Hello there" || session.Outcomes[0].Status != "completed" {
		t.Fatalf("session = %+v", session)
	}
}

func TestStoppingATurnSendsStopAndWaitsForTheAbort(t *testing.T) {
	app, chat, gateway := chatApp(t)
	events := watchEvents(app)
	s, _ := app.NewSession("stop")
	turnID, err := app.StartTurn(s.ID, "slow work")
	if err != nil {
		t.Fatal(err)
	}
	waitFrame(t, gateway)
	conn := <-gateway.conns
	if err := app.Cancel(turnID); err != nil {
		t.Fatal(err)
	}
	stop := waitFrame(t, gateway)
	payload, _ := stop["payload"].(map[string]any)
	if stop["id"] != "stop-"+turnID || stop["session_id"] != s.ID || payload["content"] != "/stop" {
		t.Fatalf("stop = %v", stop)
	}
	conn.WriteJSON(map[string]any{"type": "message.create", "payload": map[string]any{"content": "Task stopped.", "message_id": "c"}})
	chat.kernelEvent(kernelEvent{Kind: "agent.turn.end", ChatID: "web:" + s.ID, MessageID: turnID, Payload: map[string]any{"Status": "aborted"}})
	app.wg.Wait()
	session, _ := app.Session(s.ID)
	if session.Outcomes[0].Status != "cancelled" || len(session.Messages) != 1 {
		t.Fatalf("session = %+v", session)
	}
	for len(events) > 0 {
		if event := <-events; event.Type == "delta" && strings.Contains(event.Text, "Task stopped") {
			t.Fatal("the kernel's stop notice was shown as the reply")
		}
	}
}

func TestApprovalAsksThePersonAndAnswersWithTheDecision(t *testing.T) {
	app, chat, gateway := chatApp(t)
	events := watchEvents(app)
	s, _ := app.NewSession("approval")
	turnID, err := app.StartTurn(s.ID, "write a file")
	if err != nil {
		t.Fatal(err)
	}
	waitFrame(t, gateway)
	if answer := chat.kernelApproval(context.Background(), kernelApprovalRequest{ChatID: "web:other", MessageID: turnID, Tool: "exec"}); answer.Approved {
		t.Fatal("a call from another conversation was approved")
	}
	request := kernelApprovalRequest{ChatID: "web:" + s.ID, MessageID: turnID, Tool: "write_file", Arguments: map[string]any{"path": "files/a.md"}}
	answers := make(chan kernelApprovalAnswer, 1)
	go func() { answers <- chat.kernelApproval(context.Background(), request) }()
	asked := waitEvent(t, events, func(e Event) bool { return e.Type == "permission" })
	if asked.Tool != "write_file" || asked.TurnID != turnID || asked.SessionID != s.ID || asked.PermissionID == "" {
		t.Fatalf("permission = %+v", asked)
	}
	if err := app.Decide(asked.PermissionID, true); err != nil {
		t.Fatal(err)
	}
	if answer := <-answers; !answer.Approved {
		t.Fatalf("answer = %+v", answer)
	}
	result := waitEvent(t, events, func(e Event) bool { return e.Type == "permission_result" })
	if result.Allow == nil || !*result.Allow {
		t.Fatalf("result = %+v", result)
	}
	app.kernel.mu.Lock()
	app.kernel.approvalTimeout = 50 * time.Millisecond
	app.kernel.mu.Unlock()
	if answer := chat.kernelApproval(context.Background(), request); answer.Approved || !strings.Contains(answer.Reason, "in time") {
		t.Fatalf("an unanswered ask = %+v", answer)
	}
	app.kernel.mu.Lock()
	app.kernel.approvalTimeout = time.Minute
	app.kernel.mu.Unlock()
	go func() { answers <- chat.kernelApproval(context.Background(), request) }()
	waitEvent(t, events, func(e Event) bool { return e.Type == "permission" })
	chat.kernelEvent(kernelEvent{Kind: "agent.turn.end", ChatID: "web:" + s.ID, MessageID: turnID, Payload: map[string]any{"Status": "completed", "FinalContent": "ok"}})
	if answer := <-answers; answer.Approved || !strings.Contains(answer.Reason, "ended") {
		t.Fatalf("an ask that outlived its turn = %+v", answer)
	}
	app.wg.Wait()
}

func TestTurnsFailWhenTheKernelCannotFinishThem(t *testing.T) {
	app, _, gateway := chatApp(t)
	s, _ := app.NewSession("failures")
	turnID, err := app.StartTurn(s.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	waitFrame(t, gateway)
	conn := <-gateway.conns
	conn.WriteJSON(map[string]any{"type": "error", "payload": map[string]any{"code": "empty_content", "message": "message content is empty", "request_id": turnID}})
	app.wg.Wait()
	if session, _ := app.Session(s.ID); session.Outcomes[0].Status != "failed" || !strings.Contains(session.Outcomes[0].Error, "refused") {
		t.Fatalf("a refused message = %+v", session.Outcomes)
	}
	if _, err := app.StartTurn(s.ID, "second"); err != nil {
		t.Fatal(err)
	}
	waitFrame(t, gateway)
	app.kernel.mu.Lock()
	close(app.kernel.exited)
	app.kernel.mu.Unlock()
	app.wg.Wait()
	if session, _ := app.Session(s.ID); session.Outcomes[1].Status != "failed" || !strings.Contains(session.Outcomes[1].Error, "stopped unexpectedly") {
		t.Fatalf("a kernel that stopped = %+v", session.Outcomes)
	}
	app.kernel.mu.Lock()
	app.kernel.state, app.kernel.failure = "failed", errors.New("synthetic start failure")
	app.kernel.mu.Unlock()
	if _, err := app.StartTurn(s.ID, "third"); err != nil {
		t.Fatal(err)
	}
	app.wg.Wait()
	if session, _ := app.Session(s.ID); session.Outcomes[2].Status != "failed" || !strings.Contains(session.Outcomes[2].Error, "synthetic start failure") {
		t.Fatalf("a kernel that is not running = %+v", session.Outcomes)
	}
	status := app.Status()
	if kernel, _ := status["kernel"].(map[string]string); kernel["state"] != "failed" || !strings.Contains(status["notice"].(string), "synthetic start failure") {
		t.Fatalf("status = %v", status)
	}
}
