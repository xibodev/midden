// Package find searches session TRANSCRIPT CONTENT, not just metadata.
//
// The product question is "find my recent work on X". Every existing filter
// answers a different one: tool, repo, workspace and day range are facts about
// where a session lived, and Title is only its FIRST PROMPT. Measured on this
// machine, "module-v2" matched ZERO of 400 session titles while seven of the
// forty most recent transcripts contained it -- the work was there and
// unfindable.
//
// That gap made a chicken-and-egg: evidence is searchable once mined, and you
// cannot know which sessions to mine without searching them first.
package find

import (
	"bufio"
	"os"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/core"
)

// Hit is one session whose transcript matched.
type Hit struct {
	Session core.Session

	// Matches is how many lines matched, a rough relevance signal: a session
	// that mentions a topic once is not the same as one built around it.
	Matches int

	// Excerpt is the first matching line, bounded and single-line, so a caller
	// can show WHY a session matched rather than asking the user to trust it.
	Excerpt string
}

// Options bound the scan. Defaults keep an interactive search interactive.
type Options struct {
	// MaxSessions caps how many sessions are opened at all.
	MaxSessions int

	// MaxBytesPerSession caps how much of one transcript is read.
	//
	// Measured on this machine before choosing a value: the first match sat at
	// 8.6, 11.6 and 20.7 MiB in three real transcripts. An 8 MiB cap found
	// NONE of them and reported "no session contains it" -- a bound that
	// silently excluded exactly the long working sessions a user searches for.
	//
	// A cap is still needed (1.2 GiB across 1173 files), but it must not be
	// smaller than the sessions people actually work in. Reading is sequential
	// and stops at the first match, so the common case never pays the ceiling.
	MaxBytesPerSession int64

	// MaxHits stops early once enough is found.
	MaxHits int
}

const (
	defaultMaxSessions = 300
	defaultMaxBytes    = 64 << 20
	defaultMaxHits     = 25
	excerptLimit       = 160
	matchCountCeiling  = 50
)

func (o Options) withDefaults() Options {
	if o.MaxSessions <= 0 {
		o.MaxSessions = defaultMaxSessions
	}
	if o.MaxBytesPerSession <= 0 {
		o.MaxBytesPerSession = defaultMaxBytes
	}
	if o.MaxHits <= 0 {
		o.MaxHits = defaultMaxHits
	}
	return o
}

// Result reports what matched AND what was not looked at.
//
// A search that silently stops at a bound reports a number the user reads as
// complete. Scanned and Truncated travel with Hits so the count carries its own
// denominator, the same reason sessions.list ships excluded_noise.
type Result struct {
	Hits []Hit

	// Scanned is how many sessions were actually opened.
	Scanned int

	// Skipped is how many were in scope but not opened, because a bound was
	// reached. Non-zero means this is a partial answer.
	Skipped int

	// Truncated reports that at least one transcript was read only in part.
	Truncated bool
}

// Sessions searches transcript content for a query, newest first.
//
// Newest-first is deliberate: "my recent work on X" is the question being
// asked, and it also means a bounded scan spends its budget where the answer
// most likely is.
func Sessions(sessions []core.Session, query string, opt Options) Result {
	opt = opt.withDefaults()
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Result{}
	}

	ordered := make([]core.Session, len(sessions))
	copy(ordered, sessions)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Updated.After(ordered[j].Updated)
	})

	var out Result
	for _, s := range ordered {
		if len(out.Hits) >= opt.MaxHits || out.Scanned >= opt.MaxSessions {
			out.Skipped++
			continue
		}
		if s.TranscriptPath == "" {
			out.Skipped++
			continue
		}
		hit, partial, err := scan(s, q, opt.MaxBytesPerSession)
		if err != nil {
			// A transcript that cannot be read is SKIPPED, not fatal: a live
			// CLI may hold a lock, and refusing the whole search because one
			// file is busy answers nothing.
			out.Skipped++
			continue
		}
		out.Scanned++
		if partial {
			out.Truncated = true
		}
		if hit.Matches > 0 {
			out.Hits = append(out.Hits, hit)
		}
	}

	// Most matches first: a session built around a topic outranks one that
	// mentions it once.
	sort.SliceStable(out.Hits, func(i, j int) bool {
		return out.Hits[i].Matches > out.Hits[j].Matches
	})
	return out
}

func scan(s core.Session, q string, maxBytes int64) (Hit, bool, error) {
	f, err := os.Open(s.TranscriptPath)
	if err != nil {
		return Hit{}, false, err
	}
	defer f.Close()

	hit := Hit{Session: s}
	var read int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)

	for sc.Scan() {
		line := sc.Bytes()
		read += int64(len(line)) + 1
		if read > maxBytes {
			return hit, true, nil
		}
		if !strings.Contains(strings.ToLower(string(line)), q) {
			continue
		}
		hit.Matches++
		if hit.Excerpt == "" {
			hit.Excerpt = excerpt(string(line), q)
		}
		// Enough to rank and to show why. Counting every occurrence in a
		// 26 MiB transcript costs real time and changes no decision.
		if hit.Matches >= matchCountCeiling {
			return hit, true, nil
		}
	}
	// A scanner error mid-file still returns what was found: a partial answer
	// that says so beats discarding real matches.
	return hit, sc.Err() != nil, nil
}

// excerpt returns a bounded, single-line window around the match, so a caller
// can show why a session matched without dumping a transcript line.
func excerpt(line, q string) string {
	flat := strings.Join(strings.Fields(line), " ")
	i := strings.Index(strings.ToLower(flat), q)
	if i < 0 {
		if len(flat) > excerptLimit {
			return flat[:excerptLimit] + "…"
		}
		return flat
	}
	start := i - 60
	if start < 0 {
		start = 0
	}
	end := start + excerptLimit
	if end > len(flat) {
		end = len(flat)
	}
	out := flat[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(flat) {
		out += "…"
	}
	return out
}
