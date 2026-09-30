package main

import "testing"

func TestSessionSelectorSupportsExactFlagsAndRejectsMixedScope(t *testing.T) {
	_, _ = scopeFixture(t)
	session, err := selectSession("claude", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "fixture" || session.Tool != "claude" {
		t.Fatal("wrong exact session")
	}
	if _, err = selectSession("claude", "fixture", []string{"other"}); err == nil {
		t.Fatal("mixed exact and positional selector accepted")
	}
	if _, err = selectSession("unsupported", "fixture", nil); err == nil {
		t.Fatal("invalid tool accepted")
	}
	if _, err = selectSession("claude", "fix", nil); err == nil {
		t.Fatal("exact session flag was treated as a prefix")
	}
}
