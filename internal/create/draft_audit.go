package create

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/quotation"
)

type AuditFinding struct {
	Code    string `json:"code"`
	Passage int    `json:"passage"`
	Message string `json:"message"`
}
type CitedPassage struct {
	Number      int      `json:"number"`
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type DraftAudit struct {
	ContentDigest   string         `json:"content_digest"`
	MechanicalValid bool           `json:"mechanical_valid"`
	Blocked         bool           `json:"blocked"`
	SemanticReview  string         `json:"semantic_review"`
	Findings        []AuditFinding `json:"findings"`
	Passages        []CitedPassage `json:"passages"`
}

var citationPattern = regexp.MustCompile(`\[(E[1-9][0-9]*|evidence:[A-Za-z0-9_.:-]+)\]`)
var inlineQuotation = regexp.MustCompile(`["“]([^"”\n]{16,})["”]`)

func CitationKeys(evidence []index.Nugget) map[string]string {
	keys := map[string]string{}
	for i, n := range evidence {
		keys[fmt.Sprintf("E%d", i+1)] = n.UID
		keys["evidence:"+n.UID] = n.UID
	}
	return keys
}

// NormalizeSourceMetadata removes mutable workflow status from source bytes.
// The database and digest-bearing provenance are the lifecycle authority.
func NormalizeSourceMetadata(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return body
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return body
	}
	end += 4
	header := strings.Split(normalized[4:end], "\n")
	owned := false
	for _, line := range header {
		owned = owned || strings.HasPrefix(line, "midden_recipe:")
	}
	if !owned {
		return body
	}
	filtered := []string{}
	for _, line := range header {
		if !strings.HasPrefix(line, "midden_status:") {
			filtered = append(filtered, line)
		}
	}
	return "---\n" + strings.Join(filtered, "\n") + normalized[end:]
}

func stripFrontmatter(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---\n"); end >= 0 {
			return body[end+9:]
		}
	}
	return body
}

// AuditDraft checks reference coverage and literal quotations. It deliberately
// does not claim that a citation entails a paraphrase or proves a historical fact.
func AuditDraft(db *index.DB, body string, evidence []index.Nugget, keys map[string]string) (DraftAudit, error) {
	report := DraftAudit{ContentDigest: Digest([]byte(body)), MechanicalValid: true, SemanticReview: "required",
		Findings: []AuditFinding{}, Passages: []CitedPassage{}}
	if keys == nil {
		keys = CitationKeys(evidence)
	}
	allowed := map[string]index.Nugget{}
	for _, n := range evidence {
		allowed[n.UID] = n
	}
	add := func(code string, passage int, message string) {
		report.MechanicalValid = false
		report.Blocked = true
		report.Findings = append(report.Findings, AuditFinding{code, passage, message})
	}

	for _, block := range strings.Split(stripFrontmatter(body), "\n\n") {
		metadata := strings.TrimSpace(block)
		if len(report.Passages) == 0 && !strings.Contains(metadata, "\n") && len(metadata) <= 160 && strings.HasPrefix(metadata, "**Audience:**") {
			continue
		}
		lines := []string{}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == "---" || strings.HasPrefix(line, "#") {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			continue
		}
		passage := strings.Join(lines, "\n")
		number := len(report.Passages) + 1
		ids := []string{}
		matches := citationPattern.FindAllStringSubmatch(passage, -1)
		for _, match := range matches {
			id, ok := keys[match[1]]
			if _, present := allowed[id]; !ok || !present {
				add("unknown_citation", number, "Citation "+match[0]+" is outside the selected evidence")
				continue
			}
			ids = append(ids, id)
		}
		ids = uniqueStrings(ids)
		if len(matches) == 0 {
			add("missing_citation", number, "Add selected source citations or remove unsupported background; citation syntax is [E1], [E2], etc.")
		}
		report.Passages = append(report.Passages, CitedPassage{number, passage, ids})
		quotes := []string{}
		quotedLines := []string{}
		for _, line := range lines {
			if strings.HasPrefix(line, ">") {
				quotedLines = append(quotedLines, strings.TrimSpace(strings.TrimPrefix(line, ">")))
			}
		}
		if len(quotedLines) > 0 {
			quotes = append(quotes, citationPattern.ReplaceAllString(strings.Join(quotedLines, "\n"), ""))
		}
		for _, match := range inlineQuotation.FindAllStringSubmatch(passage, -1) {
			quotes = append(quotes, match[1])
		}
		for _, quote := range quotes {
			exact := false
			for _, id := range ids {
				excerpts, err := sourceExcerpts(db, allowed[id])
				if err != nil {
					return report, err
				}
				for _, excerpt := range excerpts {
					if quotation.Matches(excerpt, quote) {
						exact = true
						break
					}
				}
			}
			if !exact {
				add("quotation_not_in_source", number, "A quotation does not match retrieved source text; retrieve context or label a faithful paraphrase instead")
			}
		}
	}
	if len(report.Passages) == 0 {
		add("empty_draft", 0, "No substantive passages to review")
	}
	return report, nil
}

func sourceExcerpts(db *index.DB, n index.Nugget) ([]string, error) {
	var ref struct {
		PacketID  string   `json:"packet_id"`
		PacketIDs []string `json:"packet_ids"`
		Records   []string `json:"record_ids"`
	}
	if json.Unmarshal([]byte(n.TurnRef), &ref) != nil || (ref.PacketID == "" && len(ref.PacketIDs) == 0) {
		return nil, nil
	}
	keys := ref.PacketIDs
	if len(keys) == 0 {
		keys = []string{ref.PacketID}
	}
	out := []string{}
	for _, key := range keys {
		raw, err := db.ReadingPacket(key)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Packet struct {
				Records []struct{ ID, Excerpt, Kind string } `json:"records"`
			} `json:"packet"`
		}
		if err = json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		for _, record := range doc.Packet.Records {
			if record.Kind == "session.binary_asset" || record.Kind == "session.workspace_file_changed" {
				continue
			}
			for _, id := range ref.Records {
				if record.ID == id {
					out = append(out, record.Excerpt)
					break
				}
			}
		}
	}
	return out, nil
}
