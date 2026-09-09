package project

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/module"
)

// Support is what a target can do with one Operation.
type Support string

const (
	// Supported: fully expressible here.
	Supported Support = "supported"
	// Degraded: available with a NAMED weakening the caller can read.
	Degraded Support = "degraded"
	// Unsupported: this target cannot express it.
	Unsupported Support = "unsupported"
)

// OperationSupport is one Operation's status on one target.
type OperationSupport struct {
	Operation string  `json:"operation"`
	Support   Support `json:"support"`

	// Reason and Remedy are required unless Supported.
	Reason string `json:"reason,omitempty"`
	Remedy string `json:"remedy,omitempty"`

	// Implementation is the binding chosen for this target. It is part of
	// projection identity: two packages from one product version that bound
	// different implementations are DIFFERENT ARTIFACTS, and that difference is
	// invisible unless identity records it.
	Implementation string `json:"implementation,omitempty"`
}

// Conformance is a projection's report: what it claims and what it dropped.
//
// A projection that claims support without stating what it dropped is the
// silent-correctness failure this whole architecture exists to prevent. The
// report exists so a reader can tell "Midden does this here" from "Midden does
// this somewhere".
type Conformance struct {
	Product   string `json:"product"`
	Version   string `json:"version"`
	Target    string `json:"target"`
	TargetVia string `json:"target_via"`

	Operations []OperationSupport `json:"operations"`
	Assets     []string           `json:"assets"`

	// Identity distinguishes packages that differ by binding rather than by
	// product version.
	Identity string `json:"identity"`
}

// Project builds a conformance report for one target.
//
// It reports rather than decides: an Operation is never silently dropped, and a
// weakening is always named. Every non-Supported entry carries a remedy, because
// "unsupported" without a remedy tells a person nothing they can act on.
func Project(t Target) Conformance {
	c := Conformance{
		Product:   core.ModuleID,
		Version:   core.Version,
		Target:    t.ID,
		TargetVia: string(t.Kind) + " " + t.Version,
	}

	for _, op := range module.Operations {
		s := OperationSupport{Operation: op.ID, Support: Supported}

		switch {
		case op.MayCharge && !t.Subprocess:
			// Midden holds no API key: a charging Operation needs an AI CLI it
			// can invoke. Without subprocess authority the work cannot happen
			// at all, so this is unsupported rather than degraded.
			s.Support = Unsupported
			s.Reason = "requires an AI CLI through subprocess; this target grants none"
			s.Remedy = "use a target that grants subprocess authority, or invoke Midden directly"
		case op.MayCharge:
			s.Implementation = "harness-cli"
		default:
			s.Implementation = "deterministic-core"
		}

		// Chargeability that varies by argument cannot be expressed as a
		// per-Operation boolean on a v1 wire. Declaring the wider effect
		// over-gates the free kinds; that is the safe direction and it is
		// DEGRADED, not supported, because the caller pays a real cost in
		// unnecessary approvals.
		if op.ChargeDependsOn != "" && t.AssetForm == "module-descriptor" {
			s.Support = Degraded
			s.Reason = fmt.Sprintf(
				"chargeability varies by %q and module/v1 carries no per-argument form, "+
					"so the whole Operation declares may-charge", op.ChargeDependsOn)
			s.Remedy = "xibodev.module/v2 §3a expresses conditional chargeability; " +
				"this resolves when the host speaks v2"
		}
		c.Operations = append(c.Operations, s)
	}

	sort.Slice(c.Operations, func(i, j int) bool {
		return c.Operations[i].Operation < c.Operations[j].Operation
	})
	c.Assets = assetsFor(t)
	c.Identity = identity(c)
	return c
}

// assetsFor selects what this target actually receives.
//
// Selection, not copy-everything: a target that cannot read a skills directory
// gains nothing from being handed one, and shipping it anyway is the
// "portable means ship everything everywhere" failure.
func assetsFor(t Target) []string {
	switch t.AssetForm {
	case "skills-dir":
		return []string{"skills/session-recovery", "skills/evidence-selection",
			"skills/content-seed", "agents/midden-recovery.md"}
	case "module-descriptor":
		return []string{"descriptor", "skills/session-recovery",
			"skills/evidence-selection", "skills/content-seed"}
	case "embedded":
		// Standalone owns its own shell, so assets are compiled in rather than
		// placed where another harness scans.
		return []string{"embedded-ui", "embedded-skills"}
	}
	return []string{}
}

// identity records what would make two packages differ.
//
// Product version alone is not enough: regenerating the same version with a
// different implementation binding produces a different artifact, and a reader
// who cannot see that has no way to explain a behaviour change.
func identity(c Conformance) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s@%s/%s@%s", c.Product, c.Version, c.Target, c.TargetVia)
	for _, op := range c.Operations {
		if op.Implementation != "" {
			fmt.Fprintf(&b, "+%s=%s", op.Operation, op.Implementation)
		}
	}
	return b.String()
}
