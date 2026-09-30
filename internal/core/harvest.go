package core

import "time"

// Turn is one exchange in a session, normalised across tools.
type Turn struct {
	Index int       `json:"index"`
	Role  string    `json:"role"` // user | assistant
	Text  string    `json:"text"`
	Time  time.Time `json:"time,omitempty"`
}

// Harvester extracts turns from a session transcript.
//
// Implementations MUST stream. Transcripts reach hundreds of megabytes, and
// user messages are ~1.3% of the bytes, so the whole file is never held in
// memory to recover the part that matters.
type Harvester interface {
	// Harvest returns the first user turn plus the most recent turns, capped
	// at maxTurns. Memory is O(maxTurns), not O(file size).
	Harvest(s Session, maxTurns int) (Harvest, error)
}

// Harvest is what can be recovered from a session without an LLM.
//
// This is the deterministic floor of RECLAIM: enough context to resume the
// work elsewhere, at zero token cost.
type Harvest struct {
	Goal          *Turn  `json:"goal,omitempty"`           // first user turn: the original ask
	Recent        []Turn `json:"recent"`                   // most recent turns, oldest first
	LastAssistant *Turn  `json:"last_assistant,omitempty"` // where it left off
	UserTurns     int    `json:"user_turns"`
	TotalRecords  int    `json:"total_records"`
	Truncated     bool   `json:"truncated"` // recent omits earlier turns
}
