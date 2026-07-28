package guide

import (
	"os"
	"path/filepath"
	"strings"
)

// BusiestScope picks the workspace worth suggesting as a --workspace value.
//
// The naive answer — whichever directory has the most sessions — proposed the
// home directory on the machine this was written on, because that is where a
// CLI gets launched when it is not launched inside a project. Passing the home
// directory as a scope matches essentially everything, which is the exact
// opposite of the tool's own first piece of cost advice ("scope hard"). A
// suggestion that quietly widens the blast radius is worse than no suggestion.
func BusiestScope(byWorkspace map[string]int) string {
	best, out := 0, ""
	for dir, n := range byWorkspace {
		if n <= best || !scopeworthy(dir) {
			continue
		}
		best, out = n, dir
	}
	return out
}

// scopeworthy rejects directories that are not projects: the home directory,
// its immediate shell (Desktop, Downloads), and filesystem roots. Narrowing to
// one of these saves nothing.
func scopeworthy(dir string) bool {
	d := strings.TrimRight(strings.TrimSpace(dir), `\/`)
	if d == "" {
		return false
	}

	// A drive root ("C:" or "/") has nothing above it to scope down from.
	if len(d) <= 3 && strings.Contains(d, ":") {
		return false
	}
	if d == "/" {
		return false
	}

	if home, err := os.UserHomeDir(); err == nil {
		h := strings.TrimRight(home, `\/`)
		if strings.EqualFold(d, h) {
			return false
		}
		// Direct children of home that are shell folders rather than work.
		if parent := filepath.Dir(d); strings.EqualFold(parent, h) {
			switch strings.ToLower(filepath.Base(d)) {
			case "desktop", "downloads", "documents", "temp", "tmp":
				return false
			}
		}
	}
	return true
}
