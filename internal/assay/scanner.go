package assay

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Scanner folds records into a manifest while tracking duplicates and image
// clusters. It holds no transcript in memory: callers stream records in.
type Scanner struct {
	m *Manifest

	maxCandidates  int
	selection      Selection
	digest         hash.Hash
	first, last    *Record
	middle         []rankedRecord
	recent         []Record
	contextRecords map[int64]Record
	after          int

	// seen fingerprints repeated tool payloads. Only a hash is retained, so
	// memory stays bounded regardless of payload size.
	seen map[[16]byte]int

	// image clustering by coarse time bucket. UAT runs emit near-identical
	// frames seconds apart; one representative per bucket is enough for
	// vision, and naive per-frame analysis would dominate every cost.
	imgBuckets map[int64]bool
}

type Selection struct {
	Query                string
	Anchors              []int64
	Before, After        int
	MaxRecords, MaxChars int
	IncludeTools         bool
	Offsets              map[int64]int
}

type rankedRecord struct {
	rank   uint64
	record Record
}

// ClusterWindow is the time bucket used to collapse near-identical images.
const ClusterWindow = 30 * time.Second

func NewScanner(sessionID, tool string, maxCandidates int) *Scanner {
	if maxCandidates <= 0 {
		maxCandidates = 400
	}
	s := &Scanner{
		m:              NewManifest(sessionID, tool),
		maxCandidates:  maxCandidates,
		seen:           map[[16]byte]int{},
		imgBuckets:     map[int64]bool{},
		digest:         sha256.New(),
		contextRecords: map[int64]Record{},
	}
	s.m.Selection = "representative"
	return s
}

func NewEvidenceScanner(sessionID, tool string, selection Selection) *Scanner {
	s := NewScanner(sessionID, tool, selection.MaxRecords)
	s.selection = selection
	if s.selection.MaxChars <= 0 {
		s.selection.MaxChars = 512
	}
	if len(selection.Anchors) > 0 {
		s.m.Selection = "context"
	} else if selection.Query != "" {
		s.m.Selection = "search"
	}
	return s
}

// Observe classifies and folds one record.
//
// payload is the raw record text; it is fingerprinted and previewed but never
// retained in full.
func (s *Scanner) Observe(kind, role string, payload []byte, ts time.Time) {
	class := Classify(kind)
	var text string
	if class == Signal || (class == Exhaust && s.selection.IncludeTools) {
		text = extractText(payload)
		if injectedInstruction(kind, payload, text) {
			class = Bookkeeping
			text = ""
		}
	}
	if class == Artifact && s.selection.MaxChars > 0 {
		text = assetReference(kind, payload)
	}
	if kind == "user" {
		if toolText, found := toolResultText(payload); found {
			class = Exhaust
			role = "tool"
			text = ""
			if s.selection.IncludeTools {
				text = toolText
			}
		}
	}
	var frame [8]byte
	binary.BigEndian.PutUint64(frame[:], uint64(len(payload)))
	s.digest.Write(frame[:])
	s.digest.Write(payload)
	if !ts.IsZero() {
		if s.m.FirstTime.IsZero() || ts.Before(s.m.FirstTime) {
			s.m.FirstTime = ts
		}
		if s.m.LastTime.IsZero() || ts.After(s.m.LastTime) {
			s.m.LastTime = ts
		}
	}
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

	}
	if text != "" && (class == Signal || class == Artifact || (class == Exhaust && s.selection.IncludeTools)) {
		s.m.EligibleRecords++
		limit := s.selection.MaxChars
		if limit == 0 {
			limit = PreviewLen
			text = strings.Join(strings.Fields(text), " ")
		}
		runes := []rune(text)
		rec.TextChars = len(runes)
		start := 0
		if offset := s.selection.Offsets[rec.Index]; offset > 0 && offset <= len(text) {
			start = utf8.RuneCountInString(text[:offset])
		}
		if s.selection.Query != "" {
			lower := strings.ToLower(text)
			if at := strings.Index(lower, strings.ToLower(s.selection.Query)); at >= 0 {
				start = utf8.RuneCountInString(lower[:at]) - limit/4
				if start < 0 {
					start = 0
				}
			}
		}
		end := start + limit
		if end > len(runes) {
			end = len(runes)
		}
		rec.StartByte = len(string(runes[:start]))
		rec.EndByte = len(string(runes[:end]))
		rec.Clipped = start > 0 || end < len(runes)
		rec.Preview = string(runes[start:end])
		if len(s.selection.Anchors) > 0 {
			s.observeContext(rec)
		} else if s.selection.Query == "" || strings.Contains(strings.ToLower(text), strings.ToLower(s.selection.Query)) {
			s.m.MatchedRecords++
			s.sample(rec)
		}
	}

	s.m.Add(rec)
}

