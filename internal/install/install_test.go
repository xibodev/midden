package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillsTarget builds a target pointing at a temp directory, so tests never
// touch a real skills store.
func skillsTarget(t *testing.T) Target {
	t.Helper()
	return Target{
		ID: "test-cli", Name: "Test CLI", Kind: KindSkills,
		BinaryPath: "test", SkillsDir: t.TempDir(), Detected: true,
	}
}

// TestInstallWritesTheDeclaredBundle proves an install produces exactly the
// skills the descriptor advertises, from the binary's embedded copies rather
// than from files on disk.
func TestInstallWritesTheDeclaredBundle(t *testing.T) {
	tgt := skillsTarget(t)

	res, err := InstallSkills(tgt, false)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.Written) != len(bundle) {
		t.Fatalf("wrote %d skills, want %d", len(res.Written), len(bundle))
	}
	for _, b := range bundle {
		p := filepath.Join(tgt.SkillsDir, b.dir, "SKILL.md")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s not installed: %v", b.dir, err)
			continue
		}
		// The installed file must be usable as a skill: frontmatter the
		// harness can read, and a command an agent can actually run.
		//
		// Check the DELIMITER rather than an exact "---\n" prefix. A CRLF
		// checkout is still valid YAML frontmatter and the harness parses it
		// fine, so asserting the byte sequence fails for a reason unrelated to
		// the property being protected.
		if !strings.HasPrefix(strings.TrimLeft(string(raw), "\ufeff"), "---") {
			t.Errorf("%s has no frontmatter; the harness cannot list it", b.dir)
		}
		if !strings.Contains(string(raw), "midden module") {
			t.Errorf("%s names no invocation; an agent would learn concepts it cannot act on", b.dir)
		}
	}
	if err := VerifySkillsInstall(tgt); err != nil {
		t.Errorf("verify after install: %v", err)
	}
}

// TestInstallIsIdempotent proves a second install neither errors nor rewrites,
// so re-running is safe.
func TestInstallIsIdempotent(t *testing.T) {
	tgt := skillsTarget(t)
	if _, err := InstallSkills(tgt, false); err != nil {
		t.Fatalf("first install: %v", err)
	}
	res, err := InstallSkills(tgt, false)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(res.Written) != 0 {
		t.Errorf("second install rewrote %d files; it should skip", len(res.Written))
	}
	if len(res.Skipped) != len(bundle) {
		t.Errorf("skipped %d, want %d", len(res.Skipped), len(bundle))
	}
}

// TestForceUpdatesAnExistingInstall proves --force is the upgrade path.
func TestForceUpdatesAnExistingInstall(t *testing.T) {
	tgt := skillsTarget(t)
	if _, err := InstallSkills(tgt, false); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Simulate a stale install.
	stale := filepath.Join(tgt.SkillsDir, bundle[0].dir, "SKILL.md")
	if err := os.WriteFile(stale, []byte("---\nname: old\n---\n"), 0o644); err != nil {
		t.Fatalf("stale write: %v", err)
	}
	if err := VerifySkillsInstall(tgt); err == nil {
		t.Fatal("verify should fail against a stale install")
	}

	if _, err := InstallSkills(tgt, true); err != nil {
		t.Fatalf("force install: %v", err)
	}
	if err := VerifySkillsInstall(tgt); err != nil {
		t.Errorf("verify after force: %v", err)
	}
}

