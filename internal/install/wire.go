package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/module"
)

// Result reports what an install actually did, so a caller can show it rather
// than claim success generically.
type Result struct {
	Target  Target
	Written []string
	Skipped []string
	Command string
	Output  string
}

// bundle is the skill set Midden installs into a CLI that scans a skills
// directory. Paths are the ones the descriptor already advertises, so the
// installed content and the host-composed content cannot diverge.
var bundle = []struct {
	dir  string
	path string
}{
	{"midden-session-recovery", "skills/session-recovery/SKILL.md"},
	{"midden-evidence-selection", "skills/evidence-selection/SKILL.md"},
	{"midden-content-seed", "skills/content-seed/SKILL.md"},
}

// InstallSkills writes Midden's skills bundle into a target's skills directory.
//
// Content comes from the embedded copies rather than from disk, so an install
// works from the binary alone and cannot pick up a stale or edited file sitting
// beside it.
//
// force controls whether an existing Midden skill is overwritten. Anything the
// installer did not write is never touched: refusing to clobber a file it does
// not own is the difference between an installer and a hazard.
func InstallSkills(t Target, force bool) (*Result, error) {
	if t.Kind != KindSkills {
		return nil, fmt.Errorf("%s is not a skills target", t.Name)
	}
	if strings.TrimSpace(t.SkillsDir) == "" {
		return nil, fmt.Errorf("no skills directory known for %s", t.Name)
	}
	if !filepath.IsAbs(t.SkillsDir) {
		return nil, fmt.Errorf("skills directory %q is not absolute", t.SkillsDir)
	}
	if brokenLink(t.SkillsDir) {
		return nil, fmt.Errorf("skills directory %q is a link whose target is missing", t.SkillsDir)
	}

	res := &Result{Target: t}

	for _, b := range bundle {
		content, ok := module.OverlayContent(b.path)
		if !ok {
			return nil, fmt.Errorf("embedded skill %s is missing from this binary", b.path)
		}

		dir := filepath.Join(t.SkillsDir, b.dir)
		dest := filepath.Join(dir, "SKILL.md")

		if _, err := os.Stat(dest); err == nil && !force {
			res.Skipped = append(res.Skipped, dest)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", dest, err)
		}
		res.Written = append(res.Written, dest)
	}

	return res, nil
}

// UninstallSkills removes only the skill directories this installer creates.
//
// It removes Midden's own directories by exact name and nothing else. An
// uninstaller that removed a whole skills directory, or anything matching a
// pattern, could delete a user's unrelated work.
func UninstallSkills(t Target) (*Result, error) {
	if t.Kind != KindSkills {
		return nil, fmt.Errorf("%s is not a skills target", t.Name)
	}
	res := &Result{Target: t}
	for _, b := range bundle {
		dir := filepath.Join(t.SkillsDir, b.dir)
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			res.Skipped = append(res.Skipped, dir)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("remove %s: %w", dir, err)
		}
		res.Written = append(res.Written, dir)
	}
	return res, nil
}

// RegisterWithHost asks a module host to register this binary, by running the
// HOST's own command.
//
// Midden never writes into host state. The host validates the descriptor before
// copying anything, derives the module ID from that descriptor, chooses the
// install location, and records the result. Midden does not need to know the
// host's layout and deliberately does not look for it — a module writing into a
// host's state directory is the confinement violation this design exists to
// prevent.
//
// The command is idempotent on the host side, so re-running it after a rebuild
// is how an upgrade happens.
func RegisterWithHost(t Target) (*Result, error) {
	if t.Kind != KindModule {
		return nil, fmt.Errorf("%s is not a module host", t.Name)
	}
	if t.BinaryPath == "" {
		return nil, fmt.Errorf("%s was not found on PATH", t.Name)
	}

	self, err := SelfPath()
	if err != nil {
		return nil, err
	}

	// The subcommand is HYPHENATED. It was written as "modules add" from a
	// prose description of the host's interface and never run against the
	// real binary, which exposes `modules-add` and rejects `modules add`
	// outright. A command built from a description rather than verified
	// against the thing it invokes is a wrong answer that looks right.
	cmd := exec.Command(t.BinaryPath, "modules-add", self)
	// The host owns its own environment; pass ours through rather than
	// stripping it, because this is a user-initiated command rather than a
	// sandboxed module invocation.
	out, err := cmd.CombinedOutput()

	res := &Result{
		Target:  t,
		Command: fmt.Sprintf("%s modules-add %s", t.BinaryPath, self),
		Output:  strings.TrimSpace(string(out)),
	}
	if err != nil {
		return res, fmt.Errorf("%s refused the registration: %w", t.Name, err)
	}
	return res, nil
}

// VerifySkillsInstall re-reads what was installed and confirms it matches what
// this binary would produce.
//
// Verifying by reading back rather than by trusting the write is the difference
// between "the write returned no error" and "the right bytes are on disk".
func VerifySkillsInstall(t Target) error {
	for _, b := range bundle {
		dest := filepath.Join(t.SkillsDir, b.dir, "SKILL.md")
		onDisk, err := os.ReadFile(dest)
		if err != nil {
			return fmt.Errorf("%s is not installed: %w", b.dir, err)
		}
		want, ok := module.OverlayContent(b.path)
		if !ok {
			return fmt.Errorf("embedded skill %s missing from this binary", b.path)
		}
		if string(onDisk) != string(want) {
			return fmt.Errorf("%s on disk differs from this binary's copy; re-run with --force to update", dest)
		}
	}
	return nil
}

// PathWarning returns advice when the midden binary is not reachable as
// `midden` on PATH.
//
// An installed skill instructs an agent to run `midden module invoke ...`. If
// that name does not resolve, every command in every installed skill fails, and
// it reads to a user as a broken skill rather than a missing binary. Saying so
// at install time is far cheaper than letting them discover it.
func PathWarning() string {
	ok, found := OnPath()
	switch {
	case found == "":
		self, err := SelfPath()
		if err != nil {
			return "`midden` is not on PATH. The installed skills instruct an agent to run " +
				"`midden`, and every one of those commands will fail until it resolves."
		}
		return fmt.Sprintf("`midden` is not on PATH. The installed skills instruct an agent to run "+
			"`midden`, and every one of those commands will fail until it resolves. This binary is at %s", self)
	case !ok:
		return fmt.Sprintf("`midden` on PATH resolves to %s, which is not this binary. "+
			"Installed skills will invoke that one instead.", found)
	}
	return ""
}

// Timestamp is used by callers that record when an install happened.
func Timestamp() string { return time.Now().UTC().Format(time.RFC3339) }
