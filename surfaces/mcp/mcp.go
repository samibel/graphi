// Package mcp is the MCP stdio surface over the shared engine/query service.
//
// It speaks a minimal JSON-RPC 2.0 protocol over stdin/stdout using ONLY the Go
// standard library (encoding/json + bufio) — no external MCP SDK, no CGo, and
// zero outbound network activity (local-first contract). It exposes the same
// structural queries as MCP tools and dispatches every call to the SAME
// query.Service through query.Dispatch, then returns the bytes produced by the
// SAME canonical serializer (query.Marshal) as the CLI. The serialized result is
// therefore byte-identical to the CLI for identical inputs (MCP↔CLI parity).
//
// Layering: mcp is a surface. It imports engine/query + core only and holds no
// query/traversal/ordering/serialization logic of its own.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// protocolVersion is the MCP protocol version this stdio handler reports.
const protocolVersion = "2024-11-05"

// --- JSON-RPC 2.0 envelopes (stdlib encoding/json only) ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server is the MCP stdio handler bound to the shared query service.
type Server struct {
	svc *query.Service
}

// NewServer constructs an MCP server over the shared read-only query service.
func NewServer(svc *query.Service) *Server { return &Server{svc: svc} }

// Serve runs the JSON-RPC read/dispatch/write loop until in reaches EOF. Each
// request line is a single JSON object (line-delimited framing); responses are
// written one JSON object per line. Notifications (no id) receive no response.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Parse error with no recoverable id.
			if werr := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}); werr != nil {
				return werr
			}
			continue
		}
		resp, isNotification := s.handle(ctx, req)
		if isNotification {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	isNotification := len(req.ID) == 0
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "graphi-query", "version": "1"},
		}
	case "notifications/initialized", "initialized":
		return resp, true // notification, no reply
	case "tools/list":
		resp.Result = map[string]any{"tools": toolDescriptors()}
	case "tools/call":
		result, rerr := s.toolsCall(ctx, req.Params)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	if isNotification {
		return resp, true
	}
	return resp, false
}

// callParams is the tools/call params shape: a tool name plus its arguments.
type callParams struct {
	Name      string `json:"name"`
	Arguments struct {
		Symbol string `json:"symbol"`
		Depth  *int   `json:"depth"`
	} `json:"arguments"`
}

func (s *Server) toolsCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	depth := 1
	if p.Arguments.Depth != nil {
		depth = *p.Arguments.Depth
	}

	// The tool name IS the operation name, dispatched through the one shared table.
	res, err := s.svc.Dispatch(ctx, p.Name, model.NodeId(p.Arguments.Symbol), depth)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	b, err := query.Marshal(res)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}

	// MCP tool result: a single text content block carrying the canonical bytes —
	// byte-identical to the CLI output for the same query.
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// toolDescriptors advertises one tool per supported operation, derived from the
// engine's canonical operation list so the surface never re-lists them locally.
func toolDescriptors() []map[string]any {
	tools := make([]map[string]any, 0, len(query.Operations))
	for _, op := range query.Operations {
		props := map[string]any{
			"symbol": map[string]any{"type": "string", "description": "symbol (node) id to query"},
		}
		required := []string{"symbol"}
		if op == query.OpNeighborhood {
			props["depth"] = map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("hop depth (clamped to MaxNeighborhoodDepth=%d)", query.MaxNeighborhoodDepth),
			}
		}
		tools = append(tools, map[string]any{
			"name":        op,
			"description": "structural query: " + op,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
		})
	}
	return tools
}
