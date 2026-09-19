package create

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/index"
)

func TestDraftAuditFlagsUncitedBackgroundAndUnknownEvidence(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ns := []index.Nugget{{UID: "e1", Title: "Local limit", Body: "The tests were not run in this environment."}}
	report, err := AuditDraft(db, "# Limits\n\nThree earlier regressions were confirmed fixed.\n\nThe local tests were not run. [E2]\n", ns, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.MechanicalValid {
		t.Fatal("uncited or out-of-scope assertions passed")
	}
	raw, _ := json.Marshal(report)
	for _, code := range []string{"missing_citation", "unknown_citation"} {
		if !strings.Contains(string(raw), code) {
			t.Fatalf("missing %s: %s", code, raw)
		}
	}
}

func TestDraftAuditVerifiesQuotationsAgainstStoredSourceNotSummary(t *testing.T) {
	db, err := index.OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packet := json.RawMessage(`{"packet":{"records":[{"id":"r1","excerpt":"We could not run the tests in this environment.","kind":"assistant.message"}]}}`)
	if _, err = db.PutReadingPacket("p1", "d1", "inv", packet, map[string]int{"r1": 52}); err != nil {
		t.Fatal(err)
	}
	ns := []index.Nugget{{UID: "e1", Body: "Tests could not run.", TurnRef: `{"packet_id":"p1","record_ids":["r1"]}`}}
	good, err := AuditDraft(db, "# Limits\n\n> We could not run the tests in this environment. [E1]\n", ns, nil)
	if err != nil || !good.MechanicalValid {
		t.Fatalf("exact quote rejected: %+v %v", good, err)
	}
	bad, err := AuditDraft(db, "# Limits\n\n> The adapters are correct and all tests pass. [E1]\n", ns, nil)
	if err != nil || bad.MechanicalValid {
		t.Fatalf("reconstructed quotation accepted: %+v %v", bad, err)
	}
	if good.SemanticReview != "required" {
		t.Fatal("valid references were misrepresented as semantic proof")
	}
}

func TestSourceFrontmatterDoesNotClaimMutableReviewStatus(t *testing.T) {
	got := NormalizeSourceMetadata("---\ntitle: \"Post\"\nmidden_recipe: \"r\"\nmidden_status: draft\n---\n\nBody\n")
	if strings.Contains(got, "midden_status:") {
		t.Fatal("stale lifecycle marker survived")
	}
	if NormalizeSourceMetadata(got) != got {
		t.Fatal("source normalization is not idempotent")
	}
}
