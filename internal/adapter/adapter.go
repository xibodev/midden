// Package adapter reads sessions from each AI CLI's private storage.
//
// Every adapter opens its source store READ-ONLY. These are undocumented
// private formats belonging to tools that may be running concurrently; the
// rule is adaptive for read, conservative and version-pinned for write.
package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/core"
)

// All returns every adapter, whether or not its data is present.
func All() []core.Adapter {
	return []core.Adapter{
		NewCopilot(),
		NewClaude(),
		NewOpencode(),
	}
}

// Available returns only the adapters whose data exists on this machine.
func Available() []core.Adapter {
	var out []core.Adapter
	for _, a := range All() {
		if a.Available() {
			out = append(out, a)
		}
	}
	return out
}

// Find returns the adapter for a tool.
func Find(t core.Tool) core.Adapter {
	for _, a := range All() {
		if a.Tool() == t {
			return a
		}
	}
	return nil
}

// CollectExact resolves composite tool:id selections without collecting every
// installed adapter. The caller gets only the requested tool stores and exact
// IDs; a missing or ambiguous selection is an error rather than a widened
// recovery scope.
func CollectExact(keys []string) ([]core.Session, []error) {
	return collectExactFrom(keys, All())
}

func collectExactFrom(keys []string, adapters []core.Adapter) ([]core.Session, []error) {
	wanted := map[core.Tool]map[string]bool{}
	for _, key := range keys {
		parts := strings.SplitN(strings.TrimSpace(key), ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, []error{fmt.Errorf("invalid exact session key %q", key)}
		}
		tool := core.Tool(parts[0])
		if tool != core.ToolCopilot && tool != core.ToolClaude && tool != core.ToolOpencode {
			return nil, []error{fmt.Errorf("unknown session tool %q", parts[0])}
		}
		if wanted[tool] == nil {
			wanted[tool] = map[string]bool{}
		}
		wanted[tool][parts[1]] = true
	}

	// Index adapters by tool before reading any source. Exact composite keys
	// must never cause a best-effort all-adapter scan: an unrelated store may
	// be unavailable without making the selected source untrustworthy.
	byTool := make(map[core.Tool]core.Adapter, len(adapters))
	for _, candidate := range adapters {
		if _, selected := wanted[candidate.Tool()]; selected {
			byTool[candidate.Tool()] = candidate
		}
	}
	tools := make([]string, 0, len(wanted))
	for tool := range wanted {
		tools = append(tools, string(tool))
	}
	sort.Strings(tools)

	found := map[string]bool{}
	var out []core.Session
	var errs []error
	for _, rawTool := range tools {
		tool := core.Tool(rawTool)
		ids := wanted[tool]
		selected := byTool[tool]
		if selected == nil {
			errs = append(errs, fmt.Errorf("exact session source is unavailable: %s", tool))
			continue
		}
		requested := make([]string, 0, len(ids))
		for id := range ids {
			requested = append(requested, id)
		}
		sort.Strings(requested)
		sessions, err := selected.Sessions(core.Scope{Tools: []core.Tool{tool}, IDs: requested, IncludeNoise: true})
		if err != nil {
			errs = append(errs, fmt.Errorf("read exact %s session(s): %w", tool, err))
			continue
		}
		for _, session := range sessions {
			if session.Tool != tool || !ids[session.ID] {
				continue
			}
			out = append(out, session)
			found[string(tool)+":"+session.ID] = true
		}
		for _, id := range requested {
			if !found[string(tool)+":"+id] {
				errs = append(errs, fmt.Errorf("exact session not found: %s:%s", tool, id))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, errs
}

// Collect gathers sessions from every available adapter in scope, newest
// first. Adapter failures are returned alongside results rather than aborting
// the whole query: one tool's format drift must not blind the others.
func Collect(sc core.Scope) ([]core.Session, []error) {
	sessions, result := CollectDetailed(sc)
	return sessions, result.Errors
}

// CollectionResult records exactly which source stores were fully enumerated.
//
// Sessions may be returned alongside an error (for example, Claude can keep
// readable workspaces while reporting an unreadable subtree). Such a list is
// useful to display but not authoritative enough to delete absent index rows.
type CollectionResult struct {
	Attempted []core.Tool
	Complete  []core.Tool
	Errors    []error
}

// IsAllSourcesComplete reports whether an unfiltered collection still covers
// the same source stores at completion time. A store can appear while a long
// scan is reading another adapter; its absence from the original snapshot is
// not an error, but it does make an all-tools freshness claim untrue.
//
// Selected-tool scans can still mark their own tool fresh, but never the
// aggregate all-tools view.
func (r CollectionResult) IsAllSourcesComplete(sc core.Scope) bool {
	return r.isAllSourcesComplete(sc, AvailableTools(sc))
}

func (r CollectionResult) isAllSourcesComplete(sc core.Scope, current []core.Tool) bool {
	if len(sc.Tools) != 0 || len(r.Errors) != 0 || len(r.Complete) == 0 {
		return false
	}
	return sameToolSet(r.Attempted, r.Complete) &&
		sameToolSet(r.Attempted, current)
}

// AvailableTools snapshots source tools available at this instant, narrowed
// to scope. Keeping it here ensures callers compare the same adapter rule
// rather than reimplementing availability in the index or web packages.
func AvailableTools(sc core.Scope) []core.Tool {
	var out []core.Tool
	for _, a := range Available() {
		if sc.WantsTool(a.Tool()) {
			out = append(out, a.Tool())
		}
	}
	return out
}

// CollectDetailed gathers sessions and preserves the exact adapter snapshot
// that produced them. Callers that reconcile the index must use Complete, not
// call Available again after collection: a store can appear or disappear while
// a long scan is running.
func CollectDetailed(sc core.Scope) ([]core.Session, CollectionResult) {
	return collectFrom(sc, Available())
}

func collectFrom(sc core.Scope, adapters []core.Adapter) ([]core.Session, CollectionResult) {
	var (
		out    []core.Session
		result CollectionResult
	)
	for _, a := range adapters {
		if !sc.WantsTool(a.Tool()) {
			continue
		}
		result.Attempted = append(result.Attempted, a.Tool())
		reportProgress("reading " + string(a.Tool()) + " sessions")
		s, err := a.Sessions(sc)
		// An adapter can return an honest partial list alongside an error:
		// Claude deliberately skips an unreadable subtree rather than hiding
		// every other workspace. Keep the sessions we could read, but surface
		// the error so callers know absence is not proof and must not
		// reconcile-delete any indexed rows.
		out = append(out, s...)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		result.Complete = append(result.Complete, a.Tool())
	}
	reportProgress("")

	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })

	if sc.Limit > 0 && len(out) > sc.Limit {
		out = out[:sc.Limit]
	}
	return out, result
}

func sameToolSet(a, b []core.Tool) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[core.Tool]bool{}
	for _, tool := range a {
		seen[tool] = true
	}
	for _, tool := range b {
		if !seen[tool] {
			return false
		}
	}
	return true
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func homeJoin(parts ...string) string {
	return filepath.Join(append([]string{home()}, parts...)...)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// dirSize sums a directory tree. Metadata-only, so it stays fast even over
// tens of gigabytes; unreadable subtrees are skipped rather than fatal.
func dirSize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, ferr := d.Info(); ferr == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// Footprints reports total on-disk bytes per tool. This is the number the
// product exists to reduce; per-session transcript bytes are a subset.
func Footprints() map[core.Tool]int64 {
	out := map[core.Tool]int64{}
	for _, a := range Available() {
		out[a.Tool()] = a.Footprint()
	}
	return out
}

// coreScopeForID builds a scope that matches one session id prefix, including
// noise, so lookups by id never miss.
func coreScopeForID(id string) core.Scope {
	return core.Scope{IDPrefix: id, IncludeNoise: true}
}

// LiveSessions maps session id to live status for every tool that can report
// it.
//
// Only Claude writes per-session markers, so this is cheap: a few small JSON
// files plus a PID liveness check. It exists separately from Sessions() so a
// cached or indexed session list can be refreshed with current liveness
// without re-reading any transcript.
func LiveSessions() map[string]*core.Live {
	out := map[string]*core.Live{}
	for _, a := range All() {
		l, ok := a.(interface{ liveMap() map[string]*core.Live })
		if !ok {
			continue
		}
		for id, live := range l.liveMap() {
			out[id] = live
		}
	}
	return out
}
