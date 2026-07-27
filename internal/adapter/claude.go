package adapter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// Claude reads Claude Code sessions.
//
// Claude stores one JSONL transcript per session under a directory named after
// the encoded working directory. Resume is therefore cwd-scoped: the command
// only works from the session's original directory.
type Claude struct {
	Root string
}

func NewClaude() *Claude { return &Claude{Root: homeJoin(".claude")} }

func (c *Claude) Tool() core.Tool { return core.ToolClaude }

func (c *Claude) projectsDir() string { return filepath.Join(c.Root, "projects") }

func (c *Claude) Available() bool { return exists(c.projectsDir()) }

// Footprint covers the whole Claude data directory, not just transcripts.
func (c *Claude) Footprint() int64 { return dirSize(c.Root) }

// minTranscriptBytes filters out abandoned sessions with no real exchange.
const minTranscriptBytes = 2 << 10

func (c *Claude) Sessions(sc core.Scope) ([]core.Session, error) {
	live := c.liveMap()
	cutoff := sc.Since()

	var out []core.Session
	err := filepath.WalkDir(c.projectsDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, don't abort the scan
		}
		if d.IsDir() {
			// Sub-agent transcripts are not independently resumable.
			if d.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}

		id := strings.TrimSuffix(d.Name(), ".jsonl")
		if strings.HasPrefix(id, "agent-") || id == "journal" {
			return nil
		}

		fi, ferr := d.Info()
		if ferr != nil || fi.Size() < minTranscriptBytes {
			return nil
		}

		updated := fi.ModTime()
		if !cutoff.IsZero() && updated.Before(cutoff) {
			return nil // cheap reject before parsing the file
		}

		cwd, title, created := c.peek(path)
		if cwd == "" {
			return nil
		}

		s := core.Session{
			Tool:           core.ToolClaude,
			ID:             id,
			Dir:            filepath.Clean(cwd),
			Title:          core.CleanTitle(title),
			Created:        created,
			Updated:        updated,
			Bytes:          fi.Size(),
			TranscriptPath: path,
		}
		if s.Created.IsZero() {
			s.Created = updated
		}
		if l, ok := live[id]; ok {
			s.Live = l
		}
		s.Noise = core.IsNoise(title, s.Dir, 2)

		if sc.Match(s) {
			out = append(out, s)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	return out, nil
}

func (c *Claude) ResumeCmd(s core.Session, instruction string) string {
	if instruction != "" {
		return fmt.Sprintf("claude --resume %s %s", s.ID, shellQuote(instruction))
	}
	return "claude --resume " + s.ID
}

// peekLines caps how much of a transcript is read to describe it. Transcripts
// reach tens of MB; the identifying records are at the top.
const peekLines = 60

// peek extracts cwd, a display title and the first timestamp without reading
// the whole transcript. A `summary` record wins over the first user message.
func (c *Claude) peek(path string) (cwd, title string, created time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", time.Time{}
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20) // transcripts contain very long lines

	var summary, firstUser string
	for i := 0; i < peekLines && sc.Scan(); i++ {
		var rec claudeRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if cwd == "" && rec.Cwd != "" {
			cwd = rec.Cwd
		}
		if created.IsZero() && rec.Timestamp != "" {
			created = parseISO(rec.Timestamp)
		}
		if summary == "" && rec.Type == "summary" && rec.Summary != "" {
			summary = rec.Summary
		}
		if firstUser == "" && rec.Type == "user" {
			firstUser = rec.Message.text()
		}
		if cwd != "" && summary != "" && !created.IsZero() {
			break
		}
	}

	if summary != "" {
		return cwd, summary, created
	}
	return cwd, firstUser, created
}

// liveMap reports sessions that are open right now.
//
// Claude writes ~/.claude/sessions/<pid>.json for each interactive session.
// The PID is verified because these files outlive the process that wrote them.
func (c *Claude) liveMap() map[string]*core.Live {
	out := map[string]*core.Live{}

	entries, err := os.ReadDir(filepath.Join(c.Root, "sessions"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.Root, "sessions", e.Name()))
		if err != nil {
			continue
		}
		var m struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Status    string `json:"status"`
			Name      string `json:"name"`
		}
		if json.Unmarshal(b, &m) != nil || m.SessionID == "" || m.PID == 0 {
			continue
		}
		if !processAlive(m.PID) {
			continue
		}
		out[m.SessionID] = &core.Live{PID: m.PID, Status: m.Status, Name: m.Name}
	}
	return out
}

// claudeRecord is the subset of a transcript record Midden needs.
type claudeRecord struct {
	Type      string        `json:"type"`
	Cwd       string        `json:"cwd"`
	Summary   string        `json:"summary"`
	Timestamp string        `json:"timestamp"`
	Message   claudeMessage `json:"message"`
}

// claudeMessage handles content that is either a plain string or an array of
// typed blocks.
type claudeMessage struct {
	Content json.RawMessage `json:"content"`
}

func (m claudeMessage) text() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(m.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return b.Text
			}
		}
	}
	return ""
}
