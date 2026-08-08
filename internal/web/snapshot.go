package web

// Snapshot caching.
//
// The UI polls. Every poll used to re-derive everything from disk: read all
// sessions from three stores (including a 223 MB SQLite file) and walk ~40 GiB
// of directories to total the footprint. A single request took over 90 seconds
// under load, and with an open tab polling every few seconds the requests piled
// up until the server stopped answering at all.
//
// The index exists precisely so this work happens once. This is the missing
// piece: one shared snapshot, refreshed on a TTL, computed by exactly one
// goroutine at a time while readers get the previous value rather than
// blocking.

import (
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
)

// snapshotTTL is how stale a reading may be.
//
// Session stores change on human timescales — a turn every few seconds at
// most — so a few seconds of staleness is invisible, while the saving is the
// difference between a responsive UI and an unresponsive one.
const snapshotTTL = 15 * time.Second

// footprintTTL is longer because walking tens of gigabytes is the single most
// expensive thing the server does, and total disk usage moves slowly.
const footprintTTL = 2 * time.Minute

// partialRetryTTL prevents a persistent source-store error from launching a
// full scan every time a view rebuilds, while remaining short enough that an
// unlocked/healthy store recovers without an operator waiting five minutes.
const partialRetryTTL = 30 * time.Second

// snapshot is the derived view every handler shares.
type snapshot struct {
	Sessions      []core.Session
	State         guide.State
	TakenAt       time.Time
	Version       uint64
	IndexedAt     time.Time
	ToolIndexedAt map[core.Tool]time.Time
}

type snapshotCache struct {
	mu              sync.Mutex
	cur             *snapshot
	building        bool
	done            chan struct{}
	generation      uint64
	buildGeneration uint64
	version         uint64

	footMu       sync.Mutex
	foot         map[core.Tool]int64
	footAt       time.Time
	footTotal    int64
	footBuilding bool
}

func newSnapshotCache() *snapshotCache { return &snapshotCache{} }

// get returns a snapshot no older than the TTL.
//
// When a refresh is already in flight, callers receive the previous snapshot
// immediately rather than queueing behind it. Only a cold cache waits.
func (c *snapshotCache) get(s *Server) *snapshot {
	for {
		c.mu.Lock()

		fresh := c.cur != nil && time.Since(c.cur.TakenAt) < snapshotTTL
		if fresh {
			snap := c.cur
			c.mu.Unlock()
			return snap
		}

		if c.building {
			// A refresh is running. Serve what we have; block only if there
			// is nothing at all yet. A waiter always loops after done closes:
			// invalidate may have discarded the build it was waiting for.
			if c.cur != nil {
				snap := c.cur
				c.mu.Unlock()
				return snap
			}
			wait := c.done
			c.mu.Unlock()
			<-wait
			continue
		}

		c.building = true
		c.buildGeneration = c.generation
		c.done = make(chan struct{})
		generation := c.buildGeneration
		c.mu.Unlock()

		snap := s.buildSnapshot(c)

		published := c.finishBuild(snap, generation)

		// An explicit refresh invalidated this build while it was reading.
		// Do not give its stale result a fresh 15-second TTL; loop and build
		// against the new index instead.
		if published {
			return snap
		}
	}
}

// finishBuild publishes a snapshot only when no invalidation happened while it
// was being built. It is separate to make the invalidation race testable
// without reading real session stores.
func (c *snapshotCache) finishBuild(snap *snapshot, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	published := c.generation == generation
	if published {
		// A TTL rebuild is a new immutable view even when no explicit
		// invalidation happened. Clients compare this version across rows
		// and stats requests, so it must advance for every published build.
		c.version++
		snap.Version = c.version
		c.cur = snap
	}
	c.building = false
	close(c.done)
	return published
}

// invalidate forces the next read to rebuild. Called after an operation that
// changes what is on disk, so the UI reflects it immediately.
func (c *snapshotCache) invalidate() {
	c.mu.Lock()
	c.generation++
	c.cur = nil
	c.mu.Unlock()
}

