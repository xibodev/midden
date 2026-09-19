package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/mekjr1/midden/internal/confirmation"
)

type mcpConnection struct {
	in          *bufio.Scanner
	out         *json.Encoder
	options     mcpOptions
	elicitation bool
	nextID      int
	activeID    json.RawMessage
}

func serveMCP(in io.Reader, out io.Writer, options mcpOptions) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	connection := &mcpConnection{in: scanner, out: json.NewEncoder(out), options: options}
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var req rpcRequest
		if json.Unmarshal(line, &req) == nil {
			if req.Method == "initialize" {
				var params struct {
					ProtocolVersion string                     `json:"protocolVersion"`
					Capabilities    map[string]json.RawMessage `json:"capabilities"`
				}
				if json.Unmarshal(req.Params, &params) == nil {
					value, present := params.Capabilities["elicitation"]
					connection.elicitation = present && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) &&
						(params.ProtocolVersion == "2025-06-18" || params.ProtocolVersion == "2025-11-25")
				}
			}
			connection.activeID = req.ID
		}
		opts := connection.options
		if connection.elicitation {
			opts.ConfirmOperator = connection.confirm
		}
		handleRPCWithOptions(line, connection.out, opts)
	}
	return scanner.Err()
}

func (c *mcpConnection) confirm(ctx context.Context, proposal confirmation.Request) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.nextID++
	id, _ := json.Marshal("midden-confirmation-" + strconv.Itoa(c.nextID))
	params := map[string]any{
		"message": proposal.Message + "\n\nReview fingerprint: " + proposal.Digest,
		"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{
			"approved": map[string]any{"type": "boolean", "title": "I approve this exact selection/content", "default": false},
		}, "required": []string{"approved"}},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return false, err
	}
	if err = c.out.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: "elicitation/create", Params: raw}); err != nil {
		return false, err
	}
	for c.in.Scan() {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err = json.Unmarshal(c.in.Bytes(), &message); err != nil {
			return false, fmt.Errorf("invalid host confirmation message: %w", err)
		}
		if bytes.Equal(message.ID, id) {
			if message.Error != nil {
				return false, fmt.Errorf("host declined elicitation: %s", message.Error.Message)
			}
			var decision struct {
				Action  string `json:"action"`
				Content struct {
					Approved bool `json:"approved"`
				} `json:"content"`
			}
			if err = json.Unmarshal(message.Result, &decision); err != nil {
				return false, err
			}
			return decision.Action == "accept" && decision.Content.Approved, nil
		}
		if message.Method == "notifications/cancelled" {
			var cancellation struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(message.Params, &cancellation) == nil &&
				(bytes.Equal(cancellation.RequestID, c.activeID) || bytes.Equal(cancellation.RequestID, id)) {
				return false, nil
			}
		}
		if len(message.ID) > 0 {
			response := rpcResponse{JSONRPC: "2.0", ID: message.ID}
			if message.Method == "ping" {
				response.Result = map[string]any{}
			} else {
				response.Error = &rpcError{Code: errInternal, Message: "Waiting for the operator confirmation; retry this request afterward"}
			}
			if err = c.out.Encode(response); err != nil {
				return false, err
			}
		}
	}
	if err = c.in.Err(); err != nil {
		return false, err
	}
	return false, fmt.Errorf("host closed the confirmation channel without an operator decision")
}
