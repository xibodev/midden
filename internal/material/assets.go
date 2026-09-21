package material

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

type AssetOmission struct {
	RecordID     string `json:"record_id"`
	SourceDigest string `json:"source_digest,omitempty"`
	AssetID      string `json:"asset_id,omitempty"`
	Reason       string `json:"reason"`
}

// AssetResult reports metadata for unavailable assets as well as copied files.
// Only status "copied" and CopiedCount indicate successful extraction.
type AssetResult struct {
	ViewID       string          `json:"view_id"`
	Source       Source          `json:"source"`
	SourceDigest string          `json:"source_digest"`
	Assets       []Asset         `json:"assets"`
	Limitations  []string        `json:"limitations"`
	Omissions    []AssetOmission `json:"omissions"`
	Path         string          `json:"path,omitempty"`
	Paths        []string        `json:"paths"`
	AssetCount   int             `json:"asset_count"`
	CopiedCount  int             `json:"copied_count"`
	CopiedBytes  int64           `json:"copied_bytes"`
}

// Assets lists recorded metadata, or copies selected records' available assets
// into a new directory. Asset paths are relative to result.Path. The destination
// parent must already exist; an existing destination is never reused.
func (s Service) Assets(viewID string, recordIDs []string, out string) (AssetResult, error) {
	if out != "" && len(recordIDs) == 0 {
		return AssetResult{}, fmt.Errorf("asset extraction requires explicit record selection")
	}
	if out != "" {
		if err := s.CheckDestination(out); err != nil {
			return AssetResult{}, err
		}
	}
	cached, err := readCachedView(s.State, viewID)
	if err != nil {
		return AssetResult{}, err
	}
	view := cached.View
	read, err := s.readViewAssets(cached, recordIDs)
	if err != nil {
		return AssetResult{}, err
	}
	result := AssetResult{
		ViewID: view.ID, Source: view.Source, SourceDigest: view.Digest,
		Assets: []Asset{}, Omissions: []AssetOmission{}, Paths: []string{},
		Limitations: assetLimitations(),
	}
	for _, asset := range read.Assets {
		result.Assets = append(result.Assets, recordedAssetMetadata(view, asset))
	}
	for _, omission := range read.Omissions {
		item := AssetOmission{RecordID: assetRecordID(view.Source, omission.RecordIndex), SourceDigest: view.Digest, Reason: omission.Reason}
		if omission.AssetIndex >= 0 {
			item.AssetID = assetID(view.Source, omission.RecordIndex, omission.AssetIndex)
		}
		result.Omissions = append(result.Omissions, item)
	}
	result.AssetCount = len(result.Assets)
	if out == "" {
		return result, nil
	}
	if err = copyRecordedAssets(&result, read.Assets, out); err != nil {
		return AssetResult{}, err
	}
	return result, nil
}

func (s Service) readViewAssets(cached cachedView, recordIDs []string) (adapter.AssetRead, error) {
	view := cached.View
	if err := validateSource(view.Source); err != nil {
		return adapter.AssetRead{}, err
	}
	if cached.Session.Tool != view.Source.Tool || cached.Session.ID != view.Source.ID || view.TotalRecords != view.Boundary.Records {
		return adapter.AssetRead{}, fmt.Errorf("cached asset source does not match its pinned view")
	}
	if len(recordIDs) > adapter.MaxAssets {
		return adapter.AssetRead{}, fmt.Errorf("select at most %d asset records", adapter.MaxAssets)
	}
	indices := make([]int64, 0, len(recordIDs))
	seen := map[int64]bool{}
	for _, id := range recordIDs {
		index, _, err := recordPosition(view.Source, id)
		if err != nil || index < 0 || index >= view.TotalRecords || seen[index] {
			return adapter.AssetRead{}, fmt.Errorf("invalid, duplicate or out-of-view asset record selection")
		}
		seen[index] = true
		indices = append(indices, index)
	}
	session, evidence, _, err := s.resolve(view.Source)
	if err != nil {
		return adapter.AssetRead{}, err
	}
	reader, ok := evidence.(adapter.AssetReader)
	if !ok {
		return adapter.AssetRead{}, fmt.Errorf("source does not support recorded asset reads")
	}
	return reader.ReadAssets(session, view.Boundary, view.Digest, indices)
}

func recordedAssetMetadata(view View, asset adapter.RecordedAsset) Asset {
	return Asset{
		ID: assetID(view.Source, asset.RecordIndex, asset.Index), RecordID: assetRecordID(view.Source, asset.RecordIndex), SourceDigest: view.Digest,
		Name: asset.Name, MediaType: asset.MediaType, Status: asset.Status, Digest: asset.Digest, Bytes: asset.Bytes,
	}
}

