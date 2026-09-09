package create

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Run-scoped output layout.
//
// Produced files landed in a flat content/ directory named by kind, so a second
// ADR silently replaced the first. Measured on the real store: seven files, one
// per kind, no way to tell when or where any of them came from. That is data
// loss on the SECOND use of a capability, not a reporting gap.
//
// The layout is Midden's to define; WHERE the root lives is the driver's --
// index.OpenAt already takes it, and the module face already receives it from
// the host. This adds structure beneath a root, never a location.

// RunID identifies one production run.
//
// Derived rather than supplied because no driver currently passes a session
// identity: the module protocol carries request_id, which is per-call. A
// Midden-derived id answers "what did I produce here, and when" without a
// contract change. It deliberately does NOT claim to answer "in which of your
// sessions" -- that needs the driver to say, and inventing an answer would be
// worse than admitting the gap.
type RunID string

// NewRunID mints a sortable, human-readable run identifier.
//
// Time-ordered so a directory listing IS the history, without reading an index.
// A driver resuming tomorrow can see what happened today by looking.
func NewRunID(now time.Time) RunID {
	if now.IsZero() {
		now = time.Now()
	}
	return RunID(now.UTC().Format("20060102-150405"))
}

// OutputPath returns the path for one produced artifact, relative to the root.
//
// Run-scoped: content/<run>/<name>.<ext>. Two runs of the same kind coexist,
// which is the whole point -- a user who produces an ADR today and another
// tomorrow has two ADRs, not one.
//
// The name stays a single safe path segment. It arrives from a caller and
// becomes a directory entry, so it is exactly the input that must not be able
// to name a location outside the granted root.
func OutputPath(run RunID, name, ext string) (string, error) {
	name = strings.TrimSpace(name)
	if !SafeSegment(name) {
		return "", fmt.Errorf("output name %q is not a single safe path segment", name)
	}
	if !SafeSegment(string(run)) {
		return "", fmt.Errorf("run id %q is not a single safe path segment", run)
	}
	return filepath.ToSlash(filepath.Join("content", string(run), name+ext)), nil
}

// SafeSegment reports whether s is one path segment that cannot escape a root.
//
// Rejects separators, traversal, absolute forms and anything a filesystem would
// treat as structure. Deliberately strict: a name that "works" on one platform
// and escapes on another is the shape that turns a caller's typo into a write
// outside the granted directory.
func SafeSegment(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	if s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, `/\:`) {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
