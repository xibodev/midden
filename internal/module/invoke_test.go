package module

import (
	"errors"
	"testing"
)

// TestPartialInventoryIsStructuredNotOnlyProse pins the fix for a defect
// facet-studio found by running sessions.list through the real host.
//
// A locked source store is SKIPPED: the run succeeds, a warning names the store
// and the remedy, and every count is computed over the stores that opened. That
// is correct behaviour and a misleading number -- matched is right about what
// was read and silent about what was not.
//
// The warning alone is insufficient because prose is not machine-readable: an
// agent summarising the result quotes the figure and drops the caveat. The
// coverage must be a FIELD, for the same reason excluded_noise is one.
func TestPartialInventoryIsStructuredNotOnlyProse(t *testing.T) {
	full := ListResult{Matched: 100, StoresRead: []string{"claude"}}
	if full.PartialInventory {
		t.Error("a complete inventory reported itself partial")
	}

	part := ListResult{
		Matched:           100,
		StoresRead:        []string{"claude"},
		StoresUnavailable: []string{"opencode"},
		PartialInventory:  true,
	}
	if !part.PartialInventory {
		t.Error("an inventory missing a store did not say so")
	}
	if len(part.StoresUnavailable) == 0 {
		t.Error("a partial inventory does not name what it could not read; " +
			"a consumer cannot tell which store is missing")
	}

	// Normalize must not turn an empty coverage list into null: a consumer
	// reading null cannot distinguish "no stores failed" from "the field was
	// never populated", which is the ambiguity the field exists to remove.
	empty := ListResult{}
	empty.Normalize()
	if empty.StoresRead == nil || empty.StoresUnavailable == nil {
		t.Error("coverage lists normalized to nil; null is indistinguishable " +
			"from an unpopulated field")
	}
}

// TestStoreNameSurvivesAMalformedError guards the coverage list itself.
//
// An unnamed unavailable store is worse than an oddly named one: the list would
// silently shrink and a consumer would believe fewer stores failed than did.
func TestStoreNameSurvivesAMalformedError(t *testing.T) {
	if got := storeNameOf(errors.New("opencode: locked")); got != "opencode" {
		t.Errorf("storeNameOf = %q, want opencode", got)
	}
	if got := storeNameOf(errors.New("no colon here")); got == "" {
		t.Error("a malformed error produced an empty store name; the coverage " +
			"list would shrink without saying so")
	}
}
