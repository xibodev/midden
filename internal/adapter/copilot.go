package adapter

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// Copilot reads GitHub Copilot CLI sessions.
//
// Copilot keeps BOTH a SQLite metadata store and a per-session events.jsonl
// transcript. Size — and therefore resume risk — lives in the jsonl, not the
// database, so both must be read to describe a session honestly.
type Copilot struct {
	Root string
}

func NewCopilot() *Copilot { return &Copilot{Root: homeJoin(".copilot")} }

func (c *Copilot) Tool() core.Tool { return core.ToolCopilot }

func (c *Copilot) storeDB() string { return filepath.Join(c.Root, "session-store.db") }

func (c *Copilot) Available() bool { return exists(c.storeDB()) }

// Footprint includes session-state transcripts, logs and caches.
func (c *Copilot) Footprint() int64 { return dirSize(c.Root) }

// transcriptPath is the per-session event log. This is the file that grows to
// hundreds of MiB and eventually breaks --resume.
func (c *Copilot) transcriptPath(id string) string {
	return filepath.Join(c.Root, "session-state", id, "events.jsonl")
}

func (c *Copilot) Sessions(sc core.Scope) ([]core.Session, error) {
	db, closeDB, err := openRO(c.storeDB())
	if err != nil {
		return nil, fmt.Errorf("copilot: %w", err)
	}
	defer closeDB()

	// Title falls back to the first user turn: long-running sessions often
	// never receive an auto-generated summary, and filtering on a blank
	// summary silently hides exactly the sessions that matter most.
	const q = `
		SELECT s.id,
		       COALESCE(s.cwd, ''),
		       COALESCE(NULLIF(TRIM(s.summary), ''),
		                (SELECT SUBSTR(t.user_message, 1, 200) FROM turns t
		                  WHERE t.session_id = s.id ORDER BY t.turn_index LIMIT 1),
		                '') AS title,
		       COALESCE(s.repository, ''),
		       COALESCE(s.created_at, ''),
		       COALESCE(s.updated_at, s.created_at, ''),
		       (SELECT COUNT(*) FROM turns t2 WHERE t2.session_id = s.id) AS turns
		FROM sessions s
		WHERE s.cwd IS NOT NULL AND TRIM(s.cwd) != ''
		ORDER BY COALESCE(s.updated_at, s.created_at) DESC`

	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("copilot query: %w", err)
	}
	defer rows.Close()

	var out []core.Session
	for rows.Next() {
		var (
			id, cwd, title, repo string
			createdS, updatedS   string
			turns                int
		)
		if err := rows.Scan(&id, &cwd, &title, &repo, &createdS, &updatedS, &turns); err != nil {
			return nil, err
		}

		s := core.Session{
			Tool:           core.ToolCopilot,
			ID:             id,
			Dir:            filepath.Clean(cwd),
			Title:          core.CleanTitle(title),
			Repo:           repo,
			Created:        parseISO(createdS),
			Updated:        parseISO(updatedS),
			Turns:          turns,
			TranscriptPath: c.transcriptPath(id),
		}
		s.Bytes = fileSize(s.TranscriptPath)
		s.Noise = core.IsNoise(title, s.Dir, turns)

		if sc.Match(s) {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func (c *Copilot) ResumeCmd(s core.Session, instruction string) string {
	if instruction != "" {
		return fmt.Sprintf("copilot --resume %s --prompt %s", s.ID, shellQuote(instruction))
	}
	return "copilot --resume " + s.ID
}

// parseISO handles the ISO-8601 timestamps Copilot writes, e.g.
// "2026-07-26T17:47:32.700Z".
func parseISO(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999Z",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Local()
		}
	}
	return time.Time{}
}
