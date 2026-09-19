package create

import (
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/redact"
)

// EffectiveReview is shared by host confirmation and persistence so the
// confirmed bytes/evidence are exactly the ones the review records.
func EffectiveReview(output index.RefineryOutput, original string, c Change) (string, []string, error) {
	body := c.Body
	if body == "" {
		body = original
	}
	body = redact.Text(NormalizeSourceMetadata(body)).Text
	if strings.TrimSpace(body) == "" {
		return "", nil, fmt.Errorf("a reviewed output cannot be empty")
	}
	ids := c.EvidenceIDs
	if ids == nil {
		ids = output.EvidenceIDs
	}
	if output.Format == "jsonl" {
		var err error
		ids, err = EvidenceIDsFromJSONL(body)
		if err != nil {
			return "", nil, err
		}
	}
	ids = uniqueStrings(ids)
	allowed := map[string]bool{}
	for _, id := range output.EvidenceIDs {
		allowed[id] = true
	}
	for _, id := range ids {
		if !allowed[id] {
			return "", nil, fmt.Errorf("evidence %s is outside this output", id)
		}
	}
	if c.Decision == "reviewed" && len(ids) == 0 {
		return "", nil, fmt.Errorf("a reviewed output must retain evidence")
	}
	return body, ids, nil
}
