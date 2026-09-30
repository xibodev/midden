package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xibodev/compa/pkg/providers"
	"github.com/xibodev/compa/pkg/session"
)

const (
	historyTestKey      = "sk_v1_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	historyTestOtherKey = "sk_v1_0000000000000000000000000000000000000000000000000000000000000000"
)

type historyTestSession struct {
	Key      string              `json:"key"`
	Messages []providers.Message `json:"messages"`
	Summary  string              `json:"summary,omitempty"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}

func historyTestFixture(t *testing.T, key string) historyTestSession {
	t.Helper()
	var messages []providers.Message
	if err := json.Unmarshal([]byte(`[
		{"role":"system","content":"context","system_parts":[{"type":"text","text":"cached","cache_control":{"type":"ephemeral"}}]},
		{"role":"user","content":"Read \u4e2d\u6587","created_at":"2026-01-02T03:04:05Z","media":["image-ref"],"attachments":[{"type":"image","ref":"image-ref","url":"https://example.invalid/image","filename":"sample.png","content_type":"image/png"}]},
		{"role":"assistant","content":"","reasoning_content":"inspect the file","model_name":"opaque-model","requested_selection":"selection","served_target":"target","served_identity":"identity","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"sample.txt\"}","thought_signature":"signature"},"extra_content":{"google":{"thought_signature":"extra-signature"},"tool_feedback_explanation":"read first"}}]},
		{"role":"tool","content":"{\"text\":\"synthetic result\"}","tool_call_id":"call-1"},
		{"role":"assistant","content":"Complete.","created_at":"2026-01-02T03:05:06Z"}
	]`), &messages); err != nil {
		t.Fatal(err)
	}
	return historyTestSession{
		Key: key, Messages: messages, Summary: "Earlier context, preserved verbatim.\n",
		Created: time.Date(2026, 1, 1, 1, 2, 3, 0, time.UTC),
		Updated: time.Date(2026, 1, 2, 3, 5, 6, 0, time.UTC),
	}
}

func historyTestWrite(t *testing.T, state, name string, value historyTestSession) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	historyTestWriteBytes(t, filepath.Join(state, "kernel-history", name), data)
	return data
}

func historyTestWriteBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func historyTestOpen(t *testing.T, state string) session.SessionStore {
	t.Helper()
	store, err := openKernelHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("successful open returned no store")
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func historyTestReject(t *testing.T, state, reason string) error {
	t.Helper()
	store, err := openKernelHistory(state)
	if err == nil {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("openKernelHistory accepted %s", reason)
	}
	if store != nil {
		_ = store.Close()
		t.Fatalf("openKernelHistory returned a usable store on %s: %v", reason, err)
	}
	return err
}

func historyTestUnpublished(t *testing.T, state string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(state, "compa-history")); !os.IsNotExist(err) {
		t.Fatalf("failed migration published a target: %v", err)
	}
}

func historyTestSnapshot(t *testing.T, path string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatalf("unexpected non-file in test snapshot: %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = string(data)
	}
	return result
}

func historyTestSameMessages(t *testing.T, got, want []providers.Message) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("history changed:\ngot  %s\nwant %s", a, b)
	}
}

func TestKernelHistoryMigratesCompleteSessions(t *testing.T) {
	state := t.TempDir()
	want := historyTestFixture(t, historyTestKey)
	source := historyTestWrite(t, state, historyTestKey+".json", want)
	empty := historyTestFixture(t, historyTestOtherKey)
	empty.Messages = []providers.Message{}
	historyTestWrite(t, state, "an-old-export-name.json", empty)
	for _, name := range []string{"sessions.json", "model.json", "outcomes.json"} {
		historyTestWriteBytes(t, filepath.Join(state, name), []byte("unrelated UI data"))
	}
	before := historyTestSnapshot(t, filepath.Join(state, "kernel-history"))

	store := historyTestOpen(t, state)
	historyTestSameMessages(t, store.GetHistory(want.Key), want.Messages)
	if got := store.GetSummary(want.Key); got != want.Summary {
		t.Fatalf("summary = %q, want %q", got, want.Summary)
	}
	keys := store.ListSessions()
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{historyTestOtherKey, historyTestKey}) {
		t.Fatalf("opaque keys changed or an empty session was lost: %v", keys)
	}
	if got := store.GetSummary(empty.Key); got != empty.Summary {
		t.Fatalf("empty session summary = %q", got)
	}
	if got := historyTestSnapshot(t, filepath.Join(state, "kernel-history")); !reflect.DeepEqual(got, before) {
		t.Fatal("legacy backup was changed")
	}
	for _, name := range []string{"sessions.json", "model.json", "outcomes.json"} {
		data, err := os.ReadFile(filepath.Join(state, name))
		if err != nil || string(data) != "unrelated UI data" {
			t.Fatalf("unrelated UI file %s was touched: %v", name, err)
		}
	}

	var meta struct {
		Key       string    `json:"key"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	raw, err := os.ReadFile(filepath.Join(state, "compa-history", want.Key+".meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Key != want.Key || !meta.CreatedAt.Equal(want.Created) || !meta.UpdatedAt.Equal(want.Updated) {
		t.Fatalf("session metadata was not preserved: %+v", meta)
	}
	var receipt struct {
		Version       int  `json:"version"`
		LegacyPresent bool `json:"legacy_present"`
		Files         []struct {
			Name   string `json:"name"`
			Key    string `json:"key"`
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"files"`
	}
	raw, err = os.ReadFile(filepath.Join(state, "compa-history", "migration-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Version != 1 || !receipt.LegacyPresent || len(receipt.Files) != 2 {
		t.Fatalf("incomplete migration receipt: %+v", receipt)
	}
	digest := sha256.Sum256(source)
	found := false
	for _, file := range receipt.Files {
		if file.Key == want.Key {
			found = true
			if file.Name != want.Key+".json" || file.Bytes != int64(len(source)) || file.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("receipt does not identify exact source bytes: %+v", file)
			}
		}
	}
	if !found {
		t.Fatal("receipt omitted the migrated session")
	}
}

func TestKernelHistoryReopenNeverReimportsAfterAppend(t *testing.T) {
	state := t.TempDir()
	legacy := historyTestFixture(t, historyTestKey)
	historyTestWrite(t, state, legacy.Key+".json", legacy)
	store := historyTestOpen(t, state)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = historyTestOpen(t, state)
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	next := providers.Message{Role: "user", Content: "A new turn", CreatedAt: &now}
	store.AddFullMessage(legacy.Key, next)
	store.SetSummary(legacy.Key, "New summary")
	if err := store.Save(legacy.Key); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := historyTestSnapshot(t, filepath.Join(state, "compa-history"))
	for range 3 {
		store = historyTestOpen(t, state)
		historyTestSameMessages(t, store.GetHistory(legacy.Key), append(legacy.Messages, next))
		if got := store.GetSummary(legacy.Key); got != "New summary" {
			t.Fatalf("reopen overwrote new summary: %q", got)
		}
	}
	if after := historyTestSnapshot(t, filepath.Join(state, "compa-history")); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only reopen rewrote the migrated target")
	}
}

func TestKernelHistoryReopenPreservesCompactionAndNewSessionMetadata(t *testing.T) {
	state := t.TempDir()
	legacy := historyTestFixture(t, historyTestKey)
	historyTestWrite(t, state, "old.json", legacy)
	store := historyTestOpen(t, state)
	store.TruncateHistory(legacy.Key, 1)
	store.SetSummary(legacy.Key, "Compacted summary")
	store.SetSummary(historyTestOtherKey, "A new session without messages yet")
	if err := store.Save(legacy.Key); err != nil {
		t.Fatal(err)
	}
	before := historyTestSnapshot(t, filepath.Join(state, "compa-history"))
	store = historyTestOpen(t, state)
	historyTestSameMessages(t, store.GetHistory(legacy.Key), legacy.Messages[4:])
	if store.GetSummary(legacy.Key) != "Compacted summary" ||
		store.GetSummary(historyTestOtherKey) != "A new session without messages yet" {
		t.Fatal("reopen restored old metadata instead of respecting the live store")
	}
	if after := historyTestSnapshot(t, filepath.Join(state, "compa-history")); !reflect.DeepEqual(before, after) {
		t.Fatal("reopen changed post-migration compaction or native metadata")
	}
}

func TestKernelHistoryConcurrentStartsNeverMerge(t *testing.T) {
	state := t.TempDir()
	legacy := historyTestFixture(t, historyTestKey)
	historyTestWrite(t, state, "old.json", legacy)
	type result struct {
		store session.SessionStore
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	for range cap(results) {
		go func() {
			<-start
			store, err := openKernelHistory(state)
			results <- result{store, err}
		}()
	}
	close(start)
	successes := 0
	for range cap(results) {
		got := <-results
		if got.err != nil {
			if got.store != nil {
				_ = got.store.Close()
				t.Error("failed concurrent open returned a store")
			}
			continue
		}
		successes++
		historyTestSameMessages(t, got.store.GetHistory(legacy.Key), legacy.Messages)
		if err := got.store.Close(); err != nil {
			t.Error(err)
		}
	}
	if successes == 0 {
		t.Fatal("no concurrent caller published the conversion")
	}
	historyTestSameMessages(t, historyTestOpen(t, state).GetHistory(legacy.Key), legacy.Messages)
}

func TestKernelHistoryFreshAndEmptyStores(t *testing.T) {
	for _, legacyDirectory := range []bool{false, true} {
		for _, emptyTarget := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%v/target=%v", legacyDirectory, emptyTarget), func(t *testing.T) {
				state := filepath.Join(t.TempDir(), "new-state")
				if legacyDirectory {
					if err := os.MkdirAll(filepath.Join(state, "kernel-history"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if emptyTarget {
					if err := os.MkdirAll(filepath.Join(state, "compa-history"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				store := historyTestOpen(t, state)
				store.AddMessage(historyTestOtherKey, "user", "fresh history")
				if err := store.Save(historyTestOtherKey); err != nil {
					t.Fatal(err)
				}
				store = historyTestOpen(t, state)
				if got := store.GetHistory(historyTestOtherKey); len(got) != 1 || got[0].Content != "fresh history" {
					t.Fatalf("fresh history did not survive reopen: %+v", got)
				}
				if !legacyDirectory {
					if _, err := os.Lstat(filepath.Join(state, "kernel-history")); !os.IsNotExist(err) {
						t.Fatalf("fresh open created a legacy directory: %v", err)
					}
				}
			})
		}
	}
}

func TestKernelHistoryRejectsBadLegacyWithoutPublishing(t *testing.T) {
	good, err := json.Marshal(historyTestFixture(t, historyTestKey))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"corrupt JSON":            []byte(`{"key":`),
		"unknown JSON":            []byte(`{"unrelated":"document"}`),
		"unknown field":           bytes.Replace(good, []byte(`"key":`), []byte(`"unknown":true,"key":`), 1),
		"duplicate JSON field":    bytes.Replace(good, []byte(`"key":`), []byte(`"key":"ignored","key":`), 1),
		"case-alias duplicate":    bytes.Replace(good, []byte(`"key":`), []byte(`"Key":"ignored","key":`), 1),
		"trailing JSON":           append(append([]byte{}, good...), []byte(` {}`)...),
		"invalid UTF-8":           bytes.Replace(good, []byte("context"), []byte{0xff}, 1),
		"null content":            bytes.Replace(good, []byte(`"content":"context"`), []byte(`"content":null`), 1),
		"unpaired high surrogate": bytes.Replace(good, []byte(`"content":"context"`), []byte(`"content":"\ud800"`), 1),
		"unpaired low surrogate":  bytes.Replace(good, []byte(`"content":"context"`), []byte(`"content":"\udc00"`), 1),
		"null messages":           []byte(fmt.Sprintf(`{"key":%q,"messages":null,"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`, historyTestKey)),
		"missing messages":        []byte(fmt.Sprintf(`{"key":%q,"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`, historyTestKey)),
		"null message":            []byte(fmt.Sprintf(`{"key":%q,"messages":[null],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`, historyTestKey)),
		"bad timestamp":           bytes.Replace(good, []byte("2026-01-01T01:02:03Z"), []byte("not a time"), 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			historyTestWrite(t, state, "a-good.json", historyTestFixture(t, historyTestOtherKey))
			historyTestWriteBytes(t, filepath.Join(state, "kernel-history", "z-bad.json"), data)
			before := historyTestSnapshot(t, filepath.Join(state, "kernel-history"))
			historyTestReject(t, state, name)
			historyTestUnpublished(t, state)
			if after := historyTestSnapshot(t, filepath.Join(state, "kernel-history")); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected legacy data was modified")
			}
		})
	}
}

func TestKernelHistoryPreservesValidUnicodeWithoutCoercion(t *testing.T) {
	state := t.TempDir()
	fixture := historyTestFixture(t, historyTestKey)
	fixture.Messages[0].Content = "\U0001f600 \ufffd \\ud800"
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("\U0001f600"), []byte(`\ud83d\ude00`), 1)
	historyTestWriteBytes(t, filepath.Join(state, "kernel-history", "escaped.json"), data)
	historyTestSameMessages(t, historyTestOpen(t, state).GetHistory(fixture.Key), fixture.Messages)
}

