package adapter

import (
	"sync"
	"time"
)

// PeekEntry is transcript metadata that has already been derived once.
//
// Some stores keep no metadata at all: Claude's working directory and title
// live inside the transcript, so describing a session means opening the file.
// On Windows that triggers an on-access virus scan of the whole file even
// though only the first few dozen lines are read — measured at 1.7s per file
// cold and 0.16s warm, or roughly four minutes to describe 135 sessions.
//
// None of that data changes unless the file does, so a scan records it and
// later reads reuse it, re-deriving only when size or mtime moves.
type PeekEntry struct {
	Cwd     string
	Title   string
	Created time.Time
	Noise   bool

	// Size and ModUnix identify the exact file revision this was derived
	// from. A mismatch means the transcript grew and must be re-read.
	Size    int64
	ModUnix int64
}

var (
	peekMu    sync.RWMutex
	peekCache map[string]PeekEntry
)

// SetPeekCache installs previously derived transcript metadata, keyed by
// transcript path.
//
// This is an optimisation, never a source of truth: every adapter produces
// identical results without it, only slower. Callers that hold an index
// should install it before collecting.
func SetPeekCache(m map[string]PeekEntry) {
	peekMu.Lock()
	peekCache = m
	peekMu.Unlock()
}

// cachedPeek returns metadata for a file whose size and mtime are unchanged
// since it was recorded. Any mismatch is a miss, so a growing transcript is
// always re-read.
func cachedPeek(path string, size int64, mod time.Time) (PeekEntry, bool) {
	peekMu.RLock()
	e, ok := peekCache[path]
	peekMu.RUnlock()

	if !ok || e.Cwd == "" || e.Size != size || e.ModUnix != mod.Unix() {
		return PeekEntry{}, false
	}
	return e, true
}
