package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xibodev/compa/pkg/memory"
	"github.com/xibodev/compa/pkg/providers"
	"github.com/xibodev/compa/pkg/session"
)

const (
	kernelHistoryLegacyDir    = "kernel-history"
	kernelHistoryDir          = "compa-history"
	kernelHistoryStage        = ".compa-history-stage-v1"
	kernelHistoryReceiptName  = "migration-v1.json"
	kernelHistoryFileLimit    = 32 << 20
	kernelHistoryTotalLimit   = 128 << 20
	kernelHistorySessionLimit = 10000
	kernelHistoryRecordLimit  = 100000
	kernelHistoryTotalRecords = 1000000
)

var kernelHistoryKeyPattern = regexp.MustCompile(`^sk_v1_[0-9a-f]{64}$`)

type kernelHistorySource struct {
	Name     string `json:"name"`
	Key      string `json:"key"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Messages int    `json:"messages"`
}

type kernelHistoryReceipt struct {
	Version       int                   `json:"version"`
	LegacyPresent bool                  `json:"legacy_present"`
	Files         []kernelHistorySource `json:"files"`
}

type kernelHistorySession struct {
	Key      string
	Messages []providers.Message
	Summary  string
	Created  time.Time
	Updated  time.Time
}

// openKernelHistory runs before the agent starts. The legacy backup must remain
// byte-for-byte unchanged on subsequent starts, including its file inventory.
// Failed staging is retained for inspection, never resumed or merged implicitly.
func openKernelHistory(state string) (session.SessionStore, error) {
	absolute, root, err := kernelHistoryStateRoot(state)
	if err != nil {
		return nil, fmt.Errorf("kernel history state: %w", err)
	}
	defer root.Close()

	source, err := kernelHistoryScanSource(root)
	if err != nil {
		return nil, err
	}
	targetInfo, err := root.Lstat(kernelHistoryDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect new kernel history: %w", err)
	}
	if err == nil {
		target, err := kernelHistoryOpenDirectory(root, kernelHistoryDir)
		if err != nil {
			return nil, err
		}
		_, receiptErr := target.Lstat(kernelHistoryReceiptName)
		if receiptErr == nil {
			err = kernelHistoryCheckReceipt(target, source)
			if err == nil {
				err = kernelHistoryCheckTarget(target, source)
			}
			err = errors.Join(err, target.Close())
			if err != nil {
				return nil, err
			}
			return kernelHistoryBackend(absolute)
		}
		if !os.IsNotExist(receiptErr) {
			_ = target.Close()
			return nil, fmt.Errorf("inspect migration receipt: %w", receiptErr)
		}
		names, readErr := kernelHistoryNames(target, 1)
		readErr = errors.Join(readErr, target.Close())
		if readErr != nil || len(names) != 0 {
			return nil, fmt.Errorf("kernel history target conflict: nonempty target without a valid migration receipt: %w",
				errors.Join(errors.New("refusing to merge or overwrite history"), readErr))
		}
	}

	// This exclusive directory creation also prevents two startup conversions
	// from publishing competing batches. A stale directory requires inspection.
	if err := root.Mkdir(kernelHistoryStage, 0700); err != nil {
		return nil, fmt.Errorf("create kernel history staging directory %q (interrupted or concurrent migration; nothing overwritten): %w", kernelHistoryStage, err)
	}
	if err := kernelHistoryStageSource(root, absolute, source); err != nil {
		return nil, fmt.Errorf("kernel history not published; staging retained at %q: %w", filepath.Join(absolute, kernelHistoryStage), err)
	}

	current, statErr := root.Lstat(kernelHistoryDir)
	if targetInfo == nil {
		if statErr == nil || !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("kernel history target changed during staging; refusing publication: %w",
				errors.Join(errors.New("target is no longer absent"), statErr))
		}
	} else {
		if statErr != nil || current.Mode().Type() != os.ModeDir || !os.SameFile(targetInfo, current) {
			return nil, fmt.Errorf("kernel history target changed during staging: %w",
				errors.Join(errors.New("target directory identity changed"), statErr))
		}
		// Remove only the originally empty directory, never its contents. Remove
		// fails if another writer has populated it in the meantime.
		if err := root.Remove(kernelHistoryDir); err != nil {
			return nil, fmt.Errorf("kernel history target conflict before publication: %w", err)
		}
	}
	if err := root.Rename(kernelHistoryStage, kernelHistoryDir); err != nil {
		return nil, fmt.Errorf("publish staged kernel history (staging retained): %w", err)
	}
	if err := kernelHistorySyncDirectory(root); err != nil {
		return nil, fmt.Errorf("kernel history published but directory sync failed: %w", err)
	}
	return kernelHistoryBackend(absolute)
}

func kernelHistoryBackend(state string) (session.SessionStore, error) {
	store, err := memory.NewJSONLStore(filepath.Join(state, kernelHistoryDir))
	if err != nil {
		return nil, fmt.Errorf("open Compa kernel history: %w", err)
	}
	return session.NewJSONLBackend(store), nil
}

func kernelHistoryStateRoot(state string) (string, *os.Root, error) {
	if strings.TrimSpace(state) == "" {
		return "", nil, errors.New("empty state path")
	}
	for _, component := range strings.FieldsFunc(state, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return "", nil, errors.New("state path must not contain parent traversal")
		}
	}
	absolute, err := filepath.Abs(state)
	if err != nil {
		return "", nil, err
	}
	volume := filepath.VolumeName(absolute)
	if strings.HasPrefix(volume, `\\?\`) || strings.HasPrefix(volume, `\\.\`) {
		return "", nil, errors.New("device paths are not ordinary state directories")
	}
	base := volume + string(os.PathSeparator)
	info, err := os.Lstat(base)
	if err != nil {
		return "", nil, err
	}
	if info.Mode().Type() != os.ModeDir {
		return "", nil, fmt.Errorf("unsafe state path root %q", base)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(absolute, base), string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		if _, err := root.Lstat(component); os.IsNotExist(err) {
			if err := root.Mkdir(component, 0700); err != nil && !os.IsExist(err) {
				_ = root.Close()
				return "", nil, err
			}
		} else if err != nil {
			_ = root.Close()
			return "", nil, err
		}
		next, openErr := kernelHistoryOpenDirectory(root, component)
		closeErr := root.Close()
		if err := errors.Join(openErr, closeErr); err != nil {
			if next != nil {
				_ = next.Close()
			}
			return "", nil, err
		}
		root = next
	}
	return absolute, root, nil
}

func kernelHistoryOpenDirectory(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect history directory %q: %w", name, err)
	}
	if info.Mode().Type() != os.ModeDir {
		return nil, fmt.Errorf("unsafe history directory %q: symlinks, junctions and non-directories are not allowed", name)
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open history directory %q: %w", name, err)
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = child.Close()
		return nil, fmt.Errorf("history directory %q changed while opening: %w", name, errors.Join(errors.New("directory identity mismatch"), err))
	}
	return child, nil
}

func kernelHistoryNames(root *os.Root, limit int) ([]string, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(limit + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err := errors.Join(err, dir.Close()); err != nil {
		return nil, err
	}
	if len(entries) > limit {
		return nil, fmt.Errorf("history directory entry limit exceeded (%d)", limit)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	sort.Strings(names)
	return names, nil
}

func kernelHistoryOpenFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect history file %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("unsafe history file %q: only ordinary files are allowed", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, fmt.Errorf("open history file %q: %w", name, err)
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, fmt.Errorf("history file %q changed while opening: %w", name, errors.Join(errors.New("file identity mismatch"), err))
	}
	return file, opened, nil
}

func kernelHistoryReadFile(root *os.Root, name string, limit int64) ([]byte, error) {
	file, before, err := kernelHistoryOpenFile(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if before.Size() > limit {
		return nil, fmt.Errorf("history file %q exceeds byte limit %d", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read history file %q: %w", name, err)
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("recheck history file %q: %w", name, err)
	}
	if len(data) > int(limit) {
		return nil, fmt.Errorf("history file %q exceeds byte limit %d", name, limit)
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || int64(len(data)) != before.Size() ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, fmt.Errorf("history file %q changed while reading", name)
	}
	return data, nil
}

func kernelHistorySourceName(name string) bool {
	return filepath.IsLocal(name) && filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\:`) && strings.HasSuffix(name, ".json")
}

