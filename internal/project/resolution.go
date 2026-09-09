// Package project implements target profiles, projection and conformance.
//
// A projection is one product realized for one target. The product decides what
// Midden IS; a target decides how much of it that harness can express. Neither
// is allowed to decide the other: an Operation's semantics do not change per
// target, and a target's limits are reported rather than hidden.
package project

import "fmt"

// State is a Resolution outcome. Three states, not a boolean.
//
// A boolean calls all three of these SATISFIED, and the difference between them
// is a local refusal with a remedy versus a confusing failure later. Measured
// examples from this machine: `copilot --version` exits 0 where every real
// request fails to authenticate, and a skills directory can be a link whose
// target is missing.
type State string

const (
	// Satisfied: verified working, not merely present.
	Satisfied State = "satisfied"
	// Unsatisfied: verified absent. Refuse locally with a reason and remedy.
	Unsatisfied State = "unsatisfied"
	// Unknown: present but unproven. Never claim satisfaction from this.
	Unknown State = "unknown"
)

// Phase distinguishes WHEN a requirement was evaluated.
//
// Collapsing these is the "resolved is not usable" defect: a binary resolving at
// projection time is not proof it authenticates at runtime. Each phase answers a
// different question and none substitutes for another.
type Phase string

const (
	// PhaseProjection: can this target support this at all?
	PhaseProjection Phase = "projection"
	// PhaseInstall: is it satisfiable on this machine?
	PhaseInstall Phase = "install"
	// PhaseRuntime: is it still true right now?
	PhaseRuntime Phase = "runtime"
)

// Resolution is one requirement evaluated in one phase.
type Resolution struct {
	Requirement string `json:"requirement"`
	Phase       Phase  `json:"phase"`
	State       State  `json:"state"`

	// Reason says what was observed. Remedy says what a person can do.
	//
	// Both are required for anything other than Satisfied: "not supported" and
	// "binary missing" demand completely different actions, and a state without
	// a remedy is a dead end rather than a report.
	Reason string `json:"reason,omitempty"`
	Remedy string `json:"remedy,omitempty"`
}

// Validate enforces that a non-satisfied resolution explains itself.
func (r Resolution) Validate() error {
	if r.State == Satisfied {
		return nil
	}
	if r.Reason == "" || r.Remedy == "" {
		return fmt.Errorf("resolution %q is %s but carries reason=%q remedy=%q; "+
			"every unsupported or degraded result needs both",
			r.Requirement, r.State, r.Reason, r.Remedy)
	}
	return nil
}
