package material

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func collectAssetFixture(t *testing.T, includePlain bool) (Service, View, CollectionResult, []byte) {
	t.Helper()
	service, view, _, image := materialAssetFixture(t)
	ids := []string{view.Records[1].ID}
	if includePlain {
		ids = append(ids, view.Records[0].ID)
	}
	result, err := service.Collect(CollectOptions{
		Views: []string{view.ID}, Records: ids,
		Out: filepath.Join(t.TempDir(), "collection"), IncludeAssets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, view, result, image
}

func assertCollectionAssets(t *testing.T, path string, count int, payload []byte) CollectionManifest {
	t.Helper()
	report, err := Verify(path)
	if err != nil || !report.Valid {
		t.Fatalf("collection verification: %+v %v", report, err)
	}
	manifest, err := Manifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Assets) != count || report.AssetCount != count || report.VerifiedAssetCount != count {
		t.Fatalf("asset accounting: %+v %+v", manifest.Assets, report)
	}
	paths := map[string]bool{}
	for _, asset := range manifest.Assets {
		if asset.Status != "copied" || !strings.HasPrefix(asset.Path, "assets/") || !filepath.IsLocal(filepath.FromSlash(asset.Path)) {
			t.Fatalf("asset not portable: %+v", asset)
		}
		if paths[strings.ToLower(asset.Path)] {
			t.Fatal("asset output paths collide")
		}
		paths[strings.ToLower(asset.Path)] = true
		raw, err := os.ReadFile(filepath.Join(path, filepath.FromSlash(asset.Path)))
		if err != nil || !bytes.Equal(raw, payload) || asset.Digest != Digest(raw) || asset.Bytes != int64(len(raw)) {
			t.Fatalf("asset bytes or digest changed: %+v %v", asset, err)
		}
	}
	return manifest
}

func TestCollectionAssetsCollectsOnlyExplicitSelectedRecords(t *testing.T) {
	service, view, result, image := collectAssetFixture(t, false)
	if result.RecordCount != 1 || result.AssetCount != 2 || result.CopiedCount != 2 || result.CopiedBytes != int64(2*len(image)) || result.OmissionCount != 0 {
		t.Fatalf("wrong collection result: %+v", result)
	}
	manifest := assertCollectionAssets(t, result.Path, 2, image)
	for _, asset := range manifest.Assets {
		if asset.RecordID != view.Records[1].ID || asset.SourceDigest != view.Digest {
			t.Fatalf("asset was detached from the selected excerpt: %+v", asset)
		}
	}
	if err := os.Rename(service.Roots.Claude, service.Roots.Claude+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(service.State, service.State+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "portable")
	if err := os.Rename(result.Path, moved); err != nil {
		t.Fatal(err)
	}
	assertCollectionAssets(t, moved, 2, image)
	records, err := ReadCollection(moved)
	if err != nil || len(records) != 1 || records[0].ID != view.Records[1].ID {
		t.Fatalf("portable record changed: %+v %v", records, err)
	}
}

func TestCollectionAssetsRequireExplicitSelection(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	out := filepath.Join(t.TempDir(), "must-not-exist")
	if _, err := service.Collect(CollectOptions{Views: []string{view.ID}, Out: out, IncludeAssets: true}); err == nil {
		t.Fatal("asset collection accepted implicit source-record selection")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("rejected selection created output")
	}
	result, err := service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[0].ID}, Out: out, IncludeAssets: true})
	if err != nil || result.AssetCount != 0 || result.CopiedCount != 0 {
		t.Fatalf("unselected assets were collected: %+v %v", result, err)
	}
}

