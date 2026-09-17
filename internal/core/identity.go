package core

// Product identity, owned by the core rather than by any delivery channel.
//
// These strings name the product itself, so every face reports the same thing:
// a seed written by the CLI, by the standalone UI, and by the facet-studio
// module must carry identical provenance. Keeping them in the module package
// made a protocol face the source of truth for who produced an artifact, which
// is backwards -- and unreachable from the other two faces.
const (
	// ModuleID is the product's stable identifier on any wire.
	ModuleID = "midden"

	// Version is the product version reported in descriptors and provenance.
	Version = "0.1.0"
)