func kernelHistoryScanSource(root *os.Root) (kernelHistoryReceipt, error) {
	receipt := kernelHistoryReceipt{Version: 1, Files: []kernelHistorySource{}}
	info, err := root.Lstat(kernelHistoryLegacyDir)
	if os.IsNotExist(err) {
		return receipt, nil
	}
	if err != nil {
		return receipt, fmt.Errorf("inspect legacy kernel history: %w", err)
	}
	source, err := kernelHistoryOpenDirectory(root, kernelHistoryLegacyDir)
	if err != nil {
		return receipt, err
	}
	defer source.Close()
	receipt.LegacyPresent = true
	names, err := kernelHistoryNames(source, kernelHistorySessionLimit)
	if err != nil {
		return receipt, fmt.Errorf("enumerate legacy kernel history: %w", err)
	}
	var total int64
	for _, name := range names {
		if !kernelHistorySourceName(name) {
			return receipt, fmt.Errorf("unrecognized legacy history entry %q; refusing to skip it", name)
		}
		entry, err := source.Lstat(name)
		if err != nil {
			return receipt, fmt.Errorf("inspect legacy history %q: %w", name, err)
		}
		if !entry.Mode().IsRegular() {
			return receipt, fmt.Errorf("unsafe legacy history entry %q: only ordinary JSON files are allowed", name)
		}
		if entry.Size() > kernelHistoryFileLimit {
			return receipt, fmt.Errorf("legacy history file %q exceeds 32 MiB byte limit", name)
		}
		total += entry.Size()
		if total > kernelHistoryTotalLimit {
			return receipt, errors.New("legacy history exceeds 128 MiB total byte limit")
		}
	}
	total = 0
	totalRecords := 0
	keys := make(map[string]string, len(names))
	for _, name := range names {
		data, err := kernelHistoryReadFile(source, name, kernelHistoryFileLimit)
		if err != nil {
			return receipt, err
		}
		total += int64(len(data))
		if total > kernelHistoryTotalLimit {
			return receipt, errors.New("legacy history exceeds 128 MiB total byte limit while reading")
		}
		value, err := kernelHistoryDecodeSession(data)
		if err != nil {
			return receipt, fmt.Errorf("decode legacy history %q: %w", name, err)
		}
		if previous, exists := keys[value.Key]; exists {
			return receipt, fmt.Errorf("duplicate legacy session key %q in %q and %q", value.Key, previous, name)
		}
		keys[value.Key] = name
		totalRecords += len(value.Messages)
		if totalRecords > kernelHistoryTotalRecords {
			return receipt, errors.New("legacy history total message record limit exceeded")
		}
		sum := sha256.Sum256(data)
		receipt.Files = append(receipt.Files, kernelHistorySource{
			Name: name, Key: value.Key, Bytes: int64(len(data)),
			SHA256: hex.EncodeToString(sum[:]), Messages: len(value.Messages),
		})
	}
	current, err := root.Lstat(kernelHistoryLegacyDir)
	if err != nil || current.Mode().Type() != os.ModeDir || !os.SameFile(info, current) {
		return receipt, fmt.Errorf("legacy history directory changed during scan: %w",
			errors.Join(errors.New("source directory identity mismatch"), err))
	}
	return receipt, nil
}

