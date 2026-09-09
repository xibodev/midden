package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Install- and runtime-phase Resolution.
//
// Projection-phase asks "can this target support this at all" and is answered by
// Project(). These two ask different questions, and collapsing any pair is the
// "resolved is not usable" defect: a binary present at install time is not proof
// it authenticates at runtime.

// ResolveInstall reports whether a target's requirements are satisfiable on THIS
// machine, right now, at install time.
//
// The distinction that earns the three states: a binary being on PATH is
// UNKNOWN, not SATISFIED. `copilot --version` exits 0 on this machine while every
// real request fails to authenticate, so presence proves the file exists and
// nothing about whether it works. Claiming SATISFIED from presence is how a host
// promises a capability that fails on first use.
func ResolveInstall(t Target) []Resolution {
	var out []Resolution

	if t.AssetForm == "skills-dir" {
		out = append(out, resolveSkillsDir(t))
	}
	if t.Subprocess {
		out = append(out, resolveHarness(t))
	}
	return out
}

func resolveSkillsDir(t Target) Resolution {
	r := Resolution{Requirement: "skills-directory", Phase: PhaseInstall}
	dir := skillsDirFor(t.ID)
	if dir == "" {
		r.State = Unknown
		r.Reason = "no home directory is resolvable, so the skills location cannot be computed"
		r.Remedy = "run with a resolvable HOME, or install into an explicit directory"
		return r
	}
	fi, err := os.Stat(dir)
	switch {
	case err == nil && fi.IsDir():
		r.State = Satisfied
	case os.IsNotExist(err):
		// Absent is not a failure: the installer creates it. Reporting
		// UNSATISFIED here would refuse a perfectly installable target.
		r.State = Unknown
		r.Reason = fmt.Sprintf("%s does not exist yet", dir)
		r.Remedy = "`midden install` creates it; no action needed beforehand"
	default:
		r.State = Unsatisfied
		r.Reason = fmt.Sprintf("%s is not usable as a directory: %v", dir, err)
		r.Remedy = "check the path is not a file or a link whose target is missing"
	}
	return r
}

// resolveHarness probes for the AI CLI a target would invoke.
//
// Deliberately reports UNKNOWN on success rather than SATISFIED. Presence is not
// usability: the binary may exist and be signed out, rate-limited, or pointed at
// an account without access. Only a real invocation settles it, and that belongs
// to runtime.
func resolveHarness(t Target) Resolution {
	r := Resolution{Requirement: "agentic-harness", Phase: PhaseInstall}
	name := harnessFor(t.ID)
	if name == "" {
		r.State = Unknown
		r.Reason = "this target does not name a specific harness"
		r.Remedy = "supply an absolute binary path per invocation"
		return r
	}
	path, err := exec.LookPath(name)
	if err != nil {
		r.State = Unsatisfied
		r.Reason = fmt.Sprintf("%s was not found on PATH", name)
		r.Remedy = fmt.Sprintf("install %s, or grant a different harness for the invocation", name)
		return r
	}
	r.State = Unknown
	r.Reason = fmt.Sprintf("%s is present at %s, which proves the file exists and "+
		"nothing about whether it is signed in", name, path)
	r.Remedy = "no action needed; authentication is settled at runtime by a real call"
	return r
}

// ResolveRuntime reports whether a granted requirement is still true NOW.
//
// Install-time success does not carry: a binary can be uninstalled, a grant
// withheld, a path made unreadable between one invocation and the next.
func ResolveRuntime(binaries map[string]string, need string) Resolution {
	r := Resolution{Requirement: need, Phase: PhaseRuntime}
	path, granted := binaries[need]
	if !granted {
		r.State = Unsatisfied
		r.Reason = fmt.Sprintf("the host granted no binary named %q for this invocation", need)
		r.Remedy = "grant it in Request.Binaries, or call a capability that needs no harness"
		return r
	}
	if _, err := os.Stat(path); err != nil {
		r.State = Unsatisfied
		r.Reason = fmt.Sprintf("the granted path %s is not reachable: %v", path, err)
		r.Remedy = "grant an absolute path that exists on this machine"
		return r
	}
	r.State = Unknown
	r.Reason = fmt.Sprintf("%s exists at the granted path; whether the call succeeds "+
		"is proven only by making it", need)
	r.Remedy = "no action needed; a failed call reports its own reason"
	return r
}

func skillsDirFor(id string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch id {
	case "claude-code":
		return filepath.Join(home, ".claude", "skills")
	case "copilot-cli":
		return filepath.Join(home, ".copilot", "skills")
	case "opencode":
		return filepath.Join(home, ".config", "opencode", "skills")
	}
	return ""
}

func harnessFor(id string) string {
	switch id {
	case "claude-code":
		return "claude"
	case "copilot-cli":
		return "copilot"
	case "opencode":
		return "opencode"
	}
	return ""
}
