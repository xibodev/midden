package editorial

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/create"
)

func TestExportLifecycleIsConsistentAcrossSourceAndSidecar(t *testing.T) {
	w, _, id := reviewedChapter(t)
	result, err := (create.Workflow{DB: w.DB}).Export(create.Change{OutputID: id})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	var output struct {
		Path       string `json:"path"`
		Provenance string `json:"provenance_path"`
	}
	json.Unmarshal(raw, &output)
	body, err := os.ReadFile(output.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "midden_status: draft") {
		t.Fatal("exported source claims draft state")
	}
	prov, err := os.ReadFile(output.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Status string `json:"status"`
		Review string `json:"review_state"`
	}
	json.Unmarshal(prov, &state)
	if state.Status != "exported" || state.Review != "reviewed" {
		t.Fatalf("ambiguous exported provenance: %s", prov)
	}
	stored, err := w.DB.RefineryOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != state.Status {
		t.Fatal("sidecar and database disagree")
	}
}