func kernelHistoryDecodeSession(data []byte) (kernelHistorySession, error) {
	var wire struct {
		Key      string               `json:"key"`
		Messages []*providers.Message `json:"messages"`
		Summary  string               `json:"summary,omitempty"`
		Created  *time.Time           `json:"created"`
		Updated  *time.Time           `json:"updated"`
	}
	var value kernelHistorySession
	if err := kernelHistoryDecodeJSON(data, &wire, true); err != nil {
		return value, err
	}
	if !kernelHistoryKeyPattern.MatchString(wire.Key) {
		return value, fmt.Errorf("nonopaque legacy session key %q; expected sk_v1_ followed by 64 lowercase SHA256 hex digits", wire.Key)
	}
	if wire.Messages == nil || wire.Created == nil || wire.Updated == nil {
		return value, errors.New("legacy session requires a messages array and created/updated timestamps")
	}
	value = kernelHistorySession{
		Key: wire.Key, Summary: wire.Summary, Created: *wire.Created, Updated: *wire.Updated,
		Messages: make([]providers.Message, len(wire.Messages)),
	}
	for i, message := range wire.Messages {
		if message == nil {
			return value, fmt.Errorf("legacy message %d is null, not a message object", i)
		}
		value.Messages[i] = *message
	}
	return value, nil
}

