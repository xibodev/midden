// Package advise turns the index into recommendations that reduce future
// exhaust.
//
// Everything here is deterministic and cites its evidence. "An oracle that
// finds patterns" is unfalsifiable; "these four sessions produce 60% of your
// bytes" is checkable, and checkable is the only kind of advice worth giving.
package advise

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mekjr1/midden/internal/core"
)

// Severity orders findings by how much they matter.
type Severity int

const (
	Info Severity = iota
	Low
	Medium
	High
)

func (s Severity) String() string {
	switch s {
	case High:
		return "high"
	case Medium:
		return "medium"
	case Low:
		return "low"
	}
	return "info"
}

// Finding is one recommendation with the evidence behind it.
type Finding struct {
	Severity Severity `json:"severity"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Evidence string   `json:"evidence"`
	Action   string   `json:"action"`
	Bytes    int64    `json:"bytes,omitempty"`
}

// Input is everything the analysis reads.
type Input struct {
	Sessions   []core.Session
	Footprints map[core.Tool]int64
	Assayed    int64
	Signal     int64
	Exhaust    int64
	Artifact   int64
	Book       int64
	DupBytes   int64
	Images     int64
	Clusters   int64
	Nuggets    map[string]int64
	MCPServers map[string]int
}

// Analyse produces findings ordered by severity then size.
func Analyse(in Input) []Finding {
	var out []Finding

	out = append(out, riskFindings(in)...)
	out = append(out, concentrationFindings(in)...)
	out = append(out, exhaustFindings(in)...)
	out = append(out, hygieneFindings(in)...)
	out = append(out, workflowFindings(in)...)
	out = append(out, harvestFindings(in)...)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].Bytes > out[j].Bytes
	})
	return out
}

// riskFindings reports sessions approaching the resume cliff.
func riskFindings(in Input) []Finding {
	var crit, warn []core.Session
	for _, s := range in.Sessions {
		switch s.Risk() {
		case core.RiskCritical:
			crit = append(crit, s)
		case core.RiskWarn:
			warn = append(warn, s)
		}
	}

	var out []Finding
	if len(crit) > 0 {
		var b int64
		names := make([]string, 0, len(crit))
		for _, s := range crit {
			b += s.Bytes
			if len(names) < 3 {
				names = append(names, core.Truncate(s.Title, 28))
			}
		}
		out = append(out, Finding{
			Severity: High, Category: "risk",
			Title: fmt.Sprintf("%d session(s) past the resume cliff", len(crit)),
			Evidence: fmt.Sprintf("%s across %d transcripts, including: %s",
				humanBytes(b), len(crit), strings.Join(names, "; ")),
			Action: "These will not reload — --resume times out and silently starts a NEW " +
				"session. Harvest a brief and hand off: midden brief <id> --handoff",
			Bytes: b,
		})
	}
	if len(warn) > 0 {
		out = append(out, Finding{
			Severity: Medium, Category: "risk",
			Title:    fmt.Sprintf("%d session(s) approaching the resume cliff", len(warn)),
			Evidence: "transcripts between 450 and 640 MiB",
			Action:   "Finish at a milestone and hand off before they cross. Run midden watch to be warned.",
		})
	}
	return out
}

// concentrationFindings finds the few sessions responsible for most bytes.
func concentrationFindings(in Input) []Finding {
	sorted := append([]core.Session(nil), in.Sessions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Bytes > sorted[j].Bytes })

	var total int64
	for _, s := range sorted {
		total += s.Bytes
	}
	if total == 0 || len(sorted) < 10 {
		return nil
	}

	// How few sessions account for half the bytes?
	var acc int64
	n := 0
	for _, s := range sorted {
		acc += s.Bytes
		n++
		if acc*2 >= total {
			break
		}
	}
	pctSessions := 100 * float64(n) / float64(len(sorted))
	if pctSessions > 20 {
		return nil
	}

	return []Finding{{
		Severity: Medium, Category: "concentration",
		Title: fmt.Sprintf("%d session(s) hold half your transcript bytes", n),
		Evidence: fmt.Sprintf("%d of %d sessions (%.1f%%) account for %s of %s",
			n, len(sorted), pctSessions, humanBytes(acc), humanBytes(total)),
		Action: "Long single sessions concentrate risk and cost. Hand off at milestone " +
			"boundaries rather than growing one transcript indefinitely.",
		Bytes: acc,
	}}
}

// exhaustFindings reports avoidable bulk.
func exhaustFindings(in Input) []Finding {
	var out []Finding
	totalClassified := in.Signal + in.Exhaust + in.Artifact + in.Book
	if totalClassified == 0 {
		return nil
	}

	if share := float64(in.Exhaust) / float64(totalClassified); share > 0.35 {
		out = append(out, Finding{
			Severity: Medium, Category: "exhaust",
			Title: fmt.Sprintf("Tool output is %.0f%% of your classified bytes", share*100),
			Evidence: fmt.Sprintf("%s of exhaust versus %s of signal",
				humanBytes(in.Exhaust), humanBytes(in.Signal)),
			Action: "Prefer targeted reads over whole-file dumps, and narrow greps. " +
				"midden prune --apply recovers most of this without losing meaning.",
			Bytes: in.Exhaust,
		})
	}

	if in.DupBytes > 50<<20 {
		out = append(out, Finding{
			Severity: Medium, Category: "exhaust",
			Title:    "The same tool payloads are being fetched repeatedly",
			Evidence: fmt.Sprintf("%s of byte-identical repeated payloads", humanBytes(in.DupBytes)),
			Action: "Re-reading the same files each turn is the largest avoidable cost. " +
				"Read once and refer back, or keep notes in a scratch file.",
			Bytes: in.DupBytes,
		})
	}

	if in.Images > 200 && in.Clusters > 0 {
		ratio := float64(in.Images) / float64(in.Clusters)
		if ratio > 3 {
			out = append(out, Finding{
				Severity: Low, Category: "exhaust",
				Title: fmt.Sprintf("%d screenshots collapse to %d distinct moments", in.Images, in.Clusters),
				Evidence: fmt.Sprintf("%.1f near-identical frames per cluster; artifacts total %s",
					ratio, humanBytes(in.Artifact)),
				Action: "Capture one frame per state rather than per action. Vision analysis " +
					"over near-duplicates costs the same as over distinct evidence.",
				Bytes: in.Artifact,
			})
		}
	}
	return out
}

// hygieneFindings reports dead and noisy sessions.
func hygieneFindings(in Input) []Finding {
	var dead, noise int
	var deadBytes int64
	for _, s := range in.Sessions {
		if !s.DirExists() {
			dead++
			deadBytes += s.Bytes
		}
		if s.Noise {
			noise++
		}
	}

	var out []Finding
	if dead > 20 {
		out = append(out, Finding{
			Severity: Low, Category: "hygiene",
			Title:    fmt.Sprintf("%d session(s) point at workspaces that no longer exist", dead),
			Evidence: fmt.Sprintf("%s of transcripts for deleted directories", humanBytes(deadBytes)),
			Action:   "These can never be resumed. Harvest anything worth keeping, then archive them.",
			Bytes:    deadBytes,
		})
	}
	if noise > len(in.Sessions)/3 && noise > 20 {
		out = append(out, Finding{
			Severity: Info, Category: "hygiene",
			Title: fmt.Sprintf("%d of %d sessions are automated spawns or probes", noise, len(in.Sessions)),
			Evidence: "sessions with agent-guide preambles, shell health probes, or a single " +
				"trivial turn",
			Action: "Normal for agent-heavy workflows. They are hidden by default; nothing to do " +
				"unless they are consuming meaningful disk.",
		})
	}
	return out
}

// workflowFindings reports patterns in how sessions are used.
func workflowFindings(in Input) []Finding {
	var out []Finding

	byDir := map[string]int{}
	var longRunning int
	for _, s := range in.Sessions {
		if s.Noise {
			continue
		}
		byDir[s.Dir]++
		if s.SpanDays() >= 7 {
			longRunning++
		}
	}

	type kv struct {
		dir string
		n   int
	}
	var dirs []kv
	for d, n := range byDir {
		dirs = append(dirs, kv{d, n})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].n > dirs[j].n })

	if len(dirs) > 0 && dirs[0].n >= 15 {
		out = append(out, Finding{
			Severity: Low, Category: "workflow",
			Title:    fmt.Sprintf("%d sessions in one workspace", dirs[0].n),
			Evidence: dirs[0].dir,
			Action: "Repeated sessions in one place usually means repeated context-setting. " +
				"A rules file or skill would pay for itself here.",
		})
	}

	if longRunning >= 5 {
		out = append(out, Finding{
			Severity: Low, Category: "workflow",
			Title:    fmt.Sprintf("%d session(s) have been open longer than a week", longRunning),
			Evidence: "created 7+ days before their last activity",
			Action: "Long-lived sessions accumulate context you are paying to re-read. " +
				"Hand off at milestones to keep each session lean.",
		})
	}
	return out
}

// harvestFindings reports value sitting unclaimed.
func harvestFindings(in Input) []Finding {
	var nuggets int64
	for _, n := range in.Nuggets {
		nuggets += n
	}

	reclaimable := in.Exhaust + in.Book
	if nuggets == 0 && in.Assayed > 0 {
		return []Finding{{
			Severity: Medium, Category: "harvest",
			Title:    "Nothing has been reclaimed yet",
			Evidence: fmt.Sprintf("%d session(s) assayed, %s of signal available, 0 nuggets extracted", in.Assayed, humanBytes(in.Signal)),
			Action: "Deleting before harvesting throws away the only record of why decisions " +
				"were made. Start with: midden reclaim --days 14",
			Bytes: reclaimable,
		}}
	}

	if nuggets > 0 && nuggets < 20 {
		return []Finding{{
			Severity: Info, Category: "harvest",
			Title:    fmt.Sprintf("%d nugget(s) reclaimed so far", nuggets),
			Evidence: "enough to seed an artifact but not to cover a project",
			Action:   "Widen the scope: midden reclaim --days 30, then midden catalog",
		}}
	}
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit && exp < 4; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// Age is unused today but keeps the signature stable for time-based rules.
var _ = time.Now
