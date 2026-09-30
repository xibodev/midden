package adapter

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCopilotUsageDoesNotSumOtherSessionPrefixes(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "session-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE assistant_usage_events(session_id TEXT,input_tokens INTEGER,output_tokens INTEGER,cache_read_tokens INTEGER,cache_write_tokens INTEGER,reasoning_tokens INTEGER,total_nano_aiu INTEGER,duration_ms INTEGER,model TEXT,created_at TEXT);
INSERT INTO assistant_usage_events VALUES('selected',10,2,0,0,0,1000000000,100,'synthetic','2026-01-01');
INSERT INTO assistant_usage_events VALUES('selected-other',999,999,0,0,0,99000000000,100,'synthetic','2026-01-02');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	reader := &Copilot{Root: root}
	usage, err := reader.Usage("selected")
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 10 || usage.AIU != 1 {
		t.Fatalf("usage widened source scope: %+v", usage)
	}
	if _, err = os.Stat(filepath.Join(root, "session-store.db")); err != nil {
		t.Fatal(err)
	}
}
