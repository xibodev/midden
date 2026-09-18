package editorial

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/create"
)

func reviewedChapter(t *testing.T) (Workflow, Project, string) {
	t.Helper()
	return reviewedContent(t, "post")
}

func reviewedContent(t *testing.T, kind string) (Workflow, Project, string) {
	t.Helper()
	w, p := fixture(t)
	var err error
	a := analysis()
	if !contains(a.Opportunities[0].Formats, kind) {
		a.Opportunities[0].Formats = append(a.Opportunities[0].Formats, kind)
	}
	p, err = w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := w.Select(SelectRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, OpportunityID: "post", OutputKinds: []string{kind}})
	if err != nil {
		t.Fatal(err)
	}
	p = selected.Project
	producer := create.Workflow{DB: w.DB, Drafts: map[string]string{kind: "# Deployment eligibility\n\nCheck versions first. [after]\n"}}
	if _, err = producer.SelectEvidence(create.Change{RecipeID: selected.Recipe.UID, EvidenceIDs: selected.Recipe.EvidenceIDs}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = producer.Produce(context.Background(), selected.Recipe.UID); err != nil {
		t.Fatal(err)
	}
	outputs, err := w.DB.RefineryOutputs(selected.Recipe.UID, 0)
	if err != nil || len(outputs) != 1 {
		t.Fatal(outputs, err)
	}
	o := outputs[0]
	body, err := os.ReadFile(o.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Handoff(HandoffRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, Target: "quarto", OutputIDs: []string{o.UID}}); err == nil {
		t.Fatal("draft entered handoff")
	}
	if _, err = producer.Review(create.Change{OutputID: o.UID, Body: string(body), Decision: "reviewed", ExpectedDigest: create.Digest(body)}); err != nil {
		t.Fatal(err)
	}
	a = *p.Analysis
	a.Chapters = []Chapter{{ID: "chapter-1", Title: "Deployment eligibility", OpportunityID: "post", Status: "complete", OutputID: o.UID}}
	p, err = w.Analyze(p.ID, p.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	return w, p, o.UID
}

func TestEditorialHandoffRetiredVideoTargetIsRejected(t *testing.T) {
	w, p, id := reviewedContent(t, "video_brief")
	req := HandoffRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, Target: "openmontage", OutputIDs: []string{id}}
	if _, err := w.Handoff(req); err == nil {
		t.Fatal("retired video integration accepted a new handoff")
	}
	req.Target = "markdown"
	if _, err := w.Handoff(req); err != nil {
		t.Fatalf("generic reviewed video brief must remain portable: %v", err)
	}
}

func TestEditorialHandoffBuildsPortableReviewedQuartoSources(t *testing.T) {
	w, p, id := reviewedChapter(t)
	result, err := w.Handoff(HandoffRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, Target: "quarto", OutputIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(filepath.Dir(w.DB.Path()), filepath.FromSlash(result.Path))
	config, err := os.ReadFile(filepath.Join(root, "_quarto.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "type: book") || !strings.Contains(string(config), "sources/01.md") {
		t.Fatalf("missing book configuration: %s", config)
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest create.HandoffManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "xibodev.midden.handoff/v1" || manifest.ReviewState != "unreviewed" || manifest.Target != "quarto" || len(manifest.Outputs) != 1 {
		t.Fatalf("incorrect handoff claims: %+v", manifest)
	}
	file, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.Outputs[0].Path)))
	if err != nil {
		t.Fatal(err)
	}
	if create.Digest(file) != manifest.Outputs[0].ContentDigest {
		t.Fatal("source digest mismatch")
	}
	if _, err = os.Stat(filepath.Join(root, "editorial.json")); err != nil {
		t.Fatal(err)
	}
}

func TestEditorialHandoffRejectsWrongProjectStaleOutputAndUnsafeTarget(t *testing.T) {
	w, p, id := reviewedChapter(t)
	req := HandoffRequest{ProjectID: p.ID, ExpectedRevision: p.Revision, Target: "quarto", OutputIDs: []string{id}}
	req.Target = "../../escape"
	if _, err := w.Handoff(req); err == nil {
		t.Fatal("unsafe adapter target accepted")
	}
	req.Target = "quarto"
	req.OutputIDs = []string{"unknown"}
	if _, err := w.Handoff(req); err == nil {
		t.Fatal("unrelated output accepted")
	}
	req.OutputIDs = []string{id}
	output, err := w.DB.RefineryOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output.Path, []byte("Unreviewed edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Handoff(req); err == nil {
		t.Fatal("changed reviewed bytes accepted")
	}
}
