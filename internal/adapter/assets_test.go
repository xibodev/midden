package adapter

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

func assetTestDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assetTestFile(t *testing.T, tool core.Tool, records ...any) (core.Session, string, assay.SourceView, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "store", "session")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{dir, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var data bytes.Buffer
	for _, record := range records {
		if err := json.NewEncoder(&data).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	session := core.Session{Tool: tool, ID: "synthetic-session", Dir: workspace, TranscriptPath: path}
	return session, filepath.Join(root, "store"), assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(data.Len()), Records: int64(len(records))}, assetTestDigest(data.Bytes())
}

func assetTestBinary(data map[string]any) map[string]any {
	return map[string]any{"type": "session.binary_asset", "data": data}
}

func assetTestReasons(read AssetRead) string {
	var reasons []string
	for _, omission := range read.Omissions {
		reasons = append(reasons, omission.Reason)
	}
	return strings.Join(reasons, "\n")
}

func TestAssetsCopilotEmbeddedAndLoggedAttachments(t *testing.T) {
	payload := []byte("synthetic recorded bytes")
	encoded := base64.StdEncoding.EncodeToString(payload)
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot,
		assetTestBinary(map[string]any{"name": "image.png", "mimeType": "image/png", "base64": encoded}),
		map[string]any{"type": "user.message", "data": map[string]any{
			"content": "Ordinary text is not an asset.",
			"attachments": []any{
				map[string]any{"name": "missing.png", "path": "missing.png"},
				map[string]any{"name": "remote.png", "url": "https://example.invalid/image.png"},
			},
		}},
		assetTestBinary(map[string]any{"name": "unknown.png", "payload": encoded}),
	)
	read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Assets) != 4 {
		t.Fatalf("assets=%d, want 4", len(read.Assets))
	}
	for i, want := range []string{"embedded", "unavailable", "external_reference", "unavailable"} {
		if read.Assets[i].Status != want {
			t.Errorf("asset %d status=%q, want %q", i, read.Assets[i].Status, want)
		}
	}
	first := read.Assets[0]
	if first.RecordIndex != 0 || first.Index != 0 || first.MediaType != "image/png" || first.Digest != assetTestDigest(payload) || first.Bytes != int64(len(payload)) {
		t.Fatalf("lost embedded metadata: %+v", first)
	}
	var copied bytes.Buffer
	if n, err := first.WriteTo(&copied); err != nil || n != int64(len(payload)) || !bytes.Equal(copied.Bytes(), payload) {
		t.Fatalf("copy=%q n=%d err=%v", copied.Bytes(), n, err)
	}
	raw, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{encoded, session.TranscriptPath, "https://example.invalid"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("model-facing asset metadata contains a payload or source location")
		}
	}
	if len(read.Omissions) != 3 {
		t.Fatalf("omissions=%d, want explicit missing/external/unknown reasons", len(read.Omissions))
	}
	if _, err := read.Assets[1].WriteTo(io.Discard); err == nil {
		t.Fatal("an unavailable attachment was treated as copied")
	}
}

func TestAssetsClaudeImageBlocksDoNotInterpretText(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolClaude,
		map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "data:image/png;base64,bm90LWFuLWFzc2V0"},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "cGl4ZWw="}},
			map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.invalid/image.png"}},
		}}},
	)
	read, err := (&Claude{Root: root}).ReadAssets(session, boundary, digest, []int64{0})
	if err != nil || len(read.Assets) != 2 {
		t.Fatalf("assets=%+v err=%v", read, err)
	}
	if read.Assets[0].Status != "embedded" || read.Assets[0].Index != 0 || read.Assets[1].Status != "external_reference" {
		t.Fatalf("wrong block interpretation: %+v", read.Assets)
	}
	var copied bytes.Buffer
	if _, err := read.Assets[0].WriteTo(&copied); err != nil || copied.String() != "pixel" {
		t.Fatalf("copied=%q err=%v", copied.String(), err)
	}
}

