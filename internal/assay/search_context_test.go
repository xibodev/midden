package assay

import (
	"strings"
	"testing"
	"time"
)

func TestSearchExposesLateMatchInsideALongMessage(t *testing.T) {
	s := NewEvidenceScanner("long-message", "copilot", Selection{Query: "resolved in deployment", MaxRecords: 2, MaxChars: 512})
	body := strings.Repeat("Earlier investigation. ", 400) + "The issue was resolved in deployment."
	s.Observe("assistant.message", "assistant", message(body), time.Now())
	m := s.Manifest()
	if len(m.Candidates) != 1 || !strings.Contains(m.Candidates[0].Preview, "resolved in deployment") {
		t.Fatalf("matched resolution remained outside returned context: %+v", m.Candidates)
	}
}

func TestClaudeToolResultsCanBeSearchedExplicitly(t *testing.T) {
	s := NewEvidenceScanner("tools", "claude", Selection{Query: "tests passed", IncludeTools: true, MaxRecords: 2, MaxChars: 512})
	s.Observe("user", "user", []byte(`{"message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"12 tests passed"}]}]}}`), time.Now())
	m := s.Manifest()
	if len(m.Candidates) != 1 || !strings.Contains(m.Candidates[0].Preview, "12 tests passed") || m.Candidates[0].Role != "tool" {
		t.Fatalf("tool result missing or mislabeled as human speech: %+v", m.Candidates)
	}
}
