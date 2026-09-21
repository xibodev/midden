package adapter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

// Assayer classifies a session's records without an LLM.
//
// Implementations stream: a manifest for a 774 MiB transcript must not cost
// 774 MiB of memory.
type Assayer interface {
	Assay(s core.Session, maxCandidates int) (*assay.Manifest, error)
}

type EvidenceReader interface {
	ReadEvidence(s core.Session, selection assay.Selection) (*assay.Manifest, error)
}

// assayHeader is the minimal shape shared by the jsonl-based tools.
type assayHeader struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role string `json:"role"`
	} `json:"message"`
}

// Assay streams a Copilot events.jsonl.
func (c *Copilot) Assay(s core.Session, maxCandidates int) (*assay.Manifest, error) {
	return c.scanEvidence(s, assay.NewScanner(s.ID, string(core.ToolCopilot), maxCandidates))
}

func (c *Copilot) ReadEvidence(s core.Session, selection assay.Selection) (*assay.Manifest, error) {
	return c.scanEvidence(s, assay.NewEvidenceScanner(s.ID, string(core.ToolCopilot), selection))
}

func (c *Copilot) scanEvidence(s core.Session, sc *assay.Scanner) (*assay.Manifest, error) {
	path := s.TranscriptPath
	if path == "" {
		path = c.transcriptPath(s.ID)
	}

	start := time.Now()

	m, err := readFileView(path, sc, func(line []byte) bool {
		var h assayHeader
		if json.Unmarshal(line, &h) != nil {
			// Unparseable lines are still bytes on disk; count them as
			// exhaust rather than pretending the file is smaller than it is.
			sc.Observe("unparsed", "", line, time.Time{})
			return true
		}
		sc.Observe(h.Type, roleFromKind(h.Type), line, parseISO(h.Timestamp))
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("assay copilot: %w", err)
	}

	m.Title = s.Title
	m.Elapsed = time.Since(start)
	return m, nil
}

// Assay streams a Claude transcript.
func (c *Claude) Assay(s core.Session, maxCandidates int) (*assay.Manifest, error) {
	return c.scanEvidence(s, assay.NewScanner(s.ID, string(core.ToolClaude), maxCandidates))
}

func (c *Claude) ReadEvidence(s core.Session, selection assay.Selection) (*assay.Manifest, error) {
	return c.scanEvidence(s, assay.NewEvidenceScanner(s.ID, string(core.ToolClaude), selection))
}

func (c *Claude) scanEvidence(s core.Session, sc *assay.Scanner) (*assay.Manifest, error) {
	start := time.Now()

	m, err := readFileView(s.TranscriptPath, sc, func(line []byte) bool {
		var h assayHeader
		if json.Unmarshal(line, &h) != nil {
			sc.Observe("unparsed", "", line, time.Time{})
			return true
		}
		sc.Observe(h.Type, h.Message.Role, line, parseISO(h.Timestamp))
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("assay claude: %w", err)
	}

	m.Title = s.Title
	m.Elapsed = time.Since(start)
	return m, nil
}

// Assay reads opencode parts.
//
// opencode stores content in rows rather than a file, so record size is the
// stored JSON length. Rows stream out of the driver, so memory stays bounded.
func (o *Opencode) Assay(s core.Session, maxCandidates int) (*assay.Manifest, error) {
	return o.scanEvidence(s, assay.NewScanner(s.ID, string(core.ToolOpencode), maxCandidates))
}

func (o *Opencode) ReadEvidence(s core.Session, selection assay.Selection) (*assay.Manifest, error) {
	return o.scanEvidence(s, assay.NewEvidenceScanner(s.ID, string(core.ToolOpencode), selection))
}

func (o *Opencode) scanEvidence(s core.Session, sc *assay.Scanner) (*assay.Manifest, error) {
	limit := int64(-1)
	if view := sc.ViewBoundary(); view != nil {
		if view.Kind != rowPrefixKind || view.Records < 0 || view.Bytes != 0 {
			return nil, fmt.Errorf("unsupported saved database view; open a fresh view explicitly")
		}
		limit = view.Records
	}
	db, closeDB, err := openRO(o.DB)
	if err != nil {
		return nil, fmt.Errorf("assay opencode: %w", err)
	}
	defer closeDB()

	rows, err := db.Query(`
		SELECT p.id, p.data, p.time_created
		FROM part p
		WHERE p.session_id = ?
		ORDER BY p.time_created, p.id LIMIT ?`, s.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("assay opencode query: %w", err)
	}
	defer rows.Close()

	start := time.Now()
	fingerprint := newRowFingerprint()

	for rows.Next() {
		var id, data string
		var created int64
		if err = rows.Scan(&id, &data, &created); err != nil {
			return nil, fmt.Errorf("assay opencode row: %w", err)
		}
		raw := []byte(data)
		fingerprint.add(id, created, raw)
		var probe struct {
			Type string `json:"type"`
		}
		json.Unmarshal(raw, &probe)
		kind := probe.Type
		if kind == "" {
			kind = "unparsed"
		}
		sc.Observe(kind, "", raw, fromUnixMS(created))
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("assay opencode rows: %w", err)
	}
	m := sc.Manifest()
	if view := sc.ViewBoundary(); view != nil && m.TotalRecords < view.Records {
		return nil, fmt.Errorf("source view was truncated")
	}
	m.SourceDigest = fingerprint.sum()
	m.SourceView = assay.SourceView{Kind: rowPrefixKind, Records: m.TotalRecords}
	m.Title = s.Title
	m.Elapsed = time.Since(start)
	return m, nil
}

func roleFromKind(kind string) string {
	switch kind {
	case "user.message":
		return "user"
	case "assistant.message":
		return "assistant"
	}
	return ""
}
