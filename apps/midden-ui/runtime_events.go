package main

import (
	"context"

	"github.com/xibodev/facet-studio/pkg/agent"
)

func (h *kernelHost) BeforeTool(ctx context.Context, call *agent.ToolCallHookRequest) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
	return call, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
func (h *kernelHost) AfterTool(ctx context.Context, result *agent.ToolResultHookResponse) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
	status := "completed"
	if result.Result != nil && result.Result.IsError {
		status = "failed"
	}
	h.app.emit(Event{Type: "tool", Tool: result.Tool, Arguments: result.Arguments, Status: status})
	return result, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
