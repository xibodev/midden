package main

import (
	"github.com/xibodev/midden/internal/assay"
	"github.com/xibodev/midden/internal/core"
	"github.com/xibodev/midden/internal/redact"
)

// Free text that core prints passes the same credential filter in text and
// JSON output; identifiers, paths and numbers are left alone. Whole texts are
// filtered before midden clips them for display, so midden never cuts a
// credential into an unrecognisable fragment.

func filterText(s string) string { return redact.Text(s).Text }

// filterHarvest returns a copy of hv with every turn text filtered. Recent is
// never nil, so an empty brief encodes it as [].
func filterHarvest(hv core.Harvest) core.Harvest {
	turn := func(t *core.Turn) *core.Turn {
		if t == nil {
			return nil
		}
		copied := *t
		copied.Text = filterText(copied.Text)
		return &copied
	}
	out := hv
	out.Goal = turn(hv.Goal)
	out.LastAssistant = turn(hv.LastAssistant)
	out.Recent = make([]core.Turn, 0, len(hv.Recent))
	for _, t := range hv.Recent {
		t.Text = filterText(t.Text)
		out.Recent = append(out.Recent, t)
	}
	return out
}

// filterManifest filters a manifest's title and candidate previews in place.
func filterManifest(m *assay.Manifest) {
	m.Title = filterText(m.Title)
	if m.Candidates == nil {
		m.Candidates = []assay.Record{}
	}
	for i := range m.Candidates {
		m.Candidates[i].Preview = filterText(m.Candidates[i].Preview)
	}
}
