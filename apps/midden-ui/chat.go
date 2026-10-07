package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// kernelChat runs the App's turns on the kernel. Each turn opens the
// conversation's web chat socket, sends the message with the turn's id, and
// streams the reply from the socket. The turn's end, its tool calls and the
// approvals it asks for come through the hook, scoped by that id.
type kernelChat struct {
	app    *App
	kernel *kernelProcess

	mu    sync.Mutex
	turns map[string]*chatTurn // by turn id
}

type chatTurn struct {
	sessionID, turnID string
	ctx               context.Context // ends with the turn
	ended             chan turnOutcome
	stopping          atomic.Bool

	mu    sync.Mutex
	calls map[string][]string // tool -> ids of its calls that have not ended
}

// turnOutcome is how the kernel says a turn ended.
type turnOutcome struct {
	Status, Final, Error string
}

func newKernelChat(app *App, kernel *kernelProcess) *kernelChat {
	return &kernelChat{app: app, kernel: kernel, turns: map[string]*chatTurn{}}
}

func (t *chatTurn) end(outcome turnOutcome) {
	select {
	case t.ended <- outcome:
	default:
	}
}

func (o turnOutcome) result() (string, error) {
	switch o.Status {
	case "completed":
		return o.Final, nil
	case "aborted":
		return "", context.Canceled
	}
	if o.Error == "" {
		o.Error = "the assistant's turn failed"
	}
	return "", errors.New(o.Error)
}

// Process runs one turn and returns the assistant's final reply.
func (c *kernelChat) Process(ctx context.Context, sessionID, turnID, text string) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, kernelStartTimeout)
	err := c.kernel.waitReady(waitCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("the assistant is not running: %w", err)
	}
	port, token, exited := c.kernel.endpoint()
	turnCtx, endTurn := context.WithCancel(context.Background())
	defer endTurn()
	turn := &chatTurn{sessionID: sessionID, turnID: turnID, ctx: turnCtx, ended: make(chan turnOutcome, 1), calls: map[string][]string{}}
	c.mu.Lock()
	c.turns[turnID] = turn
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.turns, turnID); c.mu.Unlock() }()

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	address := fmt.Sprintf("ws://127.0.0.1:%d/web/ws?session_id=%s", port, url.QueryEscape(sessionID))
	conn, _, err := dialer.DialContext(ctx, address, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("could not reach the assistant: %w", err)
	}
	defer conn.Close()
	var writeMu sync.Mutex
	send := func(id, content string) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(map[string]any{"type": "message.send", "id": id, "session_id": sessionID,
			"payload": map[string]any{"content": content}})
	}
	lost := make(chan error, 1)
	go c.readFrames(conn, turn, lost)
	if err := send(turnID, text); err != nil {
		return "", fmt.Errorf("could not send the message to the assistant: %w", err)
	}
	select {
	case outcome := <-turn.ended:
		return outcome.result()
	case <-exited:
		return "", errors.New("the assistant stopped unexpectedly; it restarts by itself, so try again in a moment")
	case err := <-lost:
		// The hook may still report the end the socket did not show.
		select {
		case outcome := <-turn.ended:
			return outcome.result()
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "", fmt.Errorf("lost the connection to the assistant: %v", err)
		}
	case <-ctx.Done():
		turn.stopping.Store(true)
		if send("stop-"+turnID, "/stop") == nil {
			select {
			case <-turn.ended:
			case <-exited:
			case <-time.After(15 * time.Second):
			}
		}
		return "", ctx.Err()
	}
}

// readFrames streams the reply: each message frame carries the reply so far.
// Tool-call and thinking frames are left to the hook's events.
func (c *kernelChat) readFrames(conn *websocket.Conn, turn *chatTurn, lost chan<- error) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case lost <- err:
			default:
			}
			return
		}
		var frame struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		if json.Unmarshal(data, &frame) != nil || turn.stopping.Load() {
			continue
		}
		switch frame.Type {
		case "message.create", "message.update":
			if kind, _ := frame.Payload["kind"].(string); kind != "" {
				continue
			}
			if content, ok := frame.Payload["content"].(string); ok {
				c.app.emit(Event{Type: "delta", SessionID: turn.sessionID, TurnID: turn.turnID, Text: content})
			}
		case "error":
			if request, _ := frame.Payload["request_id"].(string); request == turn.turnID {
				message, _ := frame.Payload["message"].(string)
				turn.end(turnOutcome{Status: "error", Error: "the assistant refused the message: " + message})
			}
		}
	}
}

func (c *kernelChat) turn(chatID, messageID string) *chatTurn {
	c.mu.Lock()
	defer c.mu.Unlock()
	turn := c.turns[messageID]
	if turn == nil || chatID != "web:"+turn.sessionID {
		return nil
	}
	return turn
}

// kernelEvent ends turns and shows their tool calls.
func (c *kernelChat) kernelEvent(event kernelEvent) {
	turn := c.turn(event.ChatID, event.MessageID)
	if turn == nil {
		return
	}
	text := func(key string) string { value, _ := event.Payload[key].(string); return value }
	tool := text("Tool")
	switch event.Kind {
	case "agent.turn.end":
		turn.end(turnOutcome{Status: text("Status"), Final: text("FinalContent"), Error: text("Error")})
	case "agent.tool.exec_start":
		id := randomID()
		turn.mu.Lock()
		turn.calls[tool] = append(turn.calls[tool], id)
		turn.mu.Unlock()
		c.app.emit(Event{Type: "tool", SessionID: turn.sessionID, TurnID: turn.turnID, Tool: tool,
			Arguments: event.Payload["Arguments"], Status: "pending", CallID: id})
	case "agent.tool.exec_end":
		turn.mu.Lock()
		id := ""
		if open := turn.calls[tool]; len(open) > 0 {
			id, turn.calls[tool] = open[0], open[1:]
		}
		turn.mu.Unlock()
		status, verb := "completed", "Finished"
		if failed, _ := event.Payload["IsError"].(bool); failed {
			status, verb = "failed", "Failed"
		}
		seconds, _ := event.Payload["Duration"].(float64)
		c.app.emit(Event{Type: "tool", SessionID: turn.sessionID, TurnID: turn.turnID, Tool: tool, Status: status, CallID: id,
			Result: fmt.Sprintf("%s after %.1f s.", verb, seconds/float64(time.Second))})
	case "agent.tool.exec_skipped":
		c.app.emit(Event{Type: "tool", SessionID: turn.sessionID, TurnID: turn.turnID, Tool: tool, Status: "failed",
			CallID: randomID(), Result: text("Reason")})
	}
}

// kernelApproval asks the person about a tool call of one of the App's
// turns, and answers no for any other.
func (c *kernelChat) kernelApproval(ctx context.Context, request kernelApprovalRequest) kernelApprovalAnswer {
	turn := c.turn(request.ChatID, request.MessageID)
	if turn == nil {
		return kernelApprovalAnswer{Reason: "Midden answers only for its own conversations"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(turn.ctx, cancel)
	defer stop()
	allow, reason := c.app.askPermission(ctx, turn.sessionID, turn.turnID, request.Tool, request.Arguments, c.kernel.approvalWait())
	return kernelApprovalAnswer{Approved: allow, Reason: reason}
}
