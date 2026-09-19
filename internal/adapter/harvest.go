package adapter

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/mekjr1/midden/internal/core"
)

// maxLineBytes caps how much of any single transcript line is retained.
//
// Tool-result lines reach tens of megabytes; user messages never do. Oversized
// lines are truncated rather than aborting the scan, because bufio.Scanner
// would stop dead at the first line larger than its buffer and silently
// truncate the harvest.
const maxLineBytes = 1 << 20

// collector keeps the first turn plus a bounded ring of the most recent, so a
// 774 MiB transcript costs O(max) memory instead of O(file size).
type collector struct {
	max           int
	goal          *core.Turn
	ring          []core.Turn
	lastAssistant *core.Turn
	userTurns     int
	records       int
	dropped       bool
}

func newCollector(max int) *collector {
	if max <= 0 {
		max = 12
	}
	return &collector{max: max}
}

func (c *collector) add(t core.Turn) {
	c.records++

	if t.Role == "user" {
		c.userTurns++
		if c.goal == nil {
			g := t
			c.goal = &g
		}
	}
	if t.Role == "assistant" {
		a := t
		c.lastAssistant = &a
	}

	c.ring = append(c.ring, t)
	if len(c.ring) > c.max {
		c.ring = c.ring[1:]
		c.dropped = true
	}
}

func (c *collector) result() core.Harvest {
	return core.Harvest{
		Goal:          c.goal,
		Recent:        c.ring,
		LastAssistant: c.lastAssistant,
		UserTurns:     c.userTurns,
		TotalRecords:  c.records,
		Truncated:     c.dropped,
	}
}

// eachLine streams a file line by line, truncating any line longer than
// maxLineBytes. Returning false from fn stops the scan early.
func eachLine(path string, fn func(line []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return eachLineReader(f, fn)
}

func eachLineReader(input io.Reader, fn func(line []byte) bool) error {
	r := bufio.NewReaderSize(input, 256<<10)
	buf := make([]byte, 0, 8<<10)

	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// Line exceeds the reader buffer: keep the head, discard the rest.
			if len(buf) < maxLineBytes {
				buf = append(buf, chunk...)
			}
			continue
		}
		if len(chunk) > 0 {
			if len(buf) < maxLineBytes {
				buf = append(buf, chunk...)
			}
			if len(buf) > 0 && !fn(buf) {
				return nil
			}
			buf = buf[:0]
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// clip bounds a single turn's text so a brief stays paste-able.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n[... truncated]"
}

// --- Copilot ----------------------------------------------------------------

// copilotEvent is the subset of an events.jsonl record needed to rebuild the
// conversation. `data.content` is the clean text; `transformedContent` carries
// injected wrappers and is deliberately ignored.
type copilotEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		Content json.RawMessage `json:"content"`
	} `json:"data"`
}

// Harvest streams the per-session events.jsonl.
//
// Only user.message and assistant.message are decoded; tool events are ~68% of
// the bytes and are skipped by a cheap prefix test before any JSON parsing.
func (c *Copilot) Harvest(s core.Session, maxTurns int) (core.Harvest, error) {
	col := newCollector(maxTurns)
	path := s.TranscriptPath
	if path == "" {
		path = c.transcriptPath(s.ID)
	}

	idx := 0
	err := eachLine(path, func(line []byte) bool {
		// Cheap reject before parsing: the interesting events are a tiny
		// fraction of the file.
		if !containsAny(line, `"user.message"`, `"assistant.message"`) {
			return true
		}
		var ev copilotEvent
		if json.Unmarshal(line, &ev) != nil {
			return true
		}

		var role string
		switch ev.Type {
		case "user.message":
			role = "user"
		case "assistant.message":
			role = "assistant"
		default:
			return true
		}

		text := rawText(ev.Data.Content)
		if strings.TrimSpace(text) == "" {
			return true
		}
		idx++
		col.add(core.Turn{Index: idx, Role: role, Text: text, Time: parseISO(ev.Timestamp)})
		return true
	})
	if err != nil {
		return core.Harvest{}, err
	}
	return col.result(), nil
}

func containsAny(b []byte, subs ...string) bool {
	s := stringView(b)
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func stringView(b []byte) string { return string(b) }

// rawText decodes content that may be a plain string or an array of typed
// blocks.
func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var out []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				out = append(out, b.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// --- Claude -----------------------------------------------------------------

// Harvest streams a Claude transcript.
func (c *Claude) Harvest(s core.Session, maxTurns int) (core.Harvest, error) {
	col := newCollector(maxTurns)

	idx := 0
	err := eachLine(s.TranscriptPath, func(line []byte) bool {
		var rec claudeRecord
		if json.Unmarshal(line, &rec) != nil {
			return true
		}
		if rec.Type != "user" && rec.Type != "assistant" {
			return true
		}
		text := rec.Message.text()
		if strings.TrimSpace(text) == "" {
			return true
		}
		// Injected local-command scaffolding is not conversation.
		if strings.HasPrefix(text, "<local-command") || strings.HasPrefix(text, "<command-name") {
			return true
		}
		idx++
		col.add(core.Turn{Index: idx, Role: rec.Type, Text: text, Time: parseISO(rec.Timestamp)})
		return true
	})
	if err != nil {
		return core.Harvest{}, err
	}
	return col.result(), nil
}

// --- opencode ---------------------------------------------------------------

// Harvest reads turns from the opencode database.
//
// Roles live on `message`, text lives on `part` rows of type "text"; the join
// is ordered so a session reads back as a conversation.
func (o *Opencode) Harvest(s core.Session, maxTurns int) (core.Harvest, error) {
	db, closeDB, err := openRO(o.DB)
	if err != nil {
		return core.Harvest{}, err
	}
	defer closeDB()

	rows, err := db.Query(`
		SELECT m.data, p.data
		FROM message m
		JOIN part p ON p.message_id = m.id
		WHERE m.session_id = ?
		ORDER BY m.time_created, p.time_created`, s.ID)
	if err != nil {
		return core.Harvest{}, err
	}
	defer rows.Close()

	col := newCollector(maxTurns)
	idx := 0
	for rows.Next() {
		var msgJSON, partJSON string
		if rows.Scan(&msgJSON, &partJSON) != nil {
			continue
		}

		var msg struct {
			Role string `json:"role"`
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
		}
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(msgJSON), &msg) != nil {
			continue
		}
		if json.Unmarshal([]byte(partJSON), &part) != nil || part.Type != "text" {
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}

		idx++
		col.add(core.Turn{
			Index: idx,
			Role:  msg.Role,
			Text:  part.Text,
			Time:  fromUnixMS(msg.Time.Created),
		})
	}
	return col.result(), rows.Err()
}
