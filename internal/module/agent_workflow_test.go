package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/editorial"
	"github.com/mekjr1/midden/internal/index"
)

func TestAgentWorkflowFromSourceToReviewedExportWithoutNestedModel(t *testing.T) {
	home := t.TempDir()
	sourceRoot := writeSyntheticClaudeStore(t)
	source := editorial.Source{Tool: "claude", SessionID: "11111111-2222-3333-4444-555555555555"}
	call := func(cap string, input any) Envelope {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return Invoke(Request{Capability: cap, Input: raw, ConfirmOperator: testOperatorConsent, ExplicitSourceRoots: true, Roots: map[string]Root{
			RootMiddenHome: {Path: home, Mode: "rw"}, RootClaude: {Path: sourceRoot, Mode: "ro"},
		}})
	}
	decode := func(cap string, input any, out any) {
		t.Helper()
		env := call(cap, input)
		if !env.OK {
			t.Fatalf("%s: %+v", cap, env.Error)
		}
		if err := json.Unmarshal(env.Result, out); err != nil {
			t.Fatal(err)
		}
	}
	var packet struct {
		Digest  string `json:"digest"`
		Records []struct {
			ID string `json:"id"`
		} `json:"records"`
	}
	decode("evidence.prepare", map[string]any{"source": source, "max_records": 12}, &packet)
	if len(packet.Records) == 0 || len(packet.Records) > 12 || packet.Digest == "" {
		t.Fatalf("unbounded or empty packet: %+v", packet)
	}
	item := map[string]any{"kind": "decision", "title": "Assay before recovery", "body": "Measure the source session before choosing a recovery action.", "confidence": .8, "record_ids": []string{packet.Records[0].ID}}
	request := map[string]any{"source": source, "max_records": 12, "expected_digest": packet.Digest, "items": []any{item}}
	var stored struct {
		Evidence []index.Nugget `json:"evidence"`
		Stored   int            `json:"stored"`
	}
	decode("evidence.compose", request, &stored)
	if len(stored.Evidence) != 1 || !strings.Contains(stored.Evidence[0].TurnRef, packet.Records[0].ID) {
		t.Fatalf("missing record provenance: %+v", stored)
	}
	evidenceID := stored.Evidence[0].UID
	decode("evidence.compose", request, &stored)
	if stored.Stored != 0 {
		t.Fatal("retry duplicated evidence")
	}
	var p editorial.Project
	decode("projects.create", editorial.CreateRequest{Title: "Recovery lesson", Goal: "Write a focused internal post",
		Sources: []editorial.Source{source}, EvidenceIDs: []string{evidenceID}}, &p)
	var prepared editorial.Prepared
	decode("editorial.prepare", map[string]any{"project_id": p.ID}, &prepared)
	if prepared.EvidenceCount != 1 || prepared.Revision != p.Revision {
		t.Fatalf("wrong analysis context: %+v", prepared)
	}
	a := editorial.Analysis{
		Arcs:   []editorial.Arc{{ID: "recovery", Title: "Recovery sequence", Summary: "Understand the session first.", EvidenceIDs: []string{evidenceID}}},
		Claims: []editorial.Claim{{ID: "claim", Text: "Assay informs recovery.", Status: "supported", SupportingIDs: []string{evidenceID}}},
		Opportunities: []editorial.Opportunity{{ID: "post", Title: "Measure before recovering", Hook: "Avoid blind recovery",
			Audience: "CLI operators", Purpose: "Choose a bounded next step", Formats: []string{"post"}, ArcIDs: []string{"recovery"}, ClaimIDs: []string{"claim"},
			Rationale: "One concrete decision", Effort: "small", Risks: []string{"Internal-only source"}}},
	}
	decode("editorial.analyze", map[string]any{"project_id": p.ID, "expected_revision": p.Revision, "analysis": a}, &p)
	var selection editorial.SelectionResult
	decode("editorial.select", editorial.SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{"post"}}, &selection)
	if call("recipes.compose", map[string]any{"recipe_id": selection.Recipe.UID, "drafts": map[string]string{"post": "Unapproved"}}).OK {
		t.Fatal("unapproved composition succeeded")
	}
	var ignored map[string]any
	decode("recipes.evidence", map[string]any{"recipe_id": selection.Recipe.UID, "evidence_ids": []string{evidenceID}, "decision": "approved"}, &ignored)
	var produced struct {
		Outputs []index.RefineryOutput `json:"outputs"`
	}
	decode("recipes.compose", map[string]any{"recipe_id": selection.Recipe.UID, "drafts": map[string]string{"post": "# Measure first\n\nAssay informs recovery. [E1]"}}, &produced)
	if len(produced.Outputs) != 1 {
		t.Fatal("missing authored output")
	}
	output := produced.Outputs[0]
	if call("outputs.export", map[string]any{"output_id": output.UID}).OK {
		t.Fatal("draft exported")
	}
	var inspected struct {
		Body   string `json:"body"`
		Digest string `json:"content_digest"`
	}
	decode("outputs.inspect", map[string]any{"output_id": output.UID}, &inspected)
	decode("outputs.review", map[string]any{"output_id": output.UID, "body": inspected.Body, "decision": "reviewed", "expected_digest": inspected.Digest, "review_notes": "Checked the scoped recovery decision; this is a process recommendation, not a claim of comprehensive recovery."}, &ignored)
	var exported struct {
		Path string `json:"path"`
	}
	decode("outputs.export", map[string]any{"output_id": output.UID}, &exported)
	body, err := os.ReadFile(exported.Path)
	if err != nil || string(body) != inspected.Body {
		t.Fatalf("export mismatch: %v", err)
	}
	rel, err := filepath.Rel(home, exported.Path)
	if err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("export left explicit state root: %s", exported.Path)
	}
	var handoff struct {
		Path        string `json:"path"`
		ReviewState string `json:"review_state"`
	}
	decode("handoffs.create", map[string]any{"project_id": selection.Project.ID, "expected_revision": selection.Project.Revision,
		"target": "markdown", "output_ids": []string{output.UID}}, &handoff)
	if handoff.Path == "" || handoff.ReviewState != "unreviewed" {
		t.Fatalf("bad handoff state: %+v", handoff)
	}
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL().Exec("DELETE FROM host_reviews WHERE action='review_output' AND subject_id=?", output.UID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	unconfirmed := call("handoffs.create", map[string]any{"project_id": selection.Project.ID, "expected_revision": selection.Project.Revision,
		"target": "markdown", "output_ids": []string{output.UID}})
	if unconfirmed.OK || unconfirmed.Error.Code != ErrOperatorConfirmation {
		t.Fatal("handoff accepted an unconfirmed legacy review")
	}
}

func TestAgentWorkflowRejectsMissingWriteAuthorityAndIgnoredFields(t *testing.T) {
	for _, cap := range []string{"projects.create", "editorial.analyze", "editorial.select", "evidence.compose"} {
		env := Invoke(Request{Capability: cap, Input: json.RawMessage(`{}`), Roots: map[string]Root{RootMiddenHome: {Path: t.TempDir(), Mode: "ro"}}})
		if env.OK {
			t.Fatalf("%s ignored write authority", cap)
		}
	}
	env := Invoke(Request{Capability: "projects.list", Input: json.RawMessage(`{"approve":true}`), Roots: map[string]Root{RootMiddenHome: {Path: t.TempDir(), Mode: "ro"}}})
	if env.OK || env.Error.Code != ErrInvalidRequest {
		t.Fatalf("unknown field ignored: %+v", env)
	}
}
