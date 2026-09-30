package core

import (
	"regexp"
	"strings"
)

// Noise patterns identify sessions that are automated spawns, health probes or
// trivial, rather than real interactive work. They are hidden by default and
// never deleted without consent.
var (
	noiseTitlePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^<GARRISON_GUIDE>`),
		regexp.MustCompile(`(?i)^Reply briefly after using the shell tool`),
		regexp.MustCompile(`(?i)^\s*(hi|hey|hello|test|ping|yo)\s*$`),
	}
	noiseDirPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)[\\/]garrison-work[\\/].*[\\/]assistant[\\/]`),
		regexp.MustCompile(`(?i)[\\/]\.copilot[\\/]session-state[\\/]`),
		regexp.MustCompile(`(?i)[\\/]\.claude[\\/]projects[\\/]`),
		// Test harnesses and scratch runs: real work does not live in temp.
		regexp.MustCompile(`(?i)[\\/](AppData[\\/]Local[\\/])?Temp[\\/]`),
		regexp.MustCompile(`(?i)[\\/]pytest-of-`),
		regexp.MustCompile(`(?i)[\\/]multica_workspaces[\\/]`),
		regexp.MustCompile(`(?i)^/tmp/`),
	}
)

// IsNoise reports whether a session looks automated or trivial.
func IsNoise(title, dir string, turns int) bool {
	t := strings.TrimSpace(title)
	for _, re := range noiseTitlePatterns {
		if re.MatchString(t) {
			return true
		}
	}
	for _, re := range noiseDirPatterns {
		if re.MatchString(dir) {
			return true
		}
	}
	// A session with no real exchange has nothing to resume into.
	return turns <= 1 && len(t) < 3
}

// CleanTitle normalises a title for display: collapses whitespace and replaces
// machine-generated prologues with something a human can scan.
func CleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(untitled)"
	}
	if strings.HasPrefix(s, "<local-command") ||
		strings.HasPrefix(s, "<command-name") ||
		strings.HasPrefix(s, "<user-memory") {
		return "(slash command session)"
	}
	return strings.Join(strings.Fields(s), " ")
}

// Truncate shortens a string for column display, appending an ellipsis.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}