func TestKernelHistoryRejectsNonopaqueAndDuplicateKeys(t *testing.T) {
	for _, key := range []string{"", "../escape", `..\escape`, "sk_v1_../../escape", "sk_v1_short", "sk_v1_" + strings.Repeat("g", 64), strings.ToUpper(historyTestKey), " " + historyTestKey} {
		t.Run(key, func(t *testing.T) {
			state := t.TempDir()
			historyTestWrite(t, state, "legacy.json", historyTestFixture(t, key))
			historyTestReject(t, state, "nonopaque key")
			historyTestUnpublished(t, state)
		})
	}
	t.Run("duplicate session key", func(t *testing.T) {
		state := t.TempDir()
		fixture := historyTestFixture(t, historyTestKey)
		historyTestWrite(t, state, "first.json", fixture)
		historyTestWrite(t, state, "second.json", fixture)
		err := historyTestReject(t, state, "duplicate session key")
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			t.Fatalf("duplicate key error was not explicit: %v", err)
		}
		historyTestUnpublished(t, state)
	})
}

func TestKernelHistoryRejectsUnrecognizedLegacyEntries(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%v", directory), func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "kernel-history", "unexpected")
			if directory {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				historyTestWriteBytes(t, path, []byte("not a legacy session"))
			}
			historyTestReject(t, state, "unrecognized legacy entry")
			historyTestUnpublished(t, state)
		})
	}
}

