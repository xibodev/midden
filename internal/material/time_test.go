package material

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnknownTimesAreNotPresentedAsYearOne(t *testing.T) {
	raw, err := json.Marshal(Record{ID: "synthetic", Text: "record without a timestamp"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "0001-") {
		t.Fatal("unknown time was emitted as an apparent historical date")
	}
}
