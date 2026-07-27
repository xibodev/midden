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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
)

//go:embed ui/*
var uiFS embed.FS

// Server exposes the index and adapters over HTTP.
type Server struct {
	db *index.DB
}

func NewServer(db *index.DB) *Server { return &Server{db: db} }

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
	mux.HandleFunc("/api/assay", s.handleAssay)
	mux.HandleFunc("/api/ops", s.handleOps)

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
	sessions, _ := adapter.Collect(s.scopeFrom(r))
	out := make([]sessionView, 0, len(sessions))
	for _, x := range sessions {
		out = append(out, toView(x))
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
	sessions, _ := adapter.Collect(core.Scope{IncludeNoise: true})

	byTool := map[string]int{}
	var atRisk []sessionView
	live, dead := 0, 0
	for _, x := range sessions {
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

	footprints := adapter.Footprints()
	var total int64
	stores := map[string]int64{}
	for t, n := range footprints {
		stores[string(t)] = n
		total += n
	}

	counts, _ := s.db.NuggetCounts()
	writeJSON(w, map[string]any{
		"sessions": len(sessions), "by_tool": byTool, "footprint": total,
		"stores": stores, "live": live, "dead_workspaces": dead,
		"at_risk": atRisk, "nuggets": counts,
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
