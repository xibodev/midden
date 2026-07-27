package summary

import (
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

func sess() core.Session {
	now := time.Now()
	return core.Session{
		Tool: core.ToolCopilot, ID: "abc123", Title: "Review the audit",
		Dir: `E:\projects\thing`, Repo: "me/thing",
		Created: now.AddDate(0, 0, -7), Updated: now,
	}
}

func TestDepthCostClasses(t *testing.T) {
	// The whole reason depths are explicit is that they have different
	// prices. If shallow ever starts spending, the labelling everywhere
	// becomes a lie.
	if Shallow.Spends() {
		t.Error("shallow must never call a model")
	}
	if !Deep.Spends() || !XRay.Spends() {
		t.Error("deep and xray must be labelled as spending")
	}
}

func TestParseDepth(t *testing.T) {
	for in, want := range map[string]Depth{
		"": Shallow, "shallow": Shallow, "SHALLOW": Shallow,
		"deep": Deep, "xray": XRay, "x-ray": XRay,
	} {
		got, err := ParseDepth(in)
		if err != nil || got != want {
			t.Errorf("ParseDepth(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseDepth("exhaustive"); err == nil {
		t.Error("unknown depths must be rejected, not silently downgraded")
	}
}

func TestEveryDepthExplainsItself(t *testing.T) {
	for _, d := range Depths {
		if len(d.Describe()) < 30 {
			t.Errorf("%s does not explain what it buys", d)
		}
	}
}

func TestShallowQuotesRatherThanInterprets(t *testing.T) {
	// The free tier's value is that it is verbatim. If it ever starts
	// summarising, the operator loses the one output they can fully trust.
	c := Context{
		Session: sess(), Depth: Shallow,
		Harvest: core.Harvest{
			UserTurns:     12,
			Goal:          &core.Turn{Role: "user", Text: "VERBATIM_ASK"},
			LastAssistant: &core.Turn{Role: "assistant", Text: "VERBATIM_END"},
		},
	}
	out := RenderShallow(c)

	for _, want := range []string{"VERBATIM_ASK", "VERBATIM_END", "Review the audit", "12"} {
		if !strings.Contains(out, want) {
			t.Errorf("shallow output lost %q", want)
		}
	}
	if !strings.Contains(out, "No model was called") {
		t.Error("shallow must state that nothing was interpreted")
	}
}

func TestXRayTreatsRepositoryAsGroundTruth(t *testing.T) {
	// A dying session's final claims routinely describe work that never
	// completed. The x-ray prompt exists to check them, not echo them.
	c := Context{
		Session: sess(), Depth: XRay,
		Workspace: &WorkspaceState{
			IsGit: true, Branch: "feat/x", Dirty: true, DirtyFiles: 4,
			RecentCommits: []string{"abc123 2026-07-01 did a thing"},
			Unpushed:      2,
		},
	}
	p := c.Prompt()

	if !strings.Contains(p, "ground truth") {
		t.Error("x-ray must declare the repository authoritative")
	}
	if !strings.Contains(p, "NOT confirmed") {
		t.Error("x-ray must ask for unconfirmed claims explicitly")
	}
	for _, want := range []string{"feat/x", "dirty", "unpushed commits: 2", "abc123"} {
		if !strings.Contains(p, want) {
			t.Errorf("x-ray prompt missing workspace fact %q", want)
		}
	}
}

func TestDeepDoesNotIncludeWorkspaceState(t *testing.T) {
	// Deep is cheaper because it reads less. If it silently pulled in git
	// state, the price difference between the tiers would be a fiction.
	c := Context{
		Session: sess(), Depth: Deep,
		Workspace: &WorkspaceState{IsGit: true, Branch: "SHOULD_NOT_APPEAR"},
	}
	if strings.Contains(c.Prompt(), "SHOULD_NOT_APPEAR") {
		t.Error("deep must not include workspace state")
	}
}

func TestPromptRefusesToInvent(t *testing.T) {
	for _, d := range []Depth{Deep, XRay} {
		p := Context{Session: sess(), Depth: d}.Prompt()
		if !strings.Contains(p, "not established by the evidence") &&
			!strings.Contains(p, "say so in one line") {
			t.Errorf("%s prompt does not instruct against invention", d)
		}
		if !strings.Contains(p, "ask operator") {
			t.Errorf("%s prompt does not preserve redaction placeholders", d)
		}
	}
}

func TestInstructionsPrecedeEvidence(t *testing.T) {
	// Cache writes dominate cost, so the stable block must come first.
	c := Context{
		Session: sess(), Depth: Deep,
		Harvest: core.Harvest{Goal: &core.Turn{Text: "VARIABLE_EVIDENCE"}},
	}
	p := c.Prompt()
	if strings.Index(p, "Rules:") > strings.Index(p, "VARIABLE_EVIDENCE") {
		t.Error("stable instructions must precede variable evidence")
	}
}

func TestRedactScrubsEveryEvidenceChannel(t *testing.T) {
	// Evidence reaches a model and may be written to a file, so a secret in
	// any channel has escaped.
	secret := "AKIAIOSFODNN7EXAMPLE"
	m := assay.NewManifest("s", "copilot")
	m.Candidates = append(m.Candidates, assay.Record{Preview: "key " + secret})

	c := Context{
		Session: sess(),
		Harvest: core.Harvest{
			Goal:   &core.Turn{Text: "use " + secret},
			Recent: []core.Turn{{Text: "again " + secret}},
		},
		Manifest: m,
	}
	c.Redact()

	if strings.Contains(c.Prompt(), secret) {
		t.Fatal("a secret survived into the prompt")
	}
	if len(c.Findings) == 0 {
		t.Error("redaction should be reported to the operator")
	}
}

func TestInspectWorkspaceHandlesNonRepos(t *testing.T) {
	ws := InspectWorkspace(t.TempDir())
	if ws == nil {
		t.Fatal("a plain directory should still produce state")
	}
	if ws.IsGit {
		t.Error("an empty temp dir is not a git repository")
	}
	if InspectWorkspace("") != nil {
		t.Error("an empty path should yield no state rather than a panic")
	}
}
