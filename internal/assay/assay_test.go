package assay

import (
	"testing"
	"time"
)

func TestBinaryAssetsAreArtifactsNotSignal(t *testing.T) {
	// Regression: session.binary_asset was 382 MiB (56%) of one real session.
	// Treating it as signal reported 1.4x compression instead of 6.3x, which
	// would have made salvage look unaffordable.
	if got := Classify("session.binary_asset"); got != Artifact {
		t.Errorf("session.binary_asset = %v, want artifact", got)
	}
}

func TestClassifyKnownKinds(t *testing.T) {
	cases := map[string]Class{
		"user.message":             Signal,
		"assistant.message":        Signal,
		"tool.execution_complete":  Exhaust,
		"tool.execution_start":     Exhaust,
		"assistant.turn_start":     Bookkeeping,
		"session.usage_checkpoint": Bookkeeping,
		"file-history-snapshot":    Artifact,
		"text":                     Signal,
		"reasoning":                Signal,
		"tool":                     Exhaust,
		"step-finish":              Bookkeeping,
		"unparsed":                 Exhaust,
	}
	for kind, want := range cases {
		if got := Classify(kind); got != want {
			t.Errorf("Classify(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestClassifyUnknownKindsStructurally(t *testing.T) {
	// Format drift is guaranteed — opencode alone has shipped 38 migrations.
	// Unknown kinds must be classified by shape, and default to Signal so
	// that meaning is never silently discarded.
	cases := map[string]Class{
		"session.new_binary_thing": Artifact,
		"some.image.payload":       Artifact,
		"tool.brand_new":           Exhaust,
		"session.something_new":    Bookkeeping,
		"permission.whatever":      Bookkeeping,
		"totally_unknown":          Signal,
	}
	for kind, want := range cases {
		if got := Classify(kind); got != want {
			t.Errorf("Classify(%q) = %v, want %v", kind, got, want)
		}
		if KnownKind(kind) {
			t.Errorf("%q should not be reported as explicitly known", kind)
		}
	}
}

func TestManifestArithmetic(t *testing.T) {
	m := NewManifest("s1", "copilot")
	m.Add(Record{Class: Signal, Kind: "user.message", Bytes: 100})
	m.Add(Record{Class: Exhaust, Kind: "tool.execution_complete", Bytes: 700})
	m.Add(Record{Class: Artifact, Kind: "session.binary_asset", Bytes: 150})
	m.Add(Record{Class: Bookkeeping, Kind: "mode", Bytes: 50})

	if m.TotalBytes != 1000 {
		t.Errorf("TotalBytes = %d, want 1000", m.TotalBytes)
	}
	if m.SignalBytes() != 100 {
		t.Errorf("SignalBytes = %d, want 100", m.SignalBytes())
	}
	// Artifacts are NOT reclaimable: a screenshot is the raw material for a
	// tutorial, not garbage.
	if m.ReclaimableBytes() != 750 {
		t.Errorf("ReclaimableBytes = %d, want 750 (exhaust+bookkeeping only)", m.ReclaimableBytes())
	}
	if got := m.Compression(); got != 10 {
		t.Errorf("Compression = %.1f, want 10", got)
	}
	if got := m.SignalShare(); got != 0.1 {
		t.Errorf("SignalShare = %.2f, want 0.10", got)
	}
}

func TestManifestHandlesEmpty(t *testing.T) {
	m := NewManifest("s", "t")
	if m.Compression() != 0 || m.SignalShare() != 0 || m.EstTokens() != 0 {
		t.Error("empty manifest should report zeroes, not divide by zero")
	}
}

func TestScannerDetectsDuplicateToolPayloads(t *testing.T) {
	// The same file read on turn after turn is the largest source of
	// avoidable bulk.
	s := NewScanner("s1", "copilot", 10)
	payload := make([]byte, 2048)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}

	for i := 0; i < 4; i++ {
		s.Observe("tool.execution_complete", "", payload, time.Now())
	}

	m := s.Manifest()
	if m.DuplicateReads != 3 {
		t.Errorf("DuplicateReads = %d, want 3 (first occurrence is not a duplicate)", m.DuplicateReads)
	}
	if m.DuplicateBytes != 3*2048 {
		t.Errorf("DuplicateBytes = %d, want %d", m.DuplicateBytes, 3*2048)
	}
}

func TestScannerClustersImagesByTime(t *testing.T) {
	// UAT runs emit near-identical frames seconds apart. One representative
	// per cluster is enough for vision; per-frame analysis would dominate
	// every cost.
	s := NewScanner("s1", "copilot", 10)
	base := time.Unix(1700000000, 0)

	for i := 0; i < 10; i++ {
		s.Observe("session.binary_asset", "", []byte(`{"data":"data:image/png;base64,AAAA"}`),
			base.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < 10; i++ {
		s.Observe("session.binary_asset", "", []byte(`{"data":"data:image/png;base64,AAAA"}`),
			base.Add(5*time.Minute+time.Duration(i)*time.Second))
	}

	m := s.Manifest()
	if m.ImageCount != 20 {
		t.Errorf("ImageCount = %d, want 20", m.ImageCount)
	}
	if m.ImageClusters != 2 {
		t.Errorf("ImageClusters = %d, want 2", m.ImageClusters)
	}
}

func TestScannerBoundsCandidates(t *testing.T) {
	// Candidates are the salvage slice. They must stay bounded no matter how
	// large the transcript.
	s := NewScanner("s1", "copilot", 5)
	for i := 0; i < 500; i++ {
		s.Observe("user.message", "user", []byte(`{"data":{"content":"a real message here"}}`), time.Now())
	}

	m := s.Manifest()
	if len(m.Candidates) != 5 {
		t.Errorf("candidates = %d, want 5", len(m.Candidates))
	}
	if m.TotalRecords != 500 {
		t.Errorf("TotalRecords = %d, want 500", m.TotalRecords)
	}
	if m.EstSliceTokens() == 0 {
		t.Error("slice should have a non-zero token estimate")
	}
	if m.SliceCompression() <= 1 {
		t.Errorf("SliceCompression = %.1f, want > 1", m.SliceCompression())
	}
}

func TestExtractTextFindsContentInNestedShapes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"copilot data.content", `{"type":"user.message","data":{"content":"hello there"}}`, "hello there"},
		{"claude message.content string", `{"type":"user","message":{"content":"plain"}}`, "plain"},
		{"claude message.content blocks", `{"type":"assistant","message":{"content":[{"type":"text","text":"blocked"}]}}`, "blocked"},
		{"opencode text", `{"type":"text","text":"parted"}`, "parted"},
		{"summary", `{"type":"summary","summary":"summed"}`, "summed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractText([]byte(tc.in)); got != tc.want {
				t.Errorf("extractText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPreviewIsBoundedAndSingleLine(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "word\nline "
	}
	got := Preview(long)

	if len([]rune(got)) > PreviewLen+3 {
		t.Errorf("preview too long: %d runes", len([]rune(got)))
	}
	for _, r := range got {
		if r == '\n' {
			t.Error("preview must be single-line")
			break
		}
	}
}
