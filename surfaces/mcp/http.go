package mcp

import (
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/samibel/graphi/surfaces/guard"
)

// maxHTTPBody bounds a single streamable-HTTP request body (mirrors the stdio
// scanner cap) so a malformed/oversized request cannot exhaust memory.
const maxHTTPBody = 16 * 1024 * 1024

// HTTPHandler returns an http.Handler that exposes this MCP server over the
// streamable-HTTP transport (P9). It is ADDITIVE: stdio (Serve) is untouched.
// Each POST carries one JSON-RPC request; the handler routes it through the SAME
// transport-agnostic handle() seam stdio uses and serializes the response with
// the SAME encoder settings, so the response envelopes are byte-identical across
// transports by construction.
func (s *Server) HTTPHandler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBody))
	if err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// Parse error with no recoverable id — same shape as the stdio path.
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	resp, isNotification := s.handle(r.Context(), req)
	if isNotification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, resp)
}

// writeRPC serializes a JSON-RPC response with the SAME encoder configuration the
// stdio Serve loop uses (SetEscapeHTML(false), one JSON object + newline), so the
// bytes are identical to the stdio framing for the same response.
func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(resp)
}

// ListenLoopbackMCP binds the MCP streamable-HTTP handler to a loopback-only
// address, routing through the centralized SW-099 guard so the loopback/zero-
// egress policy is enforced in exactly one place across every transport. A
// non-loopback / wildcard bind is refused (guard.ErrNonLoopbackBind) before any
// socket is opened.
func ListenLoopbackMCP(addr string) (net.Listener, error) {
	return guard.ListenLoopback("tcp", addr)
}
