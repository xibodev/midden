package module

import (
	"encoding/json"

	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/refinery"
)

// The xibodev.module/v2 projection.
//
// Built from the SAME models the rest of this package uses -- Operations,
// ArtifactDeclarations, capabilityOperations -- rather than a parallel table
// written for the wire. A second truth table would drift from the first, and
// the drift would be invisible until a host read one and a test read another.
//
// v1 is untouched: Describe() returns exactly what it returned, and this is
// an additional projection a v2-speaking host asks for.

// ContractV2 is the behavioural contract this module speaks. Exactly one
// value: pinning is required and negotiation deferred, and a single-element
// list that merely looks negotiable is forbidden.
const ContractV2 = "xibodev.module/v2"

// V2Descriptor is the frozen v2 shape.
type V2Descriptor struct {
	Protocol        string `json:"protocol"`
	ContractVersion string `json:"contract_version"`

	Module  string `json:"module"`
	Name    string `json:"name"`
	Version string `json:"version"`

	Operations    []V2Operation             `json:"operations"`
	Capabilities  []V2Capability            `json:"capabilities"`
	ArtifactKinds map[string]V2ArtifactKind `json:"artifact_kinds"`

	RequestSchemas map[string]json.RawMessage `json:"request_schemas"`
	ResultSchemas  map[string]json.RawMessage `json:"result_schemas"`

	Permissions Permissions `json:"permissions"`
	Skills      []Skill     `json:"skills"`
}

// V2Operation is Layer 1: a semantic unit, independent of any face.
type V2Operation struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`

	Effects   V2Effects   `json:"effects"`
	Execution V2ExecProps `json:"execution"`

	Requirements []V2Requirement `json:"requirements"`
	Approval     V2Approval      `json:"approval"`
	Produces     []string        `json:"produces"`
}

// V2Capability is Layer 2: what a host invokes. Projects may be EMPTY -- a
// registry read transforms no product material and is still gated.
type V2Capability struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`

	RequestSchema string `json:"request_schema"`
	ResultSchema  string `json:"result_schema"`

	Projects []string  `json:"projects"`
	Effects  V2Effects `json:"effects"`
}

// V2Effects carries may_charge INDEPENDENTLY of cost_known: whether an
// amount is known and whether money may be spent are separate facts.
type V2Effects struct {
	Network        bool      `json:"network"`
	ExternalWrites bool      `json:"external_writes"`
	Provider       string    `json:"provider,omitempty"`
	MayCharge      MayCharge `json:"may_charge"`
	CostKnown      bool      `json:"cost_known"`
	Deterministic  bool      `json:"deterministic"`
}

// MayCharge is a boolean OR a condition on one declared input field.
//
// content.produce forced the conditional form: seven of nineteen kinds spend
// nothing, so a bare boolean over-gates them permanently, and splitting the
// Operation into nineteen would make a delivery surface into product truth.
type MayCharge struct {
	Always  bool
	Field   string
	WhenIn  []string
	Default bool
}

// IsConditional reports whether chargeability depends on an input.
func (m MayCharge) IsConditional() bool { return m.Field != "" }

// MarshalJSON emits the plain boolean when unconditional and the object form
// otherwise, matching the frozen shape.
func (m MayCharge) MarshalJSON() ([]byte, error) {
	if !m.IsConditional() {
		return json.Marshal(m.Always)
	}
	return json.Marshal(struct {
		Field   string   `json:"field"`
		WhenIn  []string `json:"when_in"`
		Default bool     `json:"default"`
	}{m.Field, m.WhenIn, m.Default})
}

// V2ExecProps are execution constraints a host must obey.
type V2ExecProps struct {
	DeadlineMSDefault int `json:"deadline_ms_default"`
}

// V2Requirement is a semantic requirement with a strength.
type V2Requirement struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Strength string `json:"strength"`
	Detail   string `json:"detail,omitempty"`
}

// V2Approval declares WHY approval is required, not merely that it is.
type V2Approval struct {
	RequiredWhen []string `json:"required_when"`
}

// V2ArtifactKind declares how an artifact may be validated.
type V2ArtifactKind struct {
	Kind      string       `json:"kind"`
	MediaType string       `json:"media_type"`
	Validator *V2Validator `json:"validator,omitempty"`
}

// V2Validator names what validates an artifact CONTENT.
type V2Validator struct {
	Type   string `json:"type"`
	Schema string `json:"schema"`
}

