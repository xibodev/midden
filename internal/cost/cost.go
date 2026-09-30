// Package cost accounts for what Midden actually spends.
//
// Estimates are guesses; this package deals in recorded truth. Every AI CLI
// writes its own token accounting to disk, and Midden already knows how to
// read those stores — so after any operation it can read back exactly what the
// operation cost, rather than trusting a prediction.
//
// This matters because the predictions were badly wrong. A pre-flight estimate
// of ~3,069 tokens for one reclaim actually consumed 219,178 input tokens: the
// estimate counted only Midden's own prompt and ignored the CLI's system
// prompt, its tool definitions, and the extra turn spent reading a staged
// prompt file. A budget enforced against a 70x underestimate is not a budget.
package cost

import (
	"fmt"
	"time"
)

// Usage is normalised token accounting, whatever tool produced it.
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

// Billable is every token that moved, which is the honest measure of what an
// operation consumed regardless of how a provider prices it.
func (u Usage) Billable() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheRead + u.CacheWrite
}

// Empty reports whether anything was recorded.
func (u Usage) Empty() bool { return u.Turns == 0 && u.Billable() == 0 }

// Add folds another usage record in.
func (u *Usage) Add(o Usage) {
	u.Turns += o.Turns
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Reasoning += o.Reasoning
	u.AIU += o.AIU
	u.USD += o.USD
	u.DurationMS += o.DurationMS
	if u.Model == "" {
		u.Model = o.Model
	}
}

// Unit renders the most meaningful cost figure a tool provides.
//
// Under a subscription the scarce resource is credits or requests, not
// dollars, so those are preferred when available.
func (u Usage) Unit() string {
	switch {
	case u.AIU > 0:
		return fmt.Sprintf("%.1f AIU", u.AIU)
	case u.USD > 0:
		return fmt.Sprintf("$%.2f", u.USD)
	default:
		return Compact(u.Billable()) + " tok"
	}
}

// Compact renders a token count briefly.
func Compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// Reader reads recorded usage for a CLI session out of that tool's own store.
type Reader interface {
	// Usage returns what a session actually consumed. A session that has not
	// been flushed to disk yet returns an empty Usage and no error.
	Usage(sessionID string) (Usage, error)
}

// Run is one recorded Midden operation, predicted and actual side by side.
type Run struct {
	UID     string `json:"uid"`
	Op      string `json:"op"`
	Scope   string `json:"scope"`
	Backend string `json:"backend"`

	// CLISessions are the sessions the backend created for this run, which is
	// how actual usage is recovered afterwards.
	CLISessions []string `json:"cli_sessions,omitempty"`

	EstTokens int   `json:"est_tokens"`
	Items     int   `json:"items"`
	Usage     Usage `json:"usage"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	OK        bool      `json:"ok"`
	Note      string    `json:"note,omitempty"`
}

// Accuracy is actual billable tokens over what was predicted.
//
// A value of 1 means the estimate was right; 70 means it was wrong by 70x,
// which is what the first implementation achieved.
func (r Run) Accuracy() float64 {
	if r.EstTokens <= 0 || r.Usage.Billable() == 0 {
		return 0
	}
	return float64(r.Usage.Billable()) / float64(r.EstTokens)
}

// PerItem is the cost of each thing produced, which is the number worth
// comparing across runs.
func (r Run) PerItem() float64 {
	if r.Items <= 0 {
		return 0
	}
	if r.Usage.AIU > 0 {
		return r.Usage.AIU / float64(r.Items)
	}
	return float64(r.Usage.Billable()) / float64(r.Items)
}

// Duration is how long the run took.
func (r Run) Duration() time.Duration {
	if r.EndedAt.IsZero() || r.StartedAt.IsZero() {
		return time.Duration(r.Usage.DurationMS) * time.Millisecond
	}
	return r.EndedAt.Sub(r.StartedAt)
}
