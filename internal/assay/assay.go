// Package assay classifies session events so that everything downstream knows
// what is worth keeping, what is worth reading, and what is pure exhaust.
//
// ASSAY is the economic engine of Midden. Its compression ratio decides
// whether reclamation is affordable: a 681 MiB session is roughly 170M tokens
// of text, and no model budget survives that. Classification is deterministic
// and free, so it always runs before anything expensive.
package assay

import (
	"strings"
	"time"
)

// Class is what a record is worth.
type Class int

const (
	// Signal is worth reading: intent, decisions, reasoning, resolutions.
	Signal Class = iota
	// Exhaust is machine bulk: tool payloads, file dumps, repeated reads.
	Exhaust
	// Artifact is a produced thing: screenshots, generated files, diffs.
	Artifact
	// Bookkeeping is protocol overhead: mode switches, queue operations.
	Bookkeeping
)

func (c Class) String() string {
	switch c {
	case Signal:
		return "signal"
	case Exhaust:
		return "exhaust"
	case Artifact:
		return "artifact"
	default:
		return "bookkeeping"
	}
}

// Record is one classified line or row from a transcript.
type Record struct {
	Class     Class     `json:"class"`
	Kind      string    `json:"kind"`
	Bytes     int64     `json:"bytes"`
	Role      string    `json:"role,omitempty"`
	Time      time.Time `json:"time,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	Index     int64     `json:"index"`
	Clipped   bool      `json:"clipped"`
	TextChars int       `json:"text_chars"`
	StartByte int       `json:"start_byte"`
	EndByte   int       `json:"end_byte"`
}

// Manifest is the result of assaying one session.
//
// Deliberately small: counts and byte totals plus a bounded candidate list.
// The manifest is what makes a slice cheap to select without re-reading the
// source.
type Manifest struct {
	SessionID string `json:"session_id"`
	Tool      string `json:"tool"`
	Title     string `json:"title,omitempty"`

	TotalRecords int64 `json:"total_records"`
	TotalBytes   int64 `json:"total_bytes"`

	Counts map[string]int64 `json:"counts"`
	Bytes  map[string]int64 `json:"bytes"`

	// ByKind is the tool-native breakdown, which is what makes a pruning
	// decision defensible ("59% of this file is tool.execution_complete").
	ByKind map[string]int64 `json:"by_kind"`

	// Candidates are Signal records worth handing to a model, newest last.
	Candidates      []Record  `json:"candidates,omitempty"`
	FirstTime       time.Time `json:"first_time"`
	LastTime        time.Time `json:"last_time"`
	SourceDigest    string    `json:"source_digest"`
	Selection       string    `json:"selection"`
	EligibleRecords int64     `json:"eligible_records"`
	MatchedRecords  int64     `json:"matched_records"`

	DuplicateReads int64 `json:"duplicate_reads"`
	DuplicateBytes int64 `json:"duplicate_bytes"`
	ImageCount     int64 `json:"image_count"`
	ImageClusters  int64 `json:"image_clusters"`

	Elapsed time.Duration `json:"-"`
}

func NewManifest(sessionID, tool string) *Manifest {
	return &Manifest{
		SessionID: sessionID,
		Tool:      tool,
		Counts:    map[string]int64{},
		Bytes:     map[string]int64{},
		ByKind:    map[string]int64{},
	}
}

// Add folds a classified record into the manifest.
func (m *Manifest) Add(r Record) {
	m.TotalRecords++
	m.TotalBytes += r.Bytes
	m.Counts[r.Class.String()]++
	m.Bytes[r.Class.String()] += r.Bytes
	if r.Kind != "" {
		m.ByKind[r.Kind] += r.Bytes
	}
}

// SignalBytes is the payload that actually carries meaning.
func (m *Manifest) SignalBytes() int64 { return m.Bytes[Signal.String()] }

// ReclaimableBytes is what could be removed without losing meaning.
func (m *Manifest) ReclaimableBytes() int64 {
	return m.Bytes[Exhaust.String()] + m.Bytes[Bookkeeping.String()]
}

// Compression is how much smaller a session becomes when only signal is kept.
// This is the number that decides whether salvage is affordable.
func (m *Manifest) Compression() float64 {
	sig := m.SignalBytes()
	if sig <= 0 {
		return float64(m.TotalBytes)
	}
	return float64(m.TotalBytes) / float64(sig)
}

// SignalShare is the fraction of bytes that carry meaning, 0..1.
func (m *Manifest) SignalShare() float64 {
	if m.TotalBytes == 0 {
		return 0
	}
	return float64(m.SignalBytes()) / float64(m.TotalBytes)
}

// EstTokens approximates the token cost of feeding the entire signal class to
// a model. Usually far too large to be practical, which is the point.
func (m *Manifest) EstTokens() int64 { return m.SignalBytes() / 4 }

// SliceBytes is the size of the bounded candidate set: previews of the most
// relevant signal records, not the whole signal class.
//
// This is the number that actually governs salvage cost. Signal at the record
// level still includes megabytes of assistant output; the slice is what
// RECLAIM sends to a model.
func (m *Manifest) SliceBytes() int64 {
	var n int64
	for _, c := range m.Candidates {
		n += int64(len(c.Preview))
	}
	return n
}

// EstSliceTokens is the token cost of the salvage slice.
func (m *Manifest) EstSliceTokens() int64 { return m.SliceBytes() / 4 }

// SliceCompression is total bytes over slice bytes: the real reduction
// achieved before any model is invoked.
func (m *Manifest) SliceCompression() float64 {
	s := m.SliceBytes()
	if s <= 0 {
		return 0
	}
	return float64(m.TotalBytes) / float64(s)
}

// classByKind maps tool-native record kinds to a class.
//
// Populated from the record kinds actually observed across a real 36 GiB
// corpus, not from documentation — none of these formats are documented.
// Anything unknown falls through to Unknown handling in Classify.
var classByKind = map[string]Class{
	// --- Copilot events.jsonl ---
	"user.message":          Signal,
	"assistant.message":     Signal,
	"skill.invoked":         Signal,
	"subagent.completed":    Signal,
	"session.plan_changed":  Signal,
	"session.task_complete": Signal,
	"session.error":         Signal,

	"tool.execution_start":    Exhaust,
	"tool.execution_complete": Exhaust,

	// Binary assets are base64 payloads: images, pasted files, screenshots.
	// Measured at 56% of one 681 MiB session, so misclassifying these as
	// signal destroys the compression ratio entirely.
	"session.binary_asset":           Artifact,
	"session.workspace_file_changed": Artifact,

	"assistant.turn_start":        Bookkeeping,
	"assistant.turn_end":          Bookkeeping,
	"system.message":              Bookkeeping,
	"system.notification":         Bookkeeping,
	"system.info":                 Bookkeeping,
	"session.info":                Bookkeeping,
	"session.warning":             Bookkeeping,
	"session.start":               Bookkeeping,
	"session.resume":              Bookkeeping,
	"session.shutdown":            Bookkeeping,
	"session.mode_changed":        Bookkeeping,
	"session.model_change":        Bookkeeping,
	"session.context_changed":     Bookkeeping,
	"session.permissions_changed": Bookkeeping,
	"session.usage_checkpoint":    Bookkeeping,
	"session.compaction_start":    Bookkeeping,
	"session.compaction_complete": Bookkeeping,
	"subagent.started":            Bookkeeping,
	"subagent.selected":           Bookkeeping,
	"permission.requested":        Bookkeeping,
	"permission.completed":        Bookkeeping,
	"abort":                       Bookkeeping,

	// --- Claude transcripts ---
	"user":      Signal,
	"assistant": Signal,
	"summary":   Signal,
	"ai-title":  Signal,
	"pr-link":   Signal,

	"tool_use":    Exhaust,
	"tool_result": Exhaust,

	"file-history-snapshot": Artifact,
	"attachment":            Artifact,
	"edited_text_file":      Artifact,

	"mode":                   Bookkeeping,
	"last-prompt":            Bookkeeping,
	"permission-mode":        Bookkeeping,
	"queue-operation":        Bookkeeping,
	"queued_command":         Bookkeeping,
	"task_reminder":          Bookkeeping,
	"deferred_tools_delta":   Bookkeeping,
	"mcp_instructions_delta": Bookkeeping,
	"hook_success":           Bookkeeping,
	"system":                 Bookkeeping,

	// --- opencode parts ---
	"text":        Signal,
	"reasoning":   Signal,
	"tool":        Exhaust,
	"step-start":  Bookkeeping,
	"step-finish": Bookkeeping,
	"file":        Artifact,
	"patch":       Artifact,
	"snapshot":    Artifact,

	// Records that failed to parse are still bytes on disk.
	"unparsed": Exhaust,
}

// Classify maps a tool-native record kind to a class.
//
// Unknown kinds are classified structurally rather than optimistically: a
// "binary"/"asset"/"image" kind is an artifact, a "tool" kind is exhaust, and
// anything else falls back to Signal because wrongly discarding meaning is
// worse than wrongly keeping bulk.
func Classify(kind string) Class {
	if strings.HasPrefix(kind, "model.") || strings.HasPrefix(kind, "hook.") {
		return Bookkeeping
	}
	if c, ok := classByKind[kind]; ok {
		return c
	}
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(k, "binary"), strings.Contains(k, "asset"),
		strings.Contains(k, "image"), strings.Contains(k, "screenshot"),
		strings.Contains(k, "attachment"), strings.Contains(k, "snapshot"):
		return Artifact
	case strings.Contains(k, "tool"), strings.Contains(k, "exec"):
		return Exhaust
	case strings.HasPrefix(k, "session."), strings.HasPrefix(k, "permission."),
		strings.HasPrefix(k, "system."), strings.Contains(k, "delta"),
		strings.Contains(k, "reminder"):
		return Bookkeeping
	}
	return Signal
}

// KnownKind reports whether a kind was explicitly classified rather than
// inferred. Used to surface format drift.
func KnownKind(kind string) bool {
	_, ok := classByKind[kind]
	return ok
}

// IsImage reports whether a record kind or payload looks like image data.
func IsImage(kind, payload string) bool {
	k := strings.ToLower(kind)
	if strings.Contains(k, "image") || strings.Contains(k, "screenshot") {
		return true
	}
	return strings.Contains(payload, "data:image/") ||
		strings.Contains(payload, `"type":"image"`)
}

// PreviewLen bounds how much of a signal record is retained in a manifest:
// enough to judge relevance, never enough to reconstruct the transcript.
const PreviewLen = 240

// Preview trims a payload to a bounded, single-line excerpt.
func Preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= PreviewLen {
		return s
	}
	return string(r[:PreviewLen]) + "..."
}