// DescribeV2 projects Midden onto the frozen v2 contract.
func DescribeV2() V2Descriptor {
	v1 := Describe()

	d := V2Descriptor{
		Protocol:        ProtocolID,
		ContractVersion: ContractV2,
		Module:          core.ModuleID,
		Name:            "Midden",
		Version:         core.Version,
		ArtifactKinds:   map[string]V2ArtifactKind{},
		RequestSchemas:  v1.RequestSchemas,
		ResultSchemas:   v1.ResultSchemas,
		Permissions:     v1.Permissions,
		Skills:          v1.Skills,
	}

	for _, op := range Operations {
		d.Operations = append(d.Operations, v2Operation(op))
	}
	for _, c := range v1.Capabilities {
		d.Capabilities = append(d.Capabilities, v2Capability(c))
	}
	// Artifact kinds are keyed by the ID a Produces entry names, NOT by media
	// type. The host resolves the reverse reference by id, so a media-type key
	// is unreachable: it reported six unreferenced kinds, each correct-looking
	// and resolvable by nothing.
	//
	// The media types Midden emits are carried INSIDE each declaration, which
	// is where they belong -- they describe the bytes, they do not name the
	// contract.
	// Produces names artifact IDS, so a host resolving them looks here. Both
	// are text: no validator for their CONTENT exists, and a shape that looks
	// validatable is not a validation contract.
	d.ArtifactKinds[ArtifactContentOutput] = V2ArtifactKind{
		Kind: string(KindText), MediaType: "text/markdown",
	}
	d.ArtifactKinds[SeedSchemaID] = V2ArtifactKind{
		Kind: string(KindText), MediaType: "application/json",
	}
	return d
}

const harnessDetail = "an AI CLI the user is already signed in to; Midden holds no API key"

// v2Operation derives the wire form of an Operation from the canonical model.
func v2Operation(op Operation) V2Operation {
	out := V2Operation{
		ID:           op.ID,
		Title:        op.ID,
		Summary:      op.Contract,
		Produces:     emptySlice(op.Produces),
		Execution:    V2ExecProps{DeadlineMSDefault: 180000},
		Requirements: []V2Requirement{},
		Approval:     V2Approval{RequiredWhen: []string{}},
	}

	charges := op.MayCharge || op.ChargeDependsOn != ""
	// The amount is never knowable: Midden shells out to a harness the user is
	// already signed in to and never sees a bill.
	out.Effects = V2Effects{
		ExternalWrites: op.ExternalWrites,
		Deterministic:  op.Deterministic,
		CostKnown:      !charges,
	}

	harness := V2Requirement{
		Kind: "binary", Name: "agentic-harness",
		Strength: "mandatory", Detail: harnessDetail,
	}

	switch {
	case op.ChargeDependsOn != "":
		// Chargeable set read from the SAME table the product uses. Default
		// true so a kind added tomorrow and not yet classified gates rather
		// than slipping through.
		out.Effects.MayCharge = MayCharge{
			Field:   op.ChargeDependsOn,
			WhenIn:  refinery.ChargeableKinds(),
			Default: true,
		}
		out.Effects.Provider = "harness"
		out.Approval.RequiredWhen = []string{"chargeable"}
		out.Requirements = append(out.Requirements, harness)
	case op.MayCharge:
		out.Effects.MayCharge = MayCharge{Always: true}
		out.Effects.Provider = "harness"
		out.Approval.RequiredWhen = []string{"chargeable"}
		out.Requirements = append(out.Requirements, harness)
	default:
		out.Effects.MayCharge = MayCharge{Always: false}
	}
	return out
}

// v2Capability projects a capability, carrying its own effects because the
// gate reads capability effects and a projection must never weaken its
// Operation. The conditional is CARRIED rather than collapsed: collapsing to
// true is legal and costs the precision the conditional form exists to give.
func v2Capability(c Capability) V2Capability {
	out := V2Capability{
		ID:            c.ID,
		Title:         c.Title,
		Summary:       c.Summary,
		RequestSchema: c.RequestSchema,
		ResultSchema:  c.ResultSchema,
		Projects:      []string{},
	}

	opID := capabilityOperations[c.ID]
	if opID == "" {
		// A registry read projects no Operation and is still gated.
		out.Effects = V2Effects{
			MayCharge:     MayCharge{Always: false},
			CostKnown:     true,
			Deterministic: true,
		}
		return out
	}
	out.Projects = []string{opID}

	op, _ := OperationByID(opID)
	out.Effects = v2Operation(op).Effects
	return out
}

func v2ArtifactKind(a ArtifactDeclaration) V2ArtifactKind {
	out := V2ArtifactKind{Kind: string(a.Kind), MediaType: a.MediaType}
	if a.Validator != "" {
		out.Validator = &V2Validator{Type: "json-schema", Schema: a.Validator}
	}
	return out
}
