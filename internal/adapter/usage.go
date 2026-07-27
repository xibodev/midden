package adapter

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/cost"
)

// Usage reads what a Copilot session actually consumed.
//
// Copilot keeps per-turn accounting in assistant_usage_events, including cache
// reads and writes and the credit charge in nano-AIU. This is the ground truth
// that pre-flight estimates are calibrated against.
func (c *Copilot) Usage(sessionID string) (cost.Usage, error) {
	db, closeDB, err := openRO(c.storeDB())
	if err != nil {
		return cost.Usage{}, err
	}
	defer closeDB()

	var (
		u       cost.Usage
		nanoAIU int64
		model   *string
	)
	row := db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0),
		       COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(total_nano_aiu),0),
		       COALESCE(SUM(duration_ms),0),
		       (SELECT model FROM assistant_usage_events
		         WHERE session_id LIKE ? ORDER BY created_at DESC LIMIT 1)
		FROM assistant_usage_events
		WHERE session_id LIKE ?`, sessionID+"%", sessionID+"%")

	if err := row.Scan(&u.Turns, &u.InputTokens, &u.OutputTokens, &u.CacheRead,
		&u.CacheWrite, &u.Reasoning, &nanoAIU, &u.DurationMS, &model); err != nil {
		return cost.Usage{}, fmt.Errorf("copilot usage: %w", err)
	}

	u.AIU = float64(nanoAIU) / 1e9
	if model != nil {
		u.Model = *model
	}
	return u, nil
}

// Usage reads what a Claude session actually consumed.
//
// Claude records usage on each assistant record in the transcript, so this
// streams the file rather than loading it.
func (c *Claude) Usage(sessionID string) (cost.Usage, error) {
	path, err := c.transcriptFor(sessionID)
	if err != nil {
		return cost.Usage{}, err
	}

	var u cost.Usage
	err = eachLine(path, func(line []byte) bool {
		if !strings.Contains(string(line), `"usage"`) {
			return true
		}
		var rec struct {
			Message struct {
				Model string `json:"model"`
				Usage struct {
					Input      int64 `json:"input_tokens"`
					Output     int64 `json:"output_tokens"`
					CacheWrite int64 `json:"cache_creation_input_tokens"`
					CacheRead  int64 `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		usage := rec.Message.Usage
		if usage.Input == 0 && usage.Output == 0 && usage.CacheRead == 0 && usage.CacheWrite == 0 {
			return true
		}
		u.Turns++
		u.InputTokens += usage.Input
		u.OutputTokens += usage.Output
		u.CacheRead += usage.CacheRead
		u.CacheWrite += usage.CacheWrite
		if rec.Message.Model != "" {
			u.Model = rec.Message.Model
		}
		return true
	})
	if err != nil {
		return cost.Usage{}, fmt.Errorf("claude usage: %w", err)
	}
	return u, nil
}

// transcriptFor locates a Claude transcript by session id. Claude keys
// transcripts by encoded working directory, so the path is not derivable from
// the id alone.
func (c *Claude) transcriptFor(sessionID string) (string, error) {
	sessions, err := c.Sessions(coreScopeForID(sessionID))
	if err != nil {
		return "", err
	}
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, sessionID) {
			return s.TranscriptPath, nil
		}
	}
	return "", fmt.Errorf("claude session %s not found", sessionID)
}

// Usage reads what an opencode session actually consumed.
//
// opencode stores per-message token counts and a dollar cost directly on the
// assistant message.
func (o *Opencode) Usage(sessionID string) (cost.Usage, error) {
	db, closeDB, err := openRO(o.DB)
	if err != nil {
		return cost.Usage{}, err
	}
	defer closeDB()

	rows, err := db.Query(`
		SELECT data FROM message
		WHERE session_id = ? AND data LIKE '%"tokens"%'`, sessionID)
	if err != nil {
		return cost.Usage{}, fmt.Errorf("opencode usage: %w", err)
	}
	defer rows.Close()

	var u cost.Usage
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var m struct {
			ModelID string  `json:"modelID"`
			Cost    float64 `json:"cost"`
			Tokens  struct {
				Input     int64 `json:"input"`
				Output    int64 `json:"output"`
				Reasoning int64 `json:"reasoning"`
				Cache     struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		}
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		u.Turns++
		u.InputTokens += m.Tokens.Input
		u.OutputTokens += m.Tokens.Output
		u.Reasoning += m.Tokens.Reasoning
		u.CacheRead += m.Tokens.Cache.Read
		u.CacheWrite += m.Tokens.Cache.Write
		u.USD += m.Cost
		if m.ModelID != "" {
			u.Model = m.ModelID
		}
	}
	return u, rows.Err()
}

// UsageFor reads recorded usage for a session from whichever tool owns it.
//
// A session that has not been flushed to disk yet reports empty usage rather
// than an error: accounting lags the run by moments.
func UsageFor(tool, sessionID string) (cost.Usage, error) {
	for _, a := range All() {
		if string(a.Tool()) != tool {
			continue
		}
		r, ok := a.(cost.Reader)
		if !ok {
			return cost.Usage{}, fmt.Errorf("%s does not record usage", tool)
		}
		return r.Usage(sessionID)
	}
	return cost.Usage{}, fmt.Errorf("unknown tool %q", tool)
}
