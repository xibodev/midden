// Package core defines the types shared by every adapter and command.
package core

import (
	"os"
	"strings"
	"time"
)

// Tool identifies an AI CLI whose sessions Midden can read.
type Tool string

const (
	ToolCopilot  Tool = "copilot"
	ToolClaude   Tool = "claude"
	ToolOpencode Tool = "opencode"
)

// Session is one resumable conversation, normalised across tools.
type Session struct {
	Tool    Tool      `json:"tool"`
	ID      string    `json:"id"`
	Dir     string    `json:"dir"`
	Title   string    `json:"title"`
	Repo    string    `json:"repo,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`

	// Turns is the message/turn count. Bytes is on-disk transcript size.
	Turns int   `json:"turns"`
	Bytes int64 `json:"bytes"`

	// Live is non-nil when the session is currently open in a running process.
	Live *Live `json:"live,omitempty"`

	// Noise marks automated spawns, health probes and trivial sessions.
	Noise bool `json:"noise"`

	// TranscriptPath is the file backing this session, when there is one.
	TranscriptPath string `json:"transcript_path,omitempty"`
}

// Live describes a session that is open right now.
type Live struct {
	PID    int    `json:"pid"`
	Status string `json:"status,omitempty"`
	Name   string `json:"name,omitempty"`
}

// Span is how long the session has been alive. A large span with a recent
// Updated is the signature of an idle-but-open session, which age alone hides.
func (s Session) Span() time.Duration {
	if s.Created.IsZero() || s.Updated.Before(s.Created) {
		return 0
	}
	return s.Updated.Sub(s.Created)
}

// SpanDays is Span in fractional days.
func (s Session) SpanDays() float64 { return s.Span().Hours() / 24 }

// Age is how long since the session was last touched.
func (s Session) Age() time.Duration { return time.Since(s.Updated) }

// DirExists reports whether the session's workspace still exists. Resuming
// into a deleted directory fails, so this is checked before offering a command.
func (s Session) DirExists() bool {
	if s.Dir == "" {
		return false
	}
	fi, err := os.Stat(s.Dir)
	return err == nil && fi.IsDir()
}

// Risk classifies a session's likelihood of failing to resume.
type Risk int

const (
	RiskNone Risk = iota
	RiskWatch
	RiskWarn
	RiskCritical
)

func (r Risk) String() string {
	switch r {
	case RiskCritical:
		return "critical"
	case RiskWarn:
		return "warn"
	case RiskWatch:
		return "watch"
	default:
		return "ok"
	}
}

// Resume-failure thresholds.
//
// Copilot CLI stores a per-session events.jsonl alongside its SQLite metadata.
// Above roughly 680 MiB, `copilot --resume` times out after ~18s, logs a
// "Deferred resume" warning, and silently starts a NEW session instead. This
// was observed destroying four sessions (774/740/687/681 MiB) on one machine.
//
// Thresholds are deliberately conservative: the cost of an early warning is a
// handoff, the cost of a late one is losing days of work.
const (
	RiskWatchBytes    int64 = 300 << 20 // 300 MiB
	RiskWarnBytes     int64 = 450 << 20 // 450 MiB
	RiskCriticalBytes int64 = 640 << 20 // 640 MiB
)

// Risk scores a session against the resume cliff. Only tools that keep a
// single monolithic transcript are affected.
func (s Session) Risk() Risk {
	switch {
	case s.Bytes >= RiskCriticalBytes:
		return RiskCritical
	case s.Bytes >= RiskWarnBytes:
		return RiskWarn
	case s.Bytes >= RiskWatchBytes:
		return RiskWatch
	default:
		return RiskNone
	}
}

// Adapter reads sessions for one AI CLI. Implementations MUST open every
// source store read-only: a live CLI may be writing to it concurrently.
type Adapter interface {
	// Tool identifies the CLI this adapter reads.
	Tool() Tool

	// Available reports whether this tool's data is present on the machine.
	Available() bool

	// Sessions returns every session matching the scope.
	Sessions(Scope) ([]Session, error)

	// ResumeCmd returns the shell command that resumes the session, assuming
	// the caller is already in the session's directory.
	ResumeCmd(s Session, instruction string) string

	// Footprint is the total bytes this tool occupies on disk, including
	// caches and indexes that no individual session accounts for. Per-session
	// transcript bytes rarely sum to it.
	Footprint() int64
}

// Scope narrows a query. The zero value matches everything.
//
// Scoping is mandatory for any expensive operation: nobody salvages 36 GB
// blind. It exists on read commands so the same filters compose downstream.
type Scope struct {
	Tools        []Tool
	Days         int    // 0 = unbounded; calendar days, not exact hours
	Workspace    string // substring match against Dir
	Repo         string
	IDPrefix     string
	IncludeNoise bool
	Limit        int

	// WithSizes requests per-session byte accounting for tools that store
	// transcripts inside a database rather than as files. Measured cost on a
	// 341k-row store: ~190s. Opt-in only, never on a default path.
	WithSizes bool
}

// Since returns the cutoff time, or the zero time when unbounded.
// Calendar-day granularity keeps every adapter consistent; mixing exact-hour
// and calendar-day cutoffs across adapters produces windows that disagree.
func (sc Scope) Since() time.Time {
	if sc.Days <= 0 {
		return time.Time{}
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return midnight.AddDate(0, 0, -sc.Days)
}

// WantsTool reports whether the scope includes a tool.
func (sc Scope) WantsTool(t Tool) bool {
	if len(sc.Tools) == 0 {
		return true
	}
	for _, x := range sc.Tools {
		if x == t {
			return true
		}
	}
	return false
}

// Match applies the scope's filters to a session.
func (sc Scope) Match(s Session) bool {
	if !sc.WantsTool(s.Tool) {
		return false
	}
	if s.Noise && !sc.IncludeNoise {
		return false
	}
	if cutoff := sc.Since(); !cutoff.IsZero() && s.Updated.Before(cutoff) {
		return false
	}
	if sc.Workspace != "" && !containsFold(s.Dir, sc.Workspace) {
		return false
	}
	if sc.Repo != "" && !containsFold(s.Repo, sc.Repo) {
		return false
	}
	if sc.IDPrefix != "" && !strings.HasPrefix(s.ID, sc.IDPrefix) {
		return false
	}
	return true
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
