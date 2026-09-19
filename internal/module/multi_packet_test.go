package module

import (
	"encoding/json"
	"testing"
)

func TestExtractionCanCiteCompatiblePacketsWithExactWindowProvenance(t *testing.T) {
	call, _ := packetCaller(t)
	orientation := preparedPacket(t, call)
	record := orientation["records"].([]any)[0].(map[string]any)
	read := call("evidence.read", map[string]any{"packet_id": orientation["packet_id"], "record_ids": []any{record["id"]}, "max_chars": 4096})
	if !read.OK {
		t.Fatal(read.Error)
	}
	var context EvidencePacket
	if err := json.Unmarshal(read.Result, &context); err != nil {
		t.Fatal(err)
	}
	result := call("evidence.compose", map[string]any{
		"packet_ids": []any{orientation["packet_id"], context.PacketID},
		"items": []any{map[string]any{"kind": "decision", "title": "Recovery sequence", "body": "The source discusses recovery before resuming.",
			"confidence": .8, "record_ids": []any{record["id"]}, "quotations": []any{map[string]any{
				"packet_id": context.PacketID, "record_id": record["id"], "text": context.Records[0].Excerpt,
			}}}},
	})
	if !result.OK {
		t.Fatal(result.Error)
	}
	var composed EvidenceComposed
	json.Unmarshal(result.Result, &composed)
	var ref struct {
		PacketIDs []string `json:"packet_ids"`
	}
	json.Unmarshal([]byte(composed.Evidence[0].TurnRef), &ref)
	if len(ref.PacketIDs) != 2 {
		t.Fatal("cross-packet lineage was lost")
	}
}
