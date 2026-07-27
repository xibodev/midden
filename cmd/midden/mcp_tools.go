package main

// MCP tool implementations.
//
// Output here is written for a model, not a terminal: no colour, no box
// drawing, dense but unambiguous, and always within a declared token budget.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/render"
)

type mcpArgs struct {
	Tool        string `json:"tool"`
	Days        int    `json:"days"`
	Workspace   string `json:"workspace"`
	Limit       int    `json:"limit"`
	Query       string `json:"query"`
	ID          string `json:"id"`
	Turns       int    `json:"turns"`
	Instruction string `json:"instruction"`
}

func (a mcpArgs) scope() core.Scope {
	sc := core.Scope{Days: a.Days, Workspace: a.Workspace, Limit: a.Limit}
	switch core.Tool(strings.ToLower(a.Tool)) {
	case core.ToolCopilot:
		sc.Tools = []core.Tool{core.ToolCopilot}
	case core.ToolClaude:
		sc.Tools = []core.Tool{core.ToolClaude}
	case core.ToolOpencode:
		sc.Tools = []core.Tool{core.ToolOpencode}
	}
	return sc
}

func callTool(name string, raw json.RawMessage) toolResult {
	var args mcpArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return errResult("invalid arguments: %v", err)
		}
	}

	switch name {
	case "midden_health":
		return mcpHealth()
	case "midden_list_sessions":
		return mcpList(args)
	case "midden_search":
		return mcpSearch(args)
	case "midden_session_brief":
		return mcpBrief(args)
	case "midden_resume_command":
		return mcpResume(args)
	}
	return errResult("unknown tool: %s", name)
}

func mcpHealth() toolResult {
	sessions, _ := adapter.Collect(core.Scope{IncludeNoise: true})

	var (
		byTool  = map[core.Tool]int{}
		atRisk  []core.Session
		live    int
		deadDir int
	)
	for _, s := range sessions {
		byTool[s.Tool]++
		if s.Risk() != core.RiskNone {
			atRisk = append(atRisk, s)
		}
		if s.Live != nil {
			live++
		}
		if !s.DirExists() {
			deadDir++
		}
	}
	sort.Slice(atRisk, func(i, j int) bool { return atRisk[i].Bytes > atRisk[j].Bytes })

	var b strings.Builder
	var footprint int64
	for _, n := range adapter.Footprints() {
		footprint += n
	}

	fmt.Fprintf(&b, "footprint: %s across %d sessions\n", render.Bytes(footprint), len(sessions))
	for _, t := range []core.Tool{core.ToolCopilot, core.ToolClaude, core.ToolOpencode} {
		if n := byTool[t]; n > 0 {
			fmt.Fprintf(&b, "  %s: %d\n", t, n)
		}
	}
	fmt.Fprintf(&b, "open now: %d\ndead workspaces: %d\nat risk of failing to resume: %d\n",
		live, deadDir, len(atRisk))

	if len(atRisk) > 0 {
		b.WriteString("\nat-risk sessions (largest first):\n")
		for i, s := range atRisk {
			if i >= 5 {
				fmt.Fprintf(&b, "  ... and %d more (see midden_list_sessions)\n", len(atRisk)-5)
				break
			}
			fmt.Fprintf(&b, "  %s %s %s %s\n",
				shortID(s.ID), strings.ToUpper(s.Risk().String()),
				render.Bytes(s.Bytes), core.Truncate(s.Title, 40))
		}
		b.WriteString("\nA transcript past ~680 MiB will not load: `--resume` times out and silently " +
			"starts a NEW session. Use midden_session_brief to recover context and hand off instead.\n")
	}

	return textResult(capTokens(b.String(), budgetHealth))
}

// sessionLine is the compact one-line format shared by list and search.
// Roughly 25 tokens per session, so hundreds fit in a normal budget.
func sessionLine(s core.Session) string {
	risk := ""
	if r := s.Risk(); r != core.RiskNone {
		risk = " " + strings.ToUpper(r.String())
	}
	live := ""
	if s.Live != nil {
		live = " OPEN"
	}
	dead := ""
	if !s.DirExists() {
		dead = " DIR-MISSING"
	}
	size := render.Size(s)
	if size != "" {
		size = " " + size
	}

	return fmt.Sprintf("%s %s | %s |%s%s%s%s | %s | %s",
		s.Tool, shortID(s.ID), s.Dir, size, risk, live, dead,
		render.Age(s.Age()), core.Truncate(s.Title, 60))
}

