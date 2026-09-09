package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Projected-package tests.
//
// Every other test in this package asserts against the SOURCE TREE: it calls
// Project() and inspects the struct. That answers "what would we project", not
// "what did this package turn out to be" -- and those diverge exactly when
// serialisation, staging or a consumer's read is where the defect lives.
//
// These write each projection out and assert against the WRITTEN ARTIFACT, the
// way a host or an installer would read it.

// writeProjection stages a conformance report as a consumer would receive it.
func writeProjection(t *testing.T, dir string, c Conformance) string {
	t.Helper()
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatalf("projection for %s is not serialisable: %v", c.Target, err)
	}
	path := filepath.Join(dir, c.Target+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("stage %s: %v", c.Target, err)
	}
	return path
}

// TestEveryProjectionSurvivesSerialisation is the property the in-memory tests
// cannot see.
//
// A conformance report is READ by someone else, so a field that vanishes on the
// way out is invisible to every assertion made before it was written. The report
// is staged and read back, and the read-back copy is what is asserted.
func TestEveryProjectionSurvivesSerialisation(t *testing.T) {
	dir := t.TempDir()
	if len(Targets) == 0 {
		t.Fatal("no targets; the sweep asserts nothing")
	}
	for _, tg := range Targets {
		want := Project(tg)
		path := writeProjection(t, dir, want)

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read back %s: %v", tg.ID, err)
		}
		var got Conformance
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s did not survive a round trip: %v", tg.ID, err)
		}

		if got.Identity != want.Identity {
			t.Errorf("%s identity changed across staging: %q -> %q",
				tg.ID, want.Identity, got.Identity)
		}
		if got.TargetVia != want.TargetVia {
			t.Errorf("%s lost the contract it was verified against", tg.ID)
		}
		if len(got.Operations) != len(want.Operations) {
			t.Errorf("%s: %d operations written, %d read back",
				tg.ID, len(want.Operations), len(got.Operations))
		}
		if len(got.Assets) != len(want.Assets) {
			t.Errorf("%s: %d assets written, %d read back",
				tg.ID, len(want.Assets), len(got.Assets))
		}
	}
}

// TestStagedReportStatesWhatWasDropped is the reason a conformance report
// exists at all.
//
// A consumer reading the staged file must be able to tell "Midden does this
// here" from "Midden does this somewhere". A degraded or unsupported entry that
// serialises without its reason is a claim with no evidence behind it.
func TestStagedReportStatesWhatWasDropped(t *testing.T) {
	dir := t.TempDir()
	var sawNonSupported int

	for _, tg := range Targets {
		path := writeProjection(t, dir, Project(tg))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got Conformance
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		for _, op := range got.Operations {
			if op.Support == Supported {
				continue
			}
			sawNonSupported++
			if op.Reason == "" || op.Remedy == "" {
				t.Errorf("%s/%s is %s in the STAGED report but carries "+
					"reason=%q remedy=%q; a consumer reads the file, not the "+
					"struct", tg.ID, op.Operation, op.Support, op.Reason, op.Remedy)
			}
		}
	}
	if sawNonSupported == 0 {
		t.Fatal("no staged report contained a degraded or unsupported entry; " +
			"the check asserts nothing and would pass however reasons are written")
	}
}

// TestStagedPackagesAreDistinguishable guards the point of projection identity.
//
// Two targets whose staged reports are byte-identical cannot be told apart by
// whoever holds them, whatever the structs said in memory.
func TestStagedPackagesAreDistinguishable(t *testing.T) {
	dir := t.TempDir()
	seen := map[string]string{}
	for _, tg := range Targets {
		raw, err := os.ReadFile(writeProjection(t, dir, Project(tg)))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for prev, prevBody := range seen {
			if prevBody == body {
				t.Errorf("%s and %s staged byte-identical reports; a holder "+
					"cannot tell which target a package is for", prev, tg.ID)
			}
		}
		seen[tg.ID] = body
	}
}

// TestStagedReportCarriesNoAbsolutePaths is the portability property, asserted
// on the written artifact.
//
// A staged report is read at a path the producer never saw. An absolute path in
// it is wrong by construction -- the same rule the seed contract holds, applied
// to conformance evidence.
func TestStagedReportCarriesNoAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	for _, tg := range Targets {
		raw, err := os.ReadFile(writeProjection(t, dir, Project(tg)))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if strings.Contains(body, filepath.ToSlash(dir)) || strings.Contains(body, dir) {
			t.Errorf("%s staged report leaks the absolute staging location", tg.ID)
		}
		for _, marker := range []string{"C:\\", "/home/", "MIDDEN_HOME"} {
			if strings.Contains(body, marker) {
				t.Errorf("%s staged report contains absolute marker %q", tg.ID, marker)
			}
		}
	}
}
