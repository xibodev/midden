package adapter

import (
	"os"
	"strings"
)

// Explicit source environment configuration is a closed set. It never changes
// the AI host's own home, authentication or conversation storage.
func EnvironmentRoots() Roots {
	roots := Roots{
		Copilot:  strings.TrimSpace(os.Getenv("MIDDEN_COPILOT_ROOT")),
		Claude:   strings.TrimSpace(os.Getenv("MIDDEN_CLAUDE_ROOT")),
		Opencode: strings.TrimSpace(os.Getenv("MIDDEN_OPENCODE_DB")),
	}
	roots.Strict = roots.Copilot != "" || roots.Claude != "" || roots.Opencode != ""
	return roots
}
