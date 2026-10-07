// Package cost holds the model usage that AI CLIs record for their own
// sessions, normalised across tools.
package cost

// Usage is normalised token accounting, whatever tool recorded it.
type Usage struct {
	Model        string  `json:"model,omitempty"`
	Turns        int     `json:"turns"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	Reasoning    int64   `json:"reasoning_tokens"`
	AIU          float64 `json:"aiu,omitempty"` // Copilot credits
	USD          float64 `json:"usd,omitempty"` // opencode reports this directly
	DurationMS   int64   `json:"duration_ms"`
}

// Reader reads recorded usage for a CLI session out of that tool's own store.
type Reader interface {
	// Usage returns what a session actually consumed. A session that has not
	// been flushed to disk yet returns an empty Usage and no error.
	Usage(sessionID string) (Usage, error)
}
