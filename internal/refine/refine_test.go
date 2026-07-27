package refine

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func nug(kind, title string) index.Nugget {
	return index.Nugget{Kind: kind, Title: title, Body: "body text", SessionID: "abcdef123456", Confidence: 0.8}
}

func TestCatalogProposesOnlySupportedArtifacts(t *testing.T) {
	// An ADR needs decisions. Proposing one from four commands would produce
	// an invented document.
	ns := []index.Nugget{nug("command", "a"), nug("command", "b"), nug("command", "c")}
	items := Catalog(ns, 2)

	names := map[string]bool{}
	for _, it := range items {
		names[it.Template] = true
	}
	if names["adr"] {
		t.Error("adr proposed with no decision nuggets")
	}
	if !names["howto"] && !names["tutorial"] {
		t.Errorf("command nuggets should support a how-to or tutorial, got %v", names)
	}
}

func TestCatalogRespectsMinimum(t *testing.T) {
	ns := []index.Nugget{nug("decision", "only one")}
	if items := Catalog(ns, 5); len(items) != 0 {
		t.Errorf("nothing should be proposed below the minimum, got %d", len(items))
	}
}

func TestPreamblePutsRulesBeforeEvidence(t *testing.T) {
	// The preamble is the cache prefix. Rules must precede evidence so the
	// stable part can be reused across artifacts.
	ev := Evidence{Scope: "test", Nuggets: []index.Nugget{nug("decision", "UNIQUE_TITLE")}}
	p := ev.Preamble()

	iRules := strings.Index(p, "Rules that apply")
	iEvid := strings.Index(p, "UNIQUE_TITLE")
	if iRules < 0 || iEvid < 0 {
		t.Fatal("preamble missing rules or evidence")
	}
	if iRules > iEvid {
		t.Error("rules must precede evidence for cache reuse")
	}
}

func TestPreambleGroupsByKindAndCitesSource(t *testing.T) {
	ev := Evidence{Nuggets: []index.Nugget{nug("decision", "D"), nug("gotcha", "G")}}
	p := ev.Preamble()

	if !strings.Contains(p, "DECISION") || !strings.Contains(p, "GOTCHA") {
		t.Error("evidence should be grouped by kind")
	}
	if !strings.Contains(p, "abcdef12") {
		t.Error("evidence should cite its source session for provenance")
	}
}

func TestRequestIsShort(t *testing.T) {
	// The per-artifact request rides on cached context, so it must stay small.
	// A large request would defeat the reason for batching.
	tpl, _ := FindTemplate("tsg")
	r := tpl.Request("")

	if len(r) > 600 {
		t.Errorf("request is %d bytes; it should be a short instruction", len(r))
	}
	if !strings.Contains(r, "Symptom") {
		t.Error("request should carry the template's shape")
	}
}

func TestCleanOutputStripsFences(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Title\n\nbody", "# Title\n\nbody"},
		{"```markdown\n# Title\n\nbody\n```", "# Title\n\nbody"},
		{"```\n# Title\n```", "# Title"},
	}
	for _, tc := range cases {
		if got := CleanOutput(tc.in); got != tc.want {
			t.Errorf("CleanOutput(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlugIsFilenameSafe(t *testing.T) {
	cases := map[string]string{
		`E:\startup projects\orvantix-tsg`: "e-startup-projects-orvantix-tsg",
		"All Reclaimed Evidence — post":    "all-reclaimed-evidence-post",
		"":                                 "artifact",
		"///":                              "artifact",
	}
	for in, want := range cases {
		got := Slug(in)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
		for _, r := range got {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Errorf("Slug(%q) produced unsafe char %q", in, r)
			}
		}
	}
}

func TestSlugIsBounded(t *testing.T) {
	long := strings.Repeat("verylongsegment ", 20)
	if got := Slug(long); len(got) > 60 {
		t.Errorf("slug too long: %d chars", len(got))
	}
}

func TestEveryTemplateIsWellFormed(t *testing.T) {
	for _, tpl := range Templates {
		if tpl.Name == "" || tpl.Title == "" {
			t.Errorf("template %+v missing name or title", tpl)
		}
		if tpl.Audience == "" {
			t.Errorf("%s has no audience; output will be unfocused", tpl.Name)
		}
		if tpl.Shape == "" {
			t.Errorf("%s has no shape; 'write a tutorial' produces mush", tpl.Name)
		}
		if len(tpl.Wants) == 0 {
			t.Errorf("%s draws on no nugget kinds", tpl.Name)
		}
		for _, w := range tpl.Wants {
			if !index.ValidKind(w) {
				t.Errorf("%s wants unknown nugget kind %q", tpl.Name, w)
			}
		}
		if _, ok := FindTemplate(tpl.Name); !ok {
			t.Errorf("%s is not findable by name", tpl.Name)
		}
	}
}

func TestEvidenceEstimateGrowsWithContent(t *testing.T) {
	small := Evidence{Nuggets: []index.Nugget{nug("decision", "a")}}
	big := Evidence{Nuggets: make([]index.Nugget, 50)}
	for i := range big.Nuggets {
		big.Nuggets[i] = index.Nugget{Kind: "decision", Title: "t", Body: strings.Repeat("x", 500)}
	}
	if big.EstTokens() <= small.EstTokens() {
		t.Error("estimate should grow with evidence size")
	}
}
