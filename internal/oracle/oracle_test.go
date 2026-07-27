package oracle

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/advise"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
)

func brief() Brief {
	return Brief{
		State: guide.State{
			Sessions: 689, Assayed: 313, AtRisk: 5, CriticalRisk: 4,
			DeadDirs: 198, Nuggets: 15, FootprintByte: 40 << 30,
			ReclaimBytes: 2 << 30, BusiestWorkspace: `E:\projects\thing`,
		},
		Findings: []advise.Finding{
			{Severity: advise.High, Category: "risk", Title: "past the cliff", Evidence: "2.8 GiB"},
		},
		Nuggets: []index.Nugget{
			{Kind: "gotcha", Title: "A gotcha", Body: "Body text", SessionID: "abcdef123", Workspace: `E:\a\b`},
		},
		TopDirs: []DirStat{{Dir: `E:\projects\thing`, Sessions: 40, Bytes: 1 << 30}},
	}
}

func TestCostIsRoughlyConstantRegardlessOfCorpusSize(t *testing.T) {
	// The point of the oracle is that it reasons over the compressed picture,
	// not the corpus. A question about 5,000 sessions must not cost 100x a
	// question about 50.
	small := brief()
	small.State.Sessions = 50

	large := brief()
	large.State.Sessions = 5000
	large.State.FootprintByte = 400 << 30

	q := "what wastes the most tokens?"
	ds, dl := small.EstTokens(q), large.EstTokens(q)

	if dl > ds*2 {
		t.Errorf("cost scaled with corpus size: %d vs %d tokens", ds, dl)
	}
}

func TestTrimRespectsTheBudget(t *testing.T) {
	b := brief()
	for i := 0; i < 400; i++ {
		b.Nuggets = append(b.Nuggets, index.Nugget{
			Kind: "gotcha", Title: "T", Body: strings.Repeat("x", 300), SessionID: "s",
		})
	}
	for i := 0; i < 60; i++ {
		b.Findings = append(b.Findings, advise.Finding{
			Category: "exhaust", Title: "T", Evidence: strings.Repeat("y", 200),
		})
	}

	q := "why?"
	if b.EstTokens(q) <= Budget {
		t.Skip("fixture did not exceed the budget")
	}
	b.Trim(q)
	if b.EstTokens(q) > Budget {
		t.Errorf("Trim left %d tokens, over the %d budget", b.EstTokens(q), Budget)
	}
	// Nuggets cost real money to produce, so they are the last thing dropped.
	if len(b.Nuggets) < 8 {
		t.Errorf("Trim discarded too many nuggets: %d left", len(b.Nuggets))
	}
}

func TestPromptForbidsInventionAndDemandsGrounding(t *testing.T) {
	p := brief().Prompt("what should I do?")
	for _, want := range []string{
		"only the evidence below",
		"Never invent",
		"say exactly what is missing",
		"ask operator",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing the guard %q", want)
		}
	}
}

func TestPromptDeclaresWhichCommandsCost(t *testing.T) {
	// The oracle recommends commands. If it cannot tell free from paid, it
	// will cheerfully suggest spending money.
	p := brief().Prompt("what next?")
	if !strings.Contains(p, "SPENDS midden reclaim") {
		t.Error("prompt must mark reclaim as spending")
	}
	if !strings.Contains(p, "free   midden doctor") && !strings.Contains(p, "free  midden doctor") {
		t.Errorf("prompt must mark doctor as free")
	}
}

func TestEvidenceCarriesTheNumbersAnAnswerNeeds(t *testing.T) {
	e := brief().Evidence()
	for _, want := range []string{"689 sessions", "313 assayed", "4 already past the cliff", "15 nuggets"} {
		if !strings.Contains(e, want) {
			t.Errorf("evidence missing %q", want)
		}
	}
}

func TestQuestionAppearsLastForCachePrefix(t *testing.T) {
	q := "UNIQUE_QUESTION_TEXT"
	p := brief().Prompt(q)
	if strings.Index(p, "Rules:") > strings.Index(p, q) {
		t.Error("instructions must precede the question for cache reuse")
	}
	if !strings.HasSuffix(strings.TrimSpace(p), q) {
		t.Error("the question should be the final thing the model reads")
	}
}

func TestRedactAllScrubsNuggetsAndFindings(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	b := brief()
	b.Nuggets[0].Body = "use " + secret
	b.Findings[0].Evidence = "found " + secret

	b.RedactAll()

	if strings.Contains(b.Prompt("q"), secret) {
		t.Fatal("a secret survived into the oracle prompt")
	}
	if len(b.Redaction) == 0 {
		t.Error("redaction should be reported")
	}
}

func TestSuggestionsMatchTheSituation(t *testing.T) {
	// An empty prompt is a dead end. Suggestions should reflect what is
	// actually true right now.
	atRisk := Suggestions(guide.State{CriticalRisk: 3})
	if !strings.Contains(strings.Join(atRisk, " "), "about to lose") {
		t.Error("should offer the loss question when sessions are at risk")
	}

	fresh := Suggestions(guide.State{})
	if len(fresh) == 0 {
		t.Error("even a fresh machine should get starter questions")
	}
	for _, q := range fresh {
		if !strings.HasSuffix(q, "?") {
			t.Errorf("suggestion is not a question: %q", q)
		}
	}
}

func TestSuggestionsAreBounded(t *testing.T) {
	s := Suggestions(guide.State{CriticalRisk: 5, ReclaimBytes: 5 << 30, Nuggets: 100})
	if len(s) > 6 {
		t.Errorf("too many suggestions (%d) — a wall of options is its own dead end", len(s))
	}
}
