package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func packetCaller(t *testing.T) (func(string, any) Envelope, string) {
	t.Helper()
	source := writeSyntheticClaudeStore(t)
	home := t.TempDir()
	return func(cap string, input any) Envelope {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return Invoke(Request{Capability: cap, Input: raw, ExplicitSourceRoots: true, Roots: map[string]Root{
			RootClaude: {Path: source, Mode: "ro"}, RootMiddenHome: {Path: home, Mode: "rw"},
		}})
	}, source
}

func preparedPacket(t *testing.T, call func(string, any) Envelope) map[string]any {
	t.Helper()
	env := call("evidence.prepare", map[string]any{"source": map[string]string{"tool": "claude", "session_id": "11111111-2222-3333-4444-555555555555"}, "max_records": 80})
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if id, _ := result["packet_id"].(string); id == "" {
		t.Fatal("prepare must persist a reusable packet id")
	}
	return result
}

func TestPacketComposeDoesNotReconstructPreparationParameters(t *testing.T) {
	call, source := packetCaller(t)
	packet := preparedPacket(t, call)
	record := packet["records"].([]any)[0].(map[string]any)["id"]
	input := map[string]any{"packet_id": packet["packet_id"], "items": []any{
		map[string]any{"kind": "decision", "title": "Recover before the cliff", "body": "Use a bounded recovery handoff rather than loading the full transcript.", "confidence": .8, "record_ids": []any{record}},
	}}
	// Stored excerpts remain usable even when the original file is temporarily
	// unavailable. They are a snapshot, not a claim about current source state.
	transcript := filepath.Join(source, "projects", "E--synthetic-workspace", "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.Rename(transcript, transcript+".offline"); err != nil {
		t.Fatal(err)
	}
	env := call("evidence.compose", input)
	if !env.OK {
		t.Fatalf("compose rescanned or guessed read parameters: %+v", env.Error)
	}
	var result EvidenceComposed
	json.Unmarshal(env.Result, &result)
	if result.Stored != 1 || !strings.Contains(result.Evidence[0].TurnRef, packet["packet_id"].(string)) {
		t.Fatalf("packet provenance missing: %+v", result)
	}
}

func TestEvidenceValidationDoesNotPolluteStoredEvidence(t *testing.T) {
	call, _ := packetCaller(t)
	packet := preparedPacket(t, call)
	record := packet["records"].([]any)[0].(map[string]any)["id"]
	env := call("evidence.validate", map[string]any{"packet_id": packet["packet_id"], "items": []any{
		map[string]any{"kind": "decision", "title": "Diagnostic proposal", "body": "This is only a validation proposal.", "confidence": .5, "record_ids": []any{record}},
	}})
	if !env.OK {
		t.Fatal(env.Error)
	}
	list := call("evidence.list", map[string]any{})
	if !list.OK {
		t.Fatal(list.Error)
	}
	var result struct {
		Evidence []any `json:"evidence"`
	}
	json.Unmarshal(list.Result, &result)
	if len(result.Evidence) != 0 {
		t.Fatal("validation persisted diagnostic evidence")
	}
}

func TestEvidenceReadReturnsContextAndSharesCumulativeBudget(t *testing.T) {
	call, _ := packetCaller(t)
	packet := preparedPacket(t, call)
	record := packet["records"].([]any)[0].(map[string]any)["id"]
	env := call("evidence.read", map[string]any{"packet_id": packet["packet_id"], "record_ids": []any{record}, "before": 0, "after": 1, "max_chars": 4096})
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result map[string]any
	json.Unmarshal(env.Result, &result)
	if result["investigation_id"] != packet["investigation_id"] {
		t.Fatal("focused read escaped the cumulative investigation")
	}
	if result["selection"] != "context" || result["source_digest"] != packet["source_digest"] {
		t.Fatal("focused snapshot identity lost")
	}
	if _, ok := result["budget"].(map[string]any); !ok {
		t.Fatal("missing cumulative budget report")
	}
}

func TestEvidenceSearchRejectsEmptyQueryAndSurfacesSourceChange(t *testing.T) {
	call, source := packetCaller(t)
	packet := preparedPacket(t, call)
	if env := call("evidence.search", map[string]any{"packet_id": packet["packet_id"], "query": ""}); env.OK {
		t.Fatal("unbounded empty search accepted")
	}
	transcript := filepath.Join(source, "projects", "E--synthetic-workspace", "11111111-2222-3333-4444-555555555555.jsonl")
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n" + `{"type":"assistant","message":{"content":"Changed source after orientation"}}` + "\n")
	f.Close()
	env := call("evidence.search", map[string]any{"packet_id": packet["packet_id"], "query": "recovery"})
	if env.OK || !strings.Contains(env.Error.Message, "source snapshot changed") {
		t.Fatalf("source change must be explicit: %+v", env)
	}
}
