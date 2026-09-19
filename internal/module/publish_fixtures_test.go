package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Midden's own protocol fixtures.
//
// They are GENERATED FROM A SYNTHETIC STORE, never captured from a developer
// machine. A fixture built from real sessions carries real session IDs, real
// workspace paths, and title text taken verbatim from a session's first
// prompt — private material that must not enter a repository intended for a
// public remote.
//
// The generator is a test so the fixtures can never drift from the code that
// produces them: running the suite with -update rewrites them, and running it
// normally asserts the checked-in files still match what the module emits.

const fixtureDir = "../../testdata/fixtures"

// updateFixtures is set by `go test ./internal/module -args -update`.
var updateFixtures = os.Getenv("MIDDEN_UPDATE_FIXTURES") != ""

func writeOrCompare(t *testing.T, rel string, got []byte) {
	t.Helper()
	path := filepath.Join(fixtureDir, filepath.FromSlash(rel))

	if updateFixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", rel, err)
		}
		t.Logf("updated %s", rel)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture %s missing; regenerate with MIDDEN_UPDATE_FIXTURES=1: %v", rel, err)
	}
	if rel == "describe/descriptor.json" {
		// Delivery build is the only intentional difference in the headless
		// descriptor. Assert its real value before comparing shared semantics.
		var emitted struct {
			Result struct {
				Build string `json:"build"`
			} `json:"result"`
		}
		if err := json.Unmarshal(got, &emitted); err != nil {
			t.Fatal(err)
		}
		if emitted.Result.Build != BuildVariant {
			t.Fatalf("build=%q want %q", emitted.Result.Build, BuildVariant)
		}
		if BuildVariant == "headless" {
			var fixture map[string]any
			json.Unmarshal(want, &fixture)
			fixture["result"].(map[string]any)["build"] = BuildVariant
			want, _ = json.Marshal(fixture)
		}
	}
	// Compare parsed documents rather than bytes: a line-ending conversion
	// must not read as a protocol change.
	if !jsonEqual(want, got) {
		t.Errorf("fixture %s no longer matches module output; regenerate with MIDDEN_UPDATE_FIXTURES=1 "+
			"and review the diff — a change here is a change to the wire contract\ngot: %s", rel, got)
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	stripVolatile(x)
	stripVolatile(y)
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

// stripVolatile removes values that legitimately differ between runs.
//
// A seed records when it was created and digests its own manifest, so both
// change every run BY DESIGN — a seed created at a different moment genuinely
// is a different seed. Faking a fixed clock in the module to make fixtures
// reproduce would be making the product lie to satisfy a test.
//
// The digest is still verified for FORMAT everywhere and recomputed against
// real content in the seed tests; what is dropped here is only the comparison
// of one run's value against another's.
func stripVolatile(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k := range t {
			switch k {
			case "created_at", "digest", "evidence_digest":
				delete(t, k)
			default:
				stripVolatile(t[k])
			}
		}
	case []any:
		for _, e := range t {
			stripVolatile(e)
		}
	}
}

func marshalIndent(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(raw, '\n')
}

