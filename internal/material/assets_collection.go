package material

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Collection operations retain bounded, verified bytes, not a path to reopen
// after verification. The input collection or source store can disappear once
// the snapshot has been read without changing the bytes subsequently written.
type collectionAsset struct {
	metadata Asset
	data     []byte
}

func (s Service) collectViewAssets(cached cachedView, records []Record) ([]collectionAsset, []AssetOmission, error) {
	byIndex := map[int64][]Record{}
	ids := []string{}
	for _, record := range records {
		index, _, err := recordPosition(cached.View.Source, record.ID)
		if err != nil || index != record.Reference.RecordIndex || record.Reference.SourceDigest != cached.View.Digest {
			return nil, nil, fmt.Errorf("selected asset record does not match its source view")
		}
		if len(byIndex[index]) == 0 {
			ids = append(ids, record.ID)
		}
		byIndex[index] = append(byIndex[index], record)
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}
	read, err := s.readViewAssets(cached, ids)
	if err != nil {
		return nil, nil, err
	}
	assets := []collectionAsset{}
	var total int64
	for _, recorded := range read.Assets {
		metadata := recordedAssetMetadata(cached.View, recorded)
		var data []byte
		if metadata.Status == "available" || metadata.Status == "embedded" {
			buffer := limitedBuffer{limit: MaxCollectionAssetBytes}
			count, err := recorded.WriteTo(&buffer)
			if err != nil {
				return nil, nil, fmt.Errorf("collect asset %s: %w", metadata.ID, err)
			}
			if count != metadata.Bytes {
				return nil, nil, fmt.Errorf("asset changed while collecting")
			}
			data = buffer.Bytes()
			digest := Digest(data)
			if metadata.Digest != "" && metadata.Digest != digest {
				return nil, nil, fmt.Errorf("asset digest changed while collecting")
			}
			metadata.Status, metadata.Digest = "copied", digest
		}
		for _, record := range byIndex[recorded.RecordIndex] {
			if len(assets) >= MaxCollectionAssets {
				return nil, nil, fmt.Errorf("collection exceeds %d assets", MaxCollectionAssets)
			}
			if int64(len(data)) > MaxCollectionTotalAssetBytes-total {
				return nil, nil, fmt.Errorf("collection assets exceed 64 MiB")
			}
			total += int64(len(data))
			metadata.RecordID = record.ID
			assets = append(assets, collectionAsset{metadata: metadata, data: data})
		}
	}
	omissions := []AssetOmission{}
	for _, omitted := range read.Omissions {
		for _, record := range byIndex[omitted.RecordIndex] {
			item := AssetOmission{RecordID: record.ID, SourceDigest: cached.View.Digest, Reason: omitted.Reason}
			if omitted.AssetIndex >= 0 {
				item.AssetID = assetID(cached.View.Source, omitted.RecordIndex, omitted.AssetIndex)
			}
			omissions = append(omissions, item)
			if len(omissions) > MaxCollectionAssets {
				return nil, nil, fmt.Errorf("collection exceeds %d asset omissions", MaxCollectionAssets)
			}
		}
	}
	return assets, omissions, nil
}

func validCollectionDigest(digest string) bool {
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil
}

func identityKey(parts ...string) string {
	raw, _ := json.Marshal(parts)
	return string(raw)
}

func collectionAssetKey(asset Asset) string {
	return identityKey(asset.ID, asset.RecordID, asset.SourceDigest)
}

func recordDigests(records []Record) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, record := range records {
		if out[record.ID] == nil {
			out[record.ID] = map[string]bool{}
		}
		out[record.ID][record.Reference.SourceDigest] = true
	}
	return out
}

func assetSourceDigest(recordID, digest string, owners map[string]map[string]bool) (string, error) {
	digests := owners[recordID]
	if digest == "" && len(digests) == 1 {
		for value := range digests {
			digest = value
		}
	}
	if !validCollectionDigest(digest) || !digests[digest] {
		return "", fmt.Errorf("asset or omission has missing, ambiguous or unknown record provenance")
	}
	return digest, nil
}

