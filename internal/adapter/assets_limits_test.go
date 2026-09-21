package adapter

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

func TestAssetsMalformedPayloadCannotBecomeEmptyFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tool   core.Tool
		record any
	}{
		{"null base64", core.ToolCopilot, assetTestBinary(map[string]any{"base64": nil})},
		{"typed encoded data", core.ToolCopilot, assetTestBinary(map[string]any{"encoding": "base64", "data": 17})},
		{"unknown encoding", core.ToolCopilot, assetTestBinary(map[string]any{"encoding": "hex", "data": "1234"})},
		{"typed claude data", core.ToolClaude, map[string]any{"type": "user", "message": map[string]any{"content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": 17}},
		}}}},
		{"null claude data", core.ToolClaude, map[string]any{"type": "user", "message": map[string]any{"content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": nil}},
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, root, boundary, digest := assetTestFile(t, tc.tool, tc.record)
			var reader AssetReader = &Copilot{Root: root}
			if tc.tool == core.ToolClaude {
				reader = &Claude{Root: root}
			}
			read, err := reader.ReadAssets(session, boundary, digest, nil)
			if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "unavailable" || len(read.Omissions) != 1 {
				t.Fatalf("malformed payload produced content: %+v %v", read, err)
			}
			if _, err = read.Assets[0].WriteTo(io.Discard); err == nil {
				t.Fatal("malformed payload was copied as an empty file")
			}
		})
	}
}

func TestAssetsExplicitEmptyBase64IsAnEmptyAsset(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot, assetTestBinary(map[string]any{"base64": ""}))
	read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "embedded" || read.Assets[0].Digest != assetTestDigest(nil) {
		t.Fatalf("explicit empty payload was lost: %+v %v", read, err)
	}
	if count, err := read.Assets[0].WriteTo(io.Discard); count != 0 || err != nil {
		t.Fatalf("copy empty payload: %d %v", count, err)
	}
}

func TestAssetsDecodedSizeBoundaryIsEnforced(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{{"exact limit", 16 << 20}, {"over limit", (16 << 20) + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			size := tc.size
			payload := bytes.Repeat([]byte{0x5a}, size)
			session, root, boundary, digest := assetTestFile(t, core.ToolCopilot,
				assetTestBinary(map[string]any{"encoding": "base64", "data": base64.StdEncoding.EncodeToString(payload)}))
			read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
			if err != nil || len(read.Assets) != 1 {
				t.Fatalf("read=%+v err=%v", read, err)
			}
			if size == 16<<20 {
				if read.Assets[0].Status != "embedded" || read.Assets[0].Bytes != int64(size) {
					t.Fatal("the supported size boundary was rejected")
				}
				if n, err := read.Assets[0].WriteTo(io.Discard); err != nil || n != int64(size) {
					t.Fatalf("copy=%d err=%v", n, err)
				}
			} else if read.Assets[0].Status != "unavailable" || !strings.Contains(assetTestReasons(read), "16 MiB") {
				t.Fatal("oversized decoded content was accepted")
			}
		})
	}
}