// TestGenerateFixtures produces Midden's published protocol fixtures.
//
// Every one is emitted by the real code path, so a fixture cannot describe a
// shape the module does not actually produce.
func TestGenerateFixtures(t *testing.T) {
	store := writeSyntheticClaudeStore(t)
	home := t.TempDir()
	roots := map[string]Root{
		RootClaude:     {Path: store, Mode: "ro"},
		RootMiddenHome: {Path: home, Mode: "rw"},
	}

	// describe
	d := Describe()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	env, err := NewResultEnvelope(OpDescribe, "", json.RawMessage(raw), LocalFree())
	if err != nil {
		t.Fatalf("describe envelope: %v", err)
	}
	writeOrCompare(t, "describe/descriptor.json", marshalIndent(t, env))

	listIn, _ := json.Marshal(AssayRequest{Tool: "claude", MaxSessions: 2})
	assayIn, _ := json.Marshal(AssayRequest{Tool: "claude", IDs: []string{"11111111-2222-3333-4444-555555555555"}, MaxSessions: 1, MaxCandidates: 5})
	seedIn, _ := json.Marshal(SeedCreateRequest{
		AssayRequest:         AssayRequest{Tool: "claude", MaxSessions: 1},
		Goal:                 "Explain how session recovery works",
		Title:                "Recovery",
		SuggestedOutputTypes: []string{"explainer"},
		MaxEvidence:          3,
		Name:                 "fixture-seed",
	})
	badToolIn, _ := json.Marshal(AssayRequest{Tool: "emacs"})

	cases := []struct {
		rel    string
		req    Request
		wantOK bool
	}{
		{"invoke/success-sessions-list.json", Request{
			Protocol: ProtocolID, Capability: CapSessionsList,
			RequestID: "req_fixture_list_0001", Input: listIn, Roots: roots,
		}, true},
		{"invoke/success-sessions-assay.json", Request{
			Protocol: ProtocolID, Capability: CapSessionsAssay,
			RequestID: "req_fixture_assay_001", Input: assayIn, Roots: roots,
		}, true},
		{"invoke/success-seed-create.json", Request{
			Protocol: ProtocolID, Capability: CapSeedCreate,
			RequestID: "req_fixture_seed_0001", Input: seedIn, Roots: roots,
		}, true},
		{"invoke/error-invalid-request.json", Request{
			Protocol: ProtocolID, Capability: CapSessionsAssay,
			RequestID: "req_fixture_badreq_01", Input: badToolIn, Roots: roots,
		}, false},
		{"invoke/error-unknown-capability.json", Request{
			Protocol: ProtocolID, Capability: "sessions.nope",
			RequestID: "req_fixture_unknown_1", Roots: roots,
		}, false},
		{"invoke/error-unsupported-protocol.json", Request{
			Protocol: "some.other/v9", Capability: CapSessionsList,
			RequestID: "req_fixture_proto_001", Roots: roots,
		}, false},
		// seed.create with no write root: the operator-ruled refusal.
		{"invoke/error-missing-root.json", Request{
			Protocol: ProtocolID, Capability: CapSeedCreate,
			RequestID: "req_fixture_noroot_01", Roots: map[string]Root{},
		}, false},
	}

	for _, c := range cases {
		// Fixture coverage must never depend on ambient developer source stores.
		c.req.ExplicitSourceRoots = true
		env := Invoke(c.req)
		if env.OK != c.wantOK {
			t.Fatalf("%s: ok=%v, want %v (error: %+v)", c.rel, env.OK, c.wantOK, env.Error)
		}
		writeOrCompare(t, c.rel, marshalIndent(t, env))
	}
}

// TestPublishedFixturesCarryNoPrivateData is the guard that matters most for a
// repository heading to a public remote.
//
// The first version of these fixtures WAS captured from a developer machine and
// carried real session IDs, real workspace paths, and title text taken verbatim
// from session prompts — because a Midden "title" is derived from a session's
// first prompt rather than being a label. Generating from a synthetic store
// fixes that; this test proves it stayed fixed.
func TestPublishedFixturesCarryNoPrivateData(t *testing.T) {
	// Markers that would indicate a fixture came from a real machine rather
	// than the synthetic store.
	banned := []string{
		"C:\\Users", "C:/Users", "/home/", "/Users/",
		"gafar", "startup projects", "open-source-projects",
		"AppData", ".claude\\projects", ".claude/projects",
		"MIDDEN_HOME", "index.db",
	}

	err := filepath.WalkDir(fixtureDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		body := string(raw)
		for _, b := range banned {
			if strings.Contains(body, b) {
				t.Errorf("%s contains %q — fixtures must be generated from the synthetic store, "+
					"never captured from a real machine", filepath.Base(path), b)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixtures: %v", err)
	}
}

// TestPublishedFixturesPassOurOwnValidator closes the loop: Midden's published
// fixtures are judged by the same validator that judges the host's. A fixture
// that our own rules reject would be worse than no fixture.
func TestPublishedFixturesPassOurOwnValidator(t *testing.T) {
	err := filepath.WalkDir(fixtureDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bad := validateEnvelopeBytes(raw); len(bad) > 0 {
			t.Errorf("%s fails Midden's own validator:\n  %s",
				filepath.Base(path), strings.Join(bad, "\n  "))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixtures: %v", err)
	}
}