func TestKernelHistoryRejectsUnsafeStateAndStorePaths(t *testing.T) {
	t.Run("empty state", func(t *testing.T) {
		historyTestReject(t, "", "empty state path")
	})
	t.Run("parent traversal", func(t *testing.T) {
		base := t.TempDir()
		separator := string(os.PathSeparator)
		state := base + separator + "state" + separator + ".." + separator + "escape"
		historyTestReject(t, state, "state parent traversal")
		entries, err := os.ReadDir(base)
		if err != nil || len(entries) != 0 {
			t.Fatalf("unsafe state path created directories: %v: %v", entries, err)
		}
	})
	for _, name := range []string{"kernel-history", "compa-history", ".compa-history-stage-v1"} {
		t.Run(name+" is a file", func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, name)
			historyTestWriteBytes(t, path, []byte("keep this ordinary file"))
			historyTestReject(t, state, "file in place of directory")
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "keep this ordinary file" {
				t.Fatalf("conflicting file was touched: %v", err)
			}
		})
	}
}

func TestKernelHistoryRejectsTargetConflicts(t *testing.T) {
	for _, receipt := range []string{"", "{broken", `{"version":99,"legacy_present":true,"files":[]}`, `{"version":1,"legacy_present":true,"files":[]}`} {
		t.Run(receipt, func(t *testing.T) {
			state := t.TempDir()
			historyTestWrite(t, state, "old.json", historyTestFixture(t, historyTestKey))
			target := filepath.Join(state, "compa-history")
			historyTestWriteBytes(t, filepath.Join(target, historyTestKey+".jsonl"), []byte("existing new turns\n"))
			if receipt != "" {
				historyTestWriteBytes(t, filepath.Join(target, "migration-v1.json"), []byte(receipt))
			}
			before := historyTestSnapshot(t, target)
			historyTestReject(t, state, "conflicting target")
			if after := historyTestSnapshot(t, target); !reflect.DeepEqual(before, after) {
				t.Fatal("conflicting target was overwritten")
			}
		})
	}
}

