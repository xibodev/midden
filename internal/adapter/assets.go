package adapter

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

const (
	MaxAssetRecordBytes       = 24 << 20
	MaxAssetBytes       int64 = 16 << 20
	MaxAssetTotalBytes  int64 = 64 << 20
	MaxAssets                 = 256
)

// AssetReader reads assets from the same bytes that validate a pinned view.
// Empty records selects the entire prefix; extraction is a separate operation.
type AssetReader interface {
	ReadAssets(core.Session, assay.SourceView, string, []int64) (AssetRead, error)
}

type AssetRead struct {
	Assets    []RecordedAsset `json:"assets"`
	Omissions []AssetOmission `json:"omissions"`
}

type AssetOmission struct {
	RecordIndex int64  `json:"record_index"`
	AssetIndex  int    `json:"asset_index"` // -1 means the whole record was omitted.
	Reason      string `json:"reason"`
}

// RecordedAsset exposes metadata only. Payloads and original local paths cannot
// be serialized; WriteTo is the explicit, size-bounded content operation.
type RecordedAsset struct {
	RecordIndex int64  `json:"record_index"`
	Index       int    `json:"asset_index"`
	Name        string `json:"name"`
	MediaType   string `json:"media_type,omitempty"`
	Status      string `json:"status"`
	Digest      string `json:"digest,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`

	data     []byte
	location assetLocation
}

type assetScan struct {
	result   AssetRead
	selected map[int64]bool
	roots    []string
	tool     core.Tool
	bytes    int64
}

func newAssetScan(s core.Session, boundary assay.SourceView, digest string, records []int64, kind, store string) (*assetScan, error) {
	if s.ID == "" || boundary.Kind != kind || boundary.Bytes < 0 || boundary.Records < 0 {
		return nil, fmt.Errorf("invalid pinned asset source view")
	}
	sum, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if !strings.HasPrefix(digest, "sha256:") || err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("invalid pinned asset source digest")
	}
	if len(records) > MaxAssets {
		return nil, fmt.Errorf("select at most %d asset records", MaxAssets)
	}
	sc := &assetScan{
		result:   AssetRead{Assets: []RecordedAsset{}, Omissions: []AssetOmission{}},
		selected: map[int64]bool{},
		tool:     s.Tool,
	}
	for _, index := range records {
		if index < 0 || index >= boundary.Records || sc.selected[index] {
			return nil, fmt.Errorf("invalid or duplicate asset record selection")
		}
		sc.selected[index] = true
	}
	if s.TranscriptPath != "" {
		sc.roots = append(sc.roots, filepath.Dir(s.TranscriptPath))
	}
	if filepath.IsAbs(s.Dir) {
		sc.roots = append(sc.roots, s.Dir)
	}
	if store != "" {
		sc.roots = append(sc.roots, store)
	}
	for i, root := range sc.roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("invalid local asset source root")
		}
		sc.roots[i] = absolute
	}
	return sc, nil
}

func (c *Copilot) ReadAssets(s core.Session, boundary assay.SourceView, digest string, records []int64) (AssetRead, error) {
	if s.Tool != core.ToolCopilot {
		return AssetRead{}, fmt.Errorf("asset source is not copilot")
	}
	if s.TranscriptPath == "" {
		s.TranscriptPath = c.transcriptPath(s.ID)
	}
	return readFileAssets(s, c.Root, boundary, digest, records)
}

func (c *Claude) ReadAssets(s core.Session, boundary assay.SourceView, digest string, records []int64) (AssetRead, error) {
	if s.Tool != core.ToolClaude || s.TranscriptPath == "" {
		return AssetRead{}, fmt.Errorf("asset source is not a claude transcript")
	}
	return readFileAssets(s, c.Root, boundary, digest, records)
}

func readFileAssets(s core.Session, store string, boundary assay.SourceView, digest string, records []int64) (AssetRead, error) {
	sc, err := newAssetScan(s, boundary, digest, records, "file-prefix-v1", store)
	if err != nil {
		return AssetRead{}, err
	}
	if err = readAssetFileView(s.TranscriptPath, boundary, digest, sc.observe); err != nil {
		return AssetRead{}, err
	}
	return sc.result, nil
}

// readAssetFileView retains at most one bounded record while hashing every byte
// of the pinned prefix, including oversized/unselected records and their tails.
// observe must only accumulate provisional data: validation completes at EOF.
func readAssetFileView(path string, boundary assay.SourceView, expected string, observe func(int64, []byte, bool) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open asset source: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat asset source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("asset source is not a regular file")
	}
	if info.Size() < boundary.Bytes {
		return fmt.Errorf("source view was truncated")
	}
	limited := &io.LimitedReader{R: file, N: boundary.Bytes}
	digest := sha256.New()
	reader := bufio.NewReaderSize(io.TeeReader(limited, digest), 64<<10)
	var raw []byte
	var index int64
	var observedErr error
	oversized := false
	for {
		chunk, readErr := reader.ReadSlice('\n')
		if !oversized {
			if len(raw)+len(chunk) > MaxAssetRecordBytes {
				oversized = true
				raw = nil
			} else {
				raw = append(raw, chunk...)
			}
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if len(raw) > 0 || oversized {
			if observedErr == nil {
				observedErr = observe(index, raw, oversized)
			}
			index++
			raw = raw[:0]
			oversized = false
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read asset source: %w", readErr)
		}
	}
	if limited.N != 0 {
		return fmt.Errorf("source view was truncated during the asset read")
	}
	if "sha256:"+hex.EncodeToString(digest.Sum(nil)) != expected {
		return fmt.Errorf("source records inside the pinned view changed; open a fresh view explicitly")
	}
	if index != boundary.Records {
		return fmt.Errorf("pinned source record count does not match its byte prefix")
	}
	return observedErr
}

