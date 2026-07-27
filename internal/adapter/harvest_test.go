package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mekjr1/midden/internal/core"
)

func TestCollectorIsMemoryBounded(t *testing.T) {
	// A 774 MiB transcript must not cost 774 MiB of RAM. The collector keeps
	// the first turn plus a bounded ring of the most recent.
	c := newCollector(5)
	for i := 1; i <= 1000; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		c.add(core.Turn{Index: i, Role: role, Text: fmt.Sprintf("turn %d", i)})
	}
	got := c.result()

	if len(got.Recent) != 5 {
		t.Errorf("ring grew unbounded: %d turns retained, want 5", len(got.Recent))
	}
	if !got.Truncated {
		t.Error("Truncated should be set when turns were dropped")
	}
	if got.Recent[len(got.Recent)-1].Index != 1000 {
		t.Errorf("ring lost the newest turn: got index %d", got.Recent[len(got.Recent)-1].Index)
	}
	if got.Recent[0].Index != 996 {
		t.Errorf("ring window wrong: starts at %d, want 996", got.Recent[0].Index)
	}
	if got.Goal == nil || got.Goal.Index != 1 {
		t.Error("the original goal (first user turn) must survive truncation")
	}
	if got.UserTurns != 500 {
		t.Errorf("UserTurns = %d, want 500", got.UserTurns)
	}
	if got.LastAssistant == nil || got.LastAssistant.Index != 1000 {
		t.Error("LastAssistant should track the newest assistant turn")
	}
}

func TestCollectorBelowCapacity(t *testing.T) {
	c := newCollector(10)
	c.add(core.Turn{Index: 1, Role: "user", Text: "only"})
	got := c.result()

	if got.Truncated {
		t.Error("Truncated must be false when nothing was dropped")
	}
	if len(got.Recent) != 1 || got.Goal == nil {
		t.Error("single turn should be retained as both goal and recent")
	}
}

func TestEachLineHandlesOversizedLines(t *testing.T) {
	// Tool-result lines reach tens of MB. bufio.Scanner would stop dead at the
	// first line larger than its buffer, silently truncating the harvest;
	// eachLine must truncate the line and keep going.
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	huge := strings.Repeat("x", 4<<20)
	content := "first\n" + huge + "\nlast\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen []string
	if err := eachLine(path, func(line []byte) bool {
		s := strings.TrimSpace(string(line))
		if len(s) > 32 {
			s = fmt.Sprintf("<%d bytes>", len(s))
		}
		seen = append(seen, s)
		return true
	}); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(seen), seen)
	}
	if seen[0] != "first" || seen[2] != "last" {
		t.Errorf("scan did not survive the oversized line: %v", seen)
	}
}

func TestEachLineNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(path, []byte("a\nb"), 0o600); err != nil {
		t.Fatal(err)
	}

	var n int
	eachLine(path, func([]byte) bool { n++; return true })
	if n != 2 {
		t.Errorf("final line without newline was dropped: got %d lines, want 2", n)
	}
}

func TestEachLineEarlyStop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.jsonl")
	os.WriteFile(path, []byte("a\nb\nc\n"), 0o600)

	var n int
	eachLine(path, func([]byte) bool { n++; return n < 2 })
	if n != 2 {
		t.Errorf("returning false should stop the scan, got %d lines", n)
	}
}

func TestRawTextDecodesBothShapes(t *testing.T) {
	if got := rawText([]byte(`"plain string"`)); got != "plain string" {
		t.Errorf("string content = %q", got)
	}
	blocks := []byte(`[{"type":"text","text":"hello"},{"type":"tool_use","id":"x"},{"type":"text","text":"world"}]`)
	if got := rawText(blocks); got != "hello\nworld" {
		t.Errorf("block content = %q, want %q", got, "hello\nworld")
	}
	if got := rawText(nil); got != "" {
		t.Errorf("nil content should yield empty, got %q", got)
	}
}

func TestAdaptersImplementHarvester(t *testing.T) {
	for _, a := range All() {
		if _, ok := a.(core.Harvester); !ok {
			t.Errorf("%s adapter does not implement Harvester", a.Tool())
		}
	}
}
