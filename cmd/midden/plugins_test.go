package main

import (
	"strings"
	"testing"
)

func TestSafeTerminalEscapesManifestControls(t *testing.T) {
	got := safeTerminal("name\x1b]0;owned\x07\nnext")
	for _, want := range []string{`\x1B`, `\x07`, `\x0A`} {
		if !strings.Contains(got, want) {
			t.Fatalf("safeTerminal(%q) = %q, missing %s", "control input", got, want)
		}
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\n") {
		t.Fatalf("safeTerminal left raw control characters: %q", got)
	}
}
