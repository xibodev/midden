package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteSeed(t *testing.T, dir string, in SeedInput) *SeedManifest {
	t.Helper()
	m, err := WriteSeed(dir, in)
	if err != nil {
		t.Fatalf("WriteSeed: %v", err)
	}
	return m
}

// TestSeedIsAPortableDirectory proves the bundle shape the consuming lane
// depends on, and that the entry file resolves from the root.
func TestSeedIsAPortableDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-one")
	m := mustWriteSeed(t, dir, SeedInput{
		Goal:      "Explain the recovery flow",
		Title:     "Recovery flow",
		KeyPoints: []string{"scope exactly", "assay before spending"},
		Evidence: []SeedEvidence{
			{SessionID: "s1", Tool: "claude", Excerpt: "the resume cliff is at 680 MiB"},
		},
		Sources: []SeedSource{{SessionID: "s1", Tool: "claude", Bytes: 1024}},
	})

	for _, f := range []string{SeedManifestFile, SeedBriefFile, SeedEvidenceFile, SeedProvenanceFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("seed is missing %s: %v", f, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, SeedAttachmentsDir)); err != nil || !fi.IsDir() {
		t.Errorf("seed is missing the %s/ directory", SeedAttachmentsDir)
	}
	if m.Schema != SeedSchemaID {
		t.Errorf("schema = %q, want %q", m.Schema, SeedSchemaID)
	}
	if m.EvidenceCount != 1 {
		t.Errorf("evidence_count = %d, want 1", m.EvidenceCount)
	}

	// The entry file must resolve from the bundle root AND directly.
	if _, err := ReadSeed(dir); err != nil {
		t.Errorf("ReadSeed(bundle root): %v", err)
	}
	if _, err := ReadSeed(filepath.Join(dir, SeedManifestFile)); err != nil {
		t.Errorf("ReadSeed(manifest path): %v", err)
	}
}

// TestSeedLoadsFromStagedCopy is the portability property, tested rather than
// asserted.
//
// The host stages a seed into its own artifact store and hands a consumer a
// read-only root, so the path a consumer sees is NOT the path Midden wrote. A
// seed is only portable if it loads from a COPY at an unrelated absolute path,
// with no Midden binary, index, or environment present. Loading it from where
// it was written would prove nothing.
func TestSeedLoadsFromStagedCopy(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "written-here")
	want := mustWriteSeed(t, origin, SeedInput{
		Goal:     "Portable seed",
		Evidence: []SeedEvidence{{SessionID: "s1", Tool: "claude", Excerpt: "hello"}},
	})

	// Stage it somewhere completely unrelated, as the host would.
	staged := filepath.Join(t.TempDir(), "staged", "artifacts", "seed-0001")
	copyTree(t, origin, staged)

	got, err := VerifySeedEvidence(staged)
	if err != nil {
		t.Fatalf("staged seed failed verification: %v", err)
	}
	if got.EvidenceDigest != want.EvidenceDigest {
		t.Errorf("digest changed across staging: %s != %s", got.EvidenceDigest, want.EvidenceDigest)
	}
	if got.Goal != want.Goal {
		t.Errorf("goal changed across staging")
	}
}

