package material

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
)

func TestAssetsRespectCurrentStrictRoots(t *testing.T) {
	for _, operation := range []string{"list", "extract", "collect"} {
		t.Run(operation, func(t *testing.T) {
			service, view, _, _ := materialAssetFixture(t)
			service.Roots = adapter.Roots{Claude: t.TempDir(), Strict: true}
			out := filepath.Join(t.TempDir(), "must-not-exist")
			var err error
			switch operation {
			case "list":
				_, err = service.Assets(view.ID, nil, "")
			case "extract":
				_, err = service.Assets(view.ID, []string{view.Records[1].ID}, out)
			case "collect":
				_, err = service.Collect(CollectOptions{
					Views: []string{view.ID}, Records: []string{view.Records[1].ID},
					Out: out, IncludeAssets: true,
				})
			}
			if err == nil {
				t.Fatal("cached source path bypassed the current strict roots")
			}
			if _, err = os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("out-of-scope assets created output")
			}
		})
	}
}

func TestAssetsResolveRelocatedSourceWithinCurrentRoots(t *testing.T) {
	service, view, oldPath, image := materialAssetFixture(t)
	raw, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	project := filepath.Join(newRoot, "projects", "synthetic")
	if err = os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "asset-session.jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	service.Roots = adapter.Roots{Claude: newRoot, Strict: true}
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil || len(listed.Assets) != 2 || listed.Assets[0].Digest != Digest(image) {
		t.Fatalf("asset read did not resolve the current store: %+v %v", listed, err)
	}
	out := filepath.Join(t.TempDir(), "relocated")
	if _, err = service.Collect(CollectOptions{
		Views: []string{view.ID}, Records: []string{view.Records[1].ID}, Out: out, IncludeAssets: true,
	}); err != nil {
		t.Fatal(err)
	}
	assertCollectionAssets(t, out, 2, image)
}

func TestAssetsRejectChangedPrefixInCurrentRoot(t *testing.T) {
	service, view, oldPath, _ := materialAssetFixture(t)
	raw, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	newRoot := t.TempDir()
	project := filepath.Join(newRoot, "projects", "synthetic")
	if err = os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("same.png"), []byte("next.png"))
	if err = os.WriteFile(filepath.Join(project, "asset-session.jsonl"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	service.Roots = adapter.Roots{Claude: newRoot, Strict: true}
	if _, err = service.Assets(view.ID, nil, ""); err == nil {
		t.Fatal("changed current source was bypassed using the unchanged cached source")
	}
}