// footprints totals on-disk usage.
//
// Walking tens of gigabytes takes minutes on Windows once a virus scanner is
// in the path, so this never blocks a request. Callers get the last known
// value — zero on a cold start — and a refresh runs in the background. The
// footprint is a headline number, not something the page needs in order to
// work, and blocking first paint on it made the server look hung.
func (c *snapshotCache) footprints() (map[core.Tool]int64, int64) {
	c.footMu.Lock()
	stale := c.foot == nil || time.Since(c.footAt) > footprintTTL
	if stale && !c.footBuilding {
		c.footBuilding = true
		go c.refreshFootprints()
	}
	f, total := c.foot, c.footTotal
	c.footMu.Unlock()

	if f == nil {
		f = map[core.Tool]int64{}
	}
	return f, total
}

// refreshFootprints does the expensive walk off the request path.
func (c *snapshotCache) refreshFootprints() {
	f := adapter.Footprints()
	var total int64
	for _, n := range f {
		total += n
	}

	c.footMu.Lock()
	c.foot, c.footTotal, c.footAt = f, total, time.Now()
	c.footBuilding = false
	c.footMu.Unlock()

	// The snapshot embeds the footprint, so let it pick up the new figure.
	c.mu.Lock()
	if c.cur != nil {
		c.cur.State.FootprintByte = total
	}
	c.mu.Unlock()
}

// buildSnapshot does the expensive work exactly once per refresh.
//
// Sessions come from the index, not the source stores. Re-reading every store
// means peeking inside ~900 transcripts to recover their working directories,
// which takes minutes — the CLI never noticed because it always passes a
// narrow scope, while the UI needs everything.
func (s *Server) buildSnapshot(c *snapshotCache) *snapshot {
	stored, err := s.db.ReadSessionSnapshot()
	hasIndex := err == nil && hasIndexSnapshot(stored)
	sessions := stored.Sessions
	// Cold index: fall back to reading the stores so the UI works before the
	// first scan. Do not asynchronously write this captured list: a complete
	// refresh can finish before that goroutine runs, and stamping old
	// observations at write time would resurrect rows it just proved absent.
	// Recollect under the scan lock instead.
	if !hasIndex {
		sessions, _ = adapter.Collect(core.Scope{IncludeNoise: true})
	} else {
		// The index has no notion of which sessions are open right now, and
		// that changes minute to minute. Overlaying it is cheap: a handful of
		// small marker files plus a liveness check.
		overlayLive(sessions)
	}
	if s.backgroundWork {
		go s.refreshIndexInBackground()
	}

	st := guide.State{HasIndex: true, Sessions: len(sessions)}
	var largest int64
	byWorkspace := map[string]int{}
	cutoff := time.Now().AddDate(0, 0, -14)

	for _, x := range sessions {
		if x.Live != nil {
			st.LiveSessions++
		}
		if !x.DirExists() {
			st.DeadDirs++
		}
		switch x.Risk() {
		case core.RiskCritical:
			st.CriticalRisk++
			st.AtRisk++
			if x.Bytes > largest {
				largest, st.LargestAtRiskID = x.Bytes, shortID(x.ID)
			}
		case core.RiskWarn, core.RiskWatch:
			st.AtRisk++
		}
		if !x.Noise && x.Updated.After(cutoff) && x.Dir != "" {
			byWorkspace[x.Dir]++
		}
	}

	st.BusiestWorkspace = guide.BusiestScope(byWorkspace)

	if s.backgroundWork {
		_, st.FootprintByte = c.footprints()
	}

	if t, err := s.db.Aggregate(""); err == nil {
		st.Assayed = int(t.Assayed)
		st.ReclaimBytes = t.Reclaimable()
	}
	if counts, err := s.db.NuggetCounts(); err == nil {
		for _, n := range counts {
			st.Nuggets += int(n)
		}
	}
	if as, err := s.db.Artifacts(1); err == nil {
		st.Artifacts = len(as)
	}

	return &snapshot{
		Sessions:      sessions,
		State:         st,
		TakenAt:       time.Now(),
		IndexedAt:     stored.IndexedAt,
		ToolIndexedAt: stored.ToolIndexedAt,
	}
}

