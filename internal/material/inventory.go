package material

import (
	"fmt"
	"sort"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/redact"
)

type Inventory struct {
	Sessions      []core.Session `json:"sessions"`
	Total         int            `json:"total"`
	Matched       int            `json:"matched"`
	ExcludedNoise int            `json:"excluded_noise"`
	Offset        int            `json:"offset"`
	NextOffset    *int           `json:"next_offset,omitempty"`
	StoresRead    []core.Tool    `json:"stores_read"`
	Warnings      []string       `json:"warnings"`
	Partial       bool           `json:"partial"`
}

func List(roots adapter.Roots, scope core.Scope, offset, limit int) (Inventory, error) {
	out := Inventory{Sessions: []core.Session{}, StoresRead: []core.Tool{}, Warnings: []string{}, Offset: offset}
	for _, tool := range scope.Tools {
		if tool != core.ToolCopilot && tool != core.ToolClaude && tool != core.ToolOpencode {
			return out, fmt.Errorf("unsupported source tool %q", tool)
		}
	}
	if scope.Days < 0 || offset < 0 {
		return out, fmt.Errorf("days and offset must be non-negative")
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return out, fmt.Errorf("inventory limit must be 1..100")
	}
	wide := scope
	wide.IncludeNoise = true
	wide.Limit = 0
	all := []core.Session{}
	for _, a := range adapter.AllWithRoots(roots) {
		if !scope.WantsTool(a.Tool()) {
			continue
		}
		if !a.Available() {
			if len(scope.Tools) > 0 {
				out.Warnings = append(out.Warnings, fmt.Sprintf("%s source is unavailable", a.Tool()))
				out.Partial = true
			}
			continue
		}
		rows, err := a.Sessions(wide)
		if err != nil {
			out.Warnings = append(out.Warnings, err.Error())
			out.Partial = true
		} else {
			out.StoresRead = append(out.StoresRead, a.Tool())
		}
		for _, row := range rows {
			if wide.Match(row) {
				all = append(all, row)
			}
		}
	}
	if len(out.StoresRead) == 0 && len(all) == 0 {
		return out, fmt.Errorf("no readable source stores in scope; warnings: %v", out.Warnings)
	}
	out.Matched = len(all)
	filtered := []core.Session{}
	for _, row := range all {
		if row.Noise && !scope.IncludeNoise {
			out.ExcludedNoise++
			continue
		}
		row.Title = clipText(redact.Text(row.Title).Text, 160)
		filtered = append(filtered, row)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Updated.Equal(filtered[j].Updated) {
			return string(filtered[i].Tool)+filtered[i].ID < string(filtered[j].Tool)+filtered[j].ID
		}
		return filtered[i].Updated.After(filtered[j].Updated)
	})
	out.Total = len(filtered)
	if offset > out.Total {
		return out, fmt.Errorf("offset exceeds matching sessions")
	}
	end := offset + limit
	if end > out.Total {
		end = out.Total
	}
	out.Sessions = append(out.Sessions, filtered[offset:end]...)
	if end < out.Total {
		out.NextOffset = &end
	}
	return out, nil
}
