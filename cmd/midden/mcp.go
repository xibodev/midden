package main

// MCP server: newline-delimited JSON-RPC 2.0 over stdio.
//
// Implemented against the stdlib rather than an SDK. The protocol surface
// needed here is small, and owning it keeps the binary dependency-light and
// keeps token budgeting under our control rather than a framework's.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mekjr1/midden/internal/index"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInternal       = -32603
)

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(s string) toolResult {
	return toolResult{Content: []toolContent{{Type: "text", Text: s}}}
}

func errResult(format string, a ...any) toolResult {
	return toolResult{
		Content: []toolContent{{Type: "text", Text: fmt.Sprintf(format, a...)}},
		IsError: true,
	}
}

// cmdMCP runs the server until stdin closes.
//
// Nothing may be written to stdout except protocol messages, so diagnostics go
// to stderr.
func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	workflow := fs.Bool("workflow", false, "opt in to local evidence, project, recipe, review and export tools; no model subprocess")
	probeOnly := fs.Bool("confirmation-probe", false, "expose only a non-mutating host confirmation diagnostic")
	home := fs.String("home", index.Dir(), "state root for opt-in workflow tools")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected MCP arguments")
	}
	root, err := filepath.Abs(*home)
	if err != nil {
		return err
	}
	options := mcpOptions{Workflow: *workflow || *probeOnly, ProbeOnly: *probeOnly, Home: root}
	return serveMCP(os.Stdin, os.Stdout, options)
}

func handleRPC(line []byte, out *json.Encoder) {
	handleRPCWithOptions(line, out, mcpOptions{})
}

func handleRPCWithOptions(line []byte, out *json.Encoder, options mcpOptions) {
	var req rpcRequest
	if json.Unmarshal(line, &req) != nil {
		out.Encode(rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: errParse, Message: "parse error"},
		})
		return
	}

	// Notifications carry no id and must never be answered.
	if len(req.ID) == 0 {
		return
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		protocol := mcpProtocolVersion
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &params) == nil && (params.ProtocolVersion == "2025-06-18" || params.ProtocolVersion == "2025-11-25") {
			protocol = params.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "midden", "version": version},
			"instructions": "Midden indexes AI CLI sessions across Copilot CLI, Claude Code and " +
				"opencode. Every tool is token-budgeted and truncates rather than flooding context. " +
				"Start with midden_health for orientation, then midden_list_sessions with filters, " +
				"then midden_session_brief for one session.",
		}
		if options.Workflow {
			resp.Result.(map[string]any)["instructions"] = workflowInstructions()
		}
		if options.ProbeOnly {
			resp.Result.(map[string]any)["instructions"] = "This connection exposes only a non-mutating confirmation diagnostic. It cannot read source sessions or change workflow state."
		}

	case "tools/list":
		tools := mcpTools()
		if options.Workflow {
			tools = workflowMCPTools()
		}
		if options.ProbeOnly {
			filtered := []mcpTool{}
			for _, tool := range tools {
				if tool.Name == "midden_host_status" {
					filtered = append(filtered, tool)
				}
			}
			tools = filtered
		}
		resp.Result = map[string]any{"tools": tools}

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			resp.Error = &rpcError{Code: errInvalidRequest, Message: "bad params"}
			break
		}
		if options.ProbeOnly && p.Name != "midden_host_status" {
			resp.Result = errResult("confirmation-probe mode exposes only midden_host_status")
			break
		}
		workflowTool := false
		if options.Workflow {
			for _, tool := range workflowMCPTools() {
				if tool.Name == p.Name {
					workflowTool = true
					break
				}
			}
		}
		if workflowTool {
			resp.Result = callWorkflowTool(p.Name, p.Arguments, options)
		} else {
			resp.Result = callTool(p.Name, p.Arguments)
		}

	case "ping":
		resp.Result = map[string]any{}

	default:
		resp.Error = &rpcError{Code: errMethodNotFound, Message: "unknown method: " + req.Method}
	}

	if err := out.Encode(resp); err != nil {
		fmt.Fprintln(os.Stderr, "midden mcp: write failed:", err)
	}
}

func obj(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

func mcpTools() []mcpTool {
	scopeProps := map[string]any{
		"tool":      str("Limit to one CLI: copilot, claude or opencode."),
		"days":      num("Only sessions touched in the last N days."),
		"workspace": str("Substring match on the session's directory."),
		"limit":     num("Maximum sessions to consider."),
	}

	return []mcpTool{
		{
			Name: "midden_health",
			Description: "Orientation, ~400 tokens. Total disk footprint, session counts per CLI, " +
				"how many sessions are at risk of failing to resume, and how many are open right now. " +
				"Call this first.",
			InputSchema: obj(map[string]any{}),
		},
		{
			Name: "midden_list_sessions",
			Description: "One compact line per session: tool, short id, workspace, size, risk, age, title. " +
				"Budgeted to ~4k tokens and truncated with a count of what was omitted, so it is safe to " +
				"call without filters. Use filters to see more.",
			InputSchema: obj(scopeProps),
		},
		{
			Name: "midden_search",
			Description: "Find sessions whose title, workspace or repository matches a query. " +
				"Same compact format as midden_list_sessions.",
			InputSchema: obj(map[string]any{
				"query":     str("Case-insensitive substring to match."),
				"tool":      scopeProps["tool"],
				"days":      scopeProps["days"],
				"workspace": scopeProps["workspace"],
			}, "query"),
		},
		{
			Name: "midden_session_brief",
			Description: "Recoverable context for one session, ~1.5k tokens: the original goal, the most " +
				"recent exchanges, and where it left off. Extracted deterministically from the transcript " +
				"with no model call. Works even on sessions too large to resume.",
			InputSchema: obj(map[string]any{
				"id":    str("Session id or unique prefix."),
				"turns": num("How many recent turns to include (default 6)."),
			}, "id"),
		},
		{
			Name: "midden_resume_command",
			Description: "The exact shell one-liner to walk to a session's workspace and resume it, " +
				"in the host OS dialect. Warns when the session is already open or too large to load.",
			InputSchema: obj(map[string]any{
				"id":          str("Session id or unique prefix."),
				"instruction": str("Optional instruction to deliver on resume."),
			}, "id"),
		},
	}
}