func mcpList(args mcpArgs) toolResult {
	sessions, errs := adapter.Collect(args.scope())

	var lines []string
	for _, s := range sessions {
		lines = append(lines, sessionLine(s))
	}
	if len(lines) == 0 {
		return textResult("No sessions match. Widen the filters or drop the days limit.")
	}

	header := fmt.Sprintf("%d sessions (newest first). Format: tool id | workspace | size risk flags | age | title\n\n",
		len(sessions))
	body := header + budgetLines(lines, budgetList, "sessions")

	for _, e := range errs {
		body += "\nwarning: " + e.Error()
	}
	return textResult(body)
}

func mcpSearch(args mcpArgs) toolResult {
	if strings.TrimSpace(args.Query) == "" {
		return errResult("query is required")
	}

	sc := args.scope()
	sc.IncludeNoise = true
	sessions, _ := adapter.Collect(sc)

	q := strings.ToLower(args.Query)
	var lines []string
	for _, s := range sessions {
		hay := strings.ToLower(s.Title + " " + s.Dir + " " + s.Repo)
		if strings.Contains(hay, q) {
			lines = append(lines, sessionLine(s))
		}
	}
	if len(lines) == 0 {
		return textResult(fmt.Sprintf("No sessions match %q.", args.Query))
	}

	header := fmt.Sprintf("%d matches for %q:\n\n", len(lines), args.Query)
	return textResult(header + budgetLines(lines, budgetSearch, "matches"))
}

func mcpBrief(args mcpArgs) toolResult {
	if strings.TrimSpace(args.ID) == "" {
		return errResult("id is required")
	}

	s, err := findOne(args.ID)
	if err != nil {
		return errResult("%v", err)
	}

	h, ok := adapter.Find(s.Tool).(core.Harvester)
	if !ok {
		return errResult("%s sessions cannot be harvested yet", s.Tool)
	}

	turns := args.Turns
	if turns <= 0 {
		turns = 6
	}
	hv, err := h.Harvest(s, turns)
	if err != nil {
		return errResult("harvest failed: %v", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "session %s (%s)\nworkspace: %s\n", s.ID, s.Tool, s.Dir)
	if s.Repo != "" {
		fmt.Fprintf(&b, "repo: %s\n", s.Repo)
	}
	fmt.Fprintf(&b, "title: %s\nactive: %s to %s (%d user turns)\n",
		s.Title, s.Created.Format("2006-01-02"), s.Updated.Format("2006-01-02"), hv.UserTurns)
	if s.Bytes > 0 {
		fmt.Fprintf(&b, "transcript: %s (%s)\n", render.Bytes(s.Bytes), s.Risk())
	}
	if s.Live != nil {
		fmt.Fprintf(&b, "STATUS: open right now (pid %d, %s) — do not resume\n", s.Live.PID, s.Live.Status)
	}

	// Per-turn share of the remaining budget, so one long turn cannot
	// crowd out the rest.
	perTurn := (budgetBrief * 4) / (len(hv.Recent) + 2)

	if hv.Goal != nil {
		fmt.Fprintf(&b, "\nORIGINAL GOAL:\n%s\n", clip(hv.Goal.Text, perTurn))
	}
	if len(hv.Recent) > 0 {
		b.WriteString("\nRECENT EXCHANGES (oldest first):\n")
		for _, t := range hv.Recent {
			fmt.Fprintf(&b, "\n[%s] %s\n", strings.ToUpper(t.Role), clip(t.Text, perTurn))
		}
	}
	if hv.Truncated {
		b.WriteString("\n(earlier turns omitted; raise `turns` for more)\n")
	}

	return textResult(capTokens(b.String(), budgetBrief))
}

func mcpResume(args mcpArgs) toolResult {
	if strings.TrimSpace(args.ID) == "" {
		return errResult("id is required")
	}

	s, err := findOne(args.ID)
	if err != nil {
		return errResult("%v", err)
	}

	var b strings.Builder
	if s.Live != nil {
		fmt.Fprintf(&b, "WARNING: already open (pid %d, %s) — switch to that terminal rather than resuming.\n\n",
			s.Live.PID, s.Live.Status)
	}
	if !s.DirExists() {
		return errResult("workspace no longer exists: %s", s.Dir)
	}
	if s.Risk() >= core.RiskWarn {
		fmt.Fprintf(&b, "WARNING: transcript is %s (%s). Resume may time out and silently start a NEW "+
			"session. Prefer midden_session_brief and a handoff.\n\n", render.Bytes(s.Bytes), s.Risk())
	}

	b.WriteString(oneLiner(s, args.Instruction))
	b.WriteString("\n")
	return textResult(capTokens(b.String(), budgetSmall))
}
