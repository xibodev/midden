package project

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/module"
)

// TestEveryTargetIsEvaluatedIndependently is the operator's ruling as a test.
//
// One target's support state must never be inferred from another's. Midden
// standalone does not consume the detached-module layer, so module/v1's limits
// say nothing about it -- and the projections must differ in a way that proves
// they were computed per target rather than copied.
func TestEveryTargetIsEvaluatedIndependently(t *testing.T) {
	seen := map[string]string{}
	for _, tg := range Targets {
		c := Project(tg)
		if c.Target != tg.ID {
			t.Errorf("projection for %s reports target %s", tg.ID, c.Target)
		}
		if c.Identity == "" {
			t.Errorf("%s produced no projection identity", tg.ID)
		}
		if prev, dup := seen[c.Identity]; dup {
			t.Errorf("%s and %s share identity %q; a projection identity that "+
				"cannot distinguish targets cannot explain a behaviour change",
				prev, tg.ID, c.Identity)
		}
		seen[c.Identity] = tg.ID
	}
}

// TestEveryNonSupportedResultCarriesReasonAndRemedy pins the conformance rule.
//
// "Unsupported" without a remedy is a dead end rather than a report: the reader
// learns that something failed and nothing about what to do. Degraded is worse
// without one, because the work still happens and the weakening is invisible.
func TestEveryNonSupportedResultCarriesReasonAndRemedy(t *testing.T) {
	for _, tg := range Targets {
		for _, op := range Project(tg).Operations {
			if op.Support == Supported {
				continue
			}
			if op.Reason == "" || op.Remedy == "" {
				t.Errorf("%s/%s is %s but reason=%q remedy=%q",
					tg.ID, op.Operation, op.Support, op.Reason, op.Remedy)
			}
		}
	}
}

// TestProjectionDoesNotInventOperations guards the semantic inventory.
//
// A target exposing more verbs must not grow the canonical inventory: CLI verbs,
// capabilities and output kinds PROJECT onto Operations, they do not become
// them. Four Operations are product truth until the semantic model is proven
// wrong, not until a harness offers a fifth button.
func TestProjectionDoesNotInventOperations(t *testing.T) {
	canonical := map[string]bool{}
	for _, op := range module.Operations {
		canonical[op.ID] = true
	}
	for _, tg := range Targets {
		c := Project(tg)
		if len(c.Operations) != len(module.Operations) {
			t.Errorf("%s projects %d operations, inventory has %d",
				tg.ID, len(c.Operations), len(module.Operations))
		}
		for _, op := range c.Operations {
			if !canonical[op.Operation] {
				t.Errorf("%s invented operation %q", tg.ID, op.Operation)
			}
		}
	}
}

// TestEveryTargetReceivesAssets audits the INPUT CLASS.
//
// The test below compares standalone against claude-code. Mutation-verified:
// returning no assets for copilot-cli and opencode passes the whole suite,
// because neither is visited. A target that silently receives nothing installs
// nothing, and the projection would still report itself supported.
func TestEveryTargetReceivesAssets(t *testing.T) {
	if len(Targets) == 0 {
		t.Fatal("no targets; the sweep asserts nothing")
	}
	for _, tg := range Targets {
		if got := Project(tg).Assets; len(got) == 0 {
			t.Errorf("%s (asset form %q) received no assets; a target that is "+
				"handed nothing installs nothing while still reporting supported",
				tg.ID, tg.AssetForm)
		}
	}
}

// TestAssetsAreSelectedNotCopiedEverywhere proves per-target selection is real.
//
// Handing every target an identical bundle is the "portable means ship
// everything everywhere" failure. Standalone owns its own shell and must not be
// handed a skills directory built for a harness that scans one.
func TestAssetsAreSelectedNotCopiedEverywhere(t *testing.T) {
	standalone, _ := TargetByID("standalone")
	claude, _ := TargetByID("claude-code")

	sa := strings.Join(Project(standalone).Assets, ",")
	cl := strings.Join(Project(claude).Assets, ",")

	// Difference alone is not proof: an EMPTY list also differs from a
	// populated one, so a comparison-only assertion passes when selection is
	// disabled entirely. Mutation-checked -- removing the standalone case left
	// this test green until it asserted content.
	if len(Project(standalone).Assets) == 0 {
		t.Fatal("standalone received no assets; selection returned nothing " +
			"rather than selecting, and a difference test cannot see that")
	}
	if len(Project(claude).Assets) == 0 {
		t.Fatal("skills target received no assets")
	}
	if sa == cl {
		t.Error("standalone and a skills target received identical assets; " +
			"selection is not happening")
	}
	if strings.Contains(sa, "agents/") {
		t.Error("standalone was handed an agent file for a harness that scans one")
	}
}

// TestDegradedIsReportedForEveryAffectedTarget audits the INPUT CLASS rather
// than one example.
//
// TestDegradedIsReportedNotHidden below checks facet-studio specifically. That
// assertion passes when the degraded rule is narrowed to `t.ID ==
// "facet-studio"` -- mutation-verified -- so a second module-descriptor target
// would silently lose its weakening report and the suite would stay green.
//
// The rule is a property of the ASSET FORM, not of one target id. This sweeps
// every target that carries the form, so adding one cannot bypass the check.
func TestDegradedIsReportedForEveryAffectedTarget(t *testing.T) {
	var checked int
	for _, tg := range Targets {
		if tg.AssetForm != "module-descriptor" {
			continue
		}
		checked++
		for _, op := range Project(tg).Operations {
			if op.Operation != "produce_content" {
				continue
			}
			if op.Support != Degraded {
				t.Errorf("%s projects produce_content as %s; conditional "+
					"chargeability is unexpressible on a module descriptor and "+
					"must report degraded on EVERY such target, not just one",
					tg.ID, op.Support)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no module-descriptor target was visited; the sweep asserts " +
			"nothing and would pass however the rule is written")
	}
}

// TestDegradedIsReportedNotHidden pins the case the v2 RFC exists to fix.
//
// produce_content's chargeability varies by argument. module/v1 has no
// per-argument form, so the projection must declare the wider effect AND say
// that it did -- an over-gate the caller pays for in unnecessary approvals is a
// real cost, not a free safety margin.
func TestDegradedIsReportedNotHidden(t *testing.T) {
	fs, _ := TargetByID("facet-studio")
	var found bool
	for _, op := range Project(fs).Operations {
		if op.Operation == "produce_content" {
			found = true
			if op.Support != Degraded {
				t.Errorf("produce_content on facet-studio is %s; conditional "+
					"chargeability cannot be expressed on module/v1 and must "+
					"report as degraded", op.Support)
			}
			if !strings.Contains(op.Remedy, "v2") {
				t.Errorf("degraded remedy does not point at the contract that "+
					"resolves it: %q", op.Remedy)
			}
		}
	}
	if !found {
		t.Fatal("produce_content missing from the facet-studio projection")
	}
}

// TestResolutionRequiresExplanation guards the Resolution contract directly.
func TestResolutionRequiresExplanation(t *testing.T) {
	if err := (Resolution{Requirement: "x", State: Satisfied}).Validate(); err != nil {
		t.Errorf("a satisfied resolution needs no remedy: %v", err)
	}
	for _, s := range []State{Unsatisfied, Unknown} {
		if err := (Resolution{Requirement: "x", State: s}).Validate(); err == nil {
			t.Errorf("%s without reason/remedy was accepted", s)
		}
	}
}
