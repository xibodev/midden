package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedSourceViewSurvivesAppendsUntilExplicitRefresh(t *testing.T) {
	call, root := packetCaller(t)
	packet := preparedPacket(t, call)
	path := filepath.Join(root, "projects", "E--synthetic-workspace", "11111111-2222-3333-4444-555555555555.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("\n" + `{"type":"assistant","timestamp":"2026-09-19T12:00:00Z","message":{"role":"assistant","content":"NEW-APPENDED-OUTCOME: the later deployment passed."}}` + "\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	read := call("evidence.search", map[string]any{"packet_id": packet["packet_id"], "query": "recovery"})
	if !read.OK {
		t.Fatalf("an append invalidated historical evidence: %+v", read.Error)
	}
	late := call("evidence.search", map[string]any{"packet_id": packet["packet_id"], "query": "NEW-APPENDED-OUTCOME"})
	if !late.OK {
		t.Fatal(late.Error)
	}
	var prior EvidencePacket
	json.Unmarshal(late.Result, &prior)
	if len(prior.Records) != 0 || prior.SourceDigest != packet["source_digest"] {
		t.Fatal("new events leaked into a pinned view")
	}
	fresh := preparedPacket(t, call)
	if fresh["source_digest"] == packet["source_digest"] {
		t.Fatal("explicit refresh did not observe the new source")
	}
}

func TestPinnedSourceViewRejectsEditsAndTruncation(t *testing.T) {
	for _, truncate := range []bool{false, true} {
		call, root := packetCaller(t)
		packet := preparedPacket(t, call)
		path := filepath.Join(root, "projects", "E--synthetic-workspace", "11111111-2222-3333-4444-555555555555.jsonl")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if truncate {
			raw = raw[:len(raw)/2]
		} else {
			raw = []byte(strings.Replace(string(raw), "Recovery", "Altered!", 1))
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		env := call("evidence.search", map[string]any{"packet_id": packet["packet_id"], "query": "recovery"})
		if env.OK {
			t.Fatal("an edited or truncated source was accepted as the old view")
		}
	}
}
