// Package web serves Midden's UI from the binary itself.
//
// The UI is embedded with embed.FS and bound to localhost, so there is no
// build step, no node_modules, and no server to deploy. Technically it is
// client-server; operationally it is a double-click.
//
// A browser tab cannot read ~/.claude/projects or open a 223 MB SQLite file,
// which is why "just a static page" was never an option.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/advise"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/oracle"
	"github.com/mekjr1/midden/internal/refine"
)

//go:embed ui/*
var uiFS embed.FS

// Server exposes the index and adapters over HTTP.
type Server struct {
	db    *index.DB
	jobs  *Jobs
	cache *snapshotCache

	reindexMu   sync.Mutex
	reindexing  bool
	reindexedAt time.Time
}

func NewServer(db *index.DB) *Server {
	s := &Server{db: db, jobs: NewJobs(), cache: newSnapshotCache()}
	// Start the expensive disk walk immediately so the headline figure is
	// usually ready by the time the operator looks at it, without any request
	// ever waiting on it.
	go s.cache.footprints()
	return s
}

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(uiFS, "ui")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	}

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/nuggets", s.handleNuggets)
	mux.HandleFunc("/api/artifacts", s.handleArtifacts)
	mux.HandleFunc("/api/artifact", s.handleArtifactBody)
	mux.HandleFunc("/api/assay", s.handleAssay)
	mux.HandleFunc("/api/ops", s.handleOps)
	mux.HandleFunc("/api/action", s.handleAction)
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("/api/job-status", s.handleJobStatus)
	mux.HandleFunc("/api/cost", s.handleCost)
	mux.HandleFunc("/api/templates", s.handleTemplates)
	mux.HandleFunc("/api/resume", s.handleResume)
	mux.HandleFunc("/api/next", s.handleNext)
	mux.HandleFunc("/api/ask-suggestions", s.handleAskSuggestions)

	return localOnly(mux)
}

