package install

import (
	"encoding/json"
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
	{"midden-editorial-production", "skills/editorial-production/SKILL.md"},
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
		content, ok := module.CLISkillContent(b.path)
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
	self, err := SelfPath()
	if err != nil {
		return nil, err
	}
	return RegisterBinaryWithHost(t, self)
}

// RegisterBinaryWithHost packages the binary and registers it with the module host.
func RegisterBinaryWithHost(t Target, binary string) (*Result, error) {
	if t.Kind != KindModule {
		return nil, fmt.Errorf("%s is not a module host", t.Name)
	}
	if t.BinaryPath == "" {
		return nil, fmt.Errorf("%s was not found on PATH", t.Name)
	}
	if strings.TrimSpace(binary) == "" {
		return nil, fmt.Errorf("module binary path is empty")
	}

	stageDir, err := os.MkdirTemp("", "midden-package-*")
	if err != nil {
		return nil, fmt.Errorf("create package stage dir: %w", err)
	}
	defer os.RemoveAll(stageDir)

	packagedBinary, err := PackageModule(binary, stageDir)
	if err != nil {
		return nil, fmt.Errorf("package module: %w", err)
	}

	cmd := exec.Command(t.BinaryPath, "modules-add", packagedBinary)
	out, err := cmd.CombinedOutput()

	res := &Result{
		Target:  t,
		Command: fmt.Sprintf("%s modules-add %s", t.BinaryPath, packagedBinary),
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
		want, ok := module.CLISkillContent(b.path)
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

	// When a DIFFERENT midden is on PATH, say whether it can actually serve
	// the skills being installed. "Not this binary" is true and unhelpful; a
	// build too old to know the `module` verb answers every documented command
	// with a usage error, which reads to a user as a broken skill rather than
	// a stale binary.
	if found != "" && !ok {
		if !speaksModuleProtocol(found) {
			return fmt.Sprintf("`midden` on PATH resolves to %s, which does NOT speak the module "+
				"protocol — it does not recognise the `module` command. Every command in the installed "+
				"skills will fail against it. Put this binary on PATH, or replace that one.", found)
		}
		return fmt.Sprintf("`midden` on PATH resolves to %s, which is not this binary. "+
			"It does speak the module protocol, but installed skills will invoke that one rather "+
			"than this build.", found)
	}

	switch {
	case found == "":
		self, err := SelfPath()
		if err != nil {
			return "`midden` is not on PATH. The installed skills instruct an agent to run " +
				"`midden`, and every one of those commands will fail until it resolves."
		}
		return fmt.Sprintf("`midden` is not on PATH. The installed skills instruct an agent to run "+
			"`midden`, and every one of those commands will fail until it resolves. This binary is at %s", self)
	}
	return ""
}

// speaksModuleProtocol probes a binary rather than trusting its name.
//
// Two builds of Midden can sit on the same machine, and only one may know the
// module verb. Asking it is cheap and definitive; inferring from a path or a
// version string is neither.
func speaksModuleProtocol(binary string) bool {
	cmd := exec.Command(binary, "module", "describe", "--json")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var env struct {
		Protocol string `json:"protocol"`
		OK       bool   `json:"ok"`
	}
	if json.Unmarshal(out, &env) != nil {
		return false
	}
	return env.OK && env.Protocol == module.ProtocolID
}

// Timestamp is used by callers that record when an install happened.
func Timestamp() string { return time.Now().UTC().Format(time.RFC3339) }
