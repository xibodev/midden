package assay

import (
	"strings"
	"testing"
	"time"
)

func TestFocusedEvidenceRetainsNonTextAssetOwners(t *testing.T) {
	for _, tc := range []struct {
		name, tool, kind, raw string
	}{
		{"image only", "claude", "user", `{"message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"SYNTHETIC_BINARY"}}]}}`},
		{"attachment event", "claude", "attachment", `{"attachments":[{"name":"recorded.bin","base64":"SYNTHETIC_BINARY"}]}`},
		{"empty text plus attachment", "copilot", "user.message", `{"data":{"content":"","attachments":[{"name":"recorded.bin","base64":"SYNTHETIC_BINARY"}]}}`},
		{"file part", "opencode", "file", `{"type":"file","filename":"recorded.bin","mime":"application/octet-stream","url":"data:application/octet-stream;base64,SYNTHETIC_BINARY"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scanner := NewEvidenceScanner("synthetic", tc.tool, Selection{Anchors: []int64{0}, MaxRecords: 1, MaxChars: 256})
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			scanner.Observe(tc.kind, "user", []byte(tc.raw), at)
			manifest := scanner.Manifest()
			if len(manifest.Candidates) != 1 {
				t.Fatalf("explicit non-text record has no owner/reference: %+v", manifest.Candidates)
			}
			record := manifest.Candidates[0]
			if record.Index != 0 || record.Kind != tc.kind || !record.Time.Equal(at) {
				t.Fatalf("reference lost native source provenance: %+v", record)
			}
			if !strings.Contains(record.Preview, "Asset reference only; content has not been inspected.") || strings.Contains(record.Preview, "SYNTHETIC_BINARY") {
				t.Fatalf("not an honest metadata-only reference: %q", record.Preview)
			}
		})
	}
}

func TestAssetReferenceNeverExposesAnEncodedLocator(t *testing.T) {
	for _, field := range []string{"name", "path"} {
		scanner := NewEvidenceScanner("synthetic", "copilot", Selection{MaxRecords: 1, MaxChars: 512})
		raw := `{"data":{"` + field + `":"data:image/png;base64,SYNTHETIC_BINARY"}}`
		scanner.Observe("session.binary_asset", "", []byte(raw), time.Time{})
		records := scanner.Manifest().Candidates
		if len(records) != 1 || strings.Contains(records[0].Preview, "SYNTHETIC_BINARY") {
			t.Fatalf("encoded locator leaked into the record text: %+v", records)
		}
	}
}

func TestMixedTextAndImagesPreserveTheRecordedText(t *testing.T) {
	scanner := NewEvidenceScanner("synthetic", "claude", Selection{Anchors: []int64{0}, MaxRecords: 1, MaxChars: 256})
	scanner.Observe("user", "user", []byte(`{"message":{"content":[{"type":"text","text":"Recorded caption, not an image analysis."},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"SYNTHETIC_BINARY"}}]}}`), time.Time{})
	records := scanner.Manifest().Candidates
	if len(records) != 1 || records[0].Preview != "Recorded caption, not an image analysis." {
		t.Fatalf("asset metadata replaced or rewrote source text: %+v", records)
	}
}
