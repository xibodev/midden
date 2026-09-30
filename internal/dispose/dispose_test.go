package dispose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func kindOf(line []byte) string {
	var p struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &p) != nil {
		return "unparsed"
	}
	return p.Type
}

// writeTranscript builds a synthetic transcript with a realistic mix.
func writeTranscript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "events.jsonl")

	big := strings.Repeat("x", 20000)
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString(`{"type":"user.message","id":"u` + itoa(i) + `","parentId":"p` + itoa(i) + `","data":{"content":"a real question from the operator"}}` + "\n")
		b.WriteString(`{"type":"assistant.message","id":"a` + itoa(i) + `","parentId":"u` + itoa(i) + `","data":{"content":"a real answer that carries meaning"}}` + "\n")
		b.WriteString(`{"type":"tool.execution_complete","id":"t` + itoa(i) + `","parentId":"a` + itoa(i) + `","data":{"output":"` + big + `"}}` + "\n")
		b.WriteString(`{"type":"assistant.turn_end","id":"e` + itoa(i) + `","parentId":"a` + itoa(i) + `","data":{"content":"` + big + `"}}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}

func TestPrunePreservesEveryRecordAndIdentifier(t *testing.T) {
	// Transcripts are chained by id/parentId. Dropping a record breaks
	// replay, so pruning must only ever replace payloads in place.
	dir := t.TempDir()
	src := writeTranscript(t, dir)
	dst := filepath.Join(dir, "pruned.jsonl")

	plan, err := PruneJSONL(src, dst, DefaultOptions(), kindOf)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RecordsTotal != 40 {
		t.Errorf("RecordsTotal = %d, want 40", plan.RecordsTotal)
	}
	if plan.AfterBytes >= plan.BeforeBytes {
		t.Errorf("pruning did not shrink the file: %d -> %d", plan.BeforeBytes, plan.AfterBytes)
	}

	v, err := Verify(src, dst, kindOf)
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK {
		t.Fatalf("verification failed: %v", v.Failures)
	}
	if v.SourceRecords != v.TargetRecords {
		t.Errorf("record count changed: %d -> %d", v.SourceRecords, v.TargetRecords)
	}
	if v.SourceIDs != v.TargetIDs {
		t.Errorf("identifier count changed: %d -> %d", v.SourceIDs, v.TargetIDs)
	}
	if v.SourceSignal != v.TargetSignal {
		t.Errorf("signal records lost: %d -> %d", v.SourceSignal, v.TargetSignal)
	}
}

func TestPruneNeverTouchesSignal(t *testing.T) {
	dir := t.TempDir()
	src := writeTranscript(t, dir)
	dst := filepath.Join(dir, "pruned.jsonl")

	if _, err := PruneJSONL(src, dst, DefaultOptions(), kindOf); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)

	if !strings.Contains(body, "a real question from the operator") {
		t.Error("user message content was destroyed")
	}
	if !strings.Contains(body, "a real answer that carries meaning") {
		t.Error("assistant message content was destroyed")
	}
	if !strings.Contains(body, "midden: pruned") {
		t.Error("expected pruning markers in the output")
	}
}

func TestPruneLeavesOriginalUntouched(t *testing.T) {
	// The single most important property: disposal never mutates the source.
	dir := t.TempDir()
	src := writeTranscript(t, dir)

	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := PruneJSONL(src, filepath.Join(dir, "out.jsonl"), DefaultOptions(), kindOf); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("PruneJSONL modified the source transcript")
	}
}

func TestArtifactsArePreservedByDefault(t *testing.T) {
	// Screenshots are the raw material for tutorials. The same bytes are
	// garbage or gold depending on whether they have been harvested, so
	// removing them must be an explicit choice.
	o := DefaultOptions()
	if o.Artifacts {
		t.Error("artifacts must not be pruned by default")
	}
	if o.shouldPrune(0 /* Signal */, 1<<20) {
		t.Error("signal must never be pruned")
	}
}

func TestSmallPayloadsAreLeftAlone(t *testing.T) {
	// Rewriting a small record costs more in marker text than it recovers.
	o := DefaultOptions()
	if o.shouldPrune(1 /* Exhaust */, 100) {
		t.Error("payloads below MinBytes should be left alone")
	}
}

func TestVerifyRejectsRecordLoss(t *testing.T) {
	dir := t.TempDir()
	src := writeTranscript(t, dir)

	// A "pruned" file that dropped records must fail verification, which is
	// the gate standing between a bad prune and a deletion.
	truncated := filepath.Join(dir, "bad.jsonl")
	b, _ := os.ReadFile(src)
	lines := strings.SplitAfter(string(b), "\n")
	os.WriteFile(truncated, []byte(strings.Join(lines[:len(lines)/2], "")), 0o600)

	v, err := Verify(src, truncated, kindOf)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK {
		t.Fatal("verification passed on a transcript that lost records")
	}
	if len(v.Failures) == 0 {
		t.Error("failures should explain what went wrong")
	}
}

func TestVerifyRejectsNonSmallerOutput(t *testing.T) {
	dir := t.TempDir()
	src := writeTranscript(t, dir)
	copyPath := filepath.Join(dir, "same.jsonl")
	b, _ := os.ReadFile(src)
	os.WriteFile(copyPath, b, 0o600)

	v, err := Verify(src, copyPath, kindOf)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK {
		t.Error("an identical copy recovers nothing and should not verify")
	}
}

func TestPruneHandlesUnparseableLines(t *testing.T) {
	// Real transcripts contain malformed lines. They must be copied through
	// verbatim rather than dropped or crashing the prune.
	dir := t.TempDir()
	src := filepath.Join(dir, "messy.jsonl")
	content := `{"type":"user.message","id":"1","data":{"content":"ok"}}` + "\n" +
		"not json at all\n" +
		`{"type":"tool.execution_complete","id":"2","data":{"output":"` + strings.Repeat("y", 5000) + `"}}` + "\n"
	os.WriteFile(src, []byte(content), 0o600)

	dst := filepath.Join(dir, "out.jsonl")
	plan, err := PruneJSONL(src, dst, DefaultOptions(), kindOf)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RecordsTotal != 3 {
		t.Errorf("RecordsTotal = %d, want 3", plan.RecordsTotal)
	}

	out, _ := os.ReadFile(dst)
	if !strings.Contains(string(out), "not json at all") {
		t.Error("unparseable line was dropped instead of copied through")
	}
}
