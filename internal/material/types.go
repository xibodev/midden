// Package material provides deterministic, host-independent session data tools.
package material

import (
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/assay"
	"github.com/mekjr1/midden/internal/core"
)

const (
	ViewSchema                   = "midden.source-view/v1"
	CollectionSchema             = "midden.collection/v1"
	MaxRecords                   = 256
	MaxChars                     = 8192
	MaxCollectionBytes           = 32 << 20
	MaxCollectionManifestBytes   = 1 << 20
	MaxCollectionRecordBytes     = 1 << 20
	MaxCollectionRecords         = 10000
	MaxCollectionAssets          = adapter.MaxAssets
	MaxCollectionAssetBytes      = adapter.MaxAssetBytes
	MaxCollectionTotalAssetBytes = adapter.MaxAssetTotalBytes
)

type Source struct {
	Tool core.Tool `json:"tool"`
	ID   string    `json:"session_id"`
}
type Reference struct {
	ViewID       string `json:"view_id"`
	SourceDigest string `json:"source_digest"`
	RecordIndex  int64  `json:"record_index"`
	StartByte    int    `json:"start_byte"`
	EndByte      int    `json:"end_byte"`
}
type Record struct {
	ID        string    `json:"id"`
	Source    Source    `json:"source"`
	Kind      string    `json:"kind"`
	Role      string    `json:"role,omitempty"`
	Time      time.Time `json:"time,omitzero"`
	Text      string    `json:"text"`
	Clipped   bool      `json:"clipped"`
	Redacted  bool      `json:"redacted"`
	Reference Reference `json:"reference"`
}
type View struct {
	Schema         string           `json:"schema"`
	ID             string           `json:"view_id"`
	Source         Source           `json:"source"`
	Title          string           `json:"title"`
	Digest         string           `json:"source_digest"`
	Boundary       assay.SourceView `json:"boundary"`
	FirstTime      time.Time        `json:"first_time,omitzero"`
	LastTime       time.Time        `json:"last_time,omitzero"`
	TotalRecords   int64            `json:"total_records"`
	MatchedRecords int64            `json:"matched_records"`
	Selection      string           `json:"selection"`
	Records        []Record         `json:"records"`
	Warnings       []string         `json:"warnings"`
}
type ReadOptions struct {
	Records              []string
	Before, After        int
	Offset, Limit, Chars int
	IncludeTools         bool
}
type Service struct {
	State string
	Roots adapter.Roots
}
type cachedView struct {
	View    View         `json:"view"`
	Session core.Session `json:"session"`
}
type CollectOptions struct {
	Views         []string
	Records       []string
	Out           string
	IncludeAssets bool
}
type CollectionSource struct {
	Source    Source           `json:"source"`
	Digest    string           `json:"source_digest"`
	Boundary  assay.SourceView `json:"boundary"`
	FirstTime time.Time        `json:"first_time,omitzero"`
	LastTime  time.Time        `json:"last_time,omitzero"`
}
type CollectionManifest struct {
	Schema         string             `json:"schema"`
	RecordFile     string             `json:"record_file"`
	RecordCount    int                `json:"record_count"`
	RecordsDigest  string             `json:"records_digest"`
	Sources        []CollectionSource `json:"sources"`
	Assets         []Asset            `json:"assets"`
	AssetOmissions []AssetOmission    `json:"asset_omissions"`
	Warnings       []string           `json:"warnings"`
}
type CollectionResult struct {
	Path          string `json:"path"`
	Manifest      string `json:"manifest"`
	RecordCount   int    `json:"record_count"`
	Digest        string `json:"records_digest"`
	AssetCount    int    `json:"asset_count"`
	CopiedCount   int    `json:"copied_count"`
	CopiedBytes   int64  `json:"copied_bytes"`
	OmissionCount int    `json:"omission_count"`
}
type Verification struct {
	Valid              bool     `json:"valid"`
	RecordCount        int      `json:"record_count"`
	Problems           []string `json:"problems"`
	AssetCount         int      `json:"asset_count"`
	VerifiedAssetCount int      `json:"verified_asset_count"`
	AssetBytes         int64    `json:"asset_bytes"`
}
type Asset struct {
	ID           string `json:"id"`
	RecordID     string `json:"record_id"`
	SourceDigest string `json:"source_digest,omitempty"`
	Name         string `json:"name"`
	MediaType    string `json:"media_type,omitempty"`
	Status       string `json:"status"`
	Path         string `json:"path,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
}
