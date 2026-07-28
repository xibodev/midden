// Package guide answers one question everywhere in the product: what should I
// do next?
//
// The audit that produced this package found three failures from the UX
// anti-pattern catalog, all in Midden itself:
//
//   - Dead Ends (Category 9). `doctor` reported 37 GiB, four sessions about to
//     be lost, and 198 dead workspaces, then stopped mid-list with no action.
//     Alarm without instruction.
//   - Assumption of Context (Category 9). The pipeline order — scan, assay,
//     prune, reclaim, catalog, refine — existed only in the author's head. The
//     tool never named it.
//   - No cost taxonomy. Eighteen commands are free and two spend, and nothing
//     said so until after you had spent.
//
// Every suggestion here is derived from observed state, never from a fixed
// script, so the tool proposes what is actually worth doing next.
package guide

import (
	"fmt"
	"sort"
	"strings"
)

// Cost is what invoking a command does to your quota.
type Cost int

const (
	// Free commands are deterministic: they read, classify and rewrite files
	// without calling a model. Most of Midden is Free.
	Free Cost = iota
	// Spends commands invoke a model through your existing CLI seat.
	Spends
)

func (c Cost) String() string {
	if c == Spends {
		return "SPENDS"
	}
	return "FREE"
}

// Command describes one command's purpose and cost class.
type Command struct {
	Name  string
	Cost  Cost
	Stage string
	Blurb string
}

// Commands is the catalogue, in pipeline order rather than alphabetically.
//
// Alphabetical ordering is what made the help text unreadable: it put `advise`
// first and `scan` near the end, which is the reverse of how the tool is used.
var Commands = []Command{
	{"doctor", Free, "see", "Health check: footprint, at-risk sessions, dead workspaces"},
	{"ls", Free, "see", "List sessions across every installed AI CLI"},
	{"show", Free, "see", "One session in detail"},
	{"summarize", Free, "see", "Summarise a session: shallow is free, deep and xray spend"},
	{"resume", Free, "see", "Walk-to-workspace + resume one-liner"},
	{"brief", Free, "protect", "Harvest a session into a handoff brief"},
	{"watch", Free, "protect", "Warn before a session hits the resume cliff"},
	{"scan", Free, "sort", "Build the index (--assay to classify transcripts)"},
	{"assay", Free, "sort", "What a scope is made of, and what is reclaimable"},
	{"prune", Free, "clean", "Rewrite transcripts with bulk payloads replaced"},
	{"archive", Free, "clean", "Move a transcript out of the tool's active path"},
	{"reclaim", Spends, "mine", "Mine sessions for reusable knowledge"},
	{"nuggets", Free, "mine", "Browse what has been reclaimed"},
	{"catalog", Free, "make", "What your evidence can support (no model call)"},
	{"refine", Spends, "make", "Write artifacts from nuggets"},
	{"artifacts", Free, "make", "List what has been generated"},
	{"advise", Free, "advise", "How to produce less exhaust, with evidence"},
	{"cost", Free, "advise", "What midden has spent, and estimate accuracy"},
	{"ask", Spends, "advise", "Ask a question about your own history"},
	{"ops", Free, "advise", "Audit log of every mutating operation"},
	{"ui", Free, "surface", "Serve the web interface on loopback"},
	{"mcp", Free, "surface", "Run as an MCP server for other AI CLIs"},
	{"start", Free, "surface", "Guided first run"},
}

// Stages is the pipeline order, which is also the order to learn the tool in.
var Stages = []struct {
	Name  string
	Label string
}{
	{"see", "See what you have"},
	{"protect", "Protect work in danger"},
	{"sort", "Measure the exhaust"},
	{"clean", "Recover disk space"},
	{"mine", "Reclaim knowledge"},
	{"make", "Write something from it"},
	{"advise", "Spend less next time"},
	{"surface", "Other surfaces"},
}

// CostOf reports a command's cost class.
func CostOf(name string) Cost {
	for _, c := range Commands {
		if c.Name == name {
			return c.Cost
		}
	}
	return Free
}

// Spending lists the commands that call a model. Kept short deliberately: if
// this list grows, the promise that "almost everything is free" stops being
// true and the labelling has to change with it.
func Spending() []string {
	var out []string
	for _, c := range Commands {
		if c.Cost == Spends {
			out = append(out, c.Name)
		}
	}
	return out
}

// InStage returns the commands belonging to a pipeline stage.
func InStage(stage string) []Command {
	var out []Command
	for _, c := range Commands {
		if c.Stage == stage {
			out = append(out, c)
		}
	}
	return out
}

// State is what the tool currently knows about the machine. Suggestions are
// computed from this, so they change as the situation changes.
type State struct {
	Sessions      int
	Assayed       int
	AtRisk        int
	CriticalRisk  int
	DeadDirs      int
	LiveSessions  int
	Nuggets       int
	Artifacts     int
	ReclaimBytes  int64
	FootprintByte int64
	HasIndex      bool

	LargestAtRiskID  string
	BusiestWorkspace string
}

// Step is one recommended action.
type Step struct {
	Why      string `json:"why"`
	Command  string `json:"command"`
	Cost     Cost   `json:"-"`
	CostName string `json:"cost"`
	Priority int    `json:"-"`
	Value    string `json:"value"`
}

