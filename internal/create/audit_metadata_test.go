package create

import (
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestDraftAudienceMetadataIsNotAnUncitedHistoricalClaim(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	report, err := AuditDraft(db, "# Article\n\n**Audience:** the engineering team\n\nCheck the deployed version before canary validation. [E1]\n",
		[]index.Nugget{{UID: "e", Body: "Check the deployed version before canary validation."}}, nil)
	if err != nil || report.Blocked {
		t.Fatalf("editorial metadata was treated as a historical assertion: %+v %v", report, err)
	}
}