// Manifest returns the accumulated result.
func (s *Scanner) Manifest() *Manifest {
	s.m.SourceDigest = "sha256:" + hex.EncodeToString(s.digest.Sum(nil))
	s.m.Candidates = []Record{}
	if len(s.selection.Anchors) > 0 {
		s.m.MatchedRecords = int64(len(s.contextRecords))
		for _, rec := range s.contextRecords {
			s.m.Candidates = append(s.m.Candidates, rec)
		}
	} else if s.last != nil {
		if s.maxCandidates > 1 && s.first.Index != s.last.Index {
			s.m.Candidates = append(s.m.Candidates, *s.first)
			for _, r := range s.middle {
				s.m.Candidates = append(s.m.Candidates, r.record)
			}
		}
		s.m.Candidates = append(s.m.Candidates, *s.last)
	}
	sort.Slice(s.m.Candidates, func(i, j int) bool { return s.m.Candidates[i].Index < s.m.Candidates[j].Index })
	if len(s.m.Candidates) > s.maxCandidates {
		s.m.Candidates = s.m.Candidates[:s.maxCandidates]
	}
	return s.m
}

func (s *Scanner) sample(rec Record) {
	if s.first == nil {
		copy := rec
		s.first = &copy
	}
	if s.last != nil && s.last.Index != s.first.Index && s.maxCandidates > 2 {
		var index [8]byte
		binary.BigEndian.PutUint64(index[:], uint64(s.last.Index))
		sum := sha256.Sum256(index[:])
		item := rankedRecord{binary.BigEndian.Uint64(sum[:8]), *s.last}
		if len(s.middle) < s.maxCandidates-2 {
			s.middle = append(s.middle, item)
		} else {
			worst := 0
			for i := range s.middle {
				if s.middle[i].rank > s.middle[worst].rank {
					worst = i
				}
			}
			if item.rank < s.middle[worst].rank {
				s.middle[worst] = item
			}
		}
	}
	copy := rec
	s.last = &copy
}

func (s *Scanner) observeContext(rec Record) {
	anchor := false
	for _, index := range s.selection.Anchors {
		if index == rec.Index {
			anchor = true
			break
		}
	}
	if anchor {
		for _, previous := range s.recent {
			s.contextRecords[previous.Index] = previous
		}
		s.contextRecords[rec.Index] = rec
		s.after = s.selection.After
	} else if s.after > 0 {
		s.contextRecords[rec.Index] = rec
		s.after--
	}
	if s.selection.Before > 0 {
		s.recent = append(s.recent, rec)
		if len(s.recent) > s.selection.Before {
			s.recent = s.recent[len(s.recent)-s.selection.Before:]
		}
	}
}

func injectedInstruction(kind string, payload []byte, text string) bool {
	if kind != "user.message" && kind != "user" {
		return false
	}
	var envelope struct {
		Data struct {
			Source       string `json:"source"`
			Continuation bool   `json:"isAutopilotContinuation"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &envelope) == nil && (envelope.Data.Source == "autopilot" || envelope.Data.Continuation) {
		return true
	}
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<system_reminder>") || strings.HasPrefix(text, "<skill-context")
}

func assetReference(kind string, payload []byte) string {
	if kind != "session.binary_asset" && kind != "session.workspace_file_changed" && kind != "file" {
		return ""
	}
	var record struct {
		Data struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Mime string `json:"mimeType"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &record) != nil {
		return ""
	}
	label := record.Data.Name
	if label == "" {
		label = record.Data.Path
	}
	if len(label) > 300 {
		label = label[:300]
	}
	return "Asset reference only; content has not been inspected. " + kind + " " + label
}

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
			var texts []string
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					texts = append(texts, b.Text)
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, "\n")
			}
		}
	}
	if result, ok := obj["result"]; ok {
		var plain string
		if json.Unmarshal(result, &plain) == nil {
			return plain
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(result, &nested) == nil {
			if content, ok := nested["content"]; ok && json.Unmarshal(content, &plain) == nil {
				return plain
			}
		}
	}
	return ""
}
