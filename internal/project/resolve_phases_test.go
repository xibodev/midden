package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPresenceNeverYieldsSatisfied is the "resolved is not usable" rule.
//
// `copilot --version` exits 0 on this machine while every real request fails to
// authenticate. A probe that reports SATISFIED from presence promises a
// capability that fails on first use, and the caller has no way to tell that
// promise from a real one.
func TestPresenceNeverYieldsSatisfied(t *testing.T) {
	for _, tg := range Targets {
		for _, r := range ResolveInstall(tg) {
			if r.Requirement != "agentic-harness" {
				continue
			}
			if r.State == Satisfied {
				t.Errorf("%s reported the harness SATISFIED at install time; "+
					"presence proves the file exists and nothing about whether "+
					"it is signed in", tg.ID)
			}
		}
	}
}

// TestEveryPhaseResolutionExplainsItself enforces the reason+remedy contract
// across both wired phases.
func TestEveryPhaseResolutionExplainsItself(t *testing.T) {
	for _, tg := range Targets {
		for _, r := range ResolveInstall(tg) {
			if err := r.Validate(); err != nil {
				t.Errorf("%s: %v", tg.ID, err)
			}
		}
	}
	for _, need := range []string{"claude", "absent-binary"} {
		r := ResolveRuntime(map[string]string{}, need)
		if err := r.Validate(); err != nil {
			t.Errorf("runtime %q: %v", need, err)
		}
	}
}

// TestRuntimeDoesNotInheritInstall pins the phases as distinct.
//
// A grant that existed at install time is not a grant now. Collapsing them means
// a module assumes authority the host did not give it this invocation.
func TestRuntimeDoesNotInheritInstall(t *testing.T) {
	ungranted := ResolveRuntime(map[string]string{}, "claude")
	if ungranted.State != Unsatisfied {
		t.Errorf("an ungranted binary resolved %s at runtime; a grant is "+
			"per invocation and never inherited", ungranted.State)
	}

	missing := ResolveRuntime(map[string]string{"claude": filepath.Join(t.TempDir(), "nope")}, "claude")
	if missing.State != Unsatisfied {
		t.Errorf("a granted-but-missing path resolved %s; the grant names a "+
			"path that must exist", missing.State)
	}

	real := filepath.Join(t.TempDir(), "claude.exe")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	present := ResolveRuntime(map[string]string{"claude": real}, "claude")
	if present.State == Satisfied {
		t.Error("a granted path that exists resolved SATISFIED; only a real " +
			"call proves the harness works")
	}
}

// TestAbsentSkillsDirIsNotUnsatisfied guards a refusal that would be wrong.
//
// The installer CREATES the skills directory. Reporting UNSATISFIED for a
// directory that does not exist yet would refuse a perfectly installable target
// -- the same shape as refusing a v1 module for declaring no contract version.
func TestAbsentSkillsDirIsNotUnsatisfied(t *testing.T) {
	for _, tg := range Targets {
		if tg.AssetForm != "skills-dir" {
			continue
		}
		for _, r := range ResolveInstall(tg) {
			if r.Requirement == "skills-directory" && r.State == Unsatisfied {
				// Only legitimate when the path exists and is unusable.
				if _, err := os.Stat(skillsDirFor(tg.ID)); os.IsNotExist(err) {
					t.Errorf("%s: a not-yet-created skills directory was reported "+
						"UNSATISFIED; the installer creates it", tg.ID)
				}
			}
		}
	}
}
