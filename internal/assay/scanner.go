package assay

import (
	"crypto/sha256"
	"encoding/json"
	"time"
)

// Scanner folds records into a manifest while tracking duplicates and image
// clusters. It holds no transcript in memory: callers stream records in.
type Scanner struct {
	m *Manifest

	maxCandidates int

	// seen fingerprints repeated tool payloads. Only a hash is retained, so
	// memory stays bounded regardless of payload size.
	seen map[[16]byte]int

	// image clustering by coarse time bucket. UAT runs emit near-identical
	// frames seconds apart; one representative per bucket is enough for
	// vision, and naive per-frame analysis would dominate every cost.
	imgBuckets map[int64]bool
}

// ClusterWindow is the time bucket used to collapse near-identical images.
const ClusterWindow = 30 * time.Second

func NewScanner(sessionID, tool string, maxCandidates int) *Scanner {
	if maxCandidates <= 0 {
		maxCandidates = 400
	}
	return &Scanner{
		m:             NewManifest(sessionID, tool),
		maxCandidates: maxCandidates,
		seen:          map[[16]byte]int{},
		imgBuckets:    map[int64]bool{},
	}
}

// Observe classifies and folds one record.
//
// payload is the raw record text; it is fingerprinted and previewed but never
// retained in full.
func (s *Scanner) Observe(kind, role string, payload []byte, ts time.Time) {
	class := Classify(kind)
	rec := Record{
		Class: class,
		Kind:  kind,
		Bytes: int64(len(payload)),
		Role:  role,
		Time:  ts,
		Index: s.m.TotalRecords,
	}

	switch class {
	case Exhaust:
		// Repeated identical tool payloads are the single largest source of
		// avoidable bulk: the same file read on turn after turn.
		if len(payload) > 512 {
			var key [16]byte
			sum := sha256.Sum256(payload)
			copy(key[:], sum[:16])
			s.seen[key]++
			if s.seen[key] > 1 {
				s.m.DuplicateReads++
				s.m.DuplicateBytes += rec.Bytes
			}
		}

	case Artifact:
		if IsImage(kind, string(payload)) {
			s.m.ImageCount++
			bucket := ts.Unix() / int64(ClusterWindow.Seconds())
			if !s.imgBuckets[bucket] {
				s.imgBuckets[bucket] = true
				s.m.ImageClusters++
			}
		}

	case Signal:
		if len(s.m.Candidates) < s.maxCandidates {
			rec.Preview = Preview(extractText(payload))
			if rec.Preview != "" {
				s.m.Candidates = append(s.m.Candidates, rec)
			}
		}
	}

	s.m.Add(rec)
}

// Manifest returns the accumulated result.
func (s *Scanner) Manifest() *Manifest { return s.m }

// extractText pulls human-readable text out of a JSON record without knowing
// its exact schema, trying the field names the three CLIs actually use.
func extractText(payload []byte) string {
	var probe map[string]json.RawMessage
	if json.Unmarshal(payload, &probe) != nil {
		return string(payload)
	}

	// Nested data objects (Copilot) hold the real content.
	if data, ok := probe["data"]; ok {
		if s := fieldText(data); s != "" {
			return s
		}
	}
	if msg, ok := probe["message"]; ok {
		if s := fieldText(msg); s != "" {
			return s
		}
	}
	if s := fieldText(payload); s != "" {
		return s
	}
	return ""
}

// fieldText looks for the usual text-carrying field names, in the order most
// likely to hold clean content rather than a wrapped variant.
func fieldText(raw json.RawMessage) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	for _, key := range []string{"content", "text", "summary", "message"} {
		v, ok := obj[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return s
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(v, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					return b.Text
				}
			}
		}
	}
	return ""
}
