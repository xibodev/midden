package refinery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
)

func TestDesignInfersMultiOutputRecipeAndSelectsEvidence(t *testing.T) {
	nuggets := []index.Nugget{
		{UID: "d1", Kind: "decision", Title: "Canonicalize headers", Body: "Normalize before HMAC.", Workspace: "payments", Confidence: .96},
		{UID: "e1", Kind: "error_fix", Title: "Signature mismatch", Body: "The targeted test passed after normalization.", Workspace: "payments", Confidence: .93},
		{UID: "other", Kind: "command", Title: "Unrelated", Body: "Ignore", Workspace: "another", Confidence: .99},
	}
	recipe, err := Design(
		"Create a tutorial, 12-slide deck, architecture diagram, video brief, and agent skill.",
		"payments", nil, nuggets, time.Unix(100, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, output := range recipe.Outputs {
		kinds[output.Kind] = true
	}
	for _, want := range []string{"tutorial", "slides", "diagram", "video_brief", "skill"} {
		if !kinds[want] {
			t.Errorf("missing inferred output %q in %#v", want, recipe.Outputs)
		}
	}
	if len(recipe.EvidenceIDs) != 2 {
		t.Fatalf("selected evidence = %#v, want only payments evidence", recipe.EvidenceIDs)
	}
}

func TestDesignInfersSafetyManifests(t *testing.T) {
	recipe, err := Design(
		"Create a retrieval pack, privacy manifest, and provenance manifest.",
		"", nil, []index.Nugget{{
			UID: "n1", Kind: "decision", Title: "Keep data local",
			Body: "Export only after review.", Confidence: .9,
		}}, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, output := range recipe.Outputs {
		kinds[output.Kind] = true
	}
	for _, want := range []string{"retrieval_pack", "privacy_manifest", "provenance_manifest"} {
		if !kinds[want] {
			t.Errorf("missing %s in %#v", want, recipe.Outputs)
		}
	}
}

func TestAgentProposalRequiresRepeatedEvidence(t *testing.T) {
	one := []index.Nugget{
		{UID: "1", Kind: "error_fix", Title: "Always rerun the failing test", Tags: []string{"verification"}, Workspace: "api"},
	}
	if got := AgentProposals(one); len(got) != 0 {
		t.Fatalf("single observation produced a rule proposal: %#v", got)
	}
	two := append(one, index.Nugget{
		UID: "2", Kind: "gotcha", Title: "Completion before verification", Tags: []string{"verification"}, Workspace: "api",
	})
	got := AgentProposals(two)
	if len(got) != 1 || got[0].Support != 2 || got[0].Kind != "instruction_patch" {
		t.Fatalf("repeated proposal = %#v", got)
	}
}

func TestYieldMarksAssayOnlyResultAsEstimated(t *testing.T) {
	yield := AssessYield(nil, []core.Session{{Dir: "payments"}}, index.Totals{
		Assayed: 4,
		Signal:  2 << 20,
	})
	if !yield.Estimated || yield.Ready || yield.Total != 0 ||
		yield.Recommended.Workspace != "" || yield.Message == "" {
		t.Fatalf("assay-only yield = %#v", yield)
	}
}

func TestDesignRequiresReclaimedEvidence(t *testing.T) {
	_, err := Design("Create a tutorial", "payments", nil, nil, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no reclaimed evidence") {
		t.Fatalf("design without evidence error=%v", err)
	}
}

func TestDesignUsesOneFocusedFallbackOutput(t *testing.T) {
	recipe, err := Design("Create a short note from this evidence.", "", nil,
		[]index.Nugget{{
			UID: "n1", Kind: "decision", Title: "Keep it focused",
			Body: "One clear output is easier to review.", Confidence: .9,
		}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(recipe.Outputs) != 1 || recipe.Outputs[0].Kind != "tutorial" {
		t.Fatalf("fallback outputs=%#v", recipe.Outputs)
	}
}

func TestDeterministicPacksCarryProvenanceWithoutRawTranscripts(t *testing.T) {
	nuggets := []index.Nugget{
		{
			UID: "n1", Tool: "claude", SessionID: "session-1", TurnRef: "turn:7",
			Kind: "decision", Title: "Use explicit approval", Body: "Do not publish automatically.",
			Workspace: "payments", Confidence: .95,
		},
	}
	recipe := index.Recipe{UID: "r1", Workspace: "payments", Title: "Safe pack"}
	spec, _ := FindTemplate("retrieval_pack")
	body, deterministic, err := DeterministicOutput(spec, recipe, nuggets)
	if err != nil {
		t.Fatal(err)
	}
	if !deterministic || strings.Contains(body, "raw transcript") {
		t.Fatalf("retrieval body = %q", body)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &row); err != nil {
		t.Fatal(err)
	}
	metadata := row["metadata"].(map[string]any)
	if metadata["session_id"] != "session-1" || metadata["turn_ref"] != "turn:7" {
		t.Fatalf("missing provenance: %#v", metadata)
	}
}

func TestPersonalizationKeepsTrainingLockedBelowConservativeGates(t *testing.T) {
	report := AssessPersonalization([]index.Nugget{
		{Kind: "decision", Confidence: .95},
		{Kind: "error_fix", Confidence: .95},
		{Kind: "dead_end", Confidence: .95},
	})
	if report.TrainingReady {
		t.Fatal("tiny data set was marked training ready")
	}
	if !strings.Contains(strings.ToLower(report.Recommendation), "retrieval") {
		t.Fatalf("recommendation = %q", report.Recommendation)
	}
}

func TestPreferencePairsRequireMatchingSubject(t *testing.T) {
	unrelated := []index.Nugget{
		{UID: "dead-auth", Kind: "dead_end", Title: "JWT refresh experiment", Tags: []string{"authentication"}, Workspace: "api", Body: "Rejected auth path."},
		{UID: "fix-cache", Kind: "error_fix", Title: "Redis eviction fix", Tags: []string{"caching"}, Workspace: "api", Body: "Accepted cache fix."},
	}
	if got := preferencePairs(unrelated); len(got) != 0 {
		t.Fatalf("unrelated evidence was paired: %#v", got)
	}
	if report := AssessPersonalization(unrelated); report.PreferencePairs != 0 {
		t.Fatalf("unrelated preference count=%d, want 0", report.PreferencePairs)
	}

	matched := append(unrelated, index.Nugget{
		UID: "fix-auth", Kind: "error_fix", Title: "JWT refresh correction",
		Tags: []string{"authentication"}, Workspace: "api", Body: "Accepted auth fix.",
	})
	pairs := preferencePairs(matched)
	if len(pairs) != 1 {
		t.Fatalf("matched pairs=%#v", pairs)
	}
	provenance := pairs[0]["provenance"].(map[string]any)
	if provenance["chosen_evidence_id"] != "fix-auth" ||
		provenance["rejected_evidence_id"] != "dead-auth" {
		t.Fatalf("pair provenance=%#v", provenance)
	}
}

func TestRecipeTitleDescribesOutcome(t *testing.T) {
	nuggets := []index.Nugget{{
		UID: "n1", Kind: "decision", Title: "Keep exports local",
		Body: "Review first.", Workspace: `E:\projects\orvantix`, Confidence: .9,
	}}
	recipe, err := Design(
		"Create a retrieval pack, evaluation pack, privacy manifest, and provenance manifest.",
		`E:\projects\orvantix`,
		[]string{"retrieval_pack", "eval_pack", "privacy_manifest", "provenance_manifest"},
		nuggets,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Title != "orvantix retrieval pack and evaluation pack + 2 more" {
		t.Fatalf("title=%q", recipe.Title)
	}
}
