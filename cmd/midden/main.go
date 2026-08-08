// Command midden indexes, measures and safely disposes of AI CLI session data.
//
// M0 scope: see everything (ls, show), get back into it (resume), and never
// lose a session to the resume cliff again (doctor). All read-only.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/guide"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
)

const version = "2.2.0"

func main() {
	// Adapters that must open transcripts to describe a session reuse what
	// the last scan derived, so an unchanged file is never opened twice.
	// Plugin commands only inspect manifests; warming the cache would create
	// or migrate ~/.midden/index.db during a command advertised as passive.
	if len(os.Args) < 2 || (os.Args[1] != "mcp" && os.Args[1] != "plugins" && os.Args[1] != "plugin") {
		index.WarmPeekCache()
	}

	// Narrate slow work on every interactive command. The first run on a
	// machine has nothing cached and can take minutes; silence for that long
	// is indistinguishable from a hang, which is exactly the failure this
	// tool exists to notice. MCP is excluded: it speaks a protocol, not to a
	// person.
	if len(os.Args) < 2 || os.Args[1] != "mcp" {
		defer narrate()()
	}

	// A bare invocation used to print twenty commands with no ordering and no
	// cost information. Guiding is more useful than listing.
	if len(os.Args) < 2 {
		if err := cmdStart(nil); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	var err error
	switch os.Args[1] {
	case "ls", "list":
		err = cmdLs(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "resume":
		err = cmdResume(os.Args[2:])
	case "doctor":
		err = cmdDoctor(os.Args[2:])
	case "brief":
		err = cmdBrief(os.Args[2:])
	case "watch":
		err = cmdWatch(os.Args[2:])
	case "mcp":
		err = cmdMCP(os.Args[2:])
	case "scan":
		err = cmdScan(os.Args[2:])
	case "assay":
		err = cmdAssay(os.Args[2:])
	case "prune":
		err = cmdPrune(os.Args[2:])
	case "archive":
		err = cmdArchive(os.Args[2:])
	case "ops":
		err = cmdOps(os.Args[2:])
	case "reclaim":
		err = cmdReclaim(os.Args[2:])
	case "nuggets":
		err = cmdNuggets(os.Args[2:])
	case "catalog":
		err = cmdCatalog(os.Args[2:])
	case "refine":
		err = cmdRefine(os.Args[2:])
	case "artifacts":
		err = cmdArtifacts(os.Args[2:])
	case "ui":
		err = cmdUI(os.Args[2:])
	case "advise":
		err = cmdAdvise(os.Args[2:])
	case "cost":
		err = cmdCost(os.Args[2:])
	case "start":
		err = cmdStart(os.Args[2:])
	case "summarize", "summarise", "summary":
		err = cmdSummarize(os.Args[2:])
	case "ask":
		err = cmdAsk(os.Args[2:])
	case "plugins", "plugin":
		err = cmdPlugins(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("midden", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`midden - a materials recovery facility for AI coding exhaust

  New here?  Run  midden start  for a guided first run.

USAGE
  midden <command> [flags]

`)

	// Commands are listed in pipeline order with their cost class, because
	// alphabetical ordering put `advise` first and `scan` near the end — the
	// reverse of how the tool is used — and nothing said which ones spend.
	for _, stage := range guide.Stages {
		cmds := guide.InStage(stage.Name)
		if len(cmds) == 0 {
			continue
		}
		fmt.Printf("%s\n", strings.ToUpper(stage.Label))
		for _, c := range cmds {
			tag := "     "
			if c.Cost == guide.Spends {
				tag = "$$$  "
			}
			fmt.Printf("  %s%-10s %s\n", tag, c.Name, c.Blurb)
		}
		fmt.Println()
	}

	fmt.Printf("INTEGRATIONS\n  %-10s %s\n  %-10s %s\n\n",
		"ui", "Set up tools and managed integrations in the desktop workbench",
		"plugins", "Advanced manifest list, probe, and verify")

	fmt.Printf("COST\n  Everything is free except %s, which call a model\n"+
		"  through the AI CLI you are already signed in to. Both preview with\n"+
		"  --dry-run before charging anything. Run `midden cost` for what you\n"+
		"  have actually spent.\n\n", strings.Join(guide.Spending(), " and "))

	fmt.Print(`SCOPE FLAGS (most commands)
  --tool <copilot|claude|opencode>   Limit to one tool
  --days <n>                         Only sessions touched in the last n days
  --workspace <substr>               Match the session directory
  --all                              Include automated/trivial sessions
  --json                             Machine-readable output

SPENDING LESS
`)
	for _, tip := range guide.Cheapest() {
		fmt.Printf("  · %s\n", tip)
	}

	fmt.Print(`
EXAMPLES
  midden start                       Guided first run
  midden doctor                      What is wrong right now
  midden ls --days 7 --group         Recent sessions, grouped by tool
  midden brief ac0c39cf --handoff    Rescue a session too big to resume
  midden scan --assay                Measure what your exhaust is made of
  midden prune                       Preview disk recovery (dry run)
  midden reclaim --workspace foo --dry-run    Estimate before spending
  midden catalog                     What your evidence can support
  midden refine tsg adr              Write both from one warm context
  midden cost                        What you have spent
  midden ui                          Open the desktop recovery workbench
`)
}

// scopeFlags registers the shared scope filters on a flag set.
func scopeFlags(fs *flag.FlagSet) (*core.Scope, *bool, *bool) {
	sc := &core.Scope{}
	tool := fs.String("tool", "", "limit to one tool (copilot|claude|opencode)")
	fs.IntVar(&sc.Days, "days", 0, "only sessions touched in the last n days")
	fs.StringVar(&sc.Workspace, "workspace", "", "match session directory")
	fs.StringVar(&sc.Repo, "repo", "", "match repository")
	fs.BoolVar(&sc.IncludeNoise, "all", false, "include automated/trivial sessions")
	fs.BoolVar(&sc.WithSizes, "sizes", false, "compute per-session sizes for DB-backed tools (slow)")
	fs.IntVar(&sc.Limit, "limit", 0, "cap results")
	asJSON := fs.Bool("json", false, "machine-readable output")
	group := fs.Bool("group", false, "group by tool")

	// Resolved after Parse via the returned closure-free pointer dance:
	// the caller invokes resolveTool.
	fs.Func("t", "alias for --tool", func(v string) error { *tool = v; return nil })

	// Stash the tool pointer on the scope after parsing.
	scopeToolPtr = tool
	return sc, asJSON, group
}

var scopeToolPtr *string

// reorderArgs moves flags ahead of positional arguments so they may be written
// in either order. Stdlib flag stops parsing at the first positional, which
// would silently drop `midden resume <id> --with "x"`.
//
// Whether a flag consumes the next token is derived from the FlagSet itself
// rather than a hardcoded list, so adding a flag can never reintroduce the
// off-by-one that this function exists to prevent.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	isBool := func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return true // unknown: assume no value, let flag report it
		}
		bf, ok := f.Value.(interface{ IsBoolFlag() bool })
		return ok && bf.IsBoolFlag()
	}

	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}

		flags = append(flags, a)

		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") || isBool(name) {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

func resolveTool(sc *core.Scope) error {
	if scopeToolPtr == nil || *scopeToolPtr == "" {
		return nil
	}
	t := core.Tool(strings.ToLower(*scopeToolPtr))
	switch t {
	case core.ToolCopilot, core.ToolClaude, core.ToolOpencode:
		sc.Tools = []core.Tool{t}
		return nil
	}
	return fmt.Errorf("unknown tool %q (want copilot, claude or opencode)", *scopeToolPtr)
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	sc, asJSON, group := scopeFlags(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}

	// One collect answers both questions. Asking twice — once filtered, once
	// wide, to learn how many the filter hid — doubled the cost of every
	// listing, and describing a session is the expensive part.
	wide := *sc
	wide.IncludeNoise = true
	wide.Limit = 0

	all, errs := adapter.Collect(wide)
	reportErrs(errs)

	sessions := make([]core.Session, 0, len(all))
	hidden := 0
	for _, s := range all {
		if s.Noise && !sc.IncludeNoise {
			hidden++
			continue
		}
		sessions = append(sessions, s)
	}
	if sc.Limit > 0 && len(sessions) > sc.Limit {
		sessions = sessions[:sc.Limit]
	}

	if *asJSON {
		return emitJSON(sessions)
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions match.")
		return nil
	}

	if *group {
		sort.SliceStable(sessions, func(i, j int) bool {
			if sessions[i].Tool != sessions[j].Tool {
				return sessions[i].Tool < sessions[j].Tool
			}
			return sessions[i].Updated.After(sessions[j].Updated)
		})
	}

	printHeader(sessions, sc, hidden)

	last := core.Tool("")
	for i, s := range sessions {
		if *group && s.Tool != last {
			fmt.Printf("\n  %s\n", render.Bold(strings.ToUpper(string(s.Tool))))
			last = s.Tool
		}
		printSession(i+1, s)
	}

	fmt.Printf("\n  %s\n", render.Dim("midden show <id>  |  midden resume <id>  |  midden doctor"))
	return nil
}

func printHeader(sessions []core.Session, sc *core.Scope, hidden int) {
	counts := map[core.Tool]int{}
	var bytes int64
	for _, s := range sessions {
		counts[s.Tool]++
		bytes += s.Bytes
	}

	window := "all time"
	if sc.Days > 0 {
		window = fmt.Sprintf("last %dd", sc.Days)
	}

	var parts []string
	for _, t := range []core.Tool{core.ToolCopilot, core.ToolClaude, core.ToolOpencode} {
		if n := counts[t]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", render.ToolColour(t), n))
		}
	}

	fmt.Printf("\n  %s  %s\n", render.Bold("SESSIONS"), render.Dim(window))
	line := fmt.Sprintf("  %s  |  %d total", strings.Join(parts, " · "), len(sessions))
	if bytes > 0 {
		line += fmt.Sprintf("  |  %s on disk", render.Bytes(bytes))
	}
	if hidden > 0 {
		line += render.Dim(fmt.Sprintf("  (%d automated hidden — use --all)", hidden))
	}
	fmt.Println(line)
	fmt.Println()
}