func addSelectedAssetOwners(reader adapter.EvidenceReader, session core.Session, view View, selected assay.Selection, manifest *assay.Manifest) error {
	if len(selected.Anchors) == 0 || manifest.ByKind["unparsed"] == 0 {
		return nil
	}
	seen := map[int64]bool{}
	for _, record := range manifest.Candidates {
		seen[record.Index] = true
	}
	missing := []int64{}
	for _, index := range selected.Anchors {
		if !seen[index] {
			missing = append(missing, index)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	assets, ok := reader.(adapter.AssetReader)
	if !ok {
		return fmt.Errorf("source does not support selected asset references")
	}
	read, err := assets.ReadAssets(session, view.Boundary, view.Digest, missing)
	if err != nil {
		return err
	}
	for _, asset := range read.Assets {
		if seen[asset.RecordIndex] {
			continue
		}
		seen[asset.RecordIndex] = true
		// No native header or text window was parsed. Retain only the raw
		// record index; do not infer its role, timestamp, or visual contents.
		manifest.Candidates = append(manifest.Candidates, assay.Record{
			Class: assay.Artifact, Kind: "asset_reference", Index: asset.RecordIndex,
			Preview: "Asset reference only; content has not been inspected.", Clipped: true,
		})
		manifest.MatchedRecords++
	}
	sort.Slice(manifest.Candidates, func(i, j int) bool {
		return manifest.Candidates[i].Index < manifest.Candidates[j].Index
	})
	if len(manifest.Candidates) > selected.MaxRecords {
		manifest.Candidates = manifest.Candidates[:selected.MaxRecords]
	}
	return nil
}

func assetLimitations() []string {
	return []string{
		"Only explicitly recorded binary assets, attachments, image/document blocks and file parts are recognized; transcript text is not interpreted as a file request.",
		"Limits: 24 MiB per raw record, 16 MiB per asset, 64 MiB total content and 256 assets or omissions per request. Oversized file records are omitted; oversized database records reject the read.",
		"Embedded bytes are pinned to the source view. Local files reflect their current contents, not necessarily the bytes originally seen by the source tool.",
		"Local references are confined to the source session directory, store or recorded workspace. Remote references are never fetched.",
		"Relative references resolve in session-directory, workspace, then store order. Only in-root relative symlinks are supported; unsafe references do not fall back to a namesake file.",
		"Metadata is not privacy clearance or proof of an asset's contents. Base64 payloads and original source paths are not included.",
	}
}

func assetRecordID(source Source, index int64) string {
	return fmt.Sprintf("%s:%s:%d@0-0", source.Tool, source.ID, index)
}

func assetID(source Source, index int64, ordinal int) string {
	return fmt.Sprintf("%s:%s:%d#asset-%d", source.Tool, source.ID, index, ordinal)
}

func copyRecordedAssets(result *AssetResult, assets []adapter.RecordedAsset, out string) (err error) {
	absolute, err := filepath.Abs(out)
	if err != nil {
		return fmt.Errorf("invalid asset output directory: %w", err)
	}
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return fmt.Errorf("asset destination parent must exist: %w", err)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return fmt.Errorf("open asset output parent: %w", err)
	}
	defer parent.Close()
	name := filepath.Base(absolute)
	if err = parent.Mkdir(name, 0700); err != nil {
		return fmt.Errorf("asset output must be a new directory: %w", err)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return errors.Join(fmt.Errorf("open new asset directory: %w", err), parent.Remove(name))
	}
	written := []string{}
	defer func() {
		if err != nil {
			for _, path := range written {
				if cleanupErr := root.Remove(path); cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("remove incomplete asset output: %w", cleanupErr))
				}
			}
		}
		err = errors.Join(err, root.Close())
		if err != nil {
			if cleanupErr := parent.Remove(name); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove incomplete asset directory: %w", cleanupErr))
			}
		}
	}()
	for i, asset := range assets {
		if asset.Status != "available" && asset.Status != "embedded" {
			continue
		}
		if asset.Bytes < 0 || asset.Bytes > adapter.MaxAssetTotalBytes-result.CopiedBytes {
			return fmt.Errorf("asset extraction exceeds the 64 MiB total content limit")
		}
		filename := fmt.Sprintf("%06d-%03d-%s", asset.RecordIndex, asset.Index, asset.Name)
		file, createErr := root.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return fmt.Errorf("create asset output: %w", createErr)
		}
		written = append(written, filename)
		digest := sha256.New()
		count, copyErr := asset.WriteTo(io.MultiWriter(file, digest))
		if copyErr == nil {
			copyErr = file.Sync()
		}
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("extract asset %s: %w", result.Assets[i].ID, errors.Join(copyErr, closeErr))
		}
		result.Assets[i].Status = "copied"
		result.Assets[i].Path = filename
		result.Assets[i].Digest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
		result.Assets[i].Bytes = count
		result.Paths = append(result.Paths, filename)
		result.CopiedCount++
		result.CopiedBytes += count
	}
	result.Path = absolute
	return nil
}
