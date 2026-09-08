package module

// The seed builder lives in internal/seed, not here.
//
// A capability implemented inside the module layer is reachable only through
// the module layer: the CLI and the standalone UI could not build a seed, and
// Midden would produce different artifacts depending on how it was launched.
// The logic moved to the core; these aliases keep the protocol face reading
// naturally without duplicating any of it.

import "github.com/mekjr1/midden/internal/seed"

type (
	SeedInput      = seed.SeedInput
	SeedEvidence   = seed.SeedEvidence
	SeedSource     = seed.SeedSource
	SeedManifest   = seed.SeedManifest
	SeedProvenance = seed.SeedProvenance
)

const (
	SeedSchemaID       = seed.SeedSchemaID
	SeedManifestFile   = seed.SeedManifestFile
	SeedBriefFile      = seed.SeedBriefFile
	SeedEvidenceFile   = seed.SeedEvidenceFile
	SeedProvenanceFile = seed.SeedProvenanceFile
	SeedAttachmentsDir = seed.SeedAttachmentsDir
	SeedTitleLimit     = seed.SeedTitleLimit
	ReviewUnreviewed   = seed.ReviewUnreviewed
)

var (
	WriteSeed          = seed.WriteSeed
	ReadSeed           = seed.ReadSeed
	VerifySeedEvidence = seed.VerifySeedEvidence
)