func printSession(n int, s core.Session) {
	fmt.Printf("  %s %s  %-9s %-11s %s%s\n",
		render.Bold(fmt.Sprintf("[%d]", n)),
		render.ToolColour(s.Tool),
		render.Age(s.Age()),
		render.Size(s),
		core.Truncate(s.Title, 62),
		render.Flags(s),
	)
	fmt.Printf("      %s\n", render.Dim(s.Dir))
	fmt.Printf("      %s\n", render.Dim(oneLiner(s, "")))
}

// oneLiner builds the copy-paste command. Claude and Copilot key sessions to
// their original cwd, so the walk is required, not decorative.
func oneLiner(s core.Session, instruction string) string {
	a := adapter.Find(s.Tool)
	if a == nil {
		return ""
	}
	return adapter.WalkAndResume(s.Dir, a.ResumeCmd(s, instruction))
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: midden show <id-or-prefix>")
	}

	s, err := findOne(fs.Arg(0))
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(s)
	}

	fmt.Printf("\n  %s  %s\n", render.Bold(core.Truncate(s.Title, 70)), render.ToolColour(s.Tool))
	fmt.Printf("  %s\n\n", render.Dim(s.ID))

	row := func(k, v string) {
		if v != "" {
			fmt.Printf("  %-14s %s\n", render.Dim(k), v)
		}
	}
	row("workspace", s.Dir)
	if !s.DirExists() {
		row("", render.Flags(core.Session{Dir: s.Dir}))
	}
	row("repository", s.Repo)
	row("created", s.Created.Format("2006-01-02 15:04"))
	row("last touched", fmt.Sprintf("%s  (%s)", s.Updated.Format("2006-01-02 15:04"), render.Age(s.Age())))
	if d := s.SpanDays(); d >= 1 {
		row("span", fmt.Sprintf("%.1f days", d))
	}
	if s.Turns > 0 {
		row("turns", fmt.Sprintf("%d", s.Turns))
	}
	if s.Bytes > 0 {
		row("transcript", fmt.Sprintf("%s  (%s)", render.Bytes(s.Bytes), render.RiskColour(s.Risk())))
	}
	if s.TranscriptPath != "" {
		row("path", s.TranscriptPath)
	}
	if s.Live != nil {
		row("live", fmt.Sprintf("pid %d, %s", s.Live.PID, s.Live.Status))
	}

	fmt.Printf("\n  %s\n  %s\n\n", render.Dim("resume:"), oneLiner(s, ""))

	if s.Risk() >= core.RiskWarn {
		fmt.Printf("  %s\n\n", render.RiskColour(s.Risk())+" — this session is near the resume cliff; see `midden doctor`")
	}
	return nil
}

