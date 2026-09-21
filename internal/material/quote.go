package material

import (
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/quotation"
)

type QuoteMatch struct {
	RecordID string `json:"record_id"`
	Matched  bool   `json:"matched"`
	Meaning  string `json:"meaning"`
}

// MatchQuote checks an explicit quotation request. It does not infer which
// phrases in an authored document were intended as quotations.
func MatchQuote(path, id, text string) (QuoteMatch, error) {
	result := QuoteMatch{RecordID: id, Meaning: "Text correspondence only; factual truth and editorial adequacy are not assessed."}
	if strings.TrimSpace(text) == "" || len(text) > 32768 {
		return result, fmt.Errorf("a bounded, nonempty quotation is required")
	}
	records, err := ReadCollection(path)
	if err != nil {
		return result, err
	}
	var selected *Record
	for i := range records {
		if records[i].ID != id {
			continue
		}
		if selected != nil {
			return result, fmt.Errorf("record id has multiple source versions; select an unambiguous source collection before quoting")
		}
		selected = &records[i]
	}
	if selected == nil {
		return result, fmt.Errorf("record is not present in the selected collection")
	}
	result.Matched = quotation.Matches(selected.Text, text)
	return result, nil
}
