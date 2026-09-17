package create

import (
	"strings"
	"testing"
)

func TestSlideContractRejectsMalformedModelOutput(t *testing.T) {
	if err := ValidateSlideSource("# Title\n--- # Next\n* junk"); err == nil {
		t.Fatal("malformed deck accepted")
	}
	if err := ValidateSlideSource(strings.Repeat("# Slide\n\n- One point\n\n::: notes\nEvidence citation\n:::\n\n---\n", 8)); err != nil {
		t.Fatal(err)
	}
}