func TestKernelHistoryReceiptRejectsChangedOrMissingBackup(t *testing.T) {
	for _, change := range []string{"bytes", "added", "deleted", "directory removed", "appeared after fresh open"} {
		t.Run(change, func(t *testing.T) {
			state := t.TempDir()
			source := filepath.Join(state, "kernel-history", "old.json")
			if change != "appeared after fresh open" {
				historyTestWrite(t, state, "old.json", historyTestFixture(t, historyTestKey))
			}
			store := historyTestOpen(t, state)
			store.AddMessage(historyTestOtherKey, "user", "new data must survive")
			before := historyTestSnapshot(t, filepath.Join(state, "compa-history"))
			switch change {
			case "bytes":
				file, err := os.OpenFile(source, os.O_WRONLY|os.O_APPEND, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteString("\n")
				if err := errors.Join(writeErr, file.Close()); err != nil {
					t.Fatal(err)
				}
			case "added", "appeared after fresh open":
				historyTestWrite(t, state, "added.json", historyTestFixture(t, historyTestOtherKey))
			case "deleted", "directory removed":
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				if change == "directory removed" {
					if err := os.Remove(filepath.Dir(source)); err != nil {
						t.Fatal(err)
					}
				}
			}
			historyTestReject(t, state, "changed legacy backup")
			if after := historyTestSnapshot(t, filepath.Join(state, "compa-history")); !reflect.DeepEqual(before, after) {
				t.Fatal("backup change caused target replay or rewrite")
			}
		})
	}
}

func TestKernelHistoryReceiptDoesNotHideMissingTargetFiles(t *testing.T) {
	for _, suffix := range []string{".jsonl", ".meta.json"} {
		t.Run(suffix, func(t *testing.T) {
			state := t.TempDir()
			historyTestWrite(t, state, "old.json", historyTestFixture(t, historyTestKey))
			store := historyTestOpen(t, state)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(state, "compa-history", historyTestKey+suffix)); err != nil {
				t.Fatal(err)
			}
			historyTestReject(t, state, "missing published file")
		})
	}
}

