// Package adapter reads sessions from each AI CLI's private storage.
//
// Every adapter opens its source store READ-ONLY. These are undocumented
// private formats belonging to tools that may be running concurrently; the
// rule is adaptive for read, conservative and version-pinned for write.
package adapter

import (
	"os"
	"path/filepath"
	"sort"

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

// Collect gathers sessions from every available adapter in scope, newest
// first. Adapter failures are returned alongside results rather than aborting
// the whole query: one tool's format drift must not blind the others.
func Collect(sc core.Scope) ([]core.Session, []error) {
	var (
		out  []core.Session
		errs []error
	)
	for _, a := range Available() {
		if !sc.WantsTool(a.Tool()) {
			continue
		}
		s, err := a.Sessions(sc)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, s...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })

	if sc.Limit > 0 && len(out) > sc.Limit {
		out = out[:sc.Limit]
	}
	return out, errs
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
