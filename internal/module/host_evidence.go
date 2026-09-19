package module

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/editorial"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/quotation"
	"github.com/mekjr1/midden/internal/redact"
)

type EvidencePrepareInput struct {
	Source     editorial.Source `json:"source"`
	MaxRecords int              `json:"max_records,omitempty" min:"1" max:"80" default:"40"`
}

type EvidenceRecord struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Role      string    `json:"role"`
	Time      time.Time `json:"time"`
	Excerpt   string    `json:"excerpt"`
	Clipped   bool      `json:"clipped"`
	TextChars int       `json:"text_chars"`
	StartByte int       `json:"start_byte"`
	EndByte   int       `json:"end_byte"`
}

type EvidencePacket struct {
	PacketID        string              `json:"packet_id"`
	InvestigationID string              `json:"investigation_id"`
	Source          editorial.Source    `json:"source"`
	Digest          string              `json:"digest"`
	SourceDigest    string              `json:"source_digest"`
	SourceView      assay.SourceView    `json:"source_view"`
	SourceFirstTime time.Time           `json:"source_first_time"`
	SourceLastTime  time.Time           `json:"source_last_time"`
	Selection       string              `json:"selection"`
	MatchedRecords  int64               `json:"matched_records"`
	Unrepresented   []RecordRange       `json:"unrepresented_ranges"`
	Budget          index.ReadingBudget `json:"budget"`
	MaxRecords      int                 `json:"max_records"`
	Records         []EvidenceRecord    `json:"records"`
	TotalRecords    int64               `json:"total_records"`
	SignalRecords   int64               `json:"signal_records"`
	SignalBytes     int64               `json:"signal_bytes"`
	SliceBytes      int64               `json:"slice_bytes"`
	EstSliceTokens  int64               `json:"est_slice_tokens"`
	Warnings        []string            `json:"warnings"`
}