func TestKernelHistoryInterruptedStageNeverOverwritesState(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("published=%v", published), func(t *testing.T) {
			state := t.TempDir()
			historyTestWrite(t, state, "old.json", historyTestFixture(t, historyTestKey))
			var before map[string]string
			if published {
				store := historyTestOpen(t, state)
				store.AddMessage(historyTestKey, "user", "already committed new turn")
				before = historyTestSnapshot(t, filepath.Join(state, "compa-history"))
			}
			stage := filepath.Join(state, ".compa-history-stage-v1")
			historyTestWriteBytes(t, filepath.Join(stage, "partial.jsonl"), []byte("interrupted conversion"))
			if published {
				store := historyTestOpen(t, state)
				if len(store.GetHistory(historyTestKey)) != 6 {
					t.Fatal("interrupted stage overwrote committed turns")
				}
				if after := historyTestSnapshot(t, filepath.Join(state, "compa-history")); !reflect.DeepEqual(before, after) {
					t.Fatal("interrupted stage rewrote a good target")
				}
			} else {
				err := historyTestReject(t, state, "interrupted staging directory")
				if !strings.Contains(strings.ToLower(err.Error()), "stag") {
					t.Fatalf("interrupted stage failure was unclear: %v", err)
				}
				historyTestUnpublished(t, state)
			}
			data, err := os.ReadFile(filepath.Join(stage, "partial.jsonl"))
			if err != nil || string(data) != "interrupted conversion" {
				t.Fatalf("stale staging data was touched: %v", err)
			}
		})
	}
}