// localOnly rejects non-loopback callers.
//
// This process can read every session on the machine, so it must never be
// reachable from the network even if the port is accidentally exposed.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.RemoteAddr
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		host = strings.Trim(host, "[]")
		if host != "127.0.0.1" && host != "::1" && host != "localhost" {
			http.Error(w, "midden serves loopback only", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	enc.Encode(v)
}

func (s *Server) scopeFrom(r *http.Request) core.Scope {
	q := r.URL.Query()
	sc := core.Scope{
		Workspace:    q.Get("workspace"),
		IncludeNoise: q.Get("all") == "1",
	}
	if d, err := strconv.Atoi(q.Get("days")); err == nil {
		sc.Days = d
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil {
		sc.Limit = l
	}
	switch core.Tool(q.Get("tool")) {
	case core.ToolCopilot:
		sc.Tools = []core.Tool{core.ToolCopilot}
	case core.ToolClaude:
		sc.Tools = []core.Tool{core.ToolClaude}
	case core.ToolOpencode:
		sc.Tools = []core.Tool{core.ToolOpencode}
	}
	return sc
}

type sessionView struct {
	Tool      string  `json:"tool"`
	ID        string  `json:"id"`
	Short     string  `json:"short"`
	Dir       string  `json:"dir"`
	Title     string  `json:"title"`
	Repo      string  `json:"repo,omitempty"`
	Updated   string  `json:"updated"`
	Age       string  `json:"age"`
	SpanDays  float64 `json:"span_days"`
	Turns     int     `json:"turns"`
	Bytes     int64   `json:"bytes"`
	Risk      string  `json:"risk"`
	Live      bool    `json:"live"`
	DirExists bool    `json:"dir_exists"`
	Noise     bool    `json:"noise"`
	Resume    string  `json:"resume"`
}

func toView(s core.Session) sessionView {
	resume := ""
	if a := adapter.Find(s.Tool); a != nil {
		resume = adapter.WalkAndResume(s.Dir, a.ResumeCmd(s, ""))
	}
	short := s.ID
	if len(short) > 8 {
		short = short[:8]
	}
	return sessionView{
		Tool: string(s.Tool), ID: s.ID, Short: short, Dir: s.Dir,
		Title: s.Title, Repo: s.Repo,
		Updated: s.Updated.Format(time.RFC3339), Age: humanAge(s.Age()),
		SpanDays: round1(s.SpanDays()), Turns: s.Turns, Bytes: s.Bytes,
		Risk: s.Risk().String(), Live: s.Live != nil,
		DirExists: s.DirExists(), Noise: s.Noise, Resume: resume,
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	// Filter the shared snapshot rather than re-reading every store.
	sc := s.scopeFrom(r)
	snap := s.cache.get(s)

	out := make([]sessionView, 0, 64)
	for _, x := range snap.Sessions {
		if !sc.Match(x) {
			continue
		}
		out = append(out, toView(x))
		if sc.Limit > 0 && len(out) >= sc.Limit {
			break
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	matches, _ := adapter.Collect(core.Scope{IDPrefix: id, IncludeNoise: true})
	if len(matches) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	sess := matches[0]
	resp := map[string]any{"session": toView(sess)}

	if h, ok := adapter.Find(sess.Tool).(core.Harvester); ok {
		if hv, err := h.Harvest(sess, 8); err == nil {
			resp["harvest"] = hv
		}
	}
	writeJSON(w, resp)
}

func (s *Server) handleAssay(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		t, err := s.db.Aggregate(r.URL.Query().Get("tool"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"sessions": t.Sessions, "assayed": t.Assayed, "bytes": t.Bytes,
			"signal": t.Signal, "exhaust": t.Exhaust, "artifact": t.Artifact,
			"bookkeeping": t.Book, "reclaimable": t.Reclaimable(),
			"compression": round1(t.Compression()),
			"images":      t.Images, "image_clusters": t.Clusters,
		})
		return
	}

	matches, _ := adapter.Collect(core.Scope{IDPrefix: id, IncludeNoise: true})
	if len(matches) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a, ok := adapter.Find(matches[0].Tool).(adapter.Assayer)
	if !ok {
		http.Error(w, "no assayer", http.StatusNotImplemented)
		return
	}
	m, err := a.Assay(matches[0], 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"session": toView(matches[0]), "manifest": m,
		"compression":       round1(m.Compression()),
		"slice_tokens":      m.EstSliceTokens(),
		"slice_compression": round1(m.SliceCompression()),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	snap := s.cache.get(s)

	byTool := map[string]int{}
	var atRisk []sessionView
	live, dead := 0, 0
	for _, x := range snap.Sessions {
		byTool[string(x.Tool)]++
		if x.Risk() != core.RiskNone {
			atRisk = append(atRisk, toView(x))
		}
		if x.Live != nil {
			live++
		}
		if !x.DirExists() {
			dead++
		}
	}
	sort.Slice(atRisk, func(i, j int) bool { return atRisk[i].Bytes > atRisk[j].Bytes })

	footprints, total := s.cache.footprints()
	stores := map[string]int64{}
	for t, n := range footprints {
		stores[string(t)] = n
	}

	counts, _ := s.db.NuggetCounts()

	// The page is served from an index, so say when that index was built.
	// Serving indexed data as though it were live is how a tool starts
	// lying quietly.
	indexedAt := ""
	if t := s.db.IndexedAt(); !t.IsZero() {
		indexedAt = t.Format(time.RFC3339)
	}

	writeJSON(w, map[string]any{
		"sessions": len(snap.Sessions), "by_tool": byTool, "footprint": total,
		"stores": stores, "live": live, "dead_workspaces": dead,
		"at_risk": atRisk, "nuggets": counts,
		"as_of":             snap.TakenAt.Format(time.RFC3339),
		"indexed_at":        indexedAt,
		"footprint_pending": total == 0,
	})
}

func (s *Server) handleNuggets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 100
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		limit = l
	}
	ns, err := s.db.Nuggets(index.NuggetQuery{
		Kind: q.Get("kind"), Workspace: q.Get("workspace"),
		Search: q.Get("search"), Limit: limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ns)
}

func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	as, err := s.db.Artifacts(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, as)
}

// handleArtifactBody reads one generated document back.
//
// The list showed a path and nothing else, so the only way to read something
// midden had written was to leave and open a file — the tool produced work it
// could not show you.
//
// Reads are confined to the artifacts directory. The path is resolved and
// checked against that root rather than pattern-matched, so symlinks and
// traversal both fail closed: this server binds to loopback, but "only I can
// reach it" is not a reason to serve arbitrary files.
func (s *Server) handleArtifactBody(w http.ResponseWriter, r *http.Request) {
	root, err := filepath.Abs(filepath.Join(index.Dir(), "artifacts"))
	if err != nil {
		http.Error(w, "artifacts unavailable", http.StatusInternalServerError)
		return
	}

	want, err := filepath.EvalSymlinks(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}

	rel, err := filepath.Rel(root, want)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.Error(w, "outside the artifacts directory", http.StatusForbidden)
		return
	}

	body, err := os.ReadFile(want)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"path": want,
		"name": filepath.Base(want),
		"body": string(body),
	})
}

func (s *Server) handleOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.db.Operations(100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ops)
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

// handleCost exposes the ledger and calibration, so the UI can show what
// anything cost rather than leaving the operator to guess.
func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	runs, err := s.db.Runs(30, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	totals, err := s.db.Costs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	calib := map[string]any{}
	for _, op := range []string{"reclaim", "refine"} {
		if st, err := s.db.CalibrationFor(op); err == nil && st.Samples > 0 {
			calib[op] = map[string]any{
				"samples": st.Samples, "mean_factor": round1(st.MeanFactor),
				"min_factor": round1(st.MinFactor), "max_factor": round1(st.MaxFactor),
				"mean_unit": round1(st.MeanUnit), "unit": st.UnitName,
			}
		}
	}
	writeJSON(w, map[string]any{"runs": runs, "totals": totals, "calibration": calib})
}

