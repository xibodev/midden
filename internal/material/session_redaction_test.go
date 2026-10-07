package material

import (
	"strings"
	"testing"

	"github.com/xibodev/midden/internal/core"
)

// Free text is filtered; identity, paths and numbers are not. Lists bound the
// title, single-session views do not.
func TestSessionFilteringKeepsIdentityAndBoundsListTitles(t *testing.T) {
	token := "ghp_" + strings.Repeat("Z", 36)
	session := core.Session{
		Tool: core.ToolClaude, ID: "session-fixture", Dir: "/srv/work", Bytes: 42, Turns: 3,
		Title:          "Rotate " + token + " " + strings.Repeat("long prompt ", 40) + "end",
		Repo:           "https://user:hunter2secret@example.invalid/owner/repo",
		Live:           &core.Live{PID: 7, Status: "busy", Name: "deploy " + token},
		TranscriptPath: "/srv/store/session.jsonl",
	}

	full := RedactSession(session)
	for _, text := range []string{full.Title, full.Repo, full.Live.Name} {
		if strings.Contains(text, token) || strings.Contains(text, "hunter2secret") {
			t.Fatalf("credential survived: %q", text)
		}
	}
	if !strings.Contains(full.Title, "ask operator") || !strings.HasSuffix(full.Title, "end") {
		t.Errorf("single-session title should be filtered but whole: %q", full.Title)
	}
	if full.ID != session.ID || full.Dir != session.Dir || full.TranscriptPath != session.TranscriptPath ||
		full.Bytes != 42 || full.Turns != 3 || full.Live.PID != 7 || full.Live.Status != "busy" {
		t.Errorf("identity, paths or numbers changed: %+v", full)
	}
	if !strings.Contains(session.Live.Name, token) {
		t.Error("filtering modified the caller's live record")
	}

	listed := ListSession(session)
	if !strings.HasSuffix(listed.Title, " [clipped]") || len([]rune(listed.Title)) > listTitleRunes+len(" [clipped]") {
		t.Errorf("list title is not bounded: %q", listed.Title)
	}
	if strings.Contains(listed.Title, token) {
		t.Errorf("list title carries the credential: %q", listed.Title)
	}
}