// TestSeedManifestCarriesNoAbsolutePaths guards the commitment made to both
// sibling lanes: a staged seed is read at a different absolute path, so any
// absolute or Midden-internal path inside it would be wrong by construction.
func TestSeedManifestCarriesNoAbsolutePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-paths")
	mustWriteSeed(t, dir, SeedInput{
		Goal:     "No absolute paths",
		Evidence: []SeedEvidence{{SessionID: "s1", Tool: "claude", Excerpt: "x"}},
		Sources:  []SeedSource{{SessionID: "s1", Tool: "claude"}},
	})

	for _, f := range []string{SeedManifestFile, SeedProvenanceFile} {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		body := string(raw)
		// The temp directory is an absolute path; it must not appear anywhere.
		if strings.Contains(body, filepath.ToSlash(dir)) || strings.Contains(body, dir) {
			t.Errorf("%s leaks the absolute seed location", f)
		}
		// Look for Midden's STATE LAYOUT, not for the string ".midden" — the
		// schema ID "xibodev.midden.seed/v1" legitimately contains it, and a
		// naive substring check flags the contract's own name.
		for _, marker := range []string{"index.db", "MIDDEN_HOME", "/.midden/", `\.midden\`, "job-leases"} {
			if strings.Contains(body, marker) {
				t.Errorf("%s leaks Midden internal %q", f, marker)
			}
		}
	}
}

// TestEmptyEvidenceIsValid records that a seed with no recovered material is a
// legitimate request rather than an error, and pins the zero-byte digest both
// lanes agreed on.
func TestEmptyEvidenceIsValid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-empty")
	m := mustWriteSeed(t, dir, SeedInput{Goal: "Hand-written goal, nothing recovered"})

	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if m.EvidenceDigest != emptySHA256 {
		t.Errorf("empty evidence digest = %s, want %s", m.EvidenceDigest, emptySHA256)
	}
	if m.EvidenceCount != 0 {
		t.Errorf("evidence_count = %d, want 0", m.EvidenceCount)
	}
	if _, err := VerifySeedEvidence(dir); err != nil {
		t.Errorf("an empty evidence set must verify, not fail: %v", err)
	}
}

// TestTamperedEvidenceIsRefused proves the digest is load-bearing: a seed whose
// evidence does not match its manifest must be refused, never consumed, so a
// downstream artifact cannot claim provenance from unverified bytes.
func TestTamperedEvidenceIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-tamper")
	mustWriteSeed(t, dir, SeedInput{
		Goal:     "Tamper check",
		Evidence: []SeedEvidence{{SessionID: "s1", Tool: "claude", Excerpt: "original"}},
	})

	if _, err := VerifySeedEvidence(dir); err != nil {
		t.Fatalf("untampered seed must verify: %v", err)
	}

	ev := filepath.Join(dir, SeedEvidenceFile)
	raw, _ := os.ReadFile(ev)
	if err := os.WriteFile(ev, append(raw, []byte(`{"id":"injected"}`+"\n")...), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	if _, err := VerifySeedEvidence(dir); err == nil {
		t.Error("tampered evidence was accepted; a seed must be refused, not consumed")
	}
}

// TestBriefEditDoesNotInvalidateEvidence is the property the evidence-set
// digest scope exists to provide, and the reason it is not a whole-directory
// digest: prose is regenerated, evidence is not.
func TestBriefEditDoesNotInvalidateEvidence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-brief")
	mustWriteSeed(t, dir, SeedInput{
		Goal:     "Digest scope",
		Evidence: []SeedEvidence{{SessionID: "s1", Tool: "claude", Excerpt: "unchanged"}},
	})

	brief := filepath.Join(dir, SeedBriefFile)
	if err := os.WriteFile(brief, []byte("# Rewritten prose\n\nEntirely different wording.\n"), 0o644); err != nil {
		t.Fatalf("rewrite brief: %v", err)
	}

	if _, err := VerifySeedEvidence(dir); err != nil {
		t.Errorf("editing brief.md invalidated the evidence digest; scope must be the evidence set: %v", err)
	}
}

// TestSecretsAreRedactedInSeeds guards the boundary crossing. A seed leaves
// Midden, so redaction happens on the way in rather than being left to callers.
func TestSecretsAreRedactedInSeeds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seed-secret")
	const token = "ghp_012345678901234567890123456789012345"
	mustWriteSeed(t, dir, SeedInput{
		Goal: "Redaction",
		Evidence: []SeedEvidence{
			{SessionID: "s1", Tool: "claude", Excerpt: "the token is " + token},
		},
	})

	raw, err := os.ReadFile(filepath.Join(dir, SeedEvidenceFile))
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Error("a credential-shaped string survived into the seed")
	}

	var rec SeedEvidence
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &rec); err != nil {
		t.Fatalf("evidence line is not valid JSON: %v", err)
	}
	if !rec.Redacted {
		t.Error("record does not report that redaction occurred")
	}
}

// TestSeedNameCannotEscapeRoot is the confinement check. A seed name arrives
// from a caller and becomes a directory, so it is exactly the input that must
// not be able to name a location outside the granted root.
func TestSeedNameCannotEscapeRoot(t *testing.T) {
	bad := []string{
		"", "..", ".", "../escape", "sub/dir", `sub\dir`, "/abs", `C:\windows`,
		"has space", "semi;colon", "dot.dot", strings.Repeat("x", 101),
	}
	for _, n := range bad {
		if validSeedName(n) {
			t.Errorf("seed name %q was accepted; it must be a single safe path segment", n)
		}
	}
	for _, n := range []string{"seed-0001", "recovery_run", "abc123"} {
		if !validSeedName(n) {
			t.Errorf("seed name %q was rejected but is legal", n)
		}
	}
}

// TestWriteSeedRequiresAbsolutePath proves the write path never accepts a
// relative directory. Under the host's empty environment Midden's own Dir()
// resolves to the RELATIVE ".midden", so a relative write target is precisely
// the silent failure the operator's ruling exists to prevent.
func TestWriteSeedRequiresAbsolutePath(t *testing.T) {
	if _, err := WriteSeed(".midden/seeds/x", SeedInput{Goal: "relative"}); err == nil {
		t.Error("a relative seed directory was accepted; it must be refused")
	}
}

// TestSeedCreateRequiresTheWriteRoot pins the operator's ruling: seed.create
// resolves its location ONLY from the host-supplied root and never falls back
// to the environment or the user profile.
func TestSeedCreateRequiresTheWriteRoot(t *testing.T) {
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSeedCreate,
		RequestID:  "req-no-root",
		Roots:      map[string]Root{}, // host granted nothing
	})

	if env.OK {
		t.Fatal("seed.create succeeded with no write root; it must refuse rather than fall back")
	}
	if env.Error == nil || env.Error.Code != ErrMissingRoot {
		t.Fatalf("expected %s, got %+v", ErrMissingRoot, env.Error)
	}
	if !env.Error.Retryable {
		t.Error("a missing root the host can supply must be retryable")
	}
	if env.RequestID != "req-no-root" {
		t.Errorf("request_id must be echoed verbatim, got %q", env.RequestID)
	}
}

// TestSeedCreateRefusesReadOnlyRoot proves a read-only grant is not silently
// treated as writable.
func TestSeedCreateRefusesReadOnlyRoot(t *testing.T) {
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSeedCreate,
		RequestID:  "req-ro",
		Roots:      map[string]Root{RootMiddenHome: {Path: t.TempDir(), Mode: "ro"}},
	})
	if env.OK {
		t.Fatal("seed.create wrote under a read-only root")
	}
	if env.Error == nil || env.Error.Code != ErrPermissionDenied {
		t.Fatalf("expected %s, got %+v", ErrPermissionDenied, env.Error)
	}
}

// TestSeedCreateRejectsUnsafeName proves confinement is enforced at the
// capability boundary, not only in the helper.
func TestSeedCreateRejectsUnsafeName(t *testing.T) {
	in, _ := json.Marshal(SeedCreateRequest{Name: "../../escape"})
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSeedCreate,
		RequestID:  "req-escape",
		Input:      in,
		Roots:      map[string]Root{RootMiddenHome: {Path: t.TempDir(), Mode: "rw"}},
	})
	if env.OK {
		t.Fatal("seed.create accepted a name that escapes the root")
	}
	if env.Error == nil || env.Error.Code != ErrInvalidRequest {
		t.Fatalf("expected %s, got %+v", ErrInvalidRequest, env.Error)
	}
}

// TestSourceTitlesAreBounded guards a defect found by inspecting a real seed
// rather than a synthetic one: a session's "title" is DERIVED FROM ITS FIRST
// PROMPT, so it is unbounded private prose, not a label.
//
// Left unbounded, two sessions produced a 5.7 KB provenance file of verbatim
// prompt text — a privacy leak across the module boundary and a breach of the
// bounded-size property the contract rests on. Synthetic fixtures with short
// titles would never have shown it.
func TestSourceTitlesAreBounded(t *testing.T) {
	long := strings.Repeat("private prompt text that goes on and on. ", 50)
	dir := filepath.Join(t.TempDir(), "seed-title")
	mustWriteSeed(t, dir, SeedInput{
		Goal:    "Bounded titles",
		Sources: []SeedSource{{SessionID: "s1", Tool: "claude", Title: long}},
	})

	raw, err := os.ReadFile(filepath.Join(dir, SeedProvenanceFile))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	var prov SeedProvenance
	if err := json.Unmarshal(raw, &prov); err != nil {
		t.Fatalf("provenance is not valid JSON: %v", err)
	}
	if len(prov.Sources) != 1 {
		t.Fatalf("expected one source, got %d", len(prov.Sources))
	}
	if got := len([]rune(prov.Sources[0].Title)); got > SeedTitleLimit+1 {
		t.Errorf("source title is %d runes; must be clipped to %d", got, SeedTitleLimit)
	}
	if len(raw) > 4096 {
		t.Errorf("provenance.json is %d bytes; a source list must stay bounded", len(raw))
	}
}

// TestSourceTitleSecretsAreRedacted proves a title goes through redaction as
// well as clipping. A title is first-prompt text and can carry a credential.
func TestSourceTitleSecretsAreRedacted(t *testing.T) {
	const token = "ghp_012345678901234567890123456789012345"
	dir := filepath.Join(t.TempDir(), "seed-title-secret")
	mustWriteSeed(t, dir, SeedInput{
		Goal:    "Redact titles",
		Sources: []SeedSource{{SessionID: "s1", Tool: "claude", Title: "deploy with " + token}},
	})

	raw, err := os.ReadFile(filepath.Join(dir, SeedProvenanceFile))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Error("a credential-shaped string survived in a source title")
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy tree: %v", err)
	}
}