func TestCollectionAssetsSelectAndExportPreserveContent(t *testing.T) {
	_, view, full, image := collectAssetFixture(t, true)
	selected := filepath.Join(t.TempDir(), "selected")
	result, err := Select(full.Path, []string{view.Records[1].ID}, selected)
	if err != nil || result.AssetCount != 2 || result.RecordCount != 1 {
		t.Fatalf("select=%+v err=%v", result, err)
	}
	before := assertCollectionAssets(t, selected, 2, image)
	without := filepath.Join(t.TempDir(), "text-only")
	if _, err = Select(full.Path, []string{view.Records[0].ID}, without); err != nil {
		t.Fatal(err)
	}
	m, err := Manifest(without)
	if err != nil || len(m.Assets) != 0 || len(m.AssetOmissions) != 0 {
		t.Fatalf("select retained unrelated assets: %+v %v", m, err)
	}
	exported := filepath.Join(t.TempDir(), "exported")
	if err = Export(selected, exported, "directory"); err != nil {
		t.Fatal(err)
	}
	after := assertCollectionAssets(t, exported, 2, image)
	if !reflect.DeepEqual(before.Assets, after.Assets) {
		t.Fatal("directory export changed asset identity")
	}
	for _, format := range []string{"jsonl", "markdown"} {
		out := filepath.Join(t.TempDir(), "flat")
		if err = Export(selected, out, format); err == nil || !strings.Contains(err.Error(), "directory") {
			t.Fatal("text-only export silently dropped assets")
		}
		if _, err = os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("unsupported asset export left output")
		}
	}
}

func TestCollectionAssetsMergeDeduplicatesAndRenamesDeterministically(t *testing.T) {
	_, view, first, image := collectAssetFixture(t, false)
	_, _, second, _ := collectAssetFixture(t, false)
	selected := filepath.Join(t.TempDir(), "duplicate")
	if _, err := Select(first.Path, []string{view.Records[1].ID}, selected); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(t.TempDir(), "merged-one")
	two := filepath.Join(t.TempDir(), "merged-two")
	a, err := Merge([]string{first.Path, second.Path, selected}, one)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Merge([]string{selected, second.Path, first.Path}, two)
	if err != nil {
		t.Fatal(err)
	}
	if a.RecordCount != 2 || a.AssetCount != 4 || a.Digest != b.Digest {
		t.Fatalf("merge accounting/order changed: %+v %+v", a, b)
	}
	m1 := assertCollectionAssets(t, one, 4, image)
	m2 := assertCollectionAssets(t, two, 4, image)
	if !reflect.DeepEqual(m1, m2) {
		t.Fatal("merge manifests depend on input order")
	}
}

