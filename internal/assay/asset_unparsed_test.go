package assay

import (
	"testing"
	"time"
)

func TestUnparsedAssetHeadsNeverBecomeToolPreviews(t *testing.T) {
	scanner := NewEvidenceScanner("synthetic", "claude", Selection{Anchors: []int64{0}, MaxRecords: 1, MaxChars: 256, IncludeTools: true})
	raw := []byte(`{"message":{"content":[{"type":"image","source":{"data":"SYNTHETIC_BINARY`)
	scanner.Observe("unparsed", "", raw, time.Time{})
	manifest := scanner.Manifest()
	if len(manifest.Candidates) != 0 || manifest.TotalRecords != 1 || manifest.ByKind["unparsed"] != int64(len(raw)) {
		t.Fatalf("unknown truncated bytes became model-facing text: %+v", manifest.Candidates)
	}
}
