package main

import (
	"context"
	"sync"
	"unicode/utf8"

	"github.com/xibodev/compa/pkg/agent"
	"github.com/xibodev/compa/pkg/tools"
)

// eventResultLimit bounds the tool result text carried by one event.
const eventResultLimit = 64 << 10

func (h *kernelHost) BeforeTool(ctx context.Context, call *agent.ToolCallHookRequest) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
	return call, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
func (h *kernelHost) AfterTool(ctx context.Context, result *agent.ToolResultHookResponse) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
	event := Event{Type: "tool", Tool: result.Tool, Arguments: result.Arguments, Status: "completed", CallID: h.endToolCall(result.Tool)}
	if result.Result != nil && result.Result.IsError {
		event.Status = "failed"
	}
	if result.Tool == "midden" {
		if args, err := coreToolArgs(result.Arguments); err == nil {
			if effect, err := classifyCoreArgs(args); err == nil {
				event.Effect = string(effect)
			}
		}
	}
	event.Result, event.ResultTruncated = clipEventResult(h.modelText(result.Result))
	h.app.emit(event)
	return result, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

// modelText is the tool result as the model receives it.
func (h *kernelHost) modelText(result *tools.ToolResult) string {
	if result == nil {
		return ""
	}
	text := result.ContentForLLM()
	if h.loop != nil {
		text = h.loop.GetConfig().FilterSensitiveData(text)
	}
	return text
}

func clipEventResult(text string) (string, bool) {
	if len(text) <= eventResultLimit {
		return text, false
	}
	cut := eventResultLimit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

// Stand-in until Compa provides a tool call id on hook requests. The kernel
// runs one tool call at a time, so the id minted when a call reaches approval
// also labels its result.
var toolCallIDs sync.Map // *App -> toolCallID

type toolCallID struct{ id, tool string }

func (h *kernelHost) beginToolCall(tool string) string {
	id := randomID()
	toolCallIDs.Store(h.app, toolCallID{id: id, tool: tool})
	return id
}

// endToolCall releases the current call id when it belongs to this tool.
func (h *kernelHost) endToolCall(tool string) string {
	value, ok := toolCallIDs.LoadAndDelete(h.app)
	if !ok {
		return ""
	}
	if current := value.(toolCallID); current.tool == tool {
		return current.id
	}
	return ""
}
