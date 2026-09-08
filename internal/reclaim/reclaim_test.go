package reclaim

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

func TestParseHandlesFencedAndProsyOutput(t *testing.T) {
	// Models wrap JSON in prose and code fences no matter how firmly they are
	// told not to, so the array must be located rather than assumed.
	s := core.Session{Tool: core.ToolCopilot, ID: "sess1", Dir: `E:\repo`}

	cases := []string{
		`[{"kind":"gotcha","title":"T","body":"B","confidence":0.9}]`,
		"```json\n" + `[{"kind":"gotcha","title":"T","body":"B","confidence":0.9}]` + "\n```",
		"Here are the nuggets:\n\n" + `[{"kind":"gotcha","title":"T","body":"B","confidence":0.9}]` + "\n\nHope that helps!",
	}
	for i, out := range cases {
		ns, err := Parse(out, s, "test-model")
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if len(ns) != 1 || ns[0].Title != "T" {
			t.Fatalf("case %d: got %+v", i, ns)
		}
	}
}

func TestParseAttachesProvenance(t *testing.T) {
	// A nugget without provenance cannot be verified or re-mined later.
	s := core.Session{Tool: core.ToolClaude, ID: "abc123", Dir: `E:\ws`, Repo: "me/repo"}
	ns, err := Parse(`[{"kind":"decision","title":"X","body":"Y","confidence":0.5}]`, s, "haiku")
	if err != nil {
		t.Fatal(err)
	}

	n := ns[0]
	if n.SessionID != "abc123" || n.Tool != "claude" {
		t.Errorf("session provenance missing: %+v", n)
	}
	if n.Model != "haiku" {
		t.Errorf("model provenance missing: %q", n.Model)
	}
	if n.Workspace != `E:\ws` || n.Repo != "me/repo" {
		t.Errorf("workspace provenance missing: %+v", n)
	}
	if n.UID == "" || n.CreatedAt.IsZero() {
		t.Error("uid and timestamp are required")
	}
}

func TestParseRejectsUnknownKinds(t *testing.T) {
	s := core.Session{Tool: core.ToolCopilot, ID: "s"}
	ns, _ := Parse(`[{"kind":"wildly_made_up","title":"T","body":"B"}]`, s, "m")
	if len(ns) != 1 {
		t.Fatal("expected one nugget")
	}
	if ns[0].Kind != "gotcha" {
		t.Errorf("unknown kind should fall back to gotcha, got %q", ns[0].Kind)
	}
}

func TestParseDropsEmptyBodies(t *testing.T) {
	s := core.Session{Tool: core.ToolCopilot, ID: "s"}
	ns, _ := Parse(`[{"kind":"gotcha","title":"T","body":"   "},{"kind":"gotcha","title":"U","body":"real"}]`, s, "m")
	if len(ns) != 1 || ns[0].Title != "U" {
		t.Errorf("empty bodies should be dropped, got %+v", ns)
	}
}

func TestParseRedactsOnTheWayIn(t *testing.T) {
	// Belt and braces: the model may echo something the preview redaction
	// missed, and the nugget store may be synced or committed.
	s := core.Session{Tool: core.ToolCopilot, ID: "s"}
	body := `use AKIAIOSFODNN7EXAMPLE to authenticate`
	ns, err := Parse(`[{"kind":"command","title":"T","body":"`+body+`"}]`, s, "m")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ns[0].Body, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("secret reached the nugget store: %q", ns[0].Body)
	}
	if !ns[0].Redacted {
		t.Error("nugget should be flagged as redacted")
	}
}

func TestParseClampsConfidence(t *testing.T) {
	s := core.Session{Tool: core.ToolCopilot, ID: "s"}
	ns, _ := Parse(`[{"kind":"gotcha","title":"A","body":"b","confidence":5},
	                 {"kind":"gotcha","title":"B","body":"b","confidence":-2}]`, s, "m")
	if ns[0].Confidence != 1 || ns[1].Confidence != 0 {
		t.Errorf("confidence not clamped: %v, %v", ns[0].Confidence, ns[1].Confidence)
	}
}

