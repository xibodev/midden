package material

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectionAssetsAcceptCopyableListedRecordIDs(t *testing.T) {
	service, view, _, image := materialAssetFixture(t)
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Collect(CollectOptions{
		Views: []string{view.ID}, Records: []string{listed.Assets[0].RecordID},
		Out: filepath.Join(t.TempDir(), "selected"), IncludeAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordCount != 1 {
		t.Fatal("listed record selection widened to unrelated records")
	}
	manifest := assertCollectionAssets(t, result.Path, 2, image)
	for _, asset := range manifest.Assets {
		if asset.RecordID != view.Records[1].ID {
			t.Fatal("listed raw-record alias did not retain its selected excerpt owner")
		}
	}
}

func TestCollectionAssetsRejectEditedCachedEvidence(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	path := filepath.Join(service.State, "views", view.ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cached cachedView
	if err = json.Unmarshal(raw, &cached); err != nil {
		t.Fatal(err)
	}
	cached.View.Records[1].Text = "Fabricated source evidence."
	raw, err = json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "must-not-exist")
	if _, err = service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[1].ID}, Out: out, IncludeAssets: true}); err == nil {
		t.Fatal("collection trusted edited cached evidence")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("edited cached evidence produced output")
	}
}