func TestCollectionAssetsPreserveMissingAndExternalOmissions(t *testing.T) {
	service, _, path, image := materialAssetFixture(t)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"user","message":{"content":"Recorded references."},"attachments":[{"name":"missing.png","path":"missing.png"},{"name":"remote.png","url":"https://example.invalid/remote.png"}]}` + "\n")
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("append fixture: %v %v", err, closeErr)
	}
	view, err := service.Open(Source{Tool: "claude", ID: "asset-session"}, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	full, err := service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[1].ID, view.Records[2].ID}, Out: filepath.Join(t.TempDir(), "all"), IncludeAssets: true})
	if err != nil || full.CopiedCount != 2 || full.AssetCount != 4 || full.OmissionCount != 2 {
		t.Fatalf("collect=%+v err=%v", full, err)
	}
	refs := filepath.Join(t.TempDir(), "references")
	if _, err = Select(full.Path, []string{view.Records[2].ID}, refs); err != nil {
		t.Fatal(err)
	}
	m, err := Manifest(refs)
	if err != nil || len(m.Assets) != 2 || len(m.AssetOmissions) != 2 {
		t.Fatalf("references or omissions lost: %+v %v", m, err)
	}
	statuses := map[string]bool{}
	for _, asset := range m.Assets {
		statuses[asset.Status] = true
		if asset.Path != "" {
			t.Fatal("reference metadata invented a portable file")
		}
	}
	if !statuses["unavailable"] || !statuses["external_reference"] {
		t.Fatalf("reference statuses changed: %v", statuses)
	}
	for _, omission := range m.AssetOmissions {
		if omission.RecordID != view.Records[2].ID || omission.SourceDigest != view.Digest || omission.Reason == "" {
			t.Fatalf("omission lost provenance: %+v", omission)
		}
	}
	onlyImages := filepath.Join(t.TempDir(), "images")
	if _, err = Select(full.Path, []string{view.Records[1].ID}, onlyImages); err != nil {
		t.Fatal(err)
	}
	assertCollectionAssets(t, onlyImages, 2, image)
	merged := filepath.Join(t.TempDir(), "merged")
	if _, err = Merge([]string{refs, onlyImages}, merged); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(t.TempDir(), "exported")
	if err = Export(merged, exported, "directory"); err != nil {
		t.Fatal(err)
	}
	final, err := Manifest(exported)
	if err != nil || len(final.AssetOmissions) != 2 || len(final.Assets) != 4 {
		t.Fatalf("transform discarded omissions: %+v %v", final, err)
	}
	if report, err := Verify(exported); err != nil || !report.Valid || report.VerifiedAssetCount != 2 {
		t.Fatalf("reference-bearing collection is invalid: %+v %v", report, err)
	}
}

func writeCollectionTestManifest(t *testing.T, path string, manifest CollectionManifest) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionAssetsRejectTamperingEscapesAndSymlinks(t *testing.T) {
	for _, kind := range []string{"digest", "missing", "traversal", "absolute", "symlink", "directory symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			_, view, result, image := collectAssetFixture(t, false)
			m, err := Manifest(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(result.Path, filepath.FromSlash(m.Assets[0].Path))
			outside := filepath.Join(t.TempDir(), "outside.png")
			if err = os.WriteFile(outside, image, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "digest":
				raw := append([]byte{}, image...)
				raw[len(raw)-1] ^= 1
				err = os.WriteFile(original, raw, 0600)
			case "missing":
				err = os.Remove(original)
			case "traversal":
				m.Assets[0].Path = "../outside.png"
			case "absolute":
				m.Assets[0].Path = outside
			case "symlink":
				link := filepath.Join(result.Path, "assets", "link.png")
				if err = os.Symlink(outside, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				m.Assets[0].Path = "assets/link.png"
			case "directory symlink":
				assetsDir := filepath.Join(result.Path, "assets")
				renamed := filepath.Join(result.Path, "original-assets")
				if err = os.Rename(assetsDir, renamed); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink("original-assets", assetsDir); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "oversized":
				err = os.Truncate(original, (16<<20)+1)
				m.Assets[0].Bytes = (16 << 20) + 1
			}
			if err != nil {
				t.Fatal(err)
			}
			writeCollectionTestManifest(t, result.Path, m)
			report, verifyErr := Verify(result.Path)
			if verifyErr == nil && report.Valid {
				t.Fatal("invalid asset verified successfully")
			}
			out := filepath.Join(t.TempDir(), "rejected")
			if _, err = Select(result.Path, []string{view.Records[1].ID}, out); err == nil {
				t.Fatal("selection accepted an invalid asset")
			}
			if _, err = Merge([]string{result.Path}, out); err == nil {
				t.Fatal("merge accepted an invalid asset")
			}
			if err = Export(result.Path, out, "directory"); err == nil {
				t.Fatal("export accepted an invalid asset")
			}
			if _, err = os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("invalid input created output")
			}
		})
	}
}

func TestCollectionAssetsNoOverwriteIncludingConcurrentEmptyDirectory(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	for i := 0; i < 16; i++ {
		out := filepath.Join(t.TempDir(), "destination")
		start := make(chan struct{})
		var creatorErr, collectorErr error
		var created os.FileInfo
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			creatorErr = os.Mkdir(out, 0700)
			if creatorErr == nil {
				created, creatorErr = os.Stat(out)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			_, collectorErr = service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[1].ID}, Out: out, IncludeAssets: true})
		}()
		close(start)
		wg.Wait()
		if creatorErr == nil {
			if collectorErr == nil {
				t.Fatal("collection replaced a concurrently claimed empty directory")
			}
			after, err := os.Stat(out)
			if err != nil || !os.SameFile(created, after) {
				t.Fatal("concurrent directory identity changed")
			}
			entries, err := os.ReadDir(out)
			if err != nil || len(entries) != 0 {
				t.Fatal("collection wrote into another owner's empty directory")
			}
		} else if !os.IsExist(creatorErr) || collectorErr != nil {
			t.Fatalf("neither claimant succeeded: %v %v", creatorErr, collectorErr)
		}
	}
}

func TestCollectionAssetsUseTheVerifiedBytesAfterSourceMutation(t *testing.T) {
	_, _, collection, image := collectAssetFixture(t, false)
	snapshot, err := verifiedCollection(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Manifest(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range manifest.Assets {
		if err = os.WriteFile(filepath.Join(collection.Path, filepath.FromSlash(asset.Path)), []byte("changed after validation"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(collection.Path, "records.jsonl"), []byte("changed after validation"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "snapshot-copy")
	if _, err = writeCollection(out, snapshot.records, snapshot.manifest, snapshot.assets); err != nil {
		t.Fatal(err)
	}
	assertCollectionAssets(t, out, 2, image)
}

func TestCollectionAssetsRejectSymlinkedMetadataAndRecords(t *testing.T) {
	for _, name := range []string{"manifest.json", "records.jsonl"} {
		t.Run(name, func(t *testing.T) {
			_, _, collection, _ := collectAssetFixture(t, false)
			original := filepath.Join(collection.Path, name)
			external := filepath.Join(t.TempDir(), name)
			if err := os.Rename(original, external); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, original); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if report, err := Verify(collection.Path); err == nil && report.Valid {
				t.Fatal("symlinked collection data was trusted")
			}
			if _, err := ReadCollection(collection.Path); err == nil {
				t.Fatal("record read bypassed collection confinement")
			}
		})
	}
}

func TestCollectionAssetsBoundedBuffersCannotBeBypassed(t *testing.T) {
	buffer := limitedBuffer{limit: 4}
	source := struct{ io.Reader }{strings.NewReader("12345")}
	if _, err := io.Copy(&buffer, source); err == nil || buffer.Len() > 4 {
		t.Fatal("io.Copy bypassed the bounded writer")
	}
}

func TestCollectionAssetsEnforceByteAndCountLimits(t *testing.T) {
	_, _, collection, _ := collectAssetFixture(t, false)
	snapshot, err := verifiedCollection(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("z"), 16<<20)
	base := snapshot.assets[0].metadata
	base.Bytes, base.Digest = int64(len(payload)), Digest(payload)
	assets := []collectionAsset{}
	for i := 0; i < 4; i++ {
		metadata := base
		metadata.ID = fmt.Sprintf("synthetic-asset-%d", i)
		assets = append(assets, collectionAsset{metadata: metadata, data: payload})
	}
	if _, err = prepareCollectionAssets(snapshot.records, assets); err != nil {
		t.Fatalf("exactly 64 MiB should be supported: %v", err)
	}
	over := base
	over.ID, over.Bytes, over.Digest = "one-too-many-bytes", 1, Digest([]byte("x"))
	assets = append(assets, collectionAsset{metadata: over, data: []byte("x")})
	out := filepath.Join(t.TempDir(), "too-large")
	if _, err = writeCollection(out, snapshot.records, snapshot.manifest, assets); err == nil {
		t.Fatal("collection copied more than 64 MiB")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("oversized asset set created partial output")
	}
	assets = nil
	for i := 0; i < 257; i++ {
		metadata := over
		metadata.ID = fmt.Sprintf("small-asset-%d", i)
		assets = append(assets, collectionAsset{metadata: metadata, data: []byte("x")})
	}
	if _, err = prepareCollectionAssets(snapshot.records, assets[:256]); err != nil {
		t.Fatalf("exactly 256 assets should be supported: %v", err)
	}
	if _, err = prepareCollectionAssets(snapshot.records, assets); err == nil {
		t.Fatal("collection asset count was not bounded")
	}
}

func TestCollectionAssetsRejectOversizedManifestRecordsAndViewCache(t *testing.T) {
	for _, kind := range []string{"manifest", "records", "view"} {
		t.Run(kind, func(t *testing.T) {
			service, view, collection, _ := collectAssetFixture(t, false)
			name := filepath.Join(collection.Path, "manifest.json")
			limit := int64(1 << 20)
			switch kind {
			case "records":
				name = filepath.Join(collection.Path, "records.jsonl")
				limit = 32 << 20
			case "view":
				name = filepath.Join(service.State, "views", view.ID+".json")
				limit = 32 << 20
			}
			if err := os.Truncate(name, limit+1); err != nil {
				t.Fatal(err)
			}
			if kind == "view" {
				out := filepath.Join(t.TempDir(), "bad-view")
				if _, err := service.Collect(CollectOptions{Views: []string{view.ID}, Out: out}); err == nil {
					t.Fatal("collection allocated an oversized cached view")
				}
				if _, err := service.Assets(view.ID, nil, ""); err == nil {
					t.Fatal("asset listing allocated an oversized cached view")
				}
			} else if _, err := ReadCollection(collection.Path); err == nil {
				t.Fatal("oversized collection input was read")
			}
		})
	}
}

func TestCollectionAssetsPreserveLegacyMetadataAndRejectAmbiguousProvenance(t *testing.T) {
	_, _, collection, image := collectAssetFixture(t, false)
	manifest, err := Manifest(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Assets {
		manifest.Assets[i].SourceDigest = ""
	}
	writeCollectionTestManifest(t, collection.Path, manifest)
	out := filepath.Join(t.TempDir(), "normalized")
	if err = Export(collection.Path, out, "directory"); err != nil {
		t.Fatal(err)
	}
	normalized := assertCollectionAssets(t, out, 2, image)
	for _, asset := range normalized.Assets {
		if asset.SourceDigest == "" {
			t.Fatal("unambiguous legacy asset provenance was not normalized")
		}
	}
	_, _, other, _ := collectAssetFixture(t, false)
	merged := filepath.Join(t.TempDir(), "merged")
	if _, err = Merge([]string{collection.Path, other.Path}, merged); err != nil {
		t.Fatal(err)
	}
	manifest, err = Manifest(merged)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Assets[0].SourceDigest = ""
	writeCollectionTestManifest(t, merged, manifest)
	if report, err := Verify(merged); err == nil && report.Valid {
		t.Fatal("an asset was guessed to belong to one of two source versions")
	}
}

func TestCollectionAssetsRejectConflictingDuplicateIdentity(t *testing.T) {
	_, _, collection, _ := collectAssetFixture(t, false)
	snapshot, err := verifiedCollection(collection.Path)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := snapshot.assets[0]
	conflicting.data = []byte("different bytes")
	conflicting.metadata.Bytes = int64(len(conflicting.data))
	conflicting.metadata.Digest = Digest(conflicting.data)
	other := filepath.Join(t.TempDir(), "conflict")
	if _, err = writeCollection(other, snapshot.records, snapshot.manifest, []collectionAsset{conflicting}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "must-not-exist")
	if _, err = Merge([]string{collection.Path, other}, out); err == nil {
		t.Fatal("merge silently chose one of two conflicting asset payloads")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("conflicting merge left output")
	}
}

func TestCollectionAssetsCannotWriteIntoSourceStores(t *testing.T) {
	service, view, _, _ := materialAssetFixture(t)
	out := filepath.Join(service.Roots.Claude, "new", "collection")
	if _, err := service.Collect(CollectOptions{Views: []string{view.ID}, Records: []string{view.Records[1].ID}, Out: out, IncludeAssets: true}); err == nil {
		t.Fatal("collection wrote into a read-only source store")
	}
	if _, err := os.Stat(filepath.Dir(out)); !os.IsNotExist(err) {
		t.Fatal("rejected collection created directories inside the source store")
	}
}