func (o *Opencode) ReadAssets(s core.Session, boundary assay.SourceView, expected string, records []int64) (AssetRead, error) {
	if s.Tool != core.ToolOpencode {
		return AssetRead{}, fmt.Errorf("asset source is not opencode")
	}
	if boundary.Kind != rowPrefixKind || boundary.Bytes != 0 {
		return AssetRead{}, fmt.Errorf("unsupported saved database view; open a fresh view explicitly")
	}
	sc, err := newAssetScan(s, boundary, expected, records, rowPrefixKind, filepath.Dir(o.DB))
	if err != nil {
		return AssetRead{}, err
	}
	db, closeDB, err := openRO(o.DB)
	if err != nil {
		return AssetRead{}, fmt.Errorf("read opencode assets: %w", err)
	}
	defer closeDB()
	// CASE bounds data before the driver allocates it. An oversized database
	// record rejects the read rather than accepting a truncated fingerprint.
	rows, err := db.Query(`
		SELECT p.id, p.time_created,
		       CASE WHEN length(CAST(p.data AS BLOB)) <= ? THEN CAST(p.data AS BLOB) END,
		       length(CAST(p.data AS BLOB))
		FROM part p WHERE p.session_id = ?
		ORDER BY p.time_created, p.id LIMIT ?`, MaxAssetRecordBytes, s.ID, boundary.Records)
	if err != nil {
		return AssetRead{}, fmt.Errorf("query opencode assets: %w", err)
	}
	defer rows.Close()
	fingerprint := newRowFingerprint()
	var index int64
	for rows.Next() {
		var raw []byte
		var id string
		var size, created int64
		if err = rows.Scan(&id, &created, &raw, &size); err != nil {
			return AssetRead{}, fmt.Errorf("read opencode asset record: %w", err)
		}
		if size > MaxAssetRecordBytes {
			return AssetRead{}, fmt.Errorf("opencode asset record %d exceeds the 24 MiB raw record limit", index)
		}
		if size != int64(len(raw)) {
			return AssetRead{}, fmt.Errorf("incomplete opencode asset record")
		}
		fingerprint.add(id, created, raw)
		if err = sc.observe(index, raw, false); err != nil {
			return AssetRead{}, err
		}
		index++
	}
	if err = rows.Err(); err != nil {
		return AssetRead{}, fmt.Errorf("read opencode assets: %w", err)
	}
	if index != boundary.Records {
		return AssetRead{}, fmt.Errorf("source view was truncated")
	}
	if fingerprint.sum() != expected {
		return AssetRead{}, fmt.Errorf("source records inside the pinned view changed; open a fresh view explicitly")
	}
	return sc.result, nil
}

func (sc *assetScan) omit(record int64, asset int, reason string) error {
	if len(sc.result.Omissions) >= MaxAssets {
		return fmt.Errorf("asset inspection exceeds %d omissions; select fewer records", MaxAssets)
	}
	sc.result.Omissions = append(sc.result.Omissions, AssetOmission{RecordIndex: record, AssetIndex: asset, Reason: reason})
	return nil
}

func (sc *assetScan) observe(index int64, raw []byte, oversized bool) error {
	if len(sc.selected) > 0 && !sc.selected[index] {
		return nil
	}
	if oversized {
		return sc.omit(index, -1, "Asset inspection omitted: raw record exceeds 24 MiB.")
	}
	specs, err := parseAssetRecord(sc.tool, raw)
	if err != nil {
		return sc.omit(index, -1, err.Error())
	}
	for ordinal, spec := range specs {
		if len(sc.result.Assets) >= MaxAssets {
			return fmt.Errorf("asset inspection exceeds %d assets; select fewer records", MaxAssets)
		}
		asset, reason := sc.prepare(index, ordinal, spec)
		if asset.Status == "embedded" || asset.Status == "available" {
			if asset.Bytes > MaxAssetTotalBytes-sc.bytes {
				asset.Status = "unavailable"
				asset.data = nil
				asset.location = assetLocation{}
				reason = "Asset omitted: request exceeds the 64 MiB total content limit; select fewer records."
			} else {
				sc.bytes += asset.Bytes
			}
		}
		sc.result.Assets = append(sc.result.Assets, asset)
		if reason != "" {
			if err = sc.omit(index, ordinal, reason); err != nil {
				return err
			}
		}
	}
	return nil
}
