package main

import (
	"encoding/json"
	"strings"

	"github.com/mekjr1/midden/internal/confirmation"
	"github.com/mekjr1/midden/internal/module"
)

type mcpOptions struct {
	Workflow        bool
	Home            string
	ConfirmOperator confirmation.Handler
}

func workflowMCPTools() []mcpTool {
	tools := []mcpTool{}
	d := module.Describe()
	for _, id := range agentWorkflowIDs() {
		c, ok := agentCapability(id)
		if !ok {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(d.RequestSchemas[c.RequestSchema], &schema); err != nil {
			panic("invalid embedded workflow schema: " + err.Error())
		}
		tools = append(tools, mcpTool{Name: "midden_" + strings.ReplaceAll(id, ".", "_"), Description: c.Summary, InputSchema: schema})
	}
	return tools
}

func callWorkflowTool(name string, input json.RawMessage, opts mcpOptions) toolResult {
	if !opts.Workflow {
		return errResult("workflow tools require server-side --workflow opt-in")
	}
	for _, id := range agentWorkflowIDs() {
		if name != "midden_"+strings.ReplaceAll(id, ".", "_") {
			continue
		}
		env := module.Invoke(module.Request{Protocol: module.ProtocolID, Capability: id, Input: input, ConfirmOperator: opts.ConfirmOperator,
			Roots: map[string]module.Root{module.RootMiddenHome: {Path: opts.Home, Mode: "rw"}}})
		raw, err := json.Marshal(env)
		if err != nil {
			return errResult("encode workflow response: %v", err)
		}
		result := textResult(string(raw))
		result.IsError = !env.OK
		return result
	}
	return errResult("unknown workflow tool: %s", name)
}
