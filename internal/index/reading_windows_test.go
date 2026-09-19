package index

import (
	"encoding/json"
	"testing"
)

func TestOverlappingReadingWindowsChargeOnlyTheirUnion(t *testing.T) {
	db, err := OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, tc := range []struct {
		spans []ReadingSpan
		used  int
	}{
		{[]ReadingSpan{{0, 100}}, 100},
		{[]ReadingSpan{{9000, 9500}}, 600},
		{[]ReadingSpan{{9200, 9700}}, 800},
		{[]ReadingSpan{{9000, 9700}}, 800},
	} {
		id := string(rune('a' + i))
		b, err := db.PutReadingPacketRanges(id, id, "inv", json.RawMessage(`{}`), map[string][]ReadingSpan{"record": tc.spans})
		if err != nil || b.UsedBytes != tc.used {
			t.Fatalf("window %d: %+v %v", i, b, err)
		}
	}
}