func TestAssetsLocalAndAggregateByteLimits(t *testing.T) {
	session, root, _, _ := assetTestFile(t, core.ToolCopilot)
	file, err := os.Create(filepath.Join(session.Dir, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(16 << 20); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	raw := bytes.Repeat([]byte(`{"type":"session.binary_asset","data":{"path":"large.bin"}}`+"\n"), 5)
	if err = os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reader := &Copilot{Root: root}
	boundary := assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(len(raw)), Records: 5}
	digest := assetTestDigest(raw)
	read, err := reader.ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 5 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	for _, asset := range read.Assets[:4] {
		if asset.Status != "available" {
			t.Fatal("content below the aggregate limit was rejected")
		}
	}
	if read.Assets[4].Status != "unavailable" || !strings.Contains(assetTestReasons(read), "64 MiB") {
		t.Fatal("aggregate copied data was not bounded")
	}
	read, err = reader.ReadAssets(session, boundary, digest, []int64{4})
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "available" || read.Assets[0].RecordIndex != 4 {
		t.Fatalf("selection did not narrow aggregate content: %+v %v", read, err)
	}
	if err = os.Truncate(filepath.Join(session.Dir, "large.bin"), (16<<20)+1); err != nil {
		t.Fatal(err)
	}
	var copied bytes.Buffer
	if _, err = read.Assets[0].WriteTo(&copied); err == nil || copied.Len() != 0 {
		t.Fatal("a local file growing past the limit was copied")
	}
	read, err = reader.ReadAssets(session, boundary, digest, []int64{4})
	if err != nil || read.Assets[0].Status != "unavailable" || !strings.Contains(assetTestReasons(read), "16 MiB") {
		t.Fatalf("oversized local file was not explicitly omitted: %+v %v", read, err)
	}
}

func TestAssetsCountLimitDoesNotSilentlyTruncate(t *testing.T) {
	attachments := make([]any, 257)
	for i := range attachments {
		attachments[i] = map[string]any{"base64": "aGk="}
	}
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot,
		map[string]any{"type": "user.message", "data": map[string]any{"attachments": attachments}})
	if read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil); err == nil || len(read.Assets) != 0 {
		t.Fatal("oversized listing returned an apparently complete asset set")
	}
}

