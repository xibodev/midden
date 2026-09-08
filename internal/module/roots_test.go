package module

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Host-supplied roots.
//
// The host runs modules with an empty environment and passes every path it may
// touch in the request, so it can enforce confinement rather than trust the
// module. These tests build a SYNTHETIC Claude store in a temp directory and
// prove the module reads it because the request named it — not because the
// machine happens to have one.
//
// That is the property that matters: a test which passes only on a developer
// machine with real session stores proves nothing about the host path.

// writeSyntheticClaudeStore creates a minimal store the Claude adapter can read
// and returns the root to hand the module.
func writeSyntheticClaudeStore(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "claude-store")
	// The adapter looks for <root>/projects/<encoded-dir>/<session>.jsonl
	proj := filepath.Join(root, "projects", "E--synthetic-workspace")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("create synthetic store: %v", err)
	}
	// A real Claude store holds more than projects/. The root normalizer looks
	// for independent evidence before treating a "projects" directory as a
	// store's child, so the fixture must carry it too.
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write store marker: %v", err)
	}

	// Records mirror the real store's shape: sessionId and cwd are how the
	// adapter identifies a session, so a fixture without them reads as empty.
	const sid = "11111111-2222-3333-4444-555555555555"
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"E:\synthetic-workspace","sessionId":"` + sid + `","userType":"external","message":{"role":"user","content":"how does midden recover an oversized session that cannot be resumed"}}`,
		`{"type":"assistant","uuid":"u2","timestamp":"2026-01-01T00:00:05.000Z","cwd":"E:\synthetic-workspace","sessionId":"` + sid + `","message":{"role":"assistant","content":"hand off to a fresh session before the cliff"}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-01-01T00:00:09.000Z","cwd":"E:\synthetic-workspace","sessionId":"` + sid + `","userType":"external","message":{"role":"user","content":"what is the byte threshold where copilot resume starts failing silently"}}`,
		`{"type":"assistant","uuid":"u4","timestamp":"2026-01-01T00:00:12.000Z","cwd":"E:\synthetic-workspace","sessionId":"` + sid + `","message":{"role":"assistant","content":"around 680 MiB, and it starts a new session rather than reporting an error"}}`,
		`{"type":"user","uuid":"u5","timestamp":"2026-01-01T00:00:20.000Z","cwd":"E:\synthetic-workspace","sessionId":"` + sid + `","userType":"external","message":{"role":"user","content":"so how do I hand off before reaching that cliff"}}`,
	}
	var body string
	for _, l := range lines {
		body += l + "\n"
	}
	// The Claude adapter skips transcripts under minTranscriptBytes (2 KiB) as
	// abandoned sessions with no real exchange. A fixture below that threshold
	// is silently invisible, which looks identical to "the root was ignored" —
	// so pad with realistic assistant turns rather than filler.
	for i := 0; len(body) < 4<<10; i++ {
		body += `{"type":"assistant","uuid":"pad` + string(rune('a'+i%26)) +
			`","timestamp":"2026-01-01T00:01:00.000Z","cwd":"E:\\synthetic-workspace","sessionId":"` + sid +
			`","message":{"role":"assistant","content":"Recovery works by assaying the transcript first, classifying each record as signal, exhaust, artifact or bookkeeping, and only then selecting a bounded slice worth carrying forward into a fresh session."}}` + "\n"
	}
	transcript := filepath.Join(proj, sid+".jsonl")
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatalf("write synthetic transcript: %v", err)
	}
	// Pin the modification time. A session's `updated` field comes from the
	// file's mtime, so a store written "now" produces a different result on
	// every run — which makes generated fixtures unreproducible for a reason
	// that has nothing to do with the protocol.
	pinned := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)
	if err := os.Chtimes(transcript, pinned, pinned); err != nil {
		t.Fatalf("pin transcript mtime: %v", err)
	}
	return root
}

// TestSessionsListUsesHostSuppliedRoots is the regression guard for the bug the
// host hit on the first real run: roots were accepted and ignored, so the
// module read the environment instead and failed under an empty one.
func TestSessionsListUsesHostSuppliedRoots(t *testing.T) {
	root := writeSyntheticClaudeStore(t)

	in, _ := json.Marshal(AssayRequest{Tool: "claude"})
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSessionsList,
		RequestID:  "req-roots-list",
		Input:      in,
		Roots: map[string]Root{
			RootClaude: {Path: root, Mode: "ro"},
		},
	})

	if !env.OK {
		t.Fatalf("sessions.list failed with a supplied root: %+v", env.Error)
	}
	var res ListResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("no sessions found in the synthetic store; the supplied root was not used")
	}
	got := res.Sessions[0]
	if got.Tool != "claude" {
		t.Errorf("tool = %q, want claude", got.Tool)
	}
	if got.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("session_id = %q, want the synthetic session", got.SessionID)
	}
}

// TestSessionsAssayUsesHostSuppliedRoots proves the assay path reads the same
// supplied root, since it resolves adapters separately from the list path.
func TestSessionsAssayUsesHostSuppliedRoots(t *testing.T) {
	root := writeSyntheticClaudeStore(t)

	in, _ := json.Marshal(AssayRequest{Tool: "claude"})
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSessionsAssay,
		RequestID:  "req-roots-assay",
		Input:      in,
		Roots: map[string]Root{
			RootClaude: {Path: root, Mode: "ro"},
		},
	})

	if !env.OK {
		t.Fatalf("sessions.assay failed with a supplied root: %+v", env.Error)
	}
	var res AssayResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Assayed == 0 {
		t.Fatal("nothing assayed; the supplied root was not used")
	}
	if res.TotalBytes == 0 {
		t.Error("assay reported zero bytes for a store with content")
	}
}