type HostEvidenceItem struct {
	Kind       string            `json:"kind" enum:"decision,error_fix,command,gotcha,dead_end,artifact,brief"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Tags       []string          `json:"tags,omitempty"`
	Confidence float64           `json:"confidence"`
	RecordIDs  []string          `json:"record_ids"`
	Quotations []SourceQuotation `json:"quotations,omitempty"`
}

type EvidenceComposeInput struct {
	PacketID       string             `json:"packet_id,omitempty"`
	PacketIDs      []string           `json:"packet_ids,omitempty" max:"8"`
	Source         editorial.Source   `json:"source,omitzero"`
	MaxRecords     int                `json:"max_records,omitempty"`
	ExpectedDigest string             `json:"expected_digest,omitempty"`
	Items          []HostEvidenceItem `json:"items"`
}

type EvidenceComposed struct {
	Evidence       []index.Nugget `json:"evidence"`
	Stored         int            `json:"stored"`
	PacketDigest   string         `json:"packet_digest"`
	ReviewState    string         `json:"review_state"`
	ValidationOnly bool           `json:"validation_only"`
}

func prepareHostEvidence(input EvidencePrepareInput, req Request) (EvidencePacket, core.Session, error) {
	return prepareReadingPacket(input, req)
}

func composeHostEvidence(input EvidenceComposeInput, req Request, db *index.DB) (EvidenceComposed, error) {
	out := EvidenceComposed{Evidence: []index.Nugget{}, ReviewState: "unreviewed"}
	if len(input.Items) > 80 {
		return out, fmt.Errorf("at most 80 evidence items can be submitted")
	}
	packets, err := compatibleReadingPackets(db, input)
	if err != nil {
		return out, err
	}
	stored := packets[0]
	packet, source := stored.Packet, stored.Session
	if input.Source.SessionID != "" && (input.Source != packet.Source) {
		return out, fmt.Errorf("source does not match the stored packet")
	}
	type candidate struct {
		packetID string
		record   EvidenceRecord
	}
	allowed := map[string][]candidate{}
	for _, p := range packets {
		for _, r := range p.Packet.Records {
			allowed[r.ID] = append(allowed[r.ID], candidate{p.Packet.PacketID, r})
		}
	}
	seen := map[string]bool{}
	for itemIndex, item := range input.Items {
		if !index.ValidKind(item.Kind) || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Body) == "" ||
			len(item.Title) > 300 || len(item.Body) > 6000 || item.Confidence < 0 || item.Confidence > 1 || len(item.Tags) > 8 {
			return out, fmt.Errorf("invalid evidence item: use a known kind, bounded title/body, and confidence 0..1")
		}
		if len(item.RecordIDs) == 0 || len(item.RecordIDs) > len(allowed) {
			return out, fmt.Errorf("every evidence item must cite prepared record IDs")
		}
		ids := append([]string(nil), item.RecordIDs...)
		sort.Strings(ids)
		for i, id := range ids {
			if _, ok := allowed[id]; !ok || (i > 0 && id == ids[i-1]) {
				return out, fmt.Errorf("unknown or duplicate record ID %q", id)
			}
		}
		usedPackets := map[string]bool{}
		for _, id := range ids {
			for _, c := range allowed[id] {
				usedPackets[c.packetID] = true
			}
		}
		for quoteIndex, q := range item.Quotations {
			matched := false
			for _, c := range allowed[q.RecordID] {
				if q.PacketID != "" && q.PacketID != c.packetID {
					continue
				}
				if assay.Classify(c.record.Kind) != assay.Artifact && containsName(ids, q.RecordID) && quotation.Matches(c.record.Excerpt, q.Text) {
					matched = true
					break
				}
			}
			if !matched {
				return out, EvidenceWindowError{Path: fmt.Sprintf("items[%d].quotations[%d]", itemIndex, quoteIndex), RecordID: q.RecordID, PacketID: q.PacketID}
			}
		}
		packetIDs := []string{}
		for id := range usedPackets {
			packetIDs = append(packetIDs, id)
		}
		sort.Strings(packetIDs)
		for _, tag := range item.Tags {
			if len(tag) > 80 || strings.Contains(tag, ",") {
				return out, fmt.Errorf("tags must be bounded and contain no commas")
			}
		}
		title, body := redact.Text(item.Title), redact.Text(item.Body)
		tags := make([]string, 0, len(item.Tags))
		redacted := title.Redacted || body.Redacted
		for _, tag := range item.Tags {
			value := redact.Text(tag)
			tags = append(tags, value.Text)
			redacted = redacted || value.Redacted
		}
		refRaw, err := json.Marshal(struct {
			PacketID   string            `json:"packet_id"`
			PacketIDs  []string          `json:"packet_ids"`
			Packet     string            `json:"packet_digest"`
			Snapshot   string            `json:"source_digest"`
			Records    []string          `json:"record_ids"`
			Quotations []SourceQuotation `json:"quotations,omitempty"`
		}{packet.PacketID, packetIDs, packet.Digest, packet.SourceDigest, ids, item.Quotations})
		if err != nil {
			return out, err
		}
		raw, err := json.Marshal([]string{string(source.Tool), source.ID, item.Kind, title.Text, body.Text, string(refRaw)})
		if err != nil {
			return out, err
		}
		id := "host-" + strings.TrimPrefix(DigestSHA256(raw), DigestPrefix)
		if seen[id] {
			continue
		}
		seen[id] = true
		n := index.Nugget{UID: id, Tool: string(source.Tool), SessionID: source.ID, Kind: item.Kind, Title: title.Text, Body: body.Text,
			Tags: tags, Workspace: source.Dir, Repo: source.Repo, Confidence: item.Confidence, Model: "host-authored",
			Redacted: redacted, TurnRef: string(refRaw), CreatedAt: time.Now().UTC()}
		out.Evidence = append(out.Evidence, n)
	}
	out.PacketDigest = packet.Digest
	if req.Capability == "evidence.validate" {
		out.ValidationOnly = true
		return out, nil
	}
	out.Stored, err = db.PutHostEvidence(out.Evidence)
	if err != nil {
		return out, err
	}
	if len(out.Evidence) > 0 {
		ids := make([]string, 0, len(out.Evidence))
		for _, n := range out.Evidence {
			ids = append(ids, n.UID)
		}
		out.Evidence, err = db.NuggetsByIDs(ids)
	}
	out.PacketDigest = packet.Digest
	return out, err
}