func TestAssetsEmbeddedBytesComeFromVerifiedRead(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot, assetTestBinary(map[string]any{"url": "data:text/plain;base64,cGlubmVk"}))
	read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(session.TranscriptPath, []byte(`{"type":"session.binary_asset","data":{"base64":"cmVwbGFjZWQ="}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var copied bytes.Buffer
	if _, err = read.Assets[0].WriteTo(&copied); err != nil || copied.String() != "pinned" {
		t.Fatalf("extraction reread unverified raw data: %q %v", copied.String(), err)
	}
}

func TestAssetsLocalFileURIs(t *testing.T) {
	for _, scheme := range []string{"file", "localfile"} {
		t.Run(scheme, func(t *testing.T) {
			session, root, _, _ := assetTestFile(t, core.ToolCopilot)
			local := filepath.Join(session.Dir, "recorded image.png")
			if err := os.WriteFile(local, []byte("image"), 0600); err != nil {
				t.Fatal(err)
			}
			uriPath := filepath.ToSlash(local)
			if !strings.HasPrefix(uriPath, "/") {
				uriPath = "/" + uriPath
			}
			raw, err := json.Marshal(assetTestBinary(map[string]any{"url": (&url.URL{Scheme: scheme, Path: uriPath}).String()}))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			boundary := assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(len(raw)), Records: 1}
			read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, assetTestDigest(raw), nil)
			if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "available" {
				t.Fatalf("local file URI was not resolved: %+v %v", read, err)
			}
		})
	}
}

func TestAssetsRootReplacementCannotEscape(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot, assetTestBinary(map[string]any{"path": "asset.bin"}))
	originalDir := filepath.Dir(session.TranscriptPath)
	externalDir := t.TempDir()
	testLink := filepath.Join(root, "symlink-probe")
	if err := os.Symlink(externalDir, testLink); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := os.Remove(testLink); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for dir, content := range map[string]string{originalDir: "inside", externalDir: "secret"} {
		path := filepath.Join(dir, "asset.bin")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "available" {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	if err = os.Rename(originalDir, originalDir+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(externalDir, originalDir); err != nil {
		t.Fatal(err)
	}
	var copied bytes.Buffer
	if _, err = read.Assets[0].WriteTo(&copied); err == nil || copied.Len() != 0 {
		t.Fatalf("replaced source root leaked external data: %q %v", copied.String(), err)
	}
}

func TestAssetsOpencodeRejectsTruncatedAndOversizedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE part (id TEXT, session_id TEXT, time_created INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO part VALUES ('one', 'ses_synthetic', 1, '{"type":"file","url":"data:text/plain;base64,aGk="}')`); err != nil {
		t.Fatal(err)
	}
	reader := &Opencode{DB: path}
	session := core.Session{Tool: core.ToolOpencode, ID: "ses_synthetic"}
	view, err := reader.ReadEvidence(session, assay.Selection{MaxRecords: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM part`); err != nil {
		t.Fatal(err)
	}
	if read, err := reader.ReadAssets(session, view.SourceView, view.SourceDigest, nil); err == nil || len(read.Assets) != 0 {
		t.Fatal("truncated database view returned assets")
	}
	if _, err = db.Exec(`INSERT INTO part VALUES ('one', 'ses_synthetic', 1, ?)`, strings.Repeat("x", (24<<20)+1)); err != nil {
		t.Fatal(err)
	}
	if read, err := reader.ReadAssets(session, view.SourceView, view.SourceDigest, nil); err == nil || !strings.Contains(err.Error(), "24 MiB") || len(read.Assets) != 0 {
		t.Fatalf("oversized database row was not rejected: %v", err)
	}
}

func TestAssetsDataURLTakesPriorityOverUnavailableReference(t *testing.T) {
	for _, tc := range []struct {
		field string
		value string
	}{
		{"dataUrl", "data:text/plain;base64,aGk="},
		{"url", "data:text/plain;base64,aGk="},
		{"content", "data:text/plain;base64,aGk="},
		{"data", "DATA:text/plain;BASE64,aGk="},
	} {
		t.Run(tc.field, func(t *testing.T) {
			session, root, boundary, digest := assetTestFile(t, core.ToolCopilot,
				assetTestBinary(map[string]any{"path": "missing.bin", tc.field: tc.value}))
			read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
			if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "embedded" || read.Assets[0].Bytes != 2 {
				t.Fatalf("explicit recorded payload was ignored: %+v %v", read, err)
			}
		})
	}
}

func TestAssetsLocalNameDoesNotExposeDirectories(t *testing.T) {
	session, root, _, _ := assetTestFile(t, core.ToolCopilot)
	path := filepath.Join(session.Dir, "local.png")
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(assetTestBinary(map[string]any{"path": path}))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	boundary := assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(len(raw)), Records: 1}
	read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, assetTestDigest(raw), nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Name != "local.png" {
		t.Fatalf("lost basename: %+v %v", read, err)
	}
	metadata, err := json.Marshal(read)
	if err != nil || bytes.Contains(metadata, []byte("workspace")) {
		t.Fatal("local directory leaked into metadata")
	}
}

func TestAssetsParserStopsAfterFirstExcessAsset(t *testing.T) {
	attachments := strings.Repeat(`{"base64":"aGk="},`, 4096) + `{"base64":"aGk="}`
	specs, err := parseAssetRecord(core.ToolCopilot, []byte(`{"attachments":[`+attachments+`]}`))
	if err != nil || len(specs) != 257 {
		t.Fatalf("parser retained more than one excess asset: %d %v", len(specs), err)
	}
}

func TestAssetsSelectionStillValidatesUnselectedRecords(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot,
		assetTestBinary(map[string]any{"base64": "b25l"}),
		assetTestBinary(map[string]any{"base64": "dHdv"}))
	raw, err := os.ReadFile(session.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(session.TranscriptPath, bytes.Replace(raw, []byte("b25l"), []byte("dHdv"), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, []int64{1}); err == nil || len(read.Assets) != 0 {
		t.Fatal("selection bypassed validation of an earlier unselected record")
	}
}

func TestAssetsUnsafeReferenceDoesNotCopyAWorkspaceNamesake(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot, assetTestBinary(map[string]any{"path": "image.png"}))
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(session.TranscriptPath), "image.png")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := os.WriteFile(filepath.Join(session.Dir, "image.png"), []byte("different asset"), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "external_reference" {
		t.Fatalf("unsafe reference was replaced by a different file: %+v %v", read, err)
	}
}

func TestAssetsRelativeStoreRootPreservesConfinement(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot, assetTestBinary(map[string]any{"path": "image.png"}))
	if err := os.WriteFile(filepath.Join(filepath.Dir(session.TranscriptPath), "image.png"), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(root))
	relative, err := filepath.Rel(filepath.Dir(root), session.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	session.TranscriptPath = relative
	read, err := (&Copilot{Root: "store"}).ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "available" {
		t.Fatalf("relative store root lost its local assets: %+v %v", read, err)
	}
}
