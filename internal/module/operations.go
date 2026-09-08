package module

// Canonical Operation inventory, machine-readable.
//
// Layer 1 of the architecture: what Midden DOES, independent of how any face
// delivers it. Derived by semantic contract (docs/OPERATIONS.md), not from the
// capability list -- a delivery surface must not become product truth.
//
// This exists in code rather than only in prose because RFC v2 §9 requires the
// no-weakening rule to be checked MECHANICALLY, and that compares an Operation's
// effects to its capability's. A markdown table gives a reviewer something to
// read and gives a test nothing to compare against, so the guarantee could only
// ever have been asserted.
//
// Nothing here reaches the wire. xibodev.module/v1 is immutable legacy and
// carries no Operation layer; this is local truth only.

// Operation is a semantic unit of product work.
type Operation struct {
	// ID is the canonical name. It is NOT a capability id: several faces may
	// project one Operation, and some capabilities project none.
	ID string

	// Contract is the semantic transformation, stated as input -> output.
	Contract string

	// MayCharge reports whether invoking this can result in a monetary charge.
	//
	// Separate from whether an amount is known (operator ruling 4) and separate
	// from whether a model is used (ruling 13): a harness the user is already
	// signed in to owns its own billing, so model usage is not chargeability.
	MayCharge bool

	// ChargeDependsOn names an input field when chargeability varies by
	// argument rather than being constant. Empty means MayCharge is absolute.
	//
	// produce_content is the case: seven of nineteen kinds spend nothing. A
	// per-Operation boolean cannot express that, and splitting it into nineteen
	// Operations would make a delivery surface into product truth.
	ChargeDependsOn string

	// Deterministic reports whether the same input yields the same bytes.
	Deterministic bool

	// ExternalWrites reports whether this writes outside a granted root.
	ExternalWrites bool
}

// Operations is the canonical inventory. Four, not six, not twenty-nine.
var Operations = []Operation{
	{
		ID:            "assay_session",
		Contract:      "session scope -> evidence manifest",
		Deterministic: true,
	},
	{
		ID:        "mine_evidence",
		Contract:  "session scope -> stored evidence set",
		MayCharge: true,
		// Not deterministic: a model decides what is worth keeping.
		//
		// Re-execution is nonetheless SAFE, because nugget identity derives
		// from the evidence, so re-mining lands on the same row rather than
		// inserting duplicates. That is why this Operation requires no durable
		// resume facility despite spending per session.
	},
	{
		ID:              "produce_content",
		Contract:        "evidence set + kind -> written output",
		MayCharge:       true,
		ChargeDependsOn: "kind",
	},
	{
		ID:            "build_seed",
		Contract:      "session scope + goal -> portable seed bundle",
		Deterministic: true,
	},
}

// capabilityOperations maps each wire capability to the Operation it projects.
//
// Two capabilities project NOTHING: sessions.list and content.types are
// registry reads that report what exists and transform no product material.
// They are still bound by the effects model -- a capability that projects no
// Operation is not thereby unregulated.
var capabilityOperations = map[string]string{
	CapSessionsAssay:   "assay_session",
	CapEvidenceExtract: "mine_evidence",
	CapContentProduce:  "produce_content",
	CapSeedCreate:      "build_seed",
	CapSessionsList:    "",
	CapContentTypes:    "",
}

// OperationByID returns an Operation and whether it exists.
func OperationByID(id string) (Operation, bool) {
	for _, op := range Operations {
		if op.ID == id {
			return op, true
		}
	}
	return Operation{}, false
}