func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	with := fs.String("with", "", "instruction to deliver on resume")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: midden resume <id-or-prefix> [--with \"instruction\"]")
	}

	s, err := findOne(fs.Arg(0))
	if err != nil {
		return err
	}

	if s.Live != nil {
		fmt.Fprintf(os.Stderr, "%s this session is already open (pid %d, %s) — switch to that terminal instead\n\n",
			render.Bold("warning:"), s.Live.PID, s.Live.Status)
	}
	if !s.DirExists() {
		return fmt.Errorf("workspace no longer exists: %s", s.Dir)
	}
	if s.Risk() >= core.RiskWarn {
		fmt.Fprintf(os.Stderr, "%s transcript is %s (%s) — resume may fail silently\n\n",
			render.Bold("warning:"), render.Bytes(s.Bytes), s.Risk())
	}

	fmt.Println(oneLiner(s, *with))
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	sc, asJSON, _ := scopeFlags(fs)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}

	// Health is about everything on disk, not just recent work.
	sc.IncludeNoise = true
	sessions, errs := adapter.Collect(*sc)
	reportErrs(errs)

	type report struct {
		Total      int              `json:"total"`
		Bytes      int64            `json:"tracked_bytes"`
		Footprint  int64            `json:"store_footprint"`
		AtRisk     []core.Session   `json:"at_risk"`
		DeadDirs   []core.Session   `json:"dead_workspaces"`
		Live       []core.Session   `json:"live"`
		ByTool     map[string]int   `json:"by_tool"`
		ToolBytes  map[string]int64 `json:"tool_tracked_bytes"`
		ToolStores map[string]int64 `json:"tool_store_bytes"`
	}

	rep := report{
		ByTool:     map[string]int{},
		ToolBytes:  map[string]int64{},
		ToolStores: map[string]int64{},
	}
	for t, n := range adapter.Footprints() {
		rep.ToolStores[string(t)] = n
		rep.Footprint += n
	}
	for _, s := range sessions {
		rep.Total++
		rep.Bytes += s.Bytes
		rep.ByTool[string(s.Tool)]++
		rep.ToolBytes[string(s.Tool)] += s.Bytes

		if s.Risk() != core.RiskNone {
			rep.AtRisk = append(rep.AtRisk, s)
		}
		if !s.DirExists() {
			rep.DeadDirs = append(rep.DeadDirs, s)
		}
		if s.Live != nil {
			rep.Live = append(rep.Live, s)
		}
	}
	sort.Slice(rep.AtRisk, func(i, j int) bool { return rep.AtRisk[i].Bytes > rep.AtRisk[j].Bytes })

	if *asJSON {
		return emitJSON(rep)
	}

	fmt.Printf("\n  %s\n", render.Bold("MIDDEN DOCTOR"))
	fmt.Printf("  %s\n\n", render.Rule(60))

	fmt.Printf("  %s  %s across %d sessions\n",
		render.Dim("on disk  "), render.Bytes(rep.Footprint), rep.Total)
	for _, t := range []core.Tool{core.ToolCopilot, core.ToolClaude, core.ToolOpencode} {
		if n := rep.ByTool[string(t)]; n > 0 {
			fmt.Printf("             %-10s %4d sessions  %10s store  %10s tracked\n",
				render.ToolColour(t), n,
				render.Bytes(rep.ToolStores[string(t)]),
				render.Bytes(rep.ToolBytes[string(t)]))
		}
	}
	if untracked := rep.Footprint - rep.Bytes; untracked > 0 {
		fmt.Printf("             %s\n", render.Dim(fmt.Sprintf(
			"%s is not attributable to any single session (caches, indexes, snapshots)",
			render.Bytes(untracked))))
	}

	fmt.Printf("\n  %s  %d\n", render.Dim("open now "), len(rep.Live))
	for _, s := range rep.Live {
		fmt.Printf("             %s  %s  %s\n",
			render.ToolColour(s.Tool), core.Truncate(s.Title, 46), render.Dim(s.Dir))
	}

	fmt.Printf("\n  %s  %d\n", render.Dim("at risk  "), len(rep.AtRisk))
	if len(rep.AtRisk) == 0 {
		fmt.Printf("             %s\n", render.Dim("no session is near the resume cliff"))
	}
	for _, s := range rep.AtRisk {
		fmt.Printf("             %-9s %-10s %s\n",
			render.RiskColour(s.Risk()), render.Bytes(s.Bytes), core.Truncate(s.Title, 44))
		fmt.Printf("             %s\n", render.Dim(s.Dir))
	}
	if len(rep.AtRisk) > 0 {
		fmt.Printf("\n             %s\n", render.Dim(
			"Copilot --resume fails silently above ~680 MiB and starts a NEW session."))
		fmt.Printf("             %s\n", render.Dim(
			"Hand off to a fresh session before the cliff rather than resuming."))
	}

	fmt.Printf("\n  %s  %d\n", render.Dim("dead dirs"), len(rep.DeadDirs))
	for i, s := range rep.DeadDirs {
		if i >= 5 {
			fmt.Printf("             %s\n", render.Dim(fmt.Sprintf("... and %d more", len(rep.DeadDirs)-5)))
			break
		}
		fmt.Printf("             %s  %s\n", render.ToolColour(s.Tool), render.Dim(s.Dir))
	}

	// Alarm without instruction is a dead end. Every finding above should
	// resolve into something the operator can actually run.
	suggestNext(collectState())
	return nil
}

// findOne resolves an id or unique prefix to a single session.
func findOne(idOrPrefix string) (core.Session, error) {
	sc := core.Scope{IncludeNoise: true, IDPrefix: idOrPrefix}
	matches, errs := adapter.Collect(sc)
	reportErrs(errs)

	switch len(matches) {
	case 0:
		return core.Session{}, fmt.Errorf("no session matches %q", idOrPrefix)
	case 1:
		return matches[0], nil
	}

	// An exact id always wins over a shared prefix.
	for _, m := range matches {
		if m.ID == idOrPrefix {
			return m, nil
		}
	}
	fmt.Fprintf(os.Stderr, "%q matches %d sessions:\n", idOrPrefix, len(matches))
	for _, m := range matches {
		fmt.Fprintf(os.Stderr, "  %s  %s  %s\n", m.Tool, m.ID, core.Truncate(m.Title, 50))
	}
	return core.Session{}, fmt.Errorf("ambiguous prefix — use more characters")
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// reportErrs surfaces adapter failures without aborting: one tool's format
// drift must not blind the others.
func reportErrs(errs []error) {
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, render.Dim("warning: "+e.Error()))
	}
}
