package material

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
)

func materialAssetFixture(t *testing.T) (Service, View, string, []byte) {
	t.Helper()
	root := t.TempDir()
	store := filepath.Join(root, "claude")
	project := filepath.Join(store, "projects", "synthetic")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	var transcript bytes.Buffer
	encoder := json.NewEncoder(&transcript)
	for _, content := range []any{
		strings.Repeat("Synthetic source material. ", 100),
		[]any{
			map[string]any{"type": "text", "text": "Two recorded images."},
			map[string]any{"type": "image", "name": "same.png", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": encoded}},
			map[string]any{"type": "image", "name": "same.png", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": encoded}},
		},
	} {
		if err := encoder.Encode(map[string]any{
			"type": "user", "cwd": root, "sessionId": "asset-session",
			"message": map[string]any{"role": "user", "content": content},
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(project, "asset-session.jsonl")
	if err := os.WriteFile(path, transcript.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	service := Service{State: filepath.Join(root, "state"), Roots: adapter.Roots{Claude: store, Strict: true}}
	view, err := service.Open(Source{Tool: core.ToolClaude, ID: "asset-session"}, ReadOptions{Limit: 10, Chars: 800})
	if err != nil {
		t.Fatal(err)
	}
	return service, view, path, imageBytes.Bytes()
}

func TestAssetsListsAndExtractsEmbeddedImages(t *testing.T) {
	service, view, path, imageBytes := materialAssetFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if listed.ViewID != view.ID || len(listed.Assets) != 2 || listed.AssetCount != 2 || listed.CopiedCount != 0 || listed.Path != "" || len(listed.Paths) != 0 || len(listed.Limitations) == 0 {
		t.Fatalf("wrong metadata result: %+v", listed)
	}
	for _, asset := range listed.Assets {
		if asset.Status != "embedded" || asset.Path != "" || asset.Digest != Digest(imageBytes) {
			t.Fatalf("wrong listed asset: %+v", asset)
		}
		if index, _, err := recordPosition(view.Source, asset.RecordID); err != nil || index != 1 {
			t.Fatalf("asset record id cannot be selected: %q %v", asset.RecordID, err)
		}
	}
	if listed.Assets[0].ID == listed.Assets[1].ID {
		t.Fatal("multiple images in one record have colliding ids")
	}
	jsonBytes, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(jsonBytes, []byte(base64.StdEncoding.EncodeToString(imageBytes))) || bytes.Contains(jsonBytes, []byte(path)) {
		t.Fatal("listing disclosed a source path or base64 payload")
	}
	out := filepath.Join(t.TempDir(), "images")
	extracted, err := service.Assets(view.ID, []string{listed.Assets[0].RecordID}, out)
	if err != nil {
		t.Fatal(err)
	}
	if extracted.CopiedCount != 2 || extracted.CopiedBytes != int64(2*len(imageBytes)) || extracted.Path != out || len(extracted.Paths) != 2 {
		t.Fatalf("wrong extraction accounting: %+v", extracted)
	}
	if extracted.Assets[0].Path == extracted.Assets[1].Path {
		t.Fatal("same-name assets collided")
	}
	for i, asset := range extracted.Assets {
		if asset.Status != "copied" || asset.ID != listed.Assets[i].ID || !filepath.IsLocal(asset.Path) {
			t.Fatalf("unstable id or nonportable output path: %+v", asset)
		}
		copied, err := os.ReadFile(filepath.Join(out, asset.Path))
		if err != nil || !bytes.Equal(copied, imageBytes) || asset.Digest != Digest(copied) {
			t.Fatalf("copied image differs: %v", err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("asset operation modified the source transcript")
	}
}

func TestAssetsRequiresSelectionAndNeverReusesDestination(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "new-assets")
	if _, err = service.Assets(view.ID, nil, out); err == nil {
		t.Fatal("extraction without explicit records succeeded")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("invalid selection created an output directory")
	}
	for _, selection := range [][]string{
		{"claude:other:1@0-0"},
		{"claude:asset-session:999@0-0"},
		{"claude:asset-session:-1@0-0"},
		{listed.Assets[0].RecordID, listed.Assets[0].RecordID},
	} {
		if _, err = service.Assets(view.ID, selection, out); err == nil {
			t.Fatalf("invalid record selection accepted: %v", selection)
		}
	}
	if err = os.Mkdir(out, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(out, "keep.txt")
	if err = os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Assets(view.ID, []string{listed.Assets[0].RecordID}, out); err == nil {
		t.Fatal("existing output directory was reused")
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "keep" {
		t.Fatal("existing destination was modified")
	}
}

func TestAssetsListingAndExtractionAreStableAcrossAppends(t *testing.T) {
	service, view, path, _ := materialAssetFixture(t)
	first, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString(`{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"bmV3"}}]}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := service.Assets(view.ID, nil, "")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("append changed listed metadata: %v", err)
	}
	selection := []string{first.Assets[0].RecordID}
	one, err := service.Assets(view.ID, selection, filepath.Join(t.TempDir(), "one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := service.Assets(view.ID, selection, filepath.Join(t.TempDir(), "two"))
	if err != nil || !reflect.DeepEqual(one.Assets, two.Assets) {
		t.Fatalf("output names or ids depend on destination: %v", err)
	}
}

func TestAssetsChangedPrefixCannotCreateOutput(t *testing.T) {
	service, view, path, _ := materialAssetFixture(t)
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("Synthetic"), []byte("Different"), 1)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "must-not-exist")
	if result, err := service.Assets(view.ID, []string{listed.Assets[0].RecordID}, out); err == nil || len(result.Assets) != 0 {
		t.Fatal("changed pinned prefix produced a successful asset result")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("unverified assets left an output directory")
	}
}

func TestAssetsCannotWriteIntoSourceStores(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	service.Roots.Copilot = t.TempDir()
	for _, store := range []string{service.Roots.Claude, service.Roots.Copilot} {
		out := filepath.Join(store, "must-not-write")
		if _, err = service.Assets(view.ID, []string{listed.Assets[0].RecordID}, out); err == nil {
			t.Fatal("asset extraction wrote into a read-only source store")
		}
		if _, err = os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("source store was modified before rejecting output")
		}
	}
}

func TestAssetsFailedCopyRemovesOnlyItsNewOutput(t *testing.T) {
	service, _, path, _ := materialAssetFixture(t)
	directory := filepath.Dir(path)
	for _, name := range []string{"one.bin", "two.bin"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("recorded"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString(`{"type":"user","message":{"content":"Recorded files."},"attachments":[{"path":"one.bin"},{"path":"two.bin"}]}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := service.Open(Source{Tool: core.ToolClaude, ID: "asset-session"}, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil || len(listed.Assets) != 4 {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	cached, err := service.load(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, err := (&adapter.Claude{Root: service.Roots.Claude}).ReadAssets(cached.Session, view.Boundary, view.Digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(directory, "two.bin")); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	marker := filepath.Join(parent, "keep.bin")
	if err = os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(parent, "failed-extraction")
	if err = copyRecordedAssets(&listed, read.Assets, out); err == nil {
		t.Fatal("a local source disappearing during extraction was ignored")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed extraction left partial output")
	}
	if keep, err := os.ReadFile(marker); err != nil || string(keep) != "keep" {
		t.Fatal("cleanup touched an unrelated destination file")
	}
}

func TestAssetsSourceStoreAliasCannotBeAnOutputParent(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "store-alias")
	if err = os.Symlink(service.Roots.Claude, alias); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	out := filepath.Join(alias, "must-not-write")
	if _, err = service.Assets(view.ID, []string{listed.Assets[0].RecordID}, out); err == nil {
		t.Fatal("source-store alias bypassed destination confinement")
	}
	if _, err = os.Stat(filepath.Join(service.Roots.Claude, "must-not-write")); !os.IsNotExist(err) {
		t.Fatal("output through an alias modified the source store")
	}
}

func TestAssetsCannotCreateAMissingConfiguredStore(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	listed, err := service.Assets(view.ID, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	service.Roots.Copilot = filepath.Join(t.TempDir(), "missing-store")
	if _, err = service.Assets(view.ID, []string{listed.Assets[0].RecordID}, service.Roots.Copilot); err == nil {
		t.Fatal("extraction created a configured source store")
	}
	if _, err = os.Stat(service.Roots.Copilot); !os.IsNotExist(err) {
		t.Fatal("a missing source store was created")
	}
}
