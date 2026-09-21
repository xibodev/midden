package material

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
)

func TestNonTextAssetsHaveReadableCollectableOwners(t *testing.T) {
	for _, kind := range []string{"image", "large image", "attachment", "file"} {
		t.Run(kind, func(t *testing.T) {
			var service Service
			var source Source
			var payload []byte
			if kind == "file" {
				service, source, payload = nonTextOpencodeFixture(t)
			} else {
				var path string
				service, _, path, payload = materialAssetFixture(t)
				source = Source{Tool: core.ToolClaude, ID: "asset-session"}
				var transcript bytes.Buffer
				encoder := json.NewEncoder(&transcript)
				if err := encoder.Encode(map[string]any{
					"type": "user", "cwd": filepath.Dir(service.State), "sessionId": source.ID,
					"message": map[string]any{"role": "user", "content": strings.Repeat("Synthetic preamble. ", 150)},
				}); err != nil {
					t.Fatal(err)
				}
				var record map[string]any
				if kind == "image" || kind == "large image" {
					record = map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "image", "source": map[string]any{
							"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(payload),
						}},
					}}}
					if kind == "large image" {
						record["padding"] = strings.Repeat("x", (1<<20)+128)
					}
				} else {
					record = map[string]any{"type": "attachment", "attachments": []any{
						map[string]any{"name": "recorded.png", "mimeType": "image/png", "base64": base64.StdEncoding.EncodeToString(payload)},
					}}
				}
				if err := encoder.Encode(record); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, transcript.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			view, err := service.Open(source, ReadOptions{Limit: 8, Chars: 512})
			if err != nil {
				t.Fatal(err)
			}
			listed, err := service.Assets(view.ID, nil, "")
			if err != nil || len(listed.Assets) != 1 {
				t.Fatalf("non-text asset was not listed: %+v %v", listed, err)
			}
			focused, err := service.Read(view.ID, ReadOptions{Records: []string{listed.Assets[0].RecordID}, Limit: 1, Chars: 512, IncludeTools: kind == "large image"})
			if err != nil || len(focused.Records) != 1 {
				t.Fatalf("asset has no focused record owner: %+v %v", focused, err)
			}
			record := focused.Records[0]
			if !strings.Contains(record.Text, "Asset reference only; content has not been inspected.") || strings.Contains(record.Text, base64.StdEncoding.EncodeToString(payload)) {
				t.Fatalf("record fabricates inspection or leaks payload: %q", record.Text)
			}
			out := filepath.Join(t.TempDir(), "collected")
			result, err := service.Collect(CollectOptions{Views: []string{focused.ID}, Records: []string{record.ID}, Out: out, IncludeAssets: true})
			if err != nil || result.RecordCount != 1 || result.CopiedCount != 1 {
				t.Fatalf("non-text collection failed: %+v %v", result, err)
			}
			manifest := assertCollectionAssets(t, out, 1, payload)
			if manifest.Assets[0].RecordID != record.ID || manifest.Assets[0].SourceDigest != focused.Digest {
				t.Fatal("portable asset lost its selected non-text record owner")
			}
			selected := filepath.Join(t.TempDir(), "selected")
			if _, err = Select(out, []string{record.ID}, selected); err != nil {
				t.Fatal(err)
			}
			assertCollectionAssets(t, selected, 1, payload)
		})
	}
}

func nonTextOpencodeFixture(t *testing.T) (Service, Source, []byte) {
	t.Helper()
	root := t.TempDir()
	store := filepath.Join(root, "source")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER, time_archived INTEGER, parent_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO session VALUES ('ses_nontext', ?, 'Synthetic file record', 1767225600000, 1767225600000, NULL, NULL)`, root); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO part VALUES ('file-part', 'ses_nontext', 1767225600000, '{"type":"file","filename":"recorded.bin","mime":"application/octet-stream","url":"data:application/octet-stream;base64,YmluYXJ5"}')`); err != nil {
		t.Fatal(err)
	}
	return Service{State: filepath.Join(root, "state"), Roots: adapter.Roots{Opencode: path, Strict: true}},
		Source{Tool: core.ToolOpencode, ID: "ses_nontext"}, []byte("binary")
}
