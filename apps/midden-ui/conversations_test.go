package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kernelChatFiles writes a chat record as Compa keeps one: <file>.jsonl and
// <file>.meta.json, whose metadata names its key and chat.
func kernelChatFiles(t *testing.T, workspace, file, key, channel, chat string) {
	t.Helper()
	folder := filepath.Join(workspace, "sessions")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"key": key, "summary": "", "skip": 0, "count": 2,
		"scope": map[string]any{"version": 1, "agent_id": "main", "channel": channel, "account": "default",
			"dimensions": []string{"chat"}, "values": map[string]string{"chat": chat}}})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(folder, file+".meta.json"), string(raw))
	writeTestFile(t, filepath.Join(folder, file+".jsonl"), `{"role":"user","content":"synthetic words"}`+"\n")
}

func deleteConversation(app *App, id string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("DELETE", "http://127.0.0.1:18890/api/sessions/"+id, nil)
	request.Header.Set("X-Midden-CSRF", app.csrf)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, keyed(app, request))
	return recorder
}

func TestDeletingAConversationRemovesItAndTheAssistantsCopy(t *testing.T) {
	app := newTestApp(t)
	gone, _ := app.NewSession("delete me")
	kept, _ := app.NewSession("keep me")
	app.emit(Event{Type: "message", SessionID: gone.ID, TurnID: "t1", Text: "private words"})
	app.emit(Event{Type: "message", SessionID: kept.ID, TurnID: "t2", Text: "other words"})
	workspace := app.paths.Workspace
	kernelChatFiles(t, workspace, "sk_v1_gone", "sk_v1_gone", "web", "direct:web:"+gone.ID)
	kernelChatFiles(t, workspace, "sk_v1_kept", "sk_v1_kept", "web", "direct:web:"+kept.ID)
	kernelChatFiles(t, workspace, "sk_v1_channel", "sk_v1_channel", "telegram", "direct:web:"+gone.ID)
	kernelChatFiles(t, workspace, "sk_v1_mismatch", "sk_v1_elsewhere", "web", "direct:web:"+gone.ID)
	writeTestFile(t, filepath.Join(workspace, "sessions", "sk_v1_broken.meta.json"), "{not json")
	events := watchEvents(app)

	if reply := deleteConversation(app, gone.ID); reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), `"deleted":true`) {
		t.Fatalf("delete: %d %s", reply.Code, reply.Body)
	}
	waitEvent(t, events, func(e Event) bool { return e.Type == "conversations_changed" && e.SessionID == "" })
	if _, err := app.Session(gone.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the conversation is still there: %v", err)
	}
	if _, err := app.Session(kept.ID); err != nil {
		t.Fatalf("another conversation went too: %v", err)
	}
	for _, name := range []string{"sk_v1_gone.jsonl", "sk_v1_gone.meta.json"} {
		if _, err := os.Stat(filepath.Join(workspace, "sessions", name)); !os.IsNotExist(err) {
			t.Fatalf("the assistant's copy remains: %s", name)
		}
	}
	for _, name := range []string{"sk_v1_kept.jsonl", "sk_v1_channel.jsonl", "sk_v1_mismatch.jsonl", "sk_v1_broken.meta.json"} {
		if _, err := os.Stat(filepath.Join(workspace, "sessions", name)); err != nil {
			t.Fatalf("an unrelated record was removed: %s", name)
		}
	}
	app.mu.Lock()
	for _, event := range app.events {
		if event.SessionID == gone.ID {
			t.Errorf("a live event of the deleted conversation remains: %+v", event)
		}
	}
	app.mu.Unlock()
	again, err := NewApp(app.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, err := again.Session(gone.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the deletion was not saved")
	}
	if reply := deleteConversation(app, gone.ID); reply.Code != http.StatusNotFound {
		t.Fatalf("deleting it again: %d", reply.Code)
	}
}

func TestAConversationWithARunningTurnIsNotDeleted(t *testing.T) {
	app := newTestApp(t)
	s, _ := app.NewSession("busy")
	app.mu.Lock()
	app.active = &activeTurn{ID: "t1", SessionID: s.ID, cancel: func() {}}
	app.mu.Unlock()
	defer func() { app.mu.Lock(); app.active = nil; app.mu.Unlock() }()
	reply := deleteConversation(app, s.ID)
	if reply.Code != http.StatusConflict || !strings.Contains(reply.Body.String(), "turn running") {
		t.Fatalf("delete during a turn: %d %s", reply.Code, reply.Body)
	}
	if _, err := app.Session(s.ID); err != nil {
		t.Fatal("the conversation was deleted during its turn")
	}
}

func TestQuitInThePageStopsTheApp(t *testing.T) {
	app := newTestApp(t)
	quit := func(csrf bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://127.0.0.1:18890/api/quit", strings.NewReader("{}"))
		if csrf {
			request.Header.Set("X-Midden-CSRF", app.csrf)
		}
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, keyed(app, request))
		return recorder
	}
	if reply := quit(true); reply.Code != http.StatusConflict {
		t.Fatalf("quit with no way to stop: %d %s", reply.Code, reply.Body)
	}
	stopped := make(chan struct{})
	app.quit = func() { close(stopped) }
	if reply := quit(false); reply.Code != http.StatusForbidden {
		t.Fatalf("quit without the page's token: %d", reply.Code)
	}
	reply := quit(true)
	if reply.Code != http.StatusAccepted || !strings.Contains(reply.Body.String(), `"stopping":true`) {
		t.Fatalf("quit: %d %s", reply.Code, reply.Body)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("quit did not stop the App")
	}
}