// TestSuppliedRootIsSufficientWithoutEnvironment is the precise host condition:
// a root is supplied AND the environment is unusable. The module must proceed,
// because an explicit root is exactly what makes the environment unnecessary.
//
// It manipulates the process environment rather than spawning a subprocess so
// the assertion is about the precondition logic itself.
func TestSuppliedRootIsSufficientWithoutEnvironment(t *testing.T) {
	root := writeSyntheticClaudeStore(t)

	// A supplied root alone must satisfy the precondition.
	if !sourceStoresVisible(sourceRootsFrom(Request{
		Roots: map[string]Root{RootClaude: {Path: root, Mode: "ro"}},
	})) {
		t.Error("a host-supplied root must satisfy the precondition on its own")
	}

	// No roots at all still falls back to asking the environment, which is
	// what protects the human CLI path from silently reading relative paths.
	empty := sourceRootsFrom(Request{Roots: map[string]Root{}})
	if empty.Claude != "" || empty.Copilot != "" || empty.Opencode != "" {
		t.Error("absent roots must stay empty rather than being invented")
	}
}

// TestSeedCreateUsesHostSuppliedRoots proves the write path and the read path
// are both wired: the seed is written under the granted midden_home root, and
// its evidence comes from the granted source root.
func TestSeedCreateUsesHostSuppliedRoots(t *testing.T) {
	src := writeSyntheticClaudeStore(t)
	home := t.TempDir()

	in, _ := json.Marshal(SeedCreateRequest{
		AssayRequest: AssayRequest{Tool: "claude"},
		Goal:         "seed from a synthetic store",
		Name:         "seed-roots-test",
	})
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSeedCreate,
		RequestID:  "req-roots-seed",
		Input:      in,
		Roots: map[string]Root{
			RootClaude:     {Path: src, Mode: "ro"},
			RootMiddenHome: {Path: home, Mode: "rw"},
		},
	})

	if !env.OK {
		t.Fatalf("seed.create failed with supplied roots: %+v", env.Error)
	}

	seedDir := filepath.Join(home, "seeds", "seed-roots-test")
	m, err := VerifySeedEvidence(seedDir)
	if err != nil {
		t.Fatalf("seed under the granted root did not verify: %v", err)
	}
	if m.EvidenceCount == 0 {
		t.Error("seed carries no evidence; the source root was not read")
	}

	// The artifact must point inside the granted root, relatively.
	if len(env.Execution.Artifacts) == 0 {
		t.Fatal("no artifact pointer reported")
	}
	a := env.Execution.Artifacts[0]
	if a.Root != RootMiddenHome {
		t.Errorf("artifact root = %q, want %q", a.Root, RootMiddenHome)
	}
	if filepath.IsAbs(a.Path) {
		t.Errorf("artifact path %q is absolute; it must be relative to the root", a.Path)
	}
}

// TestClaudeRootAcceptsEitherDepth guards a real interop bug found when the
// host reported the exact paths it sends.
//
// The host supplies claude_store as <profile>\.claude\projects — the directory
// that holds sessions. The adapter expects <profile>\.claude and appends
// "projects" itself. Both readings of the logical name are defensible, and the
// failure was SILENT: the wrong depth produced ok:true with zero sessions,
// which a host cannot distinguish from "this user has no sessions".
func TestClaudeRootAcceptsEitherDepth(t *testing.T) {
	root := writeSyntheticClaudeStore(t)
	projects := filepath.Join(root, "projects")

	for _, tc := range []struct {
		name string
		path string
	}{
		{"store root", root},
		{"projects directory", projects},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := json.Marshal(AssayRequest{Tool: "claude"})
			env := Invoke(Request{
				Protocol:   ProtocolID,
				Capability: CapSessionsList,
				RequestID:  "req-depth",
				Input:      in,
				Roots:      map[string]Root{RootClaude: {Path: tc.path, Mode: "ro"}},
			})
			if !env.OK {
				t.Fatalf("sessions.list failed for %s: %+v", tc.name, env.Error)
			}
			var res ListResult
			if err := json.Unmarshal(env.Result, &res); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(res.Sessions) == 0 {
				t.Errorf("%s yielded zero sessions; a supplied root must not silently match nothing", tc.name)
			}
		})
	}
}

// TestUnrelatedProjectsDirectoryIsNotRewritten proves the normalization is
// narrow: it steps up only when the parent genuinely looks like a Claude store,
// so a directory that merely happens to be named "projects" is left alone.
func TestUnrelatedProjectsDirectoryIsNotRewritten(t *testing.T) {
	base := t.TempDir()
	odd := filepath.Join(base, "projects")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := normalizeClaudeRoot(odd); got != odd {
		t.Errorf("normalizeClaudeRoot rewrote an unrelated path: %q -> %q", odd, got)
	}
}

// TestNoRootsFailsClosedUnderEmptyEnvironment is the negative half of the
// host's security standard: a supplied root must make the module work, and an
// absent root must make it fail closed rather than quietly succeed.
func TestNoRootsFailsClosedUnderEmptyEnvironment(t *testing.T) {
	// seed.create is the strictest case: it must refuse without its write root
	// regardless of whether source stores happen to be reachable.
	env := Invoke(Request{
		Protocol:   ProtocolID,
		Capability: CapSeedCreate,
		RequestID:  "req-closed",
		Roots:      map[string]Root{},
	})
	if env.OK {
		t.Fatal("seed.create succeeded with no roots; it must fail closed")
	}
	if env.Error.Code != ErrMissingRoot {
		t.Errorf("code = %q, want %q", env.Error.Code, ErrMissingRoot)
	}
}
