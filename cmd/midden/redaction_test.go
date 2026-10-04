package main

import (
	"strings"
	"testing"

	"github.com/xibodev/midden/internal/assay"
	"github.com/xibodev/midden/internal/core"
)

func TestFilterHarvestCopiesAndNeverReturnsNilRecent(t *testing.T) {
	if got := filterHarvest(core.Harvest{}); got.Recent == nil || got.Goal != nil || got.LastAssistant != nil {
		t.Fatalf("empty harvest: %+v", got)
	}

	goal := core.Turn{Index: 1, Role: "user", Text: "use " + synthToken}
	hv := core.Harvest{Goal: &goal, Recent: []core.Turn{goal}, LastAssistant: &core.Turn{Role: "assistant", Text: "ok " + synthAccess}, UserTurns: 1}
	got := filterHarvest(hv)
	for _, text := range []string{got.Goal.Text, got.Recent[0].Text, got.LastAssistant.Text} {
		if strings.Contains(text, synthToken) || strings.Contains(text, synthAccess) || !strings.Contains(text, "ask operator") {
			t.Errorf("turn not filtered: %q", text)
		}
	}
	if !strings.Contains(goal.Text, synthToken) || !strings.Contains(hv.Recent[0].Text, synthToken) {
		t.Error("filtering modified the caller's harvest")
	}
	if got.UserTurns != 1 || got.Goal.Index != 1 || got.Goal.Role != "user" {
		t.Errorf("turn metadata changed: %+v", got)
	}
}

func TestFilterManifestFiltersTitleAndPreviews(t *testing.T) {
	m := &assay.Manifest{Title: "deploy " + synthToken, Candidates: []assay.Record{{Class: assay.Signal, Preview: "key " + synthAccess, Index: 3}}}
	filterManifest(m)
	if strings.Contains(m.Title, synthToken) || strings.Contains(m.Candidates[0].Preview, synthAccess) {
		t.Fatalf("manifest not filtered: %+v", m)
	}
	if m.Candidates[0].Index != 3 || m.Candidates[0].Class != assay.Signal {
		t.Errorf("candidate metadata changed: %+v", m.Candidates[0])
	}
	empty := &assay.Manifest{}
	if filterManifest(empty); empty.Candidates == nil {
		t.Error("candidates must never be nil")
	}
}
