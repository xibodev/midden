package find

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xibodev/midden/internal/core"
	"github.com/xibodev/midden/internal/redact"
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

// The result is a JSON contract: snake_case fields, and [] rather than null
// when nothing matched or the query was empty.
func TestResultEncodesAsStableJSON(t *testing.T) {
	dir := t.TempDir()
	sessions := []core.Session{transcript(t, dir, "a", `{"text":"module-v2 here"}`)}
	for _, query := range []string{"absent", "   "} {
		raw, err := json.Marshal(Sessions(sessions, query, Options{}))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"hits":[]`) {
			t.Errorf("query %q: empty hits must encode as []: %s", query, raw)
		}
	}
	raw, err := json.Marshal(Sessions(sessions, "module-v2", Options{}))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"hits", "scanned", "skipped", "truncated"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("result lacks %q: %s", key, raw)
		}
	}
	hit := doc["hits"].([]any)[0].(map[string]any)
	for _, key := range []string{"session", "matches", "excerpt"} {
		if _, ok := hit[key]; !ok {
			t.Errorf("hit lacks %q: %s", key, raw)
		}
	}
}

// A credential that crosses the excerpt edge must be filtered whole. Filtering
// the clipped excerpt would leave a fragment too short to recognise.
func TestExcerptFiltersCredentialsBeforeClipping(t *testing.T) {
	token := "ghp_" + strings.Repeat("Z", 36)
	line := `{"text":"module-v2 ` + strings.Repeat("x", 130) + " " + token + ` rotated"}`
	flat := strings.Join(strings.Fields(line), " ")
	if !strings.Contains(redact.Text(flat[:excerptLimit]).Text, "ghp_") {
		t.Fatal("fixture does not place the credential across the excerpt edge")
	}

	got := excerpt(line, "module-v2")
	if strings.Contains(got, "ghp_") || strings.Contains(got, "ZZZZ") {
		t.Fatalf("credential fragment survived: %q", got)
	}
	if !strings.Contains(got, "module-v2") {
		t.Errorf("excerpt lost the match: %q", got)
	}
}

// Raw records are JSON, so KEY=\"value\" must be decoded before filtering:
// the filter recognises the text as written, not its escaped encoding.
func TestExcerptDecodesEscapesBeforeFiltering(t *testing.T) {
	secret := "synthvalue" + "0123456789"
	line := `{"message":{"content":"module-v2 needs export API_KEY=\"` + secret + `\" first"}}`
	got := excerpt(line, "module-v2")
	if strings.Contains(got, secret) {
		t.Fatalf("escaped assignment survived filtering: %q", got)
	}
	if !strings.Contains(got, "ask operator") {
		t.Errorf("filtered value should leave a placeholder: %q", got)
	}
}

// A match inside a credential still yields an excerpt, without the credential.
func TestExcerptForAMatchInsideACredential(t *testing.T) {
	token := "ghp_" + strings.Repeat("Q", 36)
	got := excerpt(`{"text":"rotated `+token+` today"}`, "ghp_")
	if strings.Contains(got, "QQQQ") || !strings.Contains(got, "ask operator") {
		t.Fatalf("excerpt = %q", got)
	}
}

func TestExcerptNeverSplitsRunes(t *testing.T) {
	line := strings.Repeat("é", 120) + " module-v2 " + strings.Repeat("ü", 200)
	got := excerpt(line, "module-v2")
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt split a multi-byte character: %q", got)
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Errorf("clipped excerpt should be marked on both sides: %q", got)
	}
}
