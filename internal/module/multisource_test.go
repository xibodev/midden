package module

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const (
	fixtureCopilotID  = "22222222-3333-4444-5555-666666666666"
	fixtureOpencodeID = "ses_fixture_opencode"
)

func writeSyntheticCopilotStore(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "copilot-store")
	if err := os.MkdirAll(filepath.Join(root, "session-state", fixtureCopilotID), 0o755); err != nil {
		t.Fatal(err)
	}
	db := openFixtureDB(t, filepath.Join(root, "session-store.db"))
	execFixtureSQL(t, db,
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT, summary TEXT, repository TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE turns (session_id TEXT, turn_index INTEGER, user_message TEXT)`,
		`INSERT INTO sessions VALUES ('`+fixtureCopilotID+`', 'E:\synthetic-copilot', 'Recover Copilot context safely', 'fixture/repo', '2026-01-02T00:00:00Z', '2026-01-02T00:01:00Z')`,
		`INSERT INTO turns VALUES ('`+fixtureCopilotID+`', 1, 'How should recovery work?')`,
		`INSERT INTO turns VALUES ('`+fixtureCopilotID+`', 2, 'Assay before extracting evidence')`,
	)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	events := strings.Join([]string{
		`{"type":"user.message","timestamp":"2026-01-02T00:00:00Z","data":{"content":"How should recovery work?"}}`,
		`{"type":"assistant.message","timestamp":"2026-01-02T00:00:05Z","data":{"content":"Assay before extracting bounded evidence."}}`,
		`{"type":"tool.result","timestamp":"2026-01-02T00:00:06Z","data":{"content":"fixture output"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "session-state", fixtureCopilotID, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeSyntheticOpencodeStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db := openFixtureDB(t, path)
	execFixtureSQL(t, db,
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER, time_archived INTEGER, parent_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, session_id TEXT, message_id TEXT, time_created INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('`+fixtureOpencodeID+`', 'E:/synthetic-opencode', 'Recover OpenCode context safely', 1767312000000, 1767312060000, 0, NULL)`,
		`INSERT INTO message VALUES ('msg_1', '`+fixtureOpencodeID+`', 1767312000000, '{"role":"user","time":{"created":1767312000000}}')`,
		`INSERT INTO message VALUES ('msg_2', '`+fixtureOpencodeID+`', 1767312005000, '{"role":"assistant","time":{"created":1767312005000}}')`,
		`INSERT INTO part VALUES ('part_1', '`+fixtureOpencodeID+`', 'msg_1', 1767312000000, '{"type":"text","text":"How should recovery work?"}')`,
		`INSERT INTO part VALUES ('part_2', '`+fixtureOpencodeID+`', 'msg_2', 1767312005000, '{"type":"text","text":"Assay before extracting bounded evidence."}')`,
	)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func openFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}

func execFixtureSQL(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("fixture SQL failed: %v\n%s", err, statement)
		}
	}
}

func TestExplicitRootsListAndAssayEverySupportedSourceReadOnly(t *testing.T) {
	claude := writeSyntheticClaudeStore(t)
	copilot := writeSyntheticCopilotStore(t)
	opencode := writeSyntheticOpencodeStore(t)
	roots := map[string]Root{
		RootClaude:   {Path: claude, Mode: "ro"},
		RootCopilot:  {Path: copilot, Mode: "ro"},
		RootOpencode: {Path: opencode, Mode: "ro"},
	}
	before := snapshotSources(t, claude, copilot, opencode)

	wantIDs := map[string]string{
		"claude":   "11111111-2222-3333-4444-555555555555",
		"copilot":  fixtureCopilotID,
		"opencode": fixtureOpencodeID,
	}
	for tool, wantID := range wantIDs {
		t.Run(tool, func(t *testing.T) {
			input, _ := json.Marshal(AssayRequest{Tool: tool, IDs: []string{wantID}, IncludeNoise: true, MaxSessions: 5, MaxCandidates: 5})
			for _, capability := range []string{CapSessionsList, CapSessionsAssay} {
				env := Invoke(Request{
					Protocol: ProtocolID, Capability: capability, RequestID: "req-" + tool,
					Input: input, Roots: roots, ExplicitSourceRoots: true,
				})
				if !env.OK {
					t.Fatalf("%s failed: %+v", capability, env.Error)
				}
				if capability == CapSessionsList {
					var result ListResult
					if err := json.Unmarshal(env.Result, &result); err != nil {
						t.Fatal(err)
					}
					if len(result.Sessions) != 1 || result.Sessions[0].SessionID != wantID {
						t.Fatalf("list sessions = %#v, want %s", result.Sessions, wantID)
					}
					if len(result.StoresRead) != 3 || result.PartialInventory {
						t.Fatalf("coverage read=%v partial=%v", result.StoresRead, result.PartialInventory)
					}
				} else {
					var result AssayResult
					if err := json.Unmarshal(env.Result, &result); err != nil {
						t.Fatal(err)
					}
					if result.Assayed != 1 || len(result.Sessions) != 1 || result.Sessions[0].SessionID != wantID {
						t.Fatalf("assay result = %#v, want %s", result, wantID)
					}
					if result.Sessions[0].TotalRecords == 0 {
						t.Fatal("synthetic source produced an empty assay")
					}
				}
			}
		})
	}

	after := snapshotSources(t, claude, copilot, opencode)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("list/assay modified source stores\nbefore: %v\nafter:  %v", before, after)
	}
}

func TestExplicitRootOutcomesAreStructuredAndSourceScoped(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "claude")
		if err := os.MkdirAll(filepath.Join(root, "projects"), 0o755); err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(AssayRequest{Tool: "claude"})
		env := Invoke(Request{Protocol: ProtocolID, Capability: CapSessionsList, RequestID: "empty", Input: input,
			Roots: map[string]Root{RootClaude: {Path: root, Mode: "ro"}}, ExplicitSourceRoots: true})
		if !env.OK {
			t.Fatalf("empty visible store failed: %+v", env.Error)
		}
		var result ListResult
		if err := json.Unmarshal(env.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.Total != 0 || result.Matched != 0 || len(result.StoresRead) != 1 || result.StoresRead[0] != "claude" || result.PartialInventory {
			t.Fatalf("empty result is not self-describing: %#v", result)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "opencode.db")
		if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
		input, _ := json.Marshal(AssayRequest{Tool: "opencode"})
		env := Invoke(Request{Protocol: ProtocolID, Capability: CapSessionsList, RequestID: "malformed", Input: input,
			Roots: map[string]Root{RootOpencode: {Path: path, Mode: "ro"}}, ExplicitSourceRoots: true})
		if !env.OK {
			t.Fatalf("one malformed source should return partial inventory: %+v", env.Error)
		}
		var result ListResult
		if err := json.Unmarshal(env.Result, &result); err != nil {
			t.Fatal(err)
		}
		if !result.PartialInventory || len(result.StoresUnavailable) != 1 || result.StoresUnavailable[0] != "opencode" {
			t.Fatalf("malformed source is not scoped structurally: %#v", result)
		}
		if len(env.Warnings) == 0 || !strings.Contains(strings.Join(env.Warnings, " "), "opencode") {
			t.Fatalf("malformed source warning lacks source: %v", env.Warnings)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		input, _ := json.Marshal(AssayRequest{Tool: "copilot", IDs: []string{fixtureCopilotID}})
		env := Invoke(Request{Protocol: ProtocolID, Capability: CapSessionsAssay, RequestID: "unavailable", Input: input,
			Roots: map[string]Root{}, ExplicitSourceRoots: true})
		if env.OK || env.Error == nil || env.Error.Code != ErrNoSourceStores {
			t.Fatalf("unavailable explicit source = %#v, want no_source_stores", env)
		}
		expected, _ := env.Error.Details["expected_roots"].([]string)
		if len(expected) != 3 {
			t.Fatalf("unavailable error lacks expected source roots: %#v", env.Error.Details)
		}
	})
}

func snapshotSources(t *testing.T, paths ...string) []string {
	t.Helper()
	var out []string
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			out = append(out, snapshotFile(t, root, filepath.Base(root)))
			continue
		}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, snapshotFile(t, path, filepath.Base(root)+"/"+filepath.ToSlash(rel)))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

func snapshotFile(t *testing.T, path, label string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return label + "|" + hex.EncodeToString(sum[:]) + "|" + info.ModTime().UTC().Format(time.RFC3339Nano)
}