// TestUninstallRemovesOnlyWhatWeOwn is the safety property that matters most.
// A user's skills directory holds their own work; an uninstaller that removed
// anything beyond its own directories would be a hazard rather than a tool.
func TestUninstallRemovesOnlyWhatWeOwn(t *testing.T) {
	tgt := skillsTarget(t)

	// A skill that is not ours, and a Midden-adjacent name that is also not
	// ours — the near-miss is the one a pattern-matching uninstaller eats.
	for _, foreign := range []string{"someone-elses-skill", "midden-lookalike"} {
		dir := filepath.Join(tgt.SkillsDir, foreign)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	if _, err := InstallSkills(tgt, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := UninstallSkills(tgt); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	for _, foreign := range []string{"someone-elses-skill", "midden-lookalike"} {
		if _, err := os.Stat(filepath.Join(tgt.SkillsDir, foreign, "SKILL.md")); err != nil {
			t.Errorf("uninstall removed %q, which this installer did not write", foreign)
		}
	}
	for _, b := range bundle {
		if _, err := os.Stat(filepath.Join(tgt.SkillsDir, b.dir)); err == nil {
			t.Errorf("%s survived uninstall", b.dir)
		}
	}
}

// TestUninstallTwiceIsSafe proves removing an absent install is not an error.
func TestUninstallTwiceIsSafe(t *testing.T) {
	tgt := skillsTarget(t)
	if _, err := InstallSkills(tgt, false); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := UninstallSkills(tgt); err != nil {
		t.Fatalf("first uninstall: %v", err)
	}
	res, err := UninstallSkills(tgt)
	if err != nil {
		t.Fatalf("second uninstall errored: %v", err)
	}
	if len(res.Written) != 0 {
		t.Errorf("second uninstall removed %d dirs; nothing was left", len(res.Written))
	}
}

// TestRelativeSkillsDirIsRefused guards the failure this project keeps hitting:
// a relative path resolves against the working directory and installs
// somewhere nobody intended, silently and successfully.
func TestRelativeSkillsDirIsRefused(t *testing.T) {
	tgt := Target{ID: "x", Name: "X", Kind: KindSkills, SkillsDir: ".skills"}
	if _, err := InstallSkills(tgt, false); err == nil {
		t.Error("a relative skills directory was accepted; it must be refused")
	}
}

// TestEmptySkillsDirIsRefused proves an unresolved home does not become a
// silent install into the current directory.
func TestEmptySkillsDirIsRefused(t *testing.T) {
	tgt := Target{ID: "x", Name: "X", Kind: KindSkills, SkillsDir: ""}
	if _, err := InstallSkills(tgt, false); err == nil {
		t.Error("an empty skills directory was accepted")
	}
}

// TestKindMismatchIsRefused proves a module host is never treated as a skills
// target, and vice versa. They are different operations with different safety
// properties and must not be interchangeable.
func TestKindMismatchIsRefused(t *testing.T) {
	host := Target{ID: "h", Name: "H", Kind: KindModule, BinaryPath: "x"}
	if _, err := InstallSkills(host, false); err == nil {
		t.Error("InstallSkills accepted a module host")
	}
	skills := skillsTarget(t)
	if _, err := RegisterWithHost(skills); err == nil {
		t.Error("RegisterWithHost accepted a skills target")
	}
}

// TestDetectReportsWithoutAsserting proves detection describes the machine
// rather than assuming it. Every target must be classified either way, and a
// missing binary must never read as detected.
func TestDetectReportsWithoutAsserting(t *testing.T) {
	targets := Detect()
	if len(targets) == 0 {
		t.Fatal("Detect returned no targets at all")
	}
	seen := map[string]bool{}
	for _, tg := range targets {
		if tg.ID == "" || tg.Name == "" {
			t.Error("target with no id or name")
		}
		if seen[tg.ID] {
			t.Errorf("duplicate target id %q", tg.ID)
		}
		seen[tg.ID] = true

		if tg.Detected && tg.BinaryPath == "" {
			t.Errorf("%s reports detected with no binary path", tg.ID)
		}
		if !tg.Detected && tg.BinaryPath != "" {
			t.Errorf("%s has a binary path but reports undetected", tg.ID)
		}
		if tg.Kind == KindSkills && tg.SkillsDir != "" && !filepath.IsAbs(tg.SkillsDir) {
			t.Errorf("%s skills dir %q is not absolute", tg.ID, tg.SkillsDir)
		}
	}
	if !seen["facet-studio"] {
		t.Error("facet-studio must always be a known target, detected or not")
	}
}

// TestSpeaksModuleProtocolProbesRatherThanGuesses guards the distinction the
// PATH warning rests on: a binary named `midden` may be a build too old to
// know the module verb, and only asking it can tell.
//
// This was found on a real machine — a August build sat first on PATH and
// answered every documented skill command with `unknown command "module"`,
// which reads as a broken skill rather than a stale binary.
func TestSpeaksModuleProtocolProbesRatherThanGuesses(t *testing.T) {
	// Something that exists and is certainly not Midden.
	//
	// The RETURN used to sit inside the loop, so the first binary found ended
	// the test and every later one was dead code. The list implied two probes
	// and exactly one ran -- a scan of assertion counts cannot see that,
	// because the assertion is real and simply never reached for most inputs.
	var probed int
	for _, notMidden := range []string{"go", "git"} {
		p := lookPath(notMidden)
		if p == "" {
			continue
		}
		probed++
		if speaksModuleProtocol(p) {
			t.Errorf("%s was reported as speaking the module protocol", p)
		}
	}
	if probed == 0 {
		t.Skip("no probe binary available")
	}
}

// TestPathWarningIsSilentWhenCorrect proves the warning is not noise. A
// warning printed on every install trains people to ignore warnings, so it
// must say nothing when `midden` on PATH is this binary.
func TestPathWarningIsSilentWhenCorrect(t *testing.T) {
	ok, found := OnPath()
	w := PathWarning()
	switch {
	case found == "" && w == "":
		t.Error("no midden on PATH at all should warn: installed skills would find nothing")
	case ok && w != "":
		t.Errorf("PATH already resolves to this binary, but it warned anyway: %s", w)
	}
}
