package main

import (
	"fmt"
	"io"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
	"github.com/mekjr1/midden/internal/cost"
)

func runUsage(args []string, out io.Writer) error {
	fs, service, asJSON := materialFlags("usage")
	tool := fs.String("tool", "", "source tool")
	id := fs.String("session", "", "exact source session id")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if *id == "" || fs.NArg() != 0 {
		return fmt.Errorf("usage requires --tool and exact --session")
	}
	kind := core.Tool(*tool)
	if kind != core.ToolCopilot && kind != core.ToolClaude && kind != core.ToolOpencode {
		return fmt.Errorf("unknown source tool")
	}
	a := adapter.FindWithRoots(kind, service.Roots)
	if a == nil || !a.Available() {
		return fmt.Errorf("source store unavailable")
	}
	sessions, err := a.Sessions(core.Scope{Tools: []core.Tool{kind}, IDs: []string{*id}, IncludeNoise: true})
	if err != nil && len(sessions) == 0 {
		return err
	}
	found := 0
	for _, s := range sessions {
		if s.ID == *id {
			found++
		}
	}
	if found != 1 {
		return fmt.Errorf("exact session unavailable or ambiguous")
	}
	reader, ok := a.(cost.Reader)
	if !ok {
		return fmt.Errorf("source does not expose recorded usage")
	}
	usage, err := reader.Usage(*id)
	if err != nil {
		return err
	}
	return emitMaterial(out, *asJSON, usage)
}
