package project

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/module"
)

// TestWrongContractVersionIsRefused is the falsification test the operator
// requires: a wrong contract_version must be refused BEFORE any v2 guarantee is
// relied upon.
//
// Everything else in a v2 conformance suite sits behind this gate. If the gate
// leaks, every guarantee behind it is asserted against a contract the other side
// never agreed to -- which is the silent-correctness failure in its purest form,
// because both parties are individually self-consistent.
func TestWrongContractVersionIsRefused(t *testing.T) {
	for _, wrong := range []string{
		"xibodev.module/v1",
		"xibodev.module/v3",
		"xibodev.module/v2 ", // trailing space: near-miss, must not pass
		"XIBODEV.MODULE/V2",  // case: exact match means exact
		"something-else",
	} {
		ok, refusal := CheckContract(wrong)
		if ok {
			t.Errorf("contract %q was ACCEPTED; exact pinning means exact", wrong)
			continue
		}
		if refusal.Reason == "" || refusal.Remedy == "" {
			t.Errorf("refusal of %q carries reason=%q remedy=%q; both required",
				wrong, refusal.Reason, refusal.Remedy)
		}
		if !strings.Contains(refusal.Reason, wrong) {
			t.Errorf("refusal of %q does not name what the host declared: %q",
				wrong, refusal.Reason)
		}
	}

	if ok, _ := CheckContract(ContractV2); !ok {
		t.Fatal("the exact contract was refused; the gate rejects everything")
	}
}

// TestAbsentContractIsRefusedDistinctlyFromWrong guards a remedy that would
// otherwise be accurate and useless.
//
// A v1 module declares no behavioural contract. Telling its author they declared
// the WRONG version is true and actionable by nobody, so absent gets its own
// remedy stating that v1 stays valid.
func TestAbsentContractIsRefusedDistinctlyFromWrong(t *testing.T) {
	_, absent := CheckContract("")
	_, wrong := CheckContract("xibodev.module/v9")

	if absent == nil || wrong == nil {
		t.Fatal("both cases must refuse")
	}
	if absent.Remedy == wrong.Remedy {
		t.Error("absent and wrong share a remedy; a v1 module's author would be " +
			"told to fix a version they correctly never declared")
	}
	if !strings.Contains(absent.Remedy, "v1") {
		t.Errorf("the absent remedy does not say v1 stays valid: %q", absent.Remedy)
	}
}

// TestContractVersionIsNotACollection pins the ruling in the TYPE.
//
// Forbidden explicitly: a single-element list that merely looks negotiable. v1
// shipped exactly that -- ProtocolVersions is a []string whose extra entries are
// inert -- and both sibling lanes read negotiation into it that was never there.
//
// This test fails if anyone widens ContractV2 to a collection, which is the
// change that would reintroduce the shape without reintroducing the behaviour.
func TestContractVersionIsNotACollection(t *testing.T) {
	if k := reflect.TypeOf(ContractV2).Kind(); k != reflect.String {
		t.Errorf("ContractV2 is %s, not a string; a collection invites "+
			"declare-several-and-pick-one, which is the forbidden shape", k)
	}
}

// TestProjectionIdentityRecordsThePinnedContract is the other half of the
// ruling: projection identity and conformance evidence must RECORD the pinned
// version, not merely check it.
//
// Evidence that does not say which contract it was gathered under cannot be
// re-checked later, and a conformance report is worth exactly what it can prove.
func TestProjectionIdentityRecordsThePinnedContract(t *testing.T) {
	fs, ok := TargetByID("facet-studio")
	if !ok {
		t.Fatal("facet-studio target missing")
	}
	c := Project(fs)
	if !strings.Contains(c.Identity, c.TargetVia) {
		t.Errorf("identity %q omits the contract it was verified against (%q)",
			c.Identity, c.TargetVia)
	}
	if c.TargetVia == "" {
		t.Error("projection records no contract identity at all")
	}
}

// TestV1WireIsUnchanged guards the boundary the ruling does NOT touch.
//
// v1's ProtocolVersions stays a list because it is frozen wire. Preserving a
// published name at the boundary is not endorsing its shape, and retrofitting
// the pin into v1 is forbidden by ruling 5.
func TestV1WireIsUnchanged(t *testing.T) {
	d := module.Describe()
	if len(d.ProtocolVersions) != 1 || d.ProtocolVersions[0] != module.ProtocolID {
		t.Errorf("v1 descriptor protocol_versions changed: %v", d.ProtocolVersions)
	}
}
