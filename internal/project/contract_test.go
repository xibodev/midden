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
		out, refusal := CheckContract(wrong)
		if out.Served() {
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

	if out, _ := CheckContract(ContractV2); !out.MayRelyOnV2() {
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

// TestEveryProjectionRecordsItsContract audits the INPUT CLASS.
//
// The single-target test below checks facet-studio. Mutation-verified: dropping
// the contract identity for every target EXCEPT facet-studio passes the whole
// suite, so any other target's evidence could silently lose the thing that says
// which contract it was gathered under.
//
// Recording the contract is a property of EVERY projection, not of one target.
// Evidence that cannot say which contract it was gathered under cannot be
// re-checked later, which is the whole reason the field exists.
func TestEveryProjectionRecordsItsContract(t *testing.T) {
	if len(Targets) == 0 {
		t.Fatal("no targets; the sweep asserts nothing and would pass however " +
			"identity is built")
	}
	for _, tg := range Targets {
		c := Project(tg)
		if c.TargetVia == "" {
			t.Errorf("%s records no contract identity", tg.ID)
			continue
		}
		if !strings.Contains(c.Identity, c.TargetVia) {
			t.Errorf("%s identity %q omits the contract it was verified against "+
				"(%q); evidence that cannot name its contract cannot be "+
				"re-checked", tg.ID, c.Identity, c.TargetVia)
		}
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

// TestAbsentContractIsSERVEDNotRefused is the defect a sibling lane found in
// their gate and I then found in mine: a boolean collapses "serve under v1" and
// "refuse", which are opposite instructions.
//
// Every Midden build today declares no contract_version. Under the collapsed
// form a host wiring this gate would REFUSE the shipping module -- while
// implementing a contract whose compatibility clause says v1 modules keep
// working.
func TestAbsentContractIsSERVEDNotRefused(t *testing.T) {
	absent, _ := CheckContract("")
	wrong, _ := CheckContract("xibodev.module/v9")

	if !absent.Served() {
		t.Error("a module declaring no contract_version was REFUSED; every " +
			"Midden build today is in that state and v1 must keep working")
	}
	if absent.MayRelyOnV2() {
		t.Error("an absent contract was treated as v2; a guarantee would be " +
			"asserted against a contract nobody declared")
	}
	if wrong.Served() {
		t.Error("a wrong contract was served; exact pinning means exact")
	}
	if absent.Served() == wrong.Served() {
		t.Error("absent and wrong are indistinguishable to a caller")
	}
}

// TestRefusalErrorIsDistinctAndComplete audits a function no passing run
// exercises.
//
// Error() is reached only when something has already gone wrong, so a defect in
// it stays invisible until the exact moment a human needs it to read correctly.
// Coverage measured it at 0% while the suite was green -- an unaudited green
// suite and a complete one produce the same observable, and the difference here
// was one uncovered function whose whole job is explaining a refusal.
//
// Distinctness is asserted as well as content: if an absent and a wrong contract
// rendered identically, a served v1 module and a hard refusal would be
// indistinguishable in the one place a person reads them. That is the exact
// conflation the three-valued Outcome exists to prevent, resurfacing one layer
// up in its own diagnostic.
func TestRefusalErrorIsDistinctAndComplete(t *testing.T) {
	_, absent := CheckContract("")
	_, wrong := CheckContract("xibodev.module/v9")

	for name, r := range map[string]*ContractRefusal{"absent": absent, "wrong": wrong} {
		msg := r.Error()
		if !strings.Contains(msg, r.Reason) {
			t.Errorf("%s: Error() drops the reason", name)
		}
		if !strings.Contains(msg, r.Remedy) {
			t.Errorf("%s: Error() drops the remedy; a refusal without a remedy "+
				"is a dead end rather than a report", name)
		}
	}

	if absent.Error() == wrong.Error() {
		t.Error("an absent and a wrong contract render identically; a served v1 " +
			"module and a hard refusal would be indistinguishable in the one " +
			"place a person reads them")
	}
}
