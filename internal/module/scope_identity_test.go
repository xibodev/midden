package module

import "testing"

func TestCompositeSessionScope(t *testing.T) {
	s, err := scopeFromAssayRequest(AssayRequest{IDs: []string{"opencode:ses_example"}})
	if err != nil || len(s.IDs) != 1 || s.IDs[0] != "ses_example" || len(s.Tools) != 1 || s.Tools[0] != "opencode" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err = scopeFromAssayRequest(AssayRequest{Tool: "claude", IDs: []string{"opencode:ses_example"}}); err == nil {
		t.Fatal("conflicting tool accepted")
	}
}
