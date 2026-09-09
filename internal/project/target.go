package project

// Target profiles.
//
// facet-studio publishes a normative Target Contract because it owns one. The
// agentic CLIs do not publish anything machine-readable, so what we hold is an
// adapter-maintained PROFILE: what was observed and tested, pinned to a version,
// never claimed as the vendor's contract.
//
// The distinction is not pedantry. A Contract can be pinned and a Profile can
// only be dated, and a projection says which it was verified against so a reader
// can tell "the host guarantees this" from "we checked in September".

// Kind separates a normative contract from an observed profile.
type Kind string

const (
	// ContractNormative: the harness owner publishes and versions it.
	ContractNormative Kind = "contract"
	// ProfileObserved: this adapter maintains it from what it verified.
	ProfileObserved Kind = "profile"
)

// Target is one harness Midden can be projected onto.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// Kind and Version identify WHAT was verified against. A profile version is
	// this adapter's, not the vendor's.
	Kind    Kind   `json:"kind"`
	Version string `json:"version"`

	// Facilities the harness offers. Absence is a real answer, not unknown:
	// these are facts about the harness, established when the profile was made.
	Subprocess bool `json:"subprocess"`
	OwnShell   bool `json:"own_shell"`

	// AssetForm is how product assets must be delivered here.
	AssetForm string `json:"asset_form"`
}

// Targets is the set Midden projects onto.
//
// Evaluated independently, per operator ruling: one target's support state is
// never inferred from another's. Midden standalone in particular does NOT
// consume the detached-module layer -- proven by import graph, internal/web and
// internal/seed reach internal/module zero times -- so module/v1's limits say
// nothing about what standalone can do.
var Targets = []Target{
	{
		ID: "claude-code", Name: "Claude Code",
		Kind: ProfileObserved, Version: "observed-2026-09",
		Subprocess: true, AssetForm: "skills-dir",
	},
	{
		ID: "copilot-cli", Name: "GitHub Copilot CLI",
		Kind: ProfileObserved, Version: "observed-2026-09",
		Subprocess: true, AssetForm: "skills-dir",
	},
	{
		ID: "opencode", Name: "OpenCode",
		Kind: ProfileObserved, Version: "observed-2026-09",
		Subprocess: true, AssetForm: "skills-dir",
	},
	{
		ID: "facet-studio", Name: "Facet Studio",
		Kind: ContractNormative, Version: "xibodev.module/v1",
		Subprocess: true, AssetForm: "module-descriptor",
	},
	{
		ID: "standalone", Name: "Midden standalone",
		Kind: ContractNormative, Version: "midden-native",
		Subprocess: true, OwnShell: true, AssetForm: "embedded",
	},
}

// TargetByID returns a target and whether it exists.
func TargetByID(id string) (Target, bool) {
	for _, t := range Targets {
		if t.ID == id {
			return t, true
		}
	}
	return Target{}, false
}