// Next returns the ordered actions worth taking, most valuable first.
//
// Ordering is by consequence, not by pipeline position: losing four days of
// work outranks recovering disk space, which outranks producing documentation.
func Next(s State) []Step {
	var steps []Step

	// Data loss beats everything.
	if s.CriticalRisk > 0 {
		cmd := "midden watch --once"
		if s.LargestAtRiskID != "" {
			cmd = "midden brief " + s.LargestAtRiskID + " --handoff"
		}
		steps = append(steps, Step{
			Why:      fmt.Sprintf("%d session(s) are past the resume cliff and will not reload", s.CriticalRisk),
			Command:  cmd,
			Cost:     Free,
			Priority: 100,
			Value:    "recover their context before it is lost",
		})
	} else if s.AtRisk > 0 {
		steps = append(steps, Step{
			Why:      fmt.Sprintf("%d session(s) are growing toward the resume cliff", s.AtRisk),
			Command:  "midden watch --once",
			Cost:     Free,
			Priority: 80,
			Value:    "get warned before one is lost",
		})
	}

	// You cannot decide what to clean or mine without measuring first.
	if !s.HasIndex || s.Assayed == 0 {
		steps = append(steps, Step{
			Why:      "nothing has been measured yet, so nothing downstream can be costed",
			Command:  "midden scan --assay",
			Cost:     Free,
			Priority: 90,
			Value:    "learn what your exhaust is actually made of",
		})
	} else if s.ReclaimBytes > 500<<20 {
		steps = append(steps, Step{
			Why:      fmt.Sprintf("%s is removable without losing meaning", human(s.ReclaimBytes)),
			Command:  "midden prune",
			Cost:     Free,
			Priority: 60,
			Value:    "preview the recovery — nothing is written without --apply",
		})
	}

	// Harvest before disposal: deleting first throws away the only record of
	// why decisions were made.
	if s.Nuggets == 0 && s.Assayed > 0 {
		cmd := "midden reclaim --days 7 --records 40 --dry-run"
		if s.BusiestWorkspace != "" {
			cmd = "midden reclaim --workspace " + quote(s.BusiestWorkspace) + " --records 40 --dry-run"
		}
		steps = append(steps, Step{
			Why:      "no knowledge has been reclaimed yet",
			Command:  cmd,
			Cost:     Spends,
			Priority: 50,
			Value:    "see the cost first — --dry-run never spends",
		})
	}

	if s.Nuggets > 0 && s.Artifacts == 0 {
		steps = append(steps, Step{
			Why:      fmt.Sprintf("%d nugget(s) are sitting unused", s.Nuggets),
			Command:  "midden catalog",
			Cost:     Free,
			Priority: 40,
			Value:    "see what they can support before writing anything",
		})
	}

	// Succeeding once used to end the guidance: with a nugget and an artifact
	// on record, every remaining suggestion was housekeeping, and the loop
	// that produces the value disappeared at the exact moment it was proven
	// to work. Mining is not a one-off setup step.
	if s.Nuggets > 0 && s.Artifacts > 0 && s.Assayed > 0 {
		steps = append(steps, Step{
			Why:      fmt.Sprintf("%d nugget(s) and %d artifact(s) so far — the rest is still unmined", s.Nuggets, s.Artifacts),
			Command:  "midden catalog",
			Cost:     Free,
			Priority: 35,
			Value:    "see what your evidence can support now, before spending again",
		})
	}

	if s.DeadDirs > 20 {
		steps = append(steps, Step{
			Why:      fmt.Sprintf("%d session(s) point at workspaces that no longer exist", s.DeadDirs),
			Command:  "midden archive --days 90",
			Cost:     Free,
			Priority: 30,
			Value:    "preview archiving them — dry run by default",
		})
	}

	if s.Assayed > 0 {
		steps = append(steps, Step{
			Why:      "there may be habits worth changing",
			Command:  "midden advise",
			Cost:     Free,
			Priority: 20,
			Value:    "evidence-backed ways to produce less exhaust",
		})
	}

	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Priority > steps[j].Priority })
	for i := range steps {
		steps[i].CostName = steps[i].Cost.String()
	}
	return steps
}

// Top returns the single most valuable next action, or false when there is
// nothing worth suggesting.
func Top(s State) (Step, bool) {
	steps := Next(s)
	if len(steps) == 0 {
		return Step{}, false
	}
	return steps[0], true
}

// Cheapest returns the guidance that actually reduces spend.
//
// These levers existed but were unfindable: spread across alphabetically
// sorted flag lists where the cost-relevant ones sat at positions 9 and 11.
func Cheapest() []string {
	return []string{
		"Scope hard — --workspace or one session id costs a fraction of --days 30.",
		"Fewer records — --records 40 roughly halves the evidence versus the default 120.",
		"Smaller model — --model <small>; extraction needs no deep reasoning.",
		"Batch artifacts — `refine tsg adr faq` loads evidence once, not three times.",
		"Always --dry-run first — it costs nothing and prints the estimate.",
	}
}

func human(n int64) string {
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

func quote(s string) string {
	if strings.ContainsRune(s, ' ') {
		return `"` + s + `"`
	}
	return s
}
