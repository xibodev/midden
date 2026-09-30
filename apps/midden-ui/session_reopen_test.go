package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKernelReopensTheSameConversationHistory(t *testing.T) {
	app, _, calls := kernelApp(t)
	s, err := app.NewSession("persistent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.StartTurn(s.ID, "Write the article."); err != nil {
		t.Fatal(err)
	}
	p := awaitPermission(t, app)
	app.Decide(p.ID, true)
	awaitIdle(t, app)
	count := calls.Load()
	app.mu.Lock()
	app.runtime.Close()
	app.runtime = nil
	app.mu.Unlock()
	if _, err = app.StartTurn(s.ID, "Continue from the saved conversation."); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, app)
	if calls.Load() != count+1 {
		t.Fatal("reopened kernel lost tool/result history and repeated the original work")
	}
	if _, err = os.Stat(filepath.Join(app.opts.Workspace, "article.md")); err != nil {
		t.Fatal(err)
	}
}
