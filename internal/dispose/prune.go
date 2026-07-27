// Package dispose removes bytes without removing meaning.
//
// Every operation here is destructive by nature, so every operation here is
// paranoid by design:
//
//  1. The source is never mutated. A pruned transcript is written to a NEW
//     file; the original is untouched until an explicit, separate deletion.
//  2. Record structure is preserved. Pruning replaces payloads inside records
//     rather than removing records, because transcripts are chained by id and
//     dropping a record breaks replay.
//  3. Nothing is deleted without verification. The caller must confirm the
//     rewritten transcript still parses and still contains its conversation.
//
// Splitting a transcript is deliberately NOT offered: records reference each
// other by uuid and tool_use/tool_result pairs must stay together, and no CLI
// has a mechanism to link split files back together.
package dispose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/assay"
)

// Plan is what a disposal would do, computed without touching anything.
type Plan struct {
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Source    string `json:"source"`
	Target    string `json:"target,omitempty"`

	BeforeBytes int64 `json:"before_bytes"`
	AfterBytes  int64 `json:"after_bytes"`

	RecordsTotal  int64 `json:"records_total"`
	RecordsPruned int64 `json:"records_pruned"`
	BytesPruned   int64 `json:"bytes_pruned"`

	// PreservedKinds are never touched regardless of size.
	PreservedKinds []string `json:"preserved_kinds,omitempty"`
}

// Saved is how many bytes the operation recovers.
func (p Plan) Saved() int64 { return p.BeforeBytes - p.AfterBytes }

// SavedPct is the recovery as a percentage of the original.
func (p Plan) SavedPct() float64 {
	if p.BeforeBytes == 0 {
		return 0
	}
	return 100 * float64(p.Saved()) / float64(p.BeforeBytes)
}

// Options control what pruning removes.
type Options struct {
	// Exhaust removes tool payloads (the bulk of most transcripts).
	Exhaust bool
	// Artifacts removes binary assets: screenshots, pasted files.
	//
	// Off by default. Artifacts are the raw material for tutorials — the same
	// bytes are garbage or gold depending on whether they have been harvested.
	Artifacts bool
	// Bookkeeping removes protocol overhead.
	Bookkeeping bool
	// MinBytes leaves small payloads alone; rewriting them costs more in
	// marker text than it recovers.
	MinBytes int
}

// DefaultOptions prunes exhaust and bookkeeping but preserves artifacts.
func DefaultOptions() Options {
	return Options{Exhaust: true, Bookkeeping: true, MinBytes: 2048}
}

// shouldPrune decides whether a record's payload is replaced.
func (o Options) shouldPrune(class assay.Class, size int) bool {
	if size < o.MinBytes {
		return false
	}
	switch class {
	case assay.Exhaust:
		return o.Exhaust
	case assay.Artifact:
		return o.Artifacts
	case assay.Bookkeeping:
		return o.Bookkeeping
	}
	return false // Signal is never pruned
}

// PruneJSONL rewrites a JSONL transcript with bulky payloads replaced by
// markers, preserving every record and every identifier.
//
// The target is a new file. The source is opened read-only and never modified.
func PruneJSONL(source, target string, opts Options, kindOf func(line []byte) (kind string)) (Plan, error) {
	plan := Plan{Source: source, Target: target}

	in, err := os.Open(source)
	if err != nil {
		return plan, fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	if fi, err := in.Stat(); err == nil {
		plan.BeforeBytes = fi.Size()
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return plan, err
	}
	// Write to a temp file and rename, so an interrupted prune never leaves a
	// half-written transcript that looks complete.
	tmp := target + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return plan, fmt.Errorf("create target: %w", err)
	}
	defer func() {
		out.Close()
		os.Remove(tmp)
	}()

	w := bufio.NewWriterSize(out, 1<<20)
	r := bufio.NewReaderSize(in, 1<<20)

	var line []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		if rerr == bufio.ErrBufferFull {
			line = append(line, chunk...)
			continue
		}
		line = append(line, chunk...)

		if len(strings.TrimSpace(string(line))) > 0 {
			plan.RecordsTotal++
			kind := kindOf(line)
			class := assay.Classify(kind)

			if opts.shouldPrune(class, len(line)) {
				pruned, ok := pruneRecord(line, kind, class)
				if ok {
					plan.RecordsPruned++
					plan.BytesPruned += int64(len(line) - len(pruned))
					line = pruned
				}
			}
			if _, werr := w.Write(line); werr != nil {
				return plan, werr
			}
		}
		line = line[:0]

		if rerr != nil {
			break
		}
	}

	if err := w.Flush(); err != nil {
		return plan, err
	}
	if err := out.Sync(); err != nil {
		return plan, err
	}
	out.Close()

	if err := os.Rename(tmp, target); err != nil {
		return plan, fmt.Errorf("finalise target: %w", err)
	}
	if fi, err := os.Stat(target); err == nil {
		plan.AfterBytes = fi.Size()
	}
	return plan, nil
}

// pruneRecord replaces heavy payload fields with a marker, leaving the
// record's identity, type and links intact.
//
// Returns ok=false when the record cannot be safely rewritten, in which case
// the original is kept verbatim.
func pruneRecord(line []byte, kind string, class assay.Class) ([]byte, bool) {
	var rec map[string]json.RawMessage
	if json.Unmarshal(line, &rec) != nil {
		return line, false
	}

	original := len(line)
	changed := false

	// Payload lives under "data" (Copilot/opencode) or "message" (Claude).
	for _, field := range []string{"data", "message"} {
		raw, ok := rec[field]
		if !ok || len(raw) < 512 {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}

		for _, key := range payloadKeys {
			v, ok := obj[key]
			if !ok || len(v) < 256 {
				continue
			}
			marker, _ := json.Marshal(fmt.Sprintf(
				"[midden: pruned %d bytes of %s %s payload]", len(v), kind, class))
			obj[key] = marker
			changed = true
		}
		if changed {
			nb, err := json.Marshal(obj)
			if err != nil {
				return line, false
			}
			rec[field] = nb
		}
	}

	if !changed {
		return line, false
	}

	out, err := json.Marshal(rec)
	if err != nil {
		return line, false
	}
	out = append(out, '\n')

	// Never emit something larger than what we started with.
	if len(out) >= original {
		return line, false
	}
	return out, true
}

// payloadKeys are the fields that carry bulk across the three tools.
var payloadKeys = []string{
	"content", "output", "result", "stdout", "stderr",
	"text", "data", "body", "snapshot", "diff", "patch", "base64",
}

// ArchivePath is where a session's archived copy lives.
func ArchivePath(root, tool, id string) string {
	return filepath.Join(root, "archive", tool, id)
}

// Manifest describes an archived session so it can be understood, and
// restored, without the original tool.
type ArchiveManifest struct {
	Tool       string    `json:"tool"`
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title"`
	Workspace  string    `json:"workspace"`
	SourcePath string    `json:"source_path"`
	Bytes      int64     `json:"bytes"`
	ArchivedAt time.Time `json:"archived_at"`
	Note       string    `json:"note"`
}
