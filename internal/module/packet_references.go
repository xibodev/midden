package module

import (
	"fmt"
	"sort"

	"github.com/mekjr1/midden/internal/index"
)

type EvidenceWindowError struct{ Path, RecordID, PacketID string }

func (e EvidenceWindowError) Error() string {
	return e.Path + ": quoted words are not present in the cited window; include the packet containing the full context or retrieve it. Formatting-only Markdown differences are accepted; do not remove quotations to hide a mismatch."
}
func (e EvidenceWindowError) ErrorDetails() map[string]any {
	return map[string]any{"path": e.Path, "reason": "quotation_outside_cited_window", "record_id": e.RecordID, "packet_id": e.PacketID,
		"next_action": "cite the matching packet_id in quotations and include it in packet_ids"}
}

func compatibleReadingPackets(db *index.DB, input EvidenceComposeInput) ([]storedReadingPacket, error) {
	keys := append([]string(nil), input.PacketIDs...)
	if input.PacketID != "" {
		keys = append(keys, input.PacketID)
	} else if len(keys) == 0 && input.ExpectedDigest != "" {
		keys = append(keys, input.ExpectedDigest)
	}
	if len(keys) == 0 || len(keys) > 8 {
		return nil, fmt.Errorf("provide packet_id or 1..8 compatible packet_ids")
	}
	sort.Strings(keys)
	out := []storedReadingPacket{}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		p, err := loadReadingPacket(db, key, "")
		if err != nil {
			return nil, err
		}
		if input.PacketID == key && input.ExpectedDigest != "" && p.Packet.Digest != input.ExpectedDigest {
			return nil, fmt.Errorf("expected_digest does not match packet_id")
		}
		if len(out) > 0 && (p.Packet.Source != out[0].Packet.Source || p.Packet.SourceDigest != out[0].Packet.SourceDigest) {
			return nil, fmt.Errorf("packet_ids must refer to the same source view; do not mix different source revisions")
		}
		out = append(out, p)
	}
	return out, nil
}
