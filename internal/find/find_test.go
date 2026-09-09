package find

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

func transcript(t *testing.T, dir, name string, lines ...string) core.Session {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return core.Session{
		Tool: "claude", ID: name, TranscriptPath: p, Updated: time.Now(),
	}
}

// TestFindsContentNoMetadataCanSee is the product gap this package closes.
//
// Every existing filter answers "where did this session live" -- tool, repo,
// workspace, age -- and Title is only the FIRST PROMPT. Measured on the real
// store: "module-v2" matched ZERO of 400 titles while seven of the forty most
// recent transcripts contained it. The work was there and unfindable.
func TestFindsContentNoMetadataCanSee(t *testing.T) {
	dir := t.TempDir()
	sessions := []core.Session{
		transcript(t, dir, "a", `{"text":"unrelated chatter"}`),
		transcript(t, dir, "b", `{"text":"we settled the module-v2 contract"}`),
	}

	res := Sessions(sessions, "module-v2", Options{})
	if len(res.Hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(res.Hits))
	}
	if res.Hits[0].Session.ID != "b" {
		t.Errorf("matched %q, want b", res.Hits[0].Session.ID)
	}
	if res.Hits[0].Excerpt == "" {
		t.Error("a hit carries no excerpt, so a user cannot see WHY it matched")
	}
}

// TestMatchDeepInALargeTranscriptIsFound guards the defect that made the first
// version of this package USELESS on the real store.
//
// The cap was 8 MiB. Measured first-match offsets in three real transcripts:
// 8.6, 11.6 and 20.7 MiB. All three were missed and the tool reported "no
// session contains it" -- a bound that silently excluded exactly the long
// working sessions a user searches for, while looking like a definitive answer.
func TestMatchDeepInALargeTranscriptIsFound(t *testing.T) {
	dir := t.TempDir()
	// The filler must EXCEED the defective cap, not merely be large. At 40k
	// lines (2.6 MiB) this test passed with the 8 MiB bound restored: it
	// asserted a property it never reached -- the same class as the bound.
	filler := strings.Repeat(`{"text":"padding line with nothing whatsoever of any interest"}`+"\n", 150000)
	s := transcript(t, dir, "deep", filler+`{"text":"the module-v2 decision"}`)

	res := Sessions([]core.Session{s}, "module-v2", Options{})
	if len(res.Hits) != 1 {
		t.Fatalf("a match past the early bytes was missed: %d hits", len(res.Hits))
	}
}

// TestBoundedScanReportsWhatItSkipped keeps the count honest.
//
// A search that stops at a bound and prints only its hits reads as a complete
// answer. Skipped travels with Hits for the same reason sessions.list ships
// excluded_noise: a number that cannot carry its own denominator invites the
// wrong conclusion.
func TestBoundedScanReportsWhatItSkipped(t *testing.T) {
	dir := t.TempDir()
	var sessions []core.Session
	for i := 0; i < 5; i++ {
		sessions = append(sessions,
			transcript(t, dir, string(rune('a'+i)), `{"text":"module-v2 here"}`))
	}

	res := Sessions(sessions, "module-v2", Options{MaxHits: 2})
	if len(res.Hits) != 2 {
		t.Fatalf("got %d hits, want the 2 requested", len(res.Hits))
	}
	if res.Skipped == 0 {
		t.Error("a bounded scan reported no skipped sessions; the result reads " +
			"as complete when three sessions were never opened")
	}
}

// TestUnreadableTranscriptIsSkippedNotFatal: a live CLI may hold a lock, and
// refusing the whole search because one file is busy answers nothing.
func TestUnreadableTranscriptIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	good := transcript(t, dir, "good", `{"text":"module-v2"}`)
	missing := core.Session{Tool: "claude", ID: "gone",
		TranscriptPath: filepath.Join(dir, "does-not-exist"), Updated: time.Now()}

	res := Sessions([]core.Session{missing, good}, "module-v2", Options{})
	if len(res.Hits) != 1 {
		t.Fatalf("an unreadable transcript broke the search: %d hits", len(res.Hits))
	}
	if res.Skipped != 1 {
		t.Errorf("skipped=%d, want 1; a skipped store must be counted", res.Skipped)
	}
}
