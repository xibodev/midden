package index

import (
	"testing"

	"github.com/xibodev/midden/internal/core"
)

func TestIndexedSessionLookupKeepsExactIDs(t *testing.T) {
	db, err := OpenAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.PutSessions([]core.Session{{Tool: core.ToolClaude, ID: "chosen", Dir: "work"}, {Tool: core.ToolClaude, ID: "chosen-other", Dir: "work"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Sessions(core.Scope{IDs: []string{"chosen"}, IncludeNoise: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "chosen" {
		t.Fatal("exact ids widened into other indexed sessions")
	}
}