func TestKernelHistoryBoundsAreEnforcedOnActualFiles(t *testing.T) {
	t.Run("32 MiB is accepted", func(t *testing.T) {
		state := t.TempDir()
		data, err := json.Marshal(historyTestFixture(t, historyTestKey))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, bytes.Repeat([]byte(" "), (32<<20)-len(data))...)
		historyTestWriteBytes(t, filepath.Join(state, "kernel-history", "large.json"), data)
		store := historyTestOpen(t, state)
		if len(store.GetHistory(historyTestKey)) != 5 {
			t.Fatal("an ordinary bounded historic session was lost")
		}
	})
	for _, tc := range []struct {
		name  string
		files int
		size  int64
	}{
		{"file limit", 1, (32 << 20) + 1},
		{"total limit", 5, 27 << 20},
		{"session limit", 10001, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := t.TempDir()
			dir := filepath.Join(state, "kernel-history")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for i := range tc.files {
				file, err := os.Create(filepath.Join(dir, fmt.Sprintf("%05d.json", i)))
				if err != nil {
					t.Fatal(err)
				}
				if err := errors.Join(file.Truncate(tc.size), file.Close()); err != nil {
					t.Fatal(err)
				}
			}
			err := historyTestReject(t, state, tc.name)
			if !strings.Contains(strings.ToLower(err.Error()), "limit") {
				t.Fatalf("bounds were not checked before parsing input: %v", err)
			}
			historyTestUnpublished(t, state)
		})
	}
	t.Run("record limit", func(t *testing.T) {
		state := t.TempDir()
		messages := strings.Repeat(`{"role":"user","content":"x"},`, 100000) + `{"role":"user","content":"x"}`
		data := fmt.Sprintf(`{"key":%q,"messages":[%s],"created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}`, historyTestKey, messages)
		historyTestWriteBytes(t, filepath.Join(state, "kernel-history", "many.json"), []byte(data))
		err := historyTestReject(t, state, "record limit")
		if !strings.Contains(strings.ToLower(err.Error()), "limit") {
			t.Fatalf("record bound was not explicit: %v", err)
		}
		historyTestUnpublished(t, state)
	})
}

func TestKernelHistoryBackendCannotSilentlyDropRecords(t *testing.T) {
	for _, message := range []providers.Message{
		{Role: "assistant", ReasoningContent: "backend would filter this"},
		{Role: "tool", Content: strings.Repeat("x", 10<<20), ToolCallID: "large-result"},
	} {
		t.Run(fmt.Sprintf("content-bytes=%d", len(message.Content)), func(t *testing.T) {
			state := t.TempDir()
			fixture := historyTestFixture(t, historyTestKey)
			fixture.Messages = []providers.Message{message}
			historyTestWrite(t, state, "legacy.json", fixture)
			historyTestReject(t, state, "records the backend cannot replay losslessly")
			historyTestUnpublished(t, state)
		})
	}
}

