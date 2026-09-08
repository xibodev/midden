package module

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOverlayAndSkillDigestsMatchContent is the property the host relies on:
// it verifies these digests before folding any of this into agent context and
// refuses on mismatch. A digest that did not describe the bytes served would
// turn that check into theatre.
func TestOverlayAndSkillDigestsMatchContent(t *testing.T) {
	d := Describe()

	if len(d.AgentOverlays) == 0 {
		t.Fatal("descriptor declares no agent overlay")
	}
	if len(d.Skills) == 0 {
		t.Fatal("descriptor declares no skills")
	}

	check := func(kind, id, path, digest string, tokens int) {
		raw, ok := OverlayContent(path)
		if !ok {
			t.Errorf("%s %s: declared path %q serves no content", kind, id, path)
			return
		}
		if got := DigestSHA256(raw); got != digest {
			t.Errorf("%s %s: declared digest %s does not match content digest %s", kind, id, digest, got)
		}
		if !ValidDigest(digest) {
			t.Errorf("%s %s: digest %q is not sha256:<lowercase-hex>", kind, id, digest)
		}
		if tokens <= 0 {
			t.Errorf("%s %s: token estimate must be positive, got %d", kind, id, tokens)
		}
		if filepath.IsAbs(path) {
			t.Errorf("%s %s: path %q is absolute; must be relative to the module root", kind, id, path)
		}
	}

	for _, o := range d.AgentOverlays {
		check("overlay", o.ID, o.Path, o.Digest, o.Tokens)
	}
	for _, s := range d.Skills {
		check("skill", s.ID, s.Path, s.Digest, s.Tokens)
	}
}

// TestEmbeddedContentMatchesPublishedFiles guards the one real hazard of
// embedding a copy: go:embed cannot reach above the package directory, so the
// bytes the descriptor digests live under internal/module/content/ while the
// paths it advertises point at the repository root.
//
// If those diverge, the host verifies a digest over one document while a human
// reads another — and both look correct in isolation.
func TestEmbeddedContentMatchesPublishedFiles(t *testing.T) {
	// Repository root, relative to this package.
	root := filepath.Join("..", "..")

	for _, path := range []string{
		pathOverlayRecovery,
		pathSkillRecovery,
		pathSkillEvidence,
		pathSkillSeed,
	} {
		published, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("published file %s is missing: %v", path, err)
			continue
		}
		embedded, ok := OverlayContent(path)
		if !ok {
			t.Errorf("embedded copy of %s is missing", path)
			continue
		}
		if string(published) != string(embedded) {
			t.Errorf("%s: embedded copy has drifted from the published file; "+
				"the host would verify a digest over content a human never sees", path)
		}
	}
}

// TestOverlayContentRefusesUndeclaredPaths proves the content helper is not a
// general file-read primitive. Serving an arbitrary path would hand a caller
// filesystem access that the module never declared and the host never granted.
func TestOverlayContentRefusesUndeclaredPaths(t *testing.T) {
	for _, p := range []string{
		"", "../../go.mod", "/etc/passwd", `C:\Windows\win.ini`,
		"internal/module/seed.go", "agents/", "skills/session-recovery/../../go.mod",
	} {
		if _, ok := OverlayContent(p); ok {
			t.Errorf("OverlayContent served undeclared path %q", p)
		}
	}
}

// TestCapabilitySkillsResolve proves every per-capability skill hint names a
// skill the descriptor actually declares. A dangling hint would have the host
// try to load knowledge that does not exist — the same class of bug as a
// dangling schema reference.
func TestCapabilitySkillsResolve(t *testing.T) {
	d := Describe()
	declared := map[string]bool{}
	for _, s := range d.Skills {
		declared[s.ID] = true
	}
	for _, c := range d.Capabilities {
		if len(c.Skills) == 0 {
			t.Errorf("capability %s names no skill; the host has nothing to load for it", c.ID)
		}
		for _, id := range c.Skills {
			if !declared[id] {
				t.Errorf("capability %s references skill %q that the descriptor does not declare", c.ID, id)
			}
		}
	}
}

// TestSkillsAreSeparateDocuments records why the skills are split rather than
// one document: the host loads progressively, so a session-recovery question
// must not cost the agent the seed contract as well.
func TestSkillsAreSeparateDocuments(t *testing.T) {
	d := Describe()
	seen := map[string]bool{}
	for _, s := range d.Skills {
		if seen[s.Path] {
			t.Errorf("two skills share path %s; progressive loading needs separate documents", s.Path)
		}
		seen[s.Path] = true

		// A skill large enough to rival the overlay defeats the point of
		// loading it selectively.
		if s.Tokens > 2000 {
			t.Errorf("skill %s is %d tokens; keep skills small enough to load on demand", s.ID, s.Tokens)
		}
	}
}

// TestDeclaredDigestsMatchPublishedFilesOnDisk is the guard for a failure the
// host hit in production: the descriptor declared a digest for content that had
// since been edited, so the host refused the overlay at load and the module ran
// without its guidance — silently, because the capabilities still worked.
//
// TestEmbeddedContentMatchesPublishedFiles compares the two COPIES of each
// document. This compares the DECLARED DIGEST against the published file, which
// is the thing a host actually verifies. They fail on different mistakes:
// editing one copy trips the first; editing both without rebuilding trips
// neither, but a host reading the published tree would still refuse.
func TestDeclaredDigestsMatchPublishedFilesOnDisk(t *testing.T) {
	root := filepath.Join("..", "..")
	d := Describe()

	check := func(kind, id, path, declared string) {
		published, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("%s %s: published file %s missing: %v", kind, id, path, err)
			return
		}
		if got := DigestSHA256(published); got != declared {
			t.Errorf("%s %s: descriptor declares %s but %s hashes to %s.\n"+
				"A host verifies the declared digest against the file it reads and REFUSES a mismatch, "+
				"so this content would never enter agent context. Rebuild after editing.",
				kind, id, declared, path, got)
		}
	}
	for _, o := range d.AgentOverlays {
		check("overlay", o.ID, o.Path, o.Digest)
	}
	for _, s := range d.Skills {
		check("skill", s.ID, s.Path, s.Digest)
	}
}
