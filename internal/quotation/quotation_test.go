package quotation

import "testing"

func TestPresentationMarkupDoesNotChangeQuotedWords(t *testing.T) {
	if !Matches("**3 production metrics have dual-contract support:** `alpha`, `beta`, and `gamma`.", "3 production metrics have dual-contract support: alpha, beta, and gamma.") {
		t.Fatal("presentation-only Markdown blocked a faithful quotation")
	}
	for _, tc := range [][2]string{
		{"The tests did not pass.", "The tests did pass."},
		{"13 metrics passed.", "3 metrics passed."},
		{"This was planned, not executed.", "This was executed."},
		{"Variable a*b is distinct.", "Variable ab is distinct."},
	} {
		if Matches(tc[0], tc[1]) {
			t.Fatalf("changed words accepted: %q / %q", tc[0], tc[1])
		}
	}
}
