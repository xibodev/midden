package module

// Artifact kind declarations, with RFC v2 §7/§9 enforced where they are written.
//
// §7 splits an artifact three ways: `document` is validatable against a declared
// contract that names its validator, `text` is readable with no validation
// contract beyond media type and digest, `media` is bytes checked by type, size
// and digest and never by JSON Schema.
//
// §9 requires TWO checks a conformance suite must make, and they are different
// failures: a named validator that DOES NOT EXIST, and one that RESOLVES BUT
// DESCRIBES SOMETHING ELSE. The second is the subtler one and it is the one this
// tree already hit -- application/json was recorded as `document` because
// xibodev.midden.content.output/v1 is a real JSON Schema. It is real, and its
// properties are kind, format and review: it validates the artifact RECORD, not
// the bytes. A name-resolution check passes that; the intent fails.
//
// So the rule is enforced HERE, at the declaration, rather than left as prose in
// docs/OPERATIONS.md where nothing could contradict it and nothing could check
// it either.

// ArtifactKind is how an artifact may be validated.
type ArtifactKind string

const (
	// KindDocument requires a validator that validates the artifact's own
	// CONTENT. Declaring it without one is the defect §9 exists to catch.
	KindDocument ArtifactKind = "document"
	// KindText is an honest statement that NO validator exists. It is not a
	// weaker document: it is a different claim.
	KindText ArtifactKind = "text"
	// KindMedia is validated by media type, size and digest. Never JSON Schema.
	KindMedia ArtifactKind = "media"
)

// ArtifactDeclaration is one emitted media type and how it may be checked.
type ArtifactDeclaration struct {
	MediaType string
	Kind      ArtifactKind

	// Validator names what validates the artifact's CONTENT. Required for
	// KindDocument, and forbidden otherwise -- a validator on a `text` kind
	// would be a validation claim the kind explicitly disclaims.
	Validator string
}

// ArtifactDeclarations is what Midden emits today.
//
// Every one is `text`. That is not a gap: no validator for the CONTENT of any
// of these exists, and under §9 a shape that LOOKS validatable is not a
// validation contract. TSV, NDJSON and JSON become `document` only when a
// validator for their content is written and named.
var ArtifactDeclarations = []ArtifactDeclaration{
	{MediaType: "text/markdown", Kind: KindText},
	{MediaType: "text/plain", Kind: KindText},
	{MediaType: "text/x-diff", Kind: KindText},
	{MediaType: "application/x-ndjson", Kind: KindText},
	{MediaType: "text/tab-separated-values", Kind: KindText},
	{MediaType: "application/json", Kind: KindText},
}
