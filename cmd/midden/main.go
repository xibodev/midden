// Command midden provides deterministic session-data tools.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/material"
	"github.com/mekjr1/midden/internal/render"
)

const version = core.Version

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "ls" || os.Args[1] == "list" || os.Args[1] == "show" || os.Args[1] == "find" || os.Args[1] == "doctor") {
		if err := index.WarmPeekCache(); err != nil {
			fmt.Fprintln(os.Stderr, "warning: derived cache unavailable:", err)
		}
	}

	if len(os.Args) > 1 && os.Args[1] != "read" && os.Args[1] != "search" && os.Args[1] != "collect" && os.Args[1] != "collection" && os.Args[1] != "assets" {
		defer narrate()()
	}

	if len(os.Args) < 2 {
		usage()
		return
	}

	var err error
	switch os.Args[1] {
	case "ls", "list":
		err = cmdLs(os.Args[2:])
	case "find":
		err = cmdFind(os.Args[2:])
	case "read", "search", "collect", "collection", "assets":
		err = runMaterial(os.Args[1], os.Args[2:], os.Stdout)
	case "legacy":
		err = runLegacy(os.Args[2:], os.Stdout)
	case "usage":
		err = runUsage(os.Args[2:], os.Stdout)
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
	case "module", "agent", "ui", "reclaim", "refine", "ask", "advise", "catalog", "nuggets", "artifacts", "plugins", "plugin", "seed", "install", "summarize", "summarise", "summary", "start", "cost", "mcp":
		err = fmt.Errorf("%q is retired from the deterministic core; use normal data commands and the separate outcome bundle", os.Args[1])
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
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`midden - deterministic tools for session material

USAGE
  midden <command> [flags]

DISCOVER AND MEASURE
  ls, list       List sessions with explicit scope
  find           Find sessions by recorded text/metadata
  show           Inspect one session
  brief          Read a bounded conversation brief
  doctor         Report availability and resume-risk heuristics
  scan           Refresh the derived core index
  assay          Measure record kinds, bytes and duplication
  resume         Print a resume command; does not launch an agent
  watch          Watch session sizes

WORK WITH MATERIAL
  read           Open a stable source view or read focused context
  search         Search a pinned view for a literal phrase
  collect        Write selected records into a portable source collection
  collection     Inspect, read, search, select, merge, verify or export
  assets         List/extract recorded assets without fetching remote URLs
  usage          Read a source session's recorded model usage
  legacy export  Copy legacy working data without migrating its store

EXPLICIT SOURCE MAINTENANCE
  prune          Preview cleanup; source changes require explicit execution
  archive        Explicitly archive selected source material
  ops            Inspect the maintenance audit log

EXAMPLES
  midden ls --days 7 --json
  midden read --tool copilot --session SESSION_ID --json
  midden search "deployment" --view VIEW_ID --json
  midden collect --view VIEW_ID --record RECORD_ID --out sources --json
  midden collection verify sources --json
  midden collection export sources --format markdown --out source-notes.md

Use each command's --help for scope, source roots and output bounds.
Core never invokes a model. The separate outcome bundle guides an existing AI
CLI and operator; the host writes, renders, inspects and delivers working files.
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
	offset := fs.Int("offset", 0, "page offset")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if err := resolveTool(sc); err != nil {
		return err
	}

	inventory, err := material.List(adapter.EnvironmentRoots(), *sc, *offset, sc.Limit)
	if err != nil {
		return err
	}
	sessions := inventory.Sessions
	if *asJSON {
		for {
			raw, err := json.Marshal(inventory)
			if err != nil {
				return err
			}
			if len(raw) <= 16<<10 {
				return emitJSON(inventory)
			}
			if len(inventory.Sessions) <= 1 {
				return fmt.Errorf("session metadata exceeds inline limit; inspect the exact session directly")
			}
			inventory.Sessions = inventory.Sessions[:len(inventory.Sessions)-1]
			next := inventory.Offset + len(inventory.Sessions)
			inventory.NextOffset = &next
		}
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

	printHeader(sessions, sc, inventory.ExcludedNoise)
	fmt.Printf("  Showing %d of %d matching sessions (%d before noise filtering).\n", len(sessions), inventory.Total, inventory.Matched)
	for _, warning := range inventory.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}

	last := core.Tool("")
	for i, s := range sessions {
		if *group && s.Tool != last {
			fmt.Printf("\n  %s\n", render.Bold(strings.ToUpper(string(s.Tool))))
			last = s.Tool
		}
		printSession(i+1, s)
	}

	fmt.Printf("\n  %s\n", render.Dim("midden show <id>  |  midden resume <id>  |  midden doctor"))
	if inventory.NextOffset != nil {
		fmt.Printf("  Next page: --offset %d\n", *inventory.NextOffset)
	}
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
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	tool := fs.String("tool", "", "source tool")
	id := fs.String("session", "", "exact session identifier")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	s, err := selectSession(*tool, *id, fs.Args())
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

	fmt.Println("\nUse read/collect for selected material, or brief for a bounded recovery note.")
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