func kernelHistoryDecodeJSON(data []byte, value any, legacy bool) error {
	if !utf8.Valid(data) {
		return errors.New("history JSON is not valid UTF-8")
	}
	if err := kernelHistoryCheckUnicodeEscapes(data); err != nil {
		return err
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	tokens.UseNumber()
	if err := kernelHistoryJSONValue(tokens, 0, legacy); err != nil {
		return err
	}
	if _, err := tokens.Token(); !errors.Is(err, io.EOF) {
		return errors.New("history JSON has trailing or malformed data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD without an error.
// Validate escapes, not decoded runes: literal U+FFFD and escaped backslashes
// are ordinary content and must not be interpreted as evidence of corruption.
func kernelHistoryCheckUnicodeEscapes(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data) && data[i] != '"'; i++ {
			if data[i] != '\\' {
				continue
			}
			i++
			if i >= len(data) || data[i] != 'u' {
				continue
			}
			if i+4 >= len(data) {
				return errors.New("incomplete Unicode escape in history JSON")
			}
			code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return fmt.Errorf("invalid Unicode escape in history JSON: %w", err)
			}
			i += 4
			if code >= 0xdc00 && code <= 0xdfff {
				return errors.New("unpaired low surrogate in history JSON")
			}
			if code >= 0xd800 && code <= 0xdbff {
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return errors.New("unpaired high surrogate in history JSON")
				}
				low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return errors.New("invalid surrogate pair in history JSON")
				}
				i += 6
			}
		}
	}
	return nil
}