// hasIndexSnapshot distinguishes an uninitialized index from an authoritative
// empty one. A successful refresh may legitimately find zero sessions; using
// len(sessions)==0 as the cold-cache test would then synchronously reread all
// source stores every 15 seconds and make an empty UI look hung.
func hasIndexSnapshot(snap index.SessionSnapshot) bool {
	return len(snap.Sessions) > 0 || !snap.IndexedAt.IsZero()
}

// overlayLive marks the sessions that are open right now.
//
// Liveness is the one fact the index cannot hold, because it changes from
// minute to minute. Recovering it is cheap — a few small marker files — so it
// is layered over the indexed list rather than forcing a full re-read.
func overlayLive(sessions []core.Session) {
	live := adapter.LiveSessions()
	if len(live) == 0 {
		return
	}
	for i := range sessions {
		if l, ok := live[sessions[i].ID]; ok {
			sessions[i].Live = l
		}
	}
}

// refreshIndexInBackground keeps the index current without any request
// waiting on it. Guarded so only one refresh runs at a time.
func (s *Server) refreshIndexInBackground() {
	s.reindexMu.Lock()
	if s.reindexing ||
		(!s.reindexedAt.IsZero() && time.Since(s.reindexedAt) < 5*time.Minute) ||
		(!s.retryAt.IsZero() && time.Since(s.retryAt) < partialRetryTTL) {
		s.reindexMu.Unlock()
		return
	}
	s.reindexing = true
	s.retryAt = time.Now()
	s.reindexMu.Unlock()

	// Reuse metadata from the last scan so unchanged transcripts are not
	// reopened. Without this a refresh costs minutes of virus-scanned reads.
	if m := s.db.PeekCache(); len(m) > 0 {
		adapter.SetPeekCache(m)
	}

	lock, err := s.db.AcquireScanLock()
	if err != nil {
		s.reindexMu.Lock()
		s.reindexing = false
		s.reindexedAt = time.Time{}
		s.retryAt = time.Now()
		s.reindexMu.Unlock()
		return
	}
	defer lock.Release()

	scope := core.Scope{IncludeNoise: true}
	generation, err := s.db.NextScanGeneration()
	if err != nil {
		s.reindexMu.Lock()
		s.reindexing = false
		s.reindexedAt = time.Time{}
		s.retryAt = time.Now()
		s.reindexMu.Unlock()
		return
	}
	sessions, collected := adapter.CollectDetailed(scope)
	scannedAt := time.Now()
	completeAll := false
	if err := s.db.PutSessionsWithGeneration(sessions, generation, scannedAt); err == nil {
		// This is a complete, successfully read source list. Anything absent
		// is now provably stale only for the adapters that completed. A failed
		// Claude read must not stop Copilot cleanup, nor may it make Claude
		// absence evidence.
		tools := index.AuthoritativeTools(scope, collected.Complete)
		if len(tools) > 0 {
			authoritative := index.SessionsForTools(sessions, tools)
			allRequested := collected.IsAllSourcesComplete(scope)
			if report, err := s.db.ReconcileAndMark(authoritative, tools, generation, scannedAt, allRequested); err == nil {
				completeAll = report.AuthoritativeAll
			}
		}
	}

	s.reindexMu.Lock()
	s.reindexing = false
	if completeAll {
		s.reindexedAt = time.Now()
		s.retryAt = time.Time{}
	} else {
		s.reindexedAt = time.Time{}
		s.retryAt = time.Now()
	}
	s.reindexMu.Unlock()

	s.cache.invalidate()
}
