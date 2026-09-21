package assay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func message(text string) []byte {
	raw, _ := json.Marshal(map[string]any{"data": map[string]string{"content": text}})
	return raw
}

func TestOrientationIncludesEarlyFailureAndLateResolution(t *testing.T) {
	s := NewScanner("synthetic-long-history", "copilot", 8)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		text := fmt.Sprintf("Independent episode %d", i)
		if i == 0 {
			text = "Initial migration tests failed."
		}
		if i == 999 {
			text = "Three weeks later: the migration passed after fixing deployment eligibility."
		}
		s.Observe("assistant.message", "assistant", message(text), start.Add(time.Duration(i)*time.Hour))
	}
	m := s.Manifest()
	if len(m.Candidates) != 8 || m.Candidates[0].Index != 0 || m.Candidates[7].Index != 999 {
		t.Fatalf("orientation lost session boundaries: %+v", m.Candidates)
	}
	if !strings.Contains(m.Candidates[7].Preview, "migration passed") {
		t.Fatal("late resolution omitted")
	}
	for i := 1; i < len(m.Candidates); i++ {
		if m.Candidates[i].Index <= m.Candidates[i-1].Index {
			t.Fatal("orientation is not chronological")
		}
	}
}

func TestInjectedInstructionsAndModelProtocolAreNotInvestigationEvidence(t *testing.T) {
	s := NewScanner("synthetic", "copilot", 8)
	s.Observe("user.message", "user", message("<system_reminder>Custom instructions: always do X</system_reminder>"), time.Now())
	s.Observe("user.message", "user", []byte(`{"data":{"source":"autopilot","content":"","transformedContent":"Continue without asking."}}`), time.Now())
	s.Observe("model.message", "", message("Duplicate model protocol content"), time.Now())
	s.Observe("user.message", "user", message("We need to check whether the deployment was updated."), time.Now())
	m := s.Manifest()
	if len(m.Candidates) != 1 || !strings.Contains(m.Candidates[0].Preview, "deployment") {
		t.Fatalf("instruction/protocol noise consumed the evidence budget: %+v", m.Candidates)
	}
}

func TestSourceTimeRangeIsNotTheSampleEndpoint(t *testing.T) {
	s := NewScanner("times", "claude", 2)
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Observe("user", "user", message("An early question"), first)
	s.Observe("assistant", "assistant", message("An answer"), first.Add(time.Hour))
	s.Observe("session.shutdown", "", []byte(`{}`), first.Add(30*24*time.Hour))
	m := s.Manifest()
	if !m.FirstTime.Equal(first) || !m.LastTime.Equal(first.Add(30*24*time.Hour)) {
		t.Fatalf("incorrect source time range: %v..%v", m.FirstTime, m.LastTime)
	}
	if m.SourceDigest == "" {
		t.Fatal("missing source snapshot identity")
	}
}

func TestFocusedContextPreservesExactTextAndReportsClipping(t *testing.T) {
	s := NewEvidenceScanner("context", "copilot", Selection{Anchors: []int64{4}, Before: 1, After: 1, MaxRecords: 5, MaxChars: 512})
	for i := 0; i < 8; i++ {
		if i%2 == 1 {
			s.Observe("assistant.turn_start", "", []byte(`{}`), time.Time{})
			continue
		}
		text := fmt.Sprintf("Message %d\nA quoted statement: \"check the deployed version\".", i)
		if i == 4 {
			text += strings.Repeat(" Long supporting context.", 40)
		}
		s.Observe("assistant.message", "assistant", message(text), time.Unix(int64(i), 0))
	}
	m := s.Manifest()
	if len(m.Candidates) != 3 || m.Candidates[0].Index != 2 || m.Candidates[1].Index != 4 || m.Candidates[2].Index != 6 {
		t.Fatalf("context must use meaningful neighbors, not metadata: %+v", m.Candidates)
	}
	if !strings.Contains(m.Candidates[1].Preview, "\nA quoted") || !m.Candidates[1].Clipped || m.Candidates[1].TextChars <= 512 {
		t.Fatalf("context/clipping information lost: %+v", m.Candidates[1])
	}
}

func TestFocusedSearchSamplesBothEarlierAndLaterMatches(t *testing.T) {
	s := NewEvidenceScanner("search", "copilot", Selection{Query: "deployment", MaxRecords: 3, MaxChars: 1024})
	for i := 0; i < 50; i++ {
		text := fmt.Sprintf("Unrelated work %d", i)
		if i%10 == 0 {
			text = fmt.Sprintf("deployment finding %d", i)
		}
		s.Observe("assistant.message", "assistant", message(text), time.Unix(int64(i), 0))
	}
	m := s.Manifest()
	if m.MatchedRecords != 5 || len(m.Candidates) != 3 || m.Candidates[0].Index != 0 || m.Candidates[2].Index != 40 {
		t.Fatalf("search lost later matches or coverage denominator: %+v", m)
	}
}

func TestMultipleTextBlocksArePreserved(t *testing.T) {
	got := extractText([]byte(`{"message":{"content":[{"type":"text","text":"Claim"},{"type":"text","text":"Later correction"}]}}`))
	if got != "Claim\nLater correction" {
		t.Fatalf("lost message blocks: %q", got)
	}

}

func TestEvidenceOrientationIncludesAssetReferencesNotBinaryPayloads(t *testing.T) {
	s := NewEvidenceScanner("assets", "copilot", Selection{MaxRecords: 4, MaxChars: 512})
	s.Observe("session.binary_asset", "", []byte(`{"data":{"name":"status.png","mimeType":"image/png","data":"data:image/png;base64,PRIVATE_BINARY"}}`), time.Now())
	m := s.Manifest()
	if len(m.Candidates) != 1 || !strings.Contains(m.Candidates[0].Preview, "status.png") || strings.Contains(m.Candidates[0].Preview, "PRIVATE_BINARY") {
		t.Fatalf("asset reference omitted or binary leaked: %+v", m.Candidates)
	}
}
