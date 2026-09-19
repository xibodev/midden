package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in local fixture contains no recovered user data or expected answers
// in the operator prompt. Its pinned source is reusable across host runs.
func TestGenerateJourneyFixture(t *testing.T) {
	home := os.Getenv("MIDDEN_JOURNEY_FIXTURE")
	if home == "" {
		t.Skip("set an isolated fixture home to generate the local acceptance source")
	}
	if !filepath.IsAbs(home) {
		t.Fatal("fixture home must be absolute")
	}
	dir := filepath.Join(home, ".claude", "projects", "synthetic-journey")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "11111111-2222-3333-4444-555555555555.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	events := []struct{ role, text string }{
		{"user", "Migrate the Northwind metrics service from the legacy score payload to the unified payload. Preserve zero scores and keep existing clients working."},
		{"assistant", "Plan: use the generic serializer and omit empty values. This is a proposal, not a validated implementation."},
		{"user", "The compatibility tests found that zero is being treated as missing. Do not call the migration successful."},
		{"assistant", "Correction: zero is a real score. Keep explicit zero values in the old output contract. The mapper needs presence-aware handling rather than a generic omit-empty rule."},
		{"user", "The local checks pass, but the live endpoint is still running version 1.8. We must establish deployment eligibility before claiming production validation."},
		{"assistant", "We changed the order: inspect the deployed package version first, then run the live comparison matrix. A green local test does not prove a deployed service changed."},
		{"user", "Version 1.9 is deployed. The direct and unified paths passed the selected structural-parity canary. LLM scores differ between independent calls; numeric identity is not the parity criterion."},
		{"assistant", "Verified scope: explicit zero preservation, deployed version 1.9, and the selected structural-parity canary. The asynchronous scheduler issue remains separate and unresolved; this is not proof that every metric and every execution path has passed."},
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	encoder := json.NewEncoder(file)
	for i, event := range events {
		if err = encoder.Encode(map[string]any{"type": event.role, "timestamp": start.Add(time.Duration(i) * 72 * time.Hour).Format(time.RFC3339), "cwd": "C:\\synthetic\\northwind", "sessionId": "11111111-2222-3333-4444-555555555555", "message": map[string]string{"role": event.role, "content": event.text}}); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 1000; j++ {
			if err = encoder.Encode(map[string]any{"type": "system.message", "timestamp": start.Add(time.Duration(i) * 72 * time.Hour).Format(time.RFC3339),
				"cwd": "C:\\synthetic\\northwind", "sessionId": "11111111-2222-3333-4444-555555555555", "message": map[string]string{"role": "system", "content": "synthetic protocol bookkeeping; not engineering evidence"}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(AssayRequest{Tool: "claude", IDs: []string{"11111111-2222-3333-4444-555555555555"}, IncludeNoise: true})
	env := Invoke(Request{Capability: CapSessionsList, Input: raw, ExplicitSourceRoots: true, Roots: map[string]Root{RootClaude: {Path: filepath.Join(home, ".claude"), Mode: "ro"}}})
	if !env.OK {
		t.Fatal(env.Error)
	}
	var result ListResult
	if err = json.Unmarshal(env.Result, &result); err != nil || len(result.Sessions) != 1 {
		t.Fatalf("generated acceptance source is not discoverable: %s", env.Result)
	}
}
