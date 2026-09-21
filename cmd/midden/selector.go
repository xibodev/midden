package main

import (
	"fmt"
	"strings"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/core"
)

func selectSession(tool, id string, positional []string) (core.Session, error) {
	var empty core.Session
	scope := core.Scope{IncludeNoise: true}
	if tool != "" {
		kind := core.Tool(tool)
		if kind != core.ToolCopilot && kind != core.ToolClaude && kind != core.ToolOpencode {
			return empty, fmt.Errorf("unknown source tool")
		}
		scope.Tools = []core.Tool{kind}
	}
	if id != "" {
		if len(positional) > 0 {
			return empty, fmt.Errorf("use --session or one positional identifier, not both")
		}
		scope.IDs = []string{id}
	} else {
		if len(positional) != 1 {
			return empty, fmt.Errorf("supply --session ID or one identifier/prefix")
		}
		scope.IDPrefix = positional[0]
	}
	rows, errs := adapter.Collect(scope)
	matches := []core.Session{}
	for _, row := range rows {
		if scope.Match(row) {
			matches = append(matches, row)
		}
	}
	if len(matches) != 1 {
		messages := []string{}
		for _, err := range errs {
			messages = append(messages, err.Error())
		}
		return empty, fmt.Errorf("session is unavailable or ambiguous (matches=%d; source diagnostics=%s)", len(matches), strings.Join(messages, "; "))
	}
	reportErrs(errs)
	return matches[0], nil
}
