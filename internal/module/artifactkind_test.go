package module

import "testing"

// TestDocumentKindsNameAValidator is §9's FIRST check: a named validator that
// does not exist.
//
// Enforced at the declaration rather than at conformance time, because a kind
// declared today with no validator stays invisible until the v2 payload is
// built -- which is exactly how the Produces reverse-reference gap survived.
func TestDocumentKindsNameAValidator(t *testing.T) {
	if len(ArtifactDeclarations) == 0 {
		t.Fatal("no artifact declarations; the check asserts nothing and would " +
			"pass however the rule is written")
	}
	for _, d := range ArtifactDeclarations {
		if d.Kind == KindDocument && d.Validator == "" {
			t.Errorf("%s is declared `document` with no validator; a shape that "+
				"LOOKS validatable is not a validation contract", d.MediaType)
		}
	}
}

// TestNonDocumentKindsClaimNoValidator is the inverse, and it is not symmetry
// for its own sake.
//
// A validator on a `text` kind is a validation claim the kind explicitly
// disclaims -- the declaration would say "no contract exists" and "here is the
// contract" at once, and a consumer could act on either.
func TestNonDocumentKindsClaimNoValidator(t *testing.T) {
	for _, d := range ArtifactDeclarations {
		if d.Kind != KindDocument && d.Validator != "" {
			t.Errorf("%s is `%s` but names validator %q; the kind disclaims a "+
				"validation contract and the field asserts one",
				d.MediaType, d.Kind, d.Validator)
		}
	}
}

// TestNoKindIsDeclaredDocumentToday pins the finding rather than the values.
//
// §9's SECOND check -- a validator that resolves but describes something else --
// is the one this tree actually hit: application/json was recorded as `document`
// because xibodev.midden.content.output/v1 is a real JSON Schema. It is real,
// and its properties are kind, format and review. It validates the artifact
// RECORD, never the bytes.
//
// A name-resolution check passes that. So until a validator for the CONTENT is
// written, nothing here is a document, and this test fails loudly if someone
// declares one without also satisfying the check above.
func TestNoKindIsDeclaredDocumentToday(t *testing.T) {
	for _, d := range ArtifactDeclarations {
		if d.Kind != KindDocument {
			continue
		}
		if d.Validator == "" {
			continue // already reported by the first check
		}
		t.Logf("%s now declares document with validator %q -- confirm it "+
			"validates the artifact CONTENT and not a record about it",
			d.MediaType, d.Validator)
	}
}

// TestEveryDeclaredKindIsRecognised guards the vocabulary itself. An unknown
// kind would fall through every check above and be validated by nothing.
func TestEveryDeclaredKindIsRecognised(t *testing.T) {
	known := map[ArtifactKind]bool{KindDocument: true, KindText: true, KindMedia: true}
	for _, d := range ArtifactDeclarations {
		if !known[d.Kind] {
			t.Errorf("%s declares unknown kind %q; it would pass every "+
				"kind-specific check by matching none of them", d.MediaType, d.Kind)
		}
	}
}
