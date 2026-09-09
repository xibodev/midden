package project

import "fmt"

// Behavioural contract pinning.
//
// Operator ruling: PINNING REQUIRED, NEGOTIATION DEFERRED. A module and a host
// each declare EXACTLY ONE behavioural contract version. Exact match proceeds;
// mismatch refuses deterministically with a reason and a remedy.
//
// Forbidden: ranges, highest-common selection, downgrade, fallback, and a
// single-element list that merely LOOKS negotiable.
//
// That last one is why ContractID is a string rather than a slice. A []string
// invites "declare several and pick one", which is exactly the shape v1 shipped
// -- ProtocolVersions is a list whose extra entries are inert, and both sibling
// lanes were misled by it into believing negotiation existed. The type enforces
// the ruling so no one has to remember it.
//
// v1's ProtocolVersions field stays exactly as it is: it is frozen wire, and
// preserving a published name at the boundary is not the same as endorsing its
// shape. This pin belongs to the v2 projection.

// ContractV2 is the behavioural contract this projection speaks. One value.
const ContractV2 = "xibodev.module/v2"

// ContractRefusal explains a rejected contract pairing.
//
// Reason and Remedy are separate because they answer different questions, and
// the ABSENT case is distinguished from the WRONG case on purpose: telling a v1
// module's author that they declared the wrong version is accurate and useless.
type ContractRefusal struct {
	Reason string
	Remedy string
}

func (r ContractRefusal) Error() string { return r.Reason + " " + r.Remedy }

// Outcome is what a contract pairing permits. THREE states, not a boolean.
//
// A boolean collapses "serve this exchange under v1" and "refuse this exchange"
// into one value, and they are opposite instructions: the first must keep
// working forever, the second must never proceed. Found by checking this file
// against a sibling lane's identical defect -- both of us had written a gate for
// the two-states-one-value failure that contained it.
type Outcome int

const (
	// OutcomeRefuse: the host declared a contract this module does not speak.
	OutcomeRefuse Outcome = iota
	// OutcomeV1: no behavioural contract declared. A v1 exchange, and valid.
	OutcomeV1
	// OutcomeV2: both sides pinned the same behavioural contract.
	OutcomeV2
)

// MayRelyOnV2 is the ONLY question a conformance suite may ask before checking
// a v2 guarantee. Deliberately not named OK: "ok" invites reading a served v1
// exchange as v2 success, which is the conflation this type exists to prevent.
func (o Outcome) MayRelyOnV2() bool { return o == OutcomeV2 }

// Served reports whether the exchange may proceed at all, under either version.
func (o Outcome) Served() bool { return o != OutcomeRefuse }

// CheckContract compares a host's declared contract against this module's.
//
// Absent is not a failure of v2 conformance -- it is a v1 exchange, which stays
// valid and always will. Wrong is a hard refusal BEFORE any v2 guarantee is
// relied upon: a guarantee assumed from a mismatched contract is exactly the
// silent-correctness failure the pin exists to prevent.
func CheckContract(hostContract string) (Outcome, *ContractRefusal) {
	switch hostContract {
	case ContractV2:
		return OutcomeV2, nil
	case "":
		return OutcomeV1, &ContractRefusal{
			Reason: "the host declared no behavioural contract version, so this is a v1 exchange",
			Remedy: "nothing to fix: v1 modules keep working and always will. A host " +
				"wanting v2 guarantees declares " + ContractV2 + " explicitly, and " +
				"this module declares it only once it implements those guarantees",
		}
	default:
		return OutcomeRefuse, &ContractRefusal{
			Reason: fmt.Sprintf("behavioural contract mismatch: host declares %q, "+
				"this module speaks %q", hostContract, ContractV2),
			Remedy: "no negotiation exists by operator ruling: pinning is required and " +
				"negotiation deferred. Both sides must declare " + ContractV2 +
				" exactly, or exchange over v1 without v2 guarantees",
		}
	}
}
