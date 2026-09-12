package viewdef_test

import (
	"strings"
	"testing"

	"github.com/mekjr1/midden/pkg/viewdef"
)

func TestMiddenWorkbenchView_DigestStability(t *testing.T) {
	v := viewdef.MiddenWorkbenchView()
	if v == nil {
		t.Fatal("expected non-nil view definition")
	}

	d1 := v.Digest()
	d2 := v.Digest()
	if d1 != d2 {
		t.Errorf("digest must be stable: %q != %q", d1, d2)
	}
	if !strings.HasPrefix(d1, "sha256:") {
		t.Errorf("digest must have sha256: prefix, got %q", d1)
	}

	if len(v.Sections) != 4 {
		t.Errorf("expected 4 sections, got %d", len(v.Sections))
	}
	if len(v.Actions) != 4 {
		t.Errorf("expected 4 actions, got %d", len(v.Actions))
	}
}