// handleTemplates lists artifact kinds and what the current evidence supports.
func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	ns, _ := s.db.Nuggets(index.NuggetQuery{Workspace: r.URL.Query().Get("workspace")})

	type item struct {
		Name      string `json:"name"`
		Title     string `json:"title"`
		Audience  string `json:"audience"`
		Supported bool   `json:"supported"`
		Why       string `json:"why,omitempty"`
	}
	supported := map[string]string{}
	for _, c := range refine.Catalog(ns, 2) {
		supported[c.Template] = c.Why
	}

	out := make([]item, 0, len(refine.Templates))
	for _, t := range refine.Templates {
		why, ok := supported[t.Name]
		out = append(out, item{Name: t.Name, Title: t.Title,
			Audience: t.Audience, Supported: ok, Why: why})
	}
	writeJSON(w, map[string]any{"templates": out, "nuggets": len(ns)})
}

// handleResume builds a resume one-liner, optionally carrying an instruction.
//
// This is the "set an instruction, copy the command" flow: composing the
// instruction in the UI and pasting one line into a terminal.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	matches, _ := adapter.Collect(core.Scope{IDPrefix: id, IncludeNoise: true})
	if len(matches) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	sess := matches[0]

	a := adapter.Find(sess.Tool)
	if a == nil {
		http.Error(w, "no adapter", http.StatusInternalServerError)
		return
	}

	var warnings []string
	if sess.Live != nil {
		warnings = append(warnings, fmt.Sprintf(
			"already open (pid %d, %s) — switch to that terminal instead", sess.Live.PID, sess.Live.Status))
	}
	if !sess.DirExists() {
		warnings = append(warnings, "workspace no longer exists: "+sess.Dir)
	}
	if sess.Risk() >= core.RiskWarn {
		warnings = append(warnings, fmt.Sprintf(
			"transcript is %d MiB (%s) — resume may time out and silently start a NEW session; prefer a handoff brief",
			sess.Bytes>>20, sess.Risk()))
	}

	writeJSON(w, map[string]any{
		"command":  adapter.WalkAndResume(sess.Dir, a.ResumeCmd(sess, q.Get("instruction"))),
		"warnings": warnings,
		"session":  toView(sess),
	})
}

// handleJobStatus is an alias for a single job, so the UI can poll one id
// without fetching the whole list.
func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.get(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, job)
}

// handleNext returns what is worth doing now, computed from observed state.
//
// The UI previously opened on four action cards in a fixed order with no
// indication of which mattered today. This is the same guidance the CLI
// prints, so both surfaces agree.
func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	st := s.guideState()
	writeJSON(w, map[string]any{
		"state":    st,
		"steps":    guide.Next(st),
		"cheapest": guide.Cheapest(),
		"spending": guide.Spending(),
	})
}

// guideState reads the shared snapshot rather than re-deriving from disk.
//
// This used to walk ~40 GiB and read every session on every call, which made
// the polling UI hang the server.
func (s *Server) guideState() guide.State {
	return s.cache.get(s).State
}

// buildBrief assembles the compressed picture the oracle reasons over.
//
// Mirrors the CLI so both surfaces answer from identical evidence.
func (s *Server) buildBrief(st guide.State) oracle.Brief {
	b := oracle.Brief{State: st}

	if ns, err := s.db.Nuggets(index.NuggetQuery{Limit: oracle.MaxNuggets}); err == nil {
		b.Nuggets = ns
	}
	if t, err := s.db.Costs(); err == nil {
		b.Costs = t
	}

	sessions := s.cache.get(s).Sessions
	totals, _ := s.db.Aggregate("")
	counts, _ := s.db.NuggetCounts()

	b.Findings = advise.Analyse(advise.Input{
		Sessions: sessions, Footprints: adapter.Footprints(),
		Assayed: totals.Assayed, Signal: totals.Signal, Exhaust: totals.Exhaust,
		Artifact: totals.Artifact, Book: totals.Book, DupBytes: totals.DupBytes,
		Images: totals.Images, Clusters: totals.Clusters, Nuggets: counts,
	})

	agg := map[string]*oracle.DirStat{}
	for _, x := range sessions {
		if x.Noise || x.Dir == "" {
			continue
		}
		d, ok := agg[x.Dir]
		if !ok {
			d = &oracle.DirStat{Dir: x.Dir}
			agg[x.Dir] = d
		}
		d.Sessions++
		d.Bytes += x.Bytes
	}
	for _, d := range agg {
		b.TopDirs = append(b.TopDirs, *d)
	}
	sort.Slice(b.TopDirs, func(i, j int) bool { return b.TopDirs[i].Bytes > b.TopDirs[j].Bytes })
	if len(b.TopDirs) > 8 {
		b.TopDirs = b.TopDirs[:8]
	}
	return b
}

// handleAskSuggestions offers starter questions, so an empty prompt is not a
// dead end.
func (s *Server) handleAskSuggestions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"suggestions": oracle.Suggestions(s.guideState())})
}
