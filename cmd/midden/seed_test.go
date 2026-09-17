package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/module"
	"github.com/mekjr1/midden/internal/seed"
)

func TestSeedCreateCLIUsesSharedModuleSeedContract(t *testing.T) {
	bin := buildModuleBinary(t)
	base := t.TempDir()
	cliDir := filepath.Join(base, "cli-seed")
	moduleDir := filepath.Join(base, "module-seed")
	inputPath := filepath.Join(base, "input.json")

	in := seed.SeedInput{
		Goal:      "Carry an approved recovery decision forward",
		Title:     "Recovery decision",
		Summary:   "Use a bounded evidence slice.",
		KeyPoints: []string{"assay before extraction"},
		Evidence: []seed.SeedEvidence{{
			ID: "ev-approved", SessionID: "session-1", Tool: "claude", Kind: "decision",
			Excerpt: "Use token sk-test-12345678901234567890 only as a redaction fixture.",
		}},
		Sources: []seed.SeedSource{{
			SessionID: "session-1", Tool: "claude",
			Title: "Private prompt with token sk-test-12345678901234567890",
			Bytes: 4096, ModifiedAt: "2026-01-01T00:00:00Z",
		}},
	}
	moduleManifest, err := module.WriteSeed(moduleDir, in)
	if err != nil {
		t.Fatalf("module seed: %v", err)
	}

	cliInput := seedCLIInput{
		SelectionApproved: true,
		Goal:              in.Goal,
		Title:             in.Title,
		Summary:           in.Summary,
		KeyPoints:         in.KeyPoints,
		Evidence:          in.Evidence,
		Sources:           in.Sources,
	}
	raw, err := json.Marshal(cliInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "seed", "create", "--input", inputPath, "--out", cliDir)
	cmd.Env = []string{}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("CLI seed: %v", err)
	}
	var result seedCLIResult
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("CLI output is not structured JSON: %v\n%s", err, out)
	}
	cliManifest, err := seed.VerifySeedEvidence(cliDir)
	if err != nil {
		t.Fatalf("verify CLI seed: %v", err)
	}
	if result.Schema != seed.SeedSchemaID || cliManifest.Schema != moduleManifest.Schema {
		t.Fatalf("schema CLI=%q module=%q", cliManifest.Schema, moduleManifest.Schema)
	}
	if result.EvidenceDigest != moduleManifest.EvidenceDigest || cliManifest.EvidenceDigest != moduleManifest.EvidenceDigest {
		t.Fatalf("digest CLI=%q module=%q", cliManifest.EvidenceDigest, moduleManifest.EvidenceDigest)
	}
	if result.ReviewState != seed.ReviewUnreviewed || result.ModelUsed {
		t.Fatalf("CLI claims review/model state incorrectly: %#v", result)
	}

	for _, name := range []string{seed.SeedEvidenceFile, seed.SeedProvenanceFile} {
		cliRaw, err := os.ReadFile(filepath.Join(cliDir, name))
		if err != nil {
			t.Fatal(err)
		}
		moduleRaw, err := os.ReadFile(filepath.Join(moduleDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == seed.SeedProvenanceFile {
			cliRaw = withoutCreatedAt(t, cliRaw)
			moduleRaw = withoutCreatedAt(t, moduleRaw)
		}
		if string(cliRaw) != string(moduleRaw) {
			t.Errorf("CLI and module %s differ\nCLI: %s\nmodule: %s", name, cliRaw, moduleRaw)
		}
		if strings.Contains(string(cliRaw), "sk-test-") {
			t.Errorf("%s contains unredacted credential", name)
		}
	}
}

func TestSeedCreateCLIRejectsInvalidInputWithoutWriting(t *testing.T) {
	bin := buildModuleBinary(t)
	base := t.TempDir()
	ambient := filepath.Join(base, "ambient-midden")
	inputPath := filepath.Join(base, "input.json")
	outDir := filepath.Join(base, "must-not-exist")
	if err := os.WriteFile(inputPath, []byte(`{"selection_approved":false,"goal":"not approved"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "seed", "create", "--input", inputPath, "--out", outDir)
	cmd.Env = []string{"MIDDEN_HOME=" + ambient}
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("unapproved selection succeeded: %s", out)
	}
	for _, path := range []string{outDir, ambient} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("invalid command wrote %s: %v", path, err)
		}
	}

	relative := exec.Command(bin, "seed", "create", "--input", "input.json", "--out", "seed-out")
	relative.Dir = base
	relative.Env = []string{"MIDDEN_HOME=" + ambient}
	if out, err := relative.CombinedOutput(); err == nil {
		t.Fatalf("relative paths succeeded: %s", out)
	}
	if _, err := os.Stat(filepath.Join(base, "seed-out")); !os.IsNotExist(err) {
		t.Errorf("relative output was created: %v", err)
	}
}

func withoutCreatedAt(t *testing.T, raw []byte) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	delete(value, "created_at")
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
