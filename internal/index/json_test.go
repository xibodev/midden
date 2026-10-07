package index

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Stored assay totals are a JSON contract with snake_case keys.
func TestTotalsJSONKeys(t *testing.T) {
	raw, err := json.Marshal(Totals{Sessions: 1, Assayed: 2, Bytes: 3, Signal: 4, Exhaust: 5, Artifact: 6, Book: 7, DupBytes: 8, Images: 9, Clusters: 10})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]int64
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := "artifact,assayed,bookkeeping,bytes,clusters,dup_bytes,exhaust,images,sessions,signal"
	if got := strings.Join(keys, ","); got != want {
		t.Fatalf("totals keys = %s, want %s", got, want)
	}
	if doc["bookkeeping"] != 7 || doc["dup_bytes"] != 8 {
		t.Fatalf("totals values moved between keys: %s", raw)
	}
}