func TestAssetsRejectInvalidEncodingsAndURIs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		reason string
	}{
		{"invalid base64", map[string]any{"base64": "not!base64"}, "base64"},
		{"noncanonical base64", map[string]any{"base64": "Zh=="}, "base64"},
		{"unsupported URI", map[string]any{"url": "ftp://example.invalid/file.png"}, "unsupported"},
		{"non-base64 data URI", map[string]any{"url": "data:text/plain,not-base64"}, "base64"},
		{"remote file URI", map[string]any{"url": "file://remote.invalid/share/file.png"}, "remote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, root, boundary, digest := assetTestFile(t, core.ToolCopilot, assetTestBinary(tc.fields))
			read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, digest, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(read.Assets) != 1 || read.Assets[0].Status == "embedded" || read.Assets[0].Status == "available" || !strings.Contains(strings.ToLower(assetTestReasons(read)), tc.reason) {
				t.Fatalf("invalid asset was not explicitly rejected: %+v", read)
			}
			if _, err := read.Assets[0].WriteTo(io.Discard); err == nil {
				t.Fatal("rejected asset was writable")
			}
		})
	}
}

func TestAssetsLocalReferencesAreConfined(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(core.Session, string) string
		want string
	}{
		{"session relative", func(_ core.Session, _ string) string { return "near.png" }, "available"},
		{"workspace relative", func(_ core.Session, _ string) string { return "workspace.png" }, "available"},
		{"workspace absolute", func(s core.Session, _ string) string { return filepath.Join(s.Dir, "workspace.png") }, "available"},
		{"store absolute", func(_ core.Session, root string) string { return filepath.Join(root, "store.png") }, "available"},
		{"outside absolute", func(_ core.Session, root string) string { return filepath.Join(filepath.Dir(root), "outside.png") }, "external_reference"},
		{"traversal", func(_ core.Session, _ string) string { return filepath.Join("..", "..", "outside.png") }, "external_reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, root, _, _ := assetTestFile(t, core.ToolCopilot)
			for _, path := range []string{
				filepath.Join(filepath.Dir(session.TranscriptPath), "near.png"),
				filepath.Join(session.Dir, "workspace.png"),
				filepath.Join(root, "store.png"),
				filepath.Join(filepath.Dir(root), "outside.png"),
			} {
				if err := os.WriteFile(path, []byte("recorded local bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := json.Marshal(assetTestBinary(map[string]any{"path": tc.path(session, root)}))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			boundary := assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(len(raw)), Records: 1}
			read, err := (&Copilot{Root: root}).ReadAssets(session, boundary, assetTestDigest(raw), nil)
			if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != tc.want {
				t.Fatalf("read=%+v err=%v, want %s", read, err, tc.want)
			}
			var copied bytes.Buffer
			_, err = read.Assets[0].WriteTo(&copied)
			if tc.want == "available" {
				if err != nil || copied.String() != "recorded local bytes" {
					t.Fatalf("copy=%q err=%v", copied.String(), err)
				}
			} else if err == nil || copied.Len() != 0 {
				t.Fatal("outside reference escaped confinement")
			}
		})
	}
}

func TestAssetsLocalSymlinkCannotEscapeOrSwapAfterListing(t *testing.T) {
	session, root, _, _ := assetTestFile(t, core.ToolCopilot)
	dir := filepath.Dir(session.TranscriptPath)
	inside := filepath.Join(dir, "inside.png")
	outside := filepath.Join(filepath.Dir(root), "outside.png")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("synthetic bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink("inside.png", link); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	raw := []byte(`{"type":"session.binary_asset","data":{"path":"link.png"}}` + "\n")
	if err := os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	boundary := assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(len(raw)), Records: 1}
	reader := &Copilot{Root: root}
	read, err := reader.ReadAssets(session, boundary, assetTestDigest(raw), nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Status != "available" {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var copied bytes.Buffer
	if _, err = read.Assets[0].WriteTo(&copied); err == nil || copied.Len() != 0 {
		t.Fatal("a symlink swap copied outside the permitted root")
	}
	read, err = reader.ReadAssets(session, boundary, assetTestDigest(raw), nil)
	if err != nil || read.Assets[0].Status != "external_reference" {
		t.Fatalf("escaping symlink was not reported as a reference: %+v %v", read, err)
	}
}

func TestAssetsPinnedFileAllowsAppendButRejectsEditsAndTruncation(t *testing.T) {
	session, root, boundary, digest := assetTestFile(t, core.ToolCopilot,
		assetTestBinary(map[string]any{"name": "first.png", "base64": "b25l"}),
	)
	original, err := os.ReadFile(session.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	reader := &Copilot{Root: root}
	appended := append(append([]byte{}, original...), []byte(`{"type":"session.binary_asset","data":{"base64":"dHdv"}}`+"\n")...)
	if err = os.WriteFile(session.TranscriptPath, appended, 0600); err != nil {
		t.Fatal(err)
	}
	read, err := reader.ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 1 || read.Assets[0].Name != "first.png" {
		t.Fatalf("append changed pinned assets: %+v %v", read, err)
	}
	changed := bytes.Replace(original, []byte("b25l"), []byte("dHdv"), 1)
	for _, invalid := range [][]byte{changed, original[:len(original)-1]} {
		if err = os.WriteFile(session.TranscriptPath, invalid, 0600); err != nil {
			t.Fatal(err)
		}
		read, err = reader.ReadAssets(session, boundary, digest, nil)
		if err == nil || len(read.Assets) != 0 {
			t.Fatal("unverified source prefix returned assets")
		}
	}
}

func TestAssetsHashEntireOversizedFileRecord(t *testing.T) {
	session, root, _, _ := assetTestFile(t, core.ToolCopilot)
	raw := []byte(`{"type":"session.binary_asset","data":{"name":"large","base64":"` +
		strings.Repeat("A", (24<<20)+17) + `"}}` + "\n")
	if err := os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	boundary := assay.SourceView{Kind: "file-prefix-v1", Bytes: int64(len(raw)), Records: 1}
	digest := assetTestDigest(raw)
	reader := &Copilot{Root: root}
	read, err := reader.ReadAssets(session, boundary, digest, nil)
	if err != nil || len(read.Assets) != 0 || !strings.Contains(assetTestReasons(read), "24 MiB") {
		t.Fatalf("oversized record was not explicitly omitted: %+v %v", read, err)
	}
	raw[len(raw)-8] = 'B'
	if err = os.WriteFile(session.TranscriptPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if read, err = reader.ReadAssets(session, boundary, digest, nil); err == nil || len(read.Assets) != 0 {
		t.Fatal("an edit after the retained record head escaped prefix verification")
	}
}

func TestAssetsOpencodeUsesPinnedOrderedScannerFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, session string, created int, name string) {
		t.Helper()
		data := fmt.Sprintf(`{"type":"file","filename":%q,"mime":"image/png","url":"data:image/png;base64,cGl4ZWw="}`, name)
		if _, err := db.Exec(`INSERT INTO part VALUES (?, ?, ?, ?)`, id, session, created, data); err != nil {
			t.Fatal(err)
		}
	}
	insert("b", "ses_synthetic", 100, "second.png")
	insert("a", "ses_synthetic", 100, "first.png")
	insert("other", "ses_other", 50, "unrelated.png")
	reader := &Opencode{DB: path}
	session := core.Session{Tool: core.ToolOpencode, ID: "ses_synthetic", Dir: t.TempDir()}
	view, err := reader.ReadEvidence(session, assay.Selection{MaxRecords: 2, MaxChars: 100})
	if err != nil {
		t.Fatal(err)
	}
	insert("c", "ses_synthetic", 200, "appended.png")
	read, err := reader.ReadAssets(session, view.SourceView, view.SourceDigest, nil)
	if err != nil || len(read.Assets) != 2 || read.Assets[0].Name != "first.png" || read.Assets[1].Name != "second.png" {
		t.Fatalf("database prefix/order mismatch: %+v %v", read, err)
	}
	var copied bytes.Buffer
	if _, err = read.Assets[0].WriteTo(&copied); err != nil || copied.String() != "pixel" {
		t.Fatalf("database image copy=%q err=%v", copied.String(), err)
	}
	if _, err = db.Exec(`UPDATE part SET data = replace(data, 'cGl4ZWw=', 'Y2hhbmdl') WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	if read, err = reader.ReadAssets(session, view.SourceView, view.SourceDigest, nil); err == nil || len(read.Assets) != 0 {
		t.Fatal("changed database prefix returned unverified assets")
	}
}
