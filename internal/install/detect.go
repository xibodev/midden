// Package install detects agentic CLI hosts on this machine and wires Midden
// into the ones the user chooses.
//
// Midden is headless: it is used through an agentic CLI that already exists, or
// as a module of a host that does. This package is install-time convenience —
// it detects and offers. It is deliberately NOT a package manager: it does not
// resolve versions, does not update on a schedule, and does not manage anything
// other than Midden's own presence. A module is a guest asking to be let in,
// not a host managing guests.
package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Target is a place Midden can be installed to.
type Target struct {
	// ID is stable and machine-readable.
	ID string
	// Name is what a person sees.
	Name string

	// Kind distinguishes how wiring happens, because the two are not the same
	// operation and must not be conflated in the UI.
	Kind Kind

	// BinaryPath is the detected executable, empty when not found.
	BinaryPath string

	// SkillsDir is where a skills bundle belongs, for KindSkills targets.
	SkillsDir string

	// Detected reports whether this target is present on the machine.
	Detected bool

	// Note carries anything a user should know before choosing this target —
	// an unverified convention, a broken link, a caveat. Empty when clean.
	Note string
}

// Kind is how Midden gets wired into a target.
type Kind int

const (
	// KindSkills installs a skills bundle into a directory the CLI scans.
	// Midden's binary must be on PATH for the skills to be actionable.
	KindSkills Kind = iota

	// KindModule registers Midden with a module host by running the HOST's
	// own command. Midden never writes into host state: the host validates,
	// chooses the install path, and records the module itself.
	KindModule
)

func (k Kind) String() string {
	switch k {
	case KindSkills:
		return "skills"
	case KindModule:
		return "module"
	}
	return "unknown"
}

// homeDir returns the user's home directory, or "" when it cannot be resolved.
//
// It returns an explicit empty string rather than a relative fallback: a
// relative path here would silently install into the working directory, which
// is the silent-success failure this project has hit repeatedly.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(h) == "" {
		return ""
	}
	return h
}

// lookPath reports the absolute path of an executable, or "" if absent.
func lookPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// dirExists reports whether path is a directory that can actually be read.
//
// It follows symlinks deliberately: a skills directory is often a link into a
// shared store, and a link pointing at a deleted target must read as absent
// rather than present-but-broken.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// brokenLink reports whether path exists as a link whose target does not.
//
// This is worth surfacing rather than ignoring: on a real machine a skills
// directory was a symlink into a repository that had been deleted, which looks
// identical to a working install until something tries to read it.
func brokenLink(path string) bool {
	if _, err := os.Lstat(path); err != nil {
		return false
	}
	_, err := os.Stat(path)
	return err != nil
}

// Detect finds every install target present on this machine.
//
// Detection is by observation — an executable on PATH, a directory that exists.
// Nothing is assumed from the operating system or from a target being popular.
func Detect() []Target {
	home := homeDir()

	targets := []Target{
		{
			ID: "claude-code", Name: "Claude Code", Kind: KindSkills,
			BinaryPath: lookPath("claude"),
			SkillsDir:  join(home, ".claude", "skills"),
		},
		{
			ID: "copilot-cli", Name: "GitHub Copilot CLI", Kind: KindSkills,
			BinaryPath: lookPath("copilot"),
			SkillsDir:  join(home, ".copilot", "skills"),
		},
		{
			ID: "opencode", Name: "OpenCode", Kind: KindSkills,
			BinaryPath: lookPath("opencode"),
			SkillsDir:  join(home, ".config", "opencode", "skills"),
		},
		{
			ID: "facet-studio", Name: "Facet Studio", Kind: KindModule,
			BinaryPath: lookPath("facet-studio"),
		},
	}

	for i := range targets {
		t := &targets[i]
		t.Detected = t.BinaryPath != ""

		if t.Kind != KindSkills {
			continue
		}
		switch {
		case home == "":
			t.Note = "home directory could not be resolved; supply an install path explicitly"
		case brokenLink(t.SkillsDir):
			t.Note = "skills directory is a link whose target is missing; installing would fail"
		case !dirExists(t.SkillsDir):
			t.Note = "skills directory does not exist yet and would be created"
		}
	}

	sort.SliceStable(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	return targets
}

// DetectedTargets returns only the targets actually present.
func DetectedTargets() []Target {
	var out []Target
	for _, t := range Detect() {
		if t.Detected {
			out = append(out, t)
		}
	}
	return out
}

func join(home string, parts ...string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

// SelfPath returns the absolute path of the running Midden binary.
//
// A host registering Midden needs the path of the binary it should invoke, and
// deriving it from the process is the only answer that stays correct when the
// binary has been renamed or moved.
func SelfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine the running binary's path: %w", err)
	}
	// Resolve symlinks so a host records the real binary rather than a link
	// that may later point elsewhere.
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Abs(p)
}

// OnPath reports whether `midden` resolves on PATH to the running binary.
//
// This matters because an installed skill tells an agent to run `midden`. If
// the binary is not on PATH, the skills install cleanly and then every command
// in them fails — which reads to a user as "the skill is broken" rather than
// "the binary is not installed".
func OnPath() (bool, string) {
	found := lookPath("midden")
	if found == "" {
		return false, ""
	}
	self, err := SelfPath()
	if err != nil {
		return true, found
	}
	if resolved, err := filepath.EvalSymlinks(found); err == nil {
		found = resolved
	}
	return strings.EqualFold(filepath.Clean(found), filepath.Clean(self)), found
}

// BinaryName is the executable name a skill invokes.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "midden.exe"
	}
	return "midden"
}
