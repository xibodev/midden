package main

// coreEffect is what one core call may change.
type coreEffect string

const (
	effectReadOnly        coreEffect = "read-only"
	effectWritesCache     coreEffect = "writes-cache"
	effectWritesWorkspace coreEffect = "writes-workspace"
)

// classifyCoreArgs derives the effect from the parsed arguments, never from
// raw text: a token consumed as a flag value is not a flag.
func classifyCoreArgs(args []string) (coreEffect, error) {
	call, err := parseCoreArgs(args)
	if err != nil {
		return "", err
	}
	return call.effect(), nil
}

// effect applies the agent-door table. Any --out writes the workspace; every
// workspace write needs --out, so help (which never runs the command) is
// read-only otherwise.
func (c coreCall) effect() coreEffect {
	if c.has("--out") {
		return effectWritesWorkspace
	}
	if c.has("--help") || c.has("--h") {
		return effectReadOnly
	}
	switch c.command {
	case "collect":
		return effectWritesWorkspace
	case "collection":
		switch c.operation {
		case "select", "merge", "export":
			return effectWritesWorkspace
		}
		return effectReadOnly
	case "read":
		// Reading a pinned view pages it; anything else pins a new view.
		if c.has("--view") && !c.has("--record") && !c.has("--tool") && !c.has("--session") {
			return effectReadOnly
		}
		return effectWritesCache
	case "assets":
		if c.has("--view") && !c.has("--tool") && !c.has("--session") {
			return effectReadOnly
		}
		return effectWritesCache
	case "search", "assay":
		return effectWritesCache
	case "brief":
		if c.has("--handoff") {
			return effectWritesCache
		}
	}
	return effectReadOnly
}