func TestParseSanitisesTags(t *testing.T) {
	// Tags are stored comma-joined, so a comma inside a tag would corrupt the
	// round-trip.
	s := core.Session{Tool: core.ToolCopilot, ID: "s"}
	ns, _ := Parse(`[{"kind":"gotcha","title":"T","body":"b","tags":["Foo,Bar"," BAZ ",""]}]`, s, "m")

	for _, tag := range ns[0].Tags {
		if strings.Contains(tag, ",") {
			t.Errorf("tag contains a comma: %q", tag)
		}
		if tag != strings.ToLower(tag) {
			t.Errorf("tag not normalised: %q", tag)
		}
	}
}

func TestBuildSliceRedactsAndBounds(t *testing.T) {
	m := assay.NewManifest("s", "copilot")
	for i := 0; i < 50; i++ {
		m.Candidates = append(m.Candidates, assay.Record{
			Class:   assay.Signal,
			Preview: "key AKIAIOSFODNN7EXAMPLE in message",
		})
	}
	sl := BuildSlice(core.Session{ID: "s"}, m, 10)

	if len(sl.Candidates) != 10 {
		t.Errorf("slice not bounded: %d candidates", len(sl.Candidates))
	}
	for _, c := range sl.Candidates {
		if strings.Contains(c.Preview, "AKIAIOSFODNN7EXAMPLE") {
			t.Fatal("secret survived into the evidence slice")
		}
	}
	if len(sl.Findings) == 0 {
		t.Error("redaction findings should be reported to the operator")
	}
}

func TestPromptPutsStablePrefixFirst(t *testing.T) {
	// Cache writes dominate cost, so the reusable instruction block must come
	// before the variable evidence.
	m := assay.NewManifest("s", "copilot")
	m.Candidates = append(m.Candidates, assay.Record{Class: assay.Signal, Preview: "VARIABLE_EVIDENCE"})
	p := BuildSlice(core.Session{ID: "s", Title: "T"}, m, 5).Prompt()

	iInstr := strings.Index(p, "Return ONLY a JSON array")
	iEvid := strings.Index(p, "VARIABLE_EVIDENCE")
	if iInstr < 0 || iEvid < 0 {
		t.Fatal("prompt missing instructions or evidence")
	}
	if iInstr > iEvid {
		t.Error("stable instructions must precede variable evidence for cache reuse")
	}
}

func TestSliceEstimateIsNonZero(t *testing.T) {
	m := assay.NewManifest("s", "copilot")
	m.Candidates = append(m.Candidates, assay.Record{Class: assay.Signal, Preview: strings.Repeat("x", 400)})
	if got := BuildSlice(core.Session{ID: "s"}, m, 5).EstTokens(); got <= 0 {
		t.Errorf("estimate should be positive, got %d", got)
	}
}

// TestParseIgnoresTrailingProse guards a failure that cost a real extraction.
//
// A model returned a valid array and then explained itself. extractJSONArray
// took the LAST "]" in the output, which was inside that trailing prose, so
// everything between was swallowed into the parse and the error read "invalid
// character 'S' after top-level value" — a symptom that hid a perfectly good
// array sitting in front of it.
func TestParseIgnoresTrailingProse(t *testing.T) {
	out := `[{"kind":"gotcha","title":"t","body":"b","confidence":0.9}]

These are the highest-value items [the rest were routine].`

	ns, err := Parse(out, core.Session{ID: "s1", Tool: core.ToolClaude}, "claude")
	if err != nil {
		t.Fatalf("trailing prose broke the parse: %v", err)
	}
	if len(ns) != 1 {
		t.Fatalf("got %d nuggets, want 1", len(ns))
	}
}

// TestParseKeepsBracketsInsideStrings proves the depth scan does not close the
// array early on a bracket that is part of a body.
func TestParseKeepsBracketsInsideStrings(t *testing.T) {
	out := `[{"kind":"command","title":"t","body":"run cmd [1] then cmd [2]","confidence":0.8},
{"kind":"gotcha","title":"u","body":"second","confidence":0.7}]`

	ns, err := Parse(out, core.Session{ID: "s1", Tool: core.ToolClaude}, "claude")
	if err != nil {
		t.Fatalf("brackets inside a string broke the parse: %v", err)
	}
	if len(ns) != 2 {
		t.Errorf("got %d nuggets, want 2 — the array closed early on a bracket in a body", len(ns))
	}
}