func TestKernelHistoryRejectsSymlinksAndJunctions(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, location := range []string{"state", "ancestor", "legacy", "source file", "target", "target file", "receipt", "stage"} {
			t.Run(kind+"/"+location, func(t *testing.T) {
				if kind == "junction" && (runtime.GOOS != "windows" || location == "source file" || location == "target file" || location == "receipt") {
					t.Skip("junctions are Windows directory links")
				}
				base, outside := t.TempDir(), t.TempDir()
				state := filepath.Join(base, "state")
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				target := outside
				link := ""
				switch location {
				case "state":
					if err := os.Remove(state); err != nil {
						t.Fatal(err)
					}
					link = state
				case "ancestor":
					link = filepath.Join(base, "alias")
					state = filepath.Join(link, "uncreated-state")
				case "legacy":
					link = filepath.Join(state, "kernel-history")
				case "source file":
					historyTestWrite(t, state, "first.json", historyTestFixture(t, historyTestOtherKey))
					target = filepath.Join(outside, "synthetic.json")
					data, err := json.Marshal(historyTestFixture(t, historyTestKey))
					if err != nil {
						t.Fatal(err)
					}
					historyTestWriteBytes(t, target, data)
					link = filepath.Join(state, "kernel-history", "linked.json")
				case "target":
					link = filepath.Join(state, "compa-history")
				case "target file", "receipt":
					historyTestWrite(t, state, "old.json", historyTestFixture(t, historyTestKey))
					store := historyTestOpen(t, state)
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					name := historyTestKey + ".jsonl"
					if location == "receipt" {
						name = "migration-v1.json"
					}
					link = filepath.Join(state, "compa-history", name)
					target = filepath.Join(outside, name)
					data, err := os.ReadFile(link)
					if err != nil {
						t.Fatal(err)
					}
					historyTestWriteBytes(t, target, data)
					if err := os.Remove(link); err != nil {
						t.Fatal(err)
					}
				case "stage":
					link = filepath.Join(state, ".compa-history-stage-v1")
				}
				if kind == "junction" {
					output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).CombinedOutput()
					if err != nil {
						t.Fatalf("create synthetic junction: %v: %s", err, output)
					}
				} else if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				t.Cleanup(func() {
					if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
						t.Error(err)
					}
				})
				before := historyTestSnapshot(t, outside)
				historyTestReject(t, state, kind+" at "+location)
				if after := historyTestSnapshot(t, outside); !reflect.DeepEqual(before, after) {
					t.Fatal("conversion followed a link and touched outside state")
				}
			})
		}
	}
}

func TestKernelHistoryReadFailureIsNotAnEmptyStore(t *testing.T) {
	state := t.TempDir()
	historyTestWrite(t, state, "locked.json", historyTestFixture(t, historyTestKey))
	path := filepath.Join(state, "kernel-history", "locked.json")
	if runtime.GOOS == "windows" {
		script := `$f = [System.IO.File]::Open($env:MIDDEN_HISTORY_LOCK_FILE, 'Open', 'Read', 'None'); try { [Console]::WriteLine('locked'); [Console]::Out.Flush(); [Console]::ReadLine() | Out-Null } finally { $f.Dispose() }`
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.Env = append(os.Environ(), "MIDDEN_HISTORY_LOCK_FILE="+path)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Errorf("synthetic file-lock helper: %v: %s", err, stderr.String())
			}
		})
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "locked" {
			t.Fatalf("lock helper did not become ready: %q: %v", line, err)
		}
	} else {
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0600); err != nil {
				t.Error(err)
			}
		})
		if file, err := os.Open(path); err == nil {
			_ = file.Close()
			t.Skip("process can read mode-000 files")
		}
	}
	historyTestReject(t, state, "unreadable source file")
	historyTestUnpublished(t, state)
}
