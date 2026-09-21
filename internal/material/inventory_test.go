package material

import (
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

func TestInventoryUsesBoundedExactSourceScope(t *testing.T) {
	service, source, _ := fixture(t)
	result, err := List(service.Roots, core.Scope{Tools: []core.Tool{source.Tool}, IDs: []string{source.ID}, IncludeNoise: true}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Matched != 1 || result.Total != 1 || result.NextOffset != nil {
		t.Fatalf("inventory scope wrong: %+v", result)
	}
	if result.Sessions[0].ID != source.ID {
		t.Fatal("wrong source identity")
	}
	if _, err = List(service.Roots, core.Scope{Tools: []core.Tool{"unsupported"}}, 0, 1); err == nil {
		t.Fatal("invalid tool widened inventory")
	}
}
