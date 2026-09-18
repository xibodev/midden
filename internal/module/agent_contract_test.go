package module

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/editorial"
	"github.com/mekjr1/midden/internal/index"
)

func TestAgentEvidenceRejectsUnboundAndForgedRecordReferences(t *testing.T) {
	root := writeSyntheticClaudeStore(t)
	req := Request{Roots: map[string]Root{RootClaude: {Path: root, Mode: "ro"}, RootMiddenHome: {Path: t.TempDir(), Mode: "rw"}}, ExplicitSourceRoots: true}
	source := editorial.Source{Tool: "claude", SessionID: "11111111-2222-3333-4444-555555555555"}
	req.Capability = "evidence.prepare"
	req.Input = json.RawMessage(`{}`)
	if Invoke(req).OK {
		t.Fatal("unscoped extraction prepared")
	}
	req.Input, _ = json.Marshal(EvidencePrepareInput{Source: source, MaxRecords: 81})
	if Invoke(req).OK {
		t.Fatal("candidate bound ignored")
	}
	req.Input, _ = json.Marshal(EvidencePrepareInput{Source: source, MaxRecords: 8})
	env := Invoke(req)
	if !env.OK {
		t.Fatal(env.Error)
	}
	var packet EvidencePacket
	if err := json.Unmarshal(env.Result, &packet); err != nil {
		t.Fatal(err)
	}
	req.Capability = "evidence.compose"
	input := EvidenceComposeInput{Source: source, MaxRecords: 8, ExpectedDigest: packet.Digest, Items: []HostEvidenceItem{
		{Kind: "decision", Title: "Invented reference", Body: "Do not accept unknown references.", Confidence: 1, RecordIDs: []string{"not-in-packet"}},
	}}
	req.Input, _ = json.Marshal(input)
	if Invoke(req).OK {
		t.Fatal("forged record citation accepted")
	}
	input.Items[0].RecordIDs = []string{packet.Records[0].ID}
	input.ExpectedDigest = DigestPrefix + "0000000000000000000000000000000000000000000000000000000000000000"
	req.Input, _ = json.Marshal(input)
	if Invoke(req).OK {
		t.Fatal("wrong packet digest accepted")
	}
}

func TestWorkflowSchemasDescribeResultsAndRequireReviewFence(t *testing.T) {
	d := Describe()
	for _, id := range []string{"editorial.analyze", "projects.inspect", "evidence.prepare", "evidence.list", "recipes.inspect", "outputs.review", "outputs.export"} {
		var s struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(d.ResultSchemas["xibodev.midden."+id+".result/v1"], &s); err != nil {
			t.Fatal(err)
		}
		if len(s.Properties) == 0 {
			t.Errorf("%s has no machine-readable result contract", id)
		}
	}
	var review struct {
		Required []string `json:"required"`
	}
	json.Unmarshal(d.RequestSchemas["xibodev.midden.outputs.review.request/v1"], &review)
	if !containsName(review.Required, "expected_digest") || !containsName(review.Required, "decision") {
		t.Fatal("review fence and decision are not required")
	}
}

func TestAgentEvidenceRetryReturnsStoredMetadataAndRedactsTags(t *testing.T) {
	root := writeSyntheticClaudeStore(t)
	home := t.TempDir()
	req := Request{Roots: map[string]Root{RootClaude: {Path: root, Mode: "ro"}, RootMiddenHome: {Path: home, Mode: "rw"}}, ExplicitSourceRoots: true}
	source := editorial.Source{Tool: "claude", SessionID: "11111111-2222-3333-4444-555555555555"}
	packet, _, err := prepareHostEvidence(EvidencePrepareInput{Source: source, MaxRecords: 8}, req)
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.OpenAt(home)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fakeToken := "ghp_" + strings.Repeat("a", 36)
	input := EvidenceComposeInput{Source: source, MaxRecords: 8, ExpectedDigest: packet.Digest, Items: []HostEvidenceItem{
		{Kind: "decision", Title: "Scope", Body: "Inspect bounded source evidence.", Confidence: .2, Tags: []string{fakeToken}, RecordIDs: []string{packet.Records[0].ID}},
	}}
	first, err := composeHostEvidence(input, req, db)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Evidence[0].Redacted || strings.Contains(strings.Join(first.Evidence[0].Tags, ","), fakeToken) {
		t.Error("credential shape leaked through tags")
	}
	input.Items[0].Confidence = .9
	retry, err := composeHostEvidence(input, req, db)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Stored != 0 || retry.Evidence[0].Confidence != .2 {
		t.Fatalf("retry described unpersisted metadata: %+v", retry)
	}
}