func validateAssetMetadata(asset Asset, owners map[string]map[string]bool) (Asset, error) {
	digest, err := assetSourceDigest(asset.RecordID, asset.SourceDigest, owners)
	if err != nil {
		return asset, err
	}
	asset.SourceDigest = digest
	if asset.ID == "" || asset.Name == "" || asset.Bytes < 0 || (asset.Digest != "" && !validCollectionDigest(asset.Digest)) {
		return asset, fmt.Errorf("invalid asset identity, size or digest")
	}
	switch asset.Status {
	case "copied":
		if asset.Bytes > MaxCollectionAssetBytes || !validCollectionDigest(asset.Digest) {
			return asset, fmt.Errorf("copied asset exceeds 16 MiB or has no valid digest")
		}
	case "unavailable", "external_reference", "embedded", "available", "missing", "reference":
		if asset.Path != "" {
			return asset, fmt.Errorf("reference-only asset must not claim an output path")
		}
	default:
		return asset, fmt.Errorf("unsupported collection asset status")
	}
	return asset, nil
}

func prepareCollectionAssets(records []Record, assets []collectionAsset) ([]collectionAsset, error) {
	owners := recordDigests(records)
	unique := map[string]collectionAsset{}
	var total int64
	for _, asset := range assets {
		metadata, err := validateAssetMetadata(asset.metadata, owners)
		if err != nil {
			return nil, err
		}
		metadata.Path = ""
		if metadata.Status == "copied" {
			if int64(len(asset.data)) != metadata.Bytes || Digest(asset.data) != metadata.Digest {
				return nil, fmt.Errorf("copied asset content does not match its size or digest")
			}
			metadata.Path = collectionAssetPath(metadata)
		} else if len(asset.data) != 0 {
			return nil, fmt.Errorf("reference-only asset unexpectedly contains copied data")
		}
		asset.metadata = metadata
		key := collectionAssetKey(metadata)
		if previous, ok := unique[key]; ok {
			if previous.metadata != metadata || !bytes.Equal(previous.data, asset.data) {
				return nil, fmt.Errorf("conflicting versions of the same recorded asset: %s", metadata.ID)
			}
			continue
		}
		if len(unique) >= MaxCollectionAssets || int64(len(asset.data)) > MaxCollectionTotalAssetBytes-total {
			return nil, fmt.Errorf("collection exceeds 256 assets or 64 MiB of copied asset content")
		}
		total += int64(len(asset.data))
		unique[key] = asset
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]collectionAsset, 0, len(keys))
	names := map[string]bool{}
	for _, key := range keys {
		asset := unique[key]
		if asset.metadata.Path != "" {
			if names[asset.metadata.Path] {
				return nil, fmt.Errorf("deterministic asset output name collision")
			}
			names[asset.metadata.Path] = true
		}
		out = append(out, asset)
	}
	return out, nil
}

func collectionAssetPath(asset Asset) string {
	extension := strings.ToLower(path.Ext(strings.ReplaceAll(asset.Name, `\`, "/")))
	valid := len(extension) >= 2 && len(extension) <= 16
	for _, char := range strings.TrimPrefix(extension, ".") {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			valid = false
		}
	}
	if !valid {
		extension = ".bin"
	}
	return "assets/" + strings.TrimPrefix(Digest([]byte(collectionAssetKey(asset))), "sha256:") + extension
}

func prepareAssetOmissions(records []Record, assets []collectionAsset, omissions []AssetOmission) ([]AssetOmission, error) {
	owners := recordDigests(records)
	known := map[string]bool{}
	for _, asset := range assets {
		known[collectionAssetKey(asset.metadata)] = true
	}
	unique := map[string]AssetOmission{}
	for _, omission := range omissions {
		digest, err := assetSourceDigest(omission.RecordID, omission.SourceDigest, owners)
		if err != nil {
			return nil, err
		}
		omission.SourceDigest = digest
		if omission.Reason == "" || (omission.AssetID != "" && !known[identityKey(omission.AssetID, omission.RecordID, digest)]) {
			return nil, fmt.Errorf("asset omission has an empty reason or unknown asset")
		}
		unique[identityKey(omission.RecordID, digest, omission.AssetID, omission.Reason)] = omission
		if len(unique) > MaxCollectionAssets {
			return nil, fmt.Errorf("collection exceeds 256 asset omissions")
		}
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]AssetOmission, 0, len(keys))
	for _, key := range keys {
		out = append(out, unique[key])
	}
	return out, nil
}

func assetsForRecords(records []Record, assets []collectionAsset, omissions []AssetOmission) ([]collectionAsset, []AssetOmission) {
	owners := recordDigests(records)
	selected := []collectionAsset{}
	keptOmissions := []AssetOmission{}
	for _, asset := range assets {
		if owners[asset.metadata.RecordID][asset.metadata.SourceDigest] {
			selected = append(selected, asset)
		}
	}
	for _, omission := range omissions {
		if owners[omission.RecordID][omission.SourceDigest] {
			keptOmissions = append(keptOmissions, omission)
		}
	}
	return selected, keptOmissions
}
