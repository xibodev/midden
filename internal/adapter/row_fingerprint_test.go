package adapter

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

func rowFingerprintFixture(t *testing.T) (*sql.DB, *Opencode, core.Session) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id   string
		time int64
	}{{"row-a", 1767225600000}, {"row-b", 1767225610000}} {
		if _, err = db.Exec(`INSERT INTO part VALUES (?, 'ses_synthetic', ?, ?)`, row.id, row.time,
			`{"type":"file","filename":"same.png","mime":"image/png","url":"data:image/png;base64,aGk="}`); err != nil {
			t.Fatal(err)
		}
	}
	return db, &Opencode{DB: path}, core.Session{Tool: core.ToolOpencode, ID: "ses_synthetic"}
}

func TestOpencodeFingerprintIncludesRowIdentityAndTimestamp(t *testing.T) {
	for _, edit := range []struct {
		name  string
		query string
	}{
		{"timestamp without reordering", `UPDATE part SET time_created = time_created + 1000 WHERE id = 'row-a'`},
		{"id without reordering", `UPDATE part SET id = 'row-aa' WHERE id = 'row-a'`},
	} {
		t.Run(edit.name, func(t *testing.T) {
			db, reader, session := rowFingerprintFixture(t)
			before, err := reader.ReadEvidence(session, assay.Selection{MaxRecords: 4, MaxChars: 256})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(edit.query); err != nil {
				t.Fatal(err)
			}
			after, err := reader.ReadEvidence(session, assay.Selection{MaxRecords: 4, MaxChars: 256, View: &before.SourceView})
			if err != nil {
				t.Fatal(err)
			}
			if after.SourceDigest == before.SourceDigest {
				t.Error("row identity or chronology changed without changing its source digest")
			}
			if _, err = reader.ReadAssets(session, before.SourceView, before.SourceDigest, []int64{1}); err == nil {
				t.Error("asset read accepted an edit to an unselected pinned row")
			}
		})
	}
}

func TestOpencodeFingerprintKeepsAppendsOutsideThePinnedRows(t *testing.T) {
	db, reader, session := rowFingerprintFixture(t)
	before, err := reader.ReadEvidence(session, assay.Selection{MaxRecords: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO part VALUES ('row-c', 'ses_synthetic', 1767225620000, '{"type":"text","text":"later append"}')`); err != nil {
		t.Fatal(err)
	}
	after, err := reader.ReadEvidence(session, assay.Selection{MaxRecords: 4, View: &before.SourceView})
	if err != nil || after.SourceDigest != before.SourceDigest || after.TotalRecords != 2 || !after.LastTime.Equal(before.LastTime) {
		t.Fatalf("append changed the pinned snapshot: %+v %v", after, err)
	}
	assets, err := reader.ReadAssets(session, before.SourceView, before.SourceDigest, nil)
	if err != nil || len(assets.Assets) != 2 {
		t.Fatalf("evidence and assets disagree on the pinned rows: %+v %v", assets, err)
	}
	legacy := before.SourceView
	legacy.Kind = "record-prefix-v1"
	if _, err = reader.ReadEvidence(session, assay.Selection{View: &legacy}); err == nil {
		t.Error("legacy payload-only row boundaries were silently accepted")
	}
	if _, err = reader.ReadAssets(session, legacy, before.SourceDigest, nil); err == nil {
		t.Error("asset reader silently accepted a legacy row boundary")
	}
}

func TestOpencodeFingerprintRejectsIncompleteRows(t *testing.T) {
	for _, column := range []string{"id", "time_created", "data"} {
		t.Run(column, func(t *testing.T) {
			db, reader, session := rowFingerprintFixture(t)
			if _, err := db.Exec(`UPDATE part SET ` + column + ` = NULL WHERE id = 'row-a'`); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.ReadEvidence(session, assay.Selection{}); err == nil {
				t.Fatal("an incomplete row was skipped or fingerprinted without its metadata")
			}
		})
	}
}