// encoding/json otherwise accepts duplicate object fields with last-value-wins
// semantics. Check the shape and bound arrays before allocating typed messages.
func kernelHistoryJSONValue(decoder *json.Decoder, depth int, legacy bool) error {
	if depth > 64 {
		return errors.New("history JSON nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case nil:
		// The old writer omits nil optional fields and always writes an array
		// for messages; null must not silently become empty content or metadata.
		if legacy {
			return errors.New("null is not a value in the persisted legacy history format")
		}
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			field, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := field.(string)
			if !ok || seen[name] {
				return fmt.Errorf("invalid or duplicate JSON field %q", field)
			}
			if legacy {
				for previous := range seen {
					if strings.EqualFold(name, previous) {
						return fmt.Errorf("duplicate case-insensitive legacy JSON fields %q and %q", previous, name)
					}
				}
			}
			seen[name] = true
			if len(seen) > 256 {
				return errors.New("history JSON object field limit exceeded")
			}
			if err := kernelHistoryJSONValue(decoder, depth+1, legacy); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	case json.Delim('['):
		count := 0
		for decoder.More() {
			count++
			if count > kernelHistoryRecordLimit {
				return errors.New("history message/array record limit exceeded (100000)")
			}
			if err := kernelHistoryJSONValue(decoder, depth+1, legacy); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	}
	return err
}

func kernelHistoryCheckReceipt(target *os.Root, source kernelHistoryReceipt) error {
	data, err := kernelHistoryReadFile(target, kernelHistoryReceiptName, 8<<20)
	if err != nil {
		return fmt.Errorf("read kernel history migration receipt: %w", err)
	}
	var receipt kernelHistoryReceipt
	if err := kernelHistoryDecodeJSON(data, &receipt, false); err != nil {
		return fmt.Errorf("invalid kernel history migration receipt: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	presence := string(bytes.TrimSpace(fields["legacy_present"]))
	if receipt.Version != 1 || receipt.Files == nil || (presence != "true" && presence != "false") {
		return errors.New("invalid or unsupported kernel history migration receipt")
	}
	if receipt.LegacyPresent != source.LegacyPresent || !slices.Equal(receipt.Files, source.Files) {
		return errors.New("kernel history migration receipt conflict: legacy backup is missing, added or changed; refusing to reimport or overwrite new turns")
	}
	return nil
}

func kernelHistoryStageSource(root *os.Root, absolute string, receipt kernelHistoryReceipt) error {
	stage, err := kernelHistoryOpenDirectory(root, kernelHistoryStage)
	if err != nil {
		return err
	}
	defer stage.Close()
	store, err := memory.NewJSONLStore(filepath.Join(absolute, kernelHistoryStage))
	if err != nil {
		return err
	}
	defer store.Close()
	var source *os.Root
	if receipt.LegacyPresent {
		source, err = kernelHistoryOpenDirectory(root, kernelHistoryLegacyDir)
		if err != nil {
			return err
		}
		defer source.Close()
	}
	for _, file := range receipt.Files {
		data, err := kernelHistoryReadFile(source, file.Name, kernelHistoryFileLimit)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != file.Bytes || hex.EncodeToString(sum[:]) != file.SHA256 {
			return fmt.Errorf("legacy history %q changed before staging", file.Name)
		}
		value, err := kernelHistoryDecodeSession(data)
		if err != nil {
			return err
		}
		// SetHistory filters messages and fills absent timestamps. Encode the
		// public on-disk types instead, then demand lossless public-API replay.
		var lines bytes.Buffer
		encoder := json.NewEncoder(&lines)
		for _, message := range value.Messages {
			if err := encoder.Encode(message); err != nil {
				return fmt.Errorf("encode legacy history %q: %w", file.Name, err)
			}
		}
		if err := kernelHistoryWriteFile(stage, value.Key+".jsonl", lines.Bytes()); err != nil {
			return err
		}
		meta := memory.SessionMeta{
			Key: value.Key, Summary: value.Summary, Count: len(value.Messages),
			CreatedAt: value.Created, UpdatedAt: value.Updated,
		}
		metadata, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if err := kernelHistoryWriteFile(stage, value.Key+".meta.json", metadata); err != nil {
			return err
		}
		got, err := store.GetHistory(context.Background(), value.Key)
		if err != nil {
			return fmt.Errorf("verify legacy history %q through Compa JSONL backend: %w", file.Name, err)
		}
		expected, err := json.Marshal(value.Messages)
		if err != nil {
			return err
		}
		actual, err := json.Marshal(got)
		if err != nil {
			return err
		}
		if !bytes.Equal(expected, actual) {
			return fmt.Errorf("Compa cannot replay every message in legacy history %q losslessly; refusing to drop or change records", file.Name)
		}
		readMeta, err := store.GetSessionMeta(context.Background(), value.Key)
		if err != nil {
			return fmt.Errorf("verify legacy session metadata %q: %w", file.Name, err)
		}
		if readMeta.Key != meta.Key || readMeta.Summary != meta.Summary || readMeta.Count != meta.Count || readMeta.Skip != 0 ||
			!readMeta.CreatedAt.Equal(meta.CreatedAt) || !readMeta.UpdatedAt.Equal(meta.UpdatedAt) {
			return fmt.Errorf("legacy session metadata %q did not survive staging", file.Name)
		}
	}
	current, err := kernelHistoryScanSource(root)
	if err != nil {
		return err
	}
	if current.LegacyPresent != receipt.LegacyPresent || !slices.Equal(current.Files, receipt.Files) {
		return errors.New("legacy kernel history changed during staging; nothing published")
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := kernelHistoryWriteFile(stage, kernelHistoryReceiptName, data); err != nil {
		return err
	}
	if err := kernelHistoryCheckReceipt(stage, receipt); err != nil {
		return err
	}
	if err := kernelHistoryCheckTarget(stage, receipt); err != nil {
		return err
	}
	return kernelHistorySyncDirectory(stage)
}

func kernelHistoryWriteFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create staged history %q: %w", name, err)
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("persist staged history %q: %w", name, err)
	}
	return nil
}

func kernelHistoryCheckTarget(target *os.Root, receipt kernelHistoryReceipt) error {
	dir, err := target.Open(".")
	if err != nil {
		return fmt.Errorf("read published history directory: %w", err)
	}
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(256)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read published history entries: %w", readErr)
		}
		for _, entry := range entries {
			name := entry.Name()
			file, _, err := kernelHistoryOpenFile(target, name)
			if err != nil {
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			if name == kernelHistoryReceiptName {
				continue
			}
			switch {
			case strings.HasSuffix(name, ".meta.json"):
				key := strings.TrimSuffix(name, ".meta.json")
				if !kernelHistoryKeyPattern.MatchString(key) {
					return fmt.Errorf("unrecognized history metadata file %q", name)
				}
				data, err := kernelHistoryReadFile(target, name, kernelHistoryFileLimit)
				if err != nil {
					return err
				}
				var meta memory.SessionMeta
				if err := kernelHistoryDecodeJSON(data, &meta, false); err != nil {
					return fmt.Errorf("invalid history metadata %q: %w", name, err)
				}
				if meta.Key != key || meta.Count < 0 || meta.Skip < 0 || meta.Skip > meta.Count {
					return fmt.Errorf("conflicting history metadata %q", name)
				}
				if meta.Count > 0 {
					info, err := target.Lstat(key + ".jsonl")
					if err != nil {
						return fmt.Errorf("missing published history for %q: %w", key, err)
					}
					if !info.Mode().IsRegular() || info.Size() == 0 {
						return fmt.Errorf("unsafe or empty published history for %q", key)
					}
				}
			case strings.HasSuffix(name, ".jsonl"):
				key := strings.TrimSuffix(name, ".jsonl")
				if !kernelHistoryKeyPattern.MatchString(key) {
					return fmt.Errorf("unrecognized history message file %q", name)
				}
				if _, err := target.Lstat(key + ".meta.json"); err != nil {
					return fmt.Errorf("missing metadata for published history %q: %w", key, err)
				}
			default:
				return fmt.Errorf("unrecognized entry %q in Compa kernel history", name)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	for _, source := range receipt.Files {
		for _, suffix := range []string{".jsonl", ".meta.json"} {
			file, _, err := kernelHistoryOpenFile(target, source.Key+suffix)
			if err != nil {
				return fmt.Errorf("migration receipt references missing or unreadable published history: %w", err)
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func kernelHistorySyncDirectory(root *os.Root) error {
	// Go cannot fsync directory handles on Windows. Files are individually
	// synced before the single rename; a missing published file fails reopen.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
