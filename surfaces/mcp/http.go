package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/samibel/graphi/surfaces/guard"
)

// maxHTTPBody bounds a single streamable-HTTP request body (mirrors the stdio
// scanner cap) so a malformed/oversized request cannot exhaust memory.
const maxHTTPBody = 16 * 1024 * 1024

// mcpHTTPBodyTooLargeCode is a transport-level JSON-RPC server error. Keeping a
// typed code alongside HTTP 413 lets clients distinguish size rejection from a
// JSON parse error.
const mcpHTTPBodyTooLargeCode = -32001

// HTTPHandler returns an http.Handler that exposes this MCP server over the
// streamable-HTTP transport (P9). It is ADDITIVE: stdio (Serve) is untouched.
// Each POST carries one JSON-RPC request; the handler routes it through the SAME
// transport-agnostic dispatcher stdio uses and serializes the response with the
// SAME encoder settings, so ordinary response envelopes are byte-identical
// across transports. This handler does not expose a request-associated SSE
// response stream. A binder-backed HTTP session must therefore supply rootUri or
// inline roots during initialize; roots/list discovery remains stdio-only and is
// rejected explicitly instead of being silently dropped.
func (s *Server) HTTPHandler() http.Handler {
	return mcpRequestSecurityGuard(http.HandlerFunc(s.serveHTTP))
}

// mcpRequestSecurityGuard prevents DNS rebinding and cross-origin browser
// access to the loopback MCP endpoint. Origin-less local clients remain valid;
// when Origin is present it must exactly match scheme, normalized host and port.
func mcpRequestSecurityGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		requestAuthority, err := parseMCPHTTPAuthority(r.Host, scheme)
		if err != nil || !isLoopbackMCPHTTPHost(requestAuthority.host) {
			writeRPCStatus(w, http.StatusForbidden, rpcResponse{
				JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{Code: -32003, Message: "request host must be loopback or localhost"},
			})
			return
		}
		origins := r.Header.Values("Origin")
		if len(origins) > 1 {
			writeMCPOriginForbidden(w)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			originAuthority, originScheme, err := parseMCPHTTPOrigin(origin)
			if err != nil || originScheme != scheme || originAuthority != requestAuthority {
				writeMCPOriginForbidden(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeMCPOriginForbidden(w http.ResponseWriter) {
	writeRPCStatus(w, http.StatusForbidden, rpcResponse{
		JSONRPC: "2.0", ID: json.RawMessage("null"),
		Error: &rpcError{Code: -32003, Message: "cross-origin requests are not allowed"},
	})
}

type mcpHTTPAuthority struct {
	host string
	port string
}

func parseMCPHTTPAuthority(raw, scheme string) (mcpHTTPAuthority, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "/?#@") {
		return mcpHTTPAuthority{}, errors.New("invalid authority")
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		switch {
		case net.ParseIP(raw) != nil:
			host = raw
		case !strings.Contains(raw, ":"):
			host = raw
		default:
			return mcpHTTPAuthority{}, fmt.Errorf("invalid authority %q: %w", raw, err)
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return mcpHTTPAuthority{}, errors.New("empty authority host")
	}
	if port == "" {
		port = defaultMCPHTTPPort(scheme)
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return mcpHTTPAuthority{}, errors.New("invalid authority port")
		}
		port = strconv.Itoa(n)
	}
	return mcpHTTPAuthority{host: host, port: port}, nil
}

func parseMCPHTTPOrigin(raw string) (mcpHTTPAuthority, string, error) {
	if raw == "null" {
		return mcpHTTPAuthority{}, "", errors.New("opaque origin")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return mcpHTTPAuthority{}, "", errors.New("invalid origin")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return mcpHTTPAuthority{}, "", errors.New("invalid origin scheme")
	}
	authority, err := parseMCPHTTPAuthority(u.Host, scheme)
	if err != nil || !isLoopbackMCPHTTPHost(authority.host) {
		return mcpHTTPAuthority{}, "", errors.New("invalid origin authority")
	}
	return authority, scheme, nil
}

func defaultMCPHTTPPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func isLoopbackMCPHTTPHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxHTTPBody)
	body, err := io.ReadAll(limited)
	_ = limited.Close()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeRPCStatus(w, http.StatusRequestEntityTooLarge, rpcResponse{
				JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{Code: mcpHTTPBodyTooLargeCode, Message: fmt.Sprintf("request body exceeds %d bytes", maxHTTPBody)},
			})
			return
		}
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// Parse error with no recoverable id — same shape as the stdio path.
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	resp, isNotification, outbound := s.handleForTransport(r.Context(), req, false)
	if outbound != nil {
		// Defensive invariant: handleForTransport(false) must reject any lifecycle
		// that needs a server request before it reaches this transport boundary.
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code: -32603, Message: "server request is unavailable on this HTTP transport",
		}})
		return
	}
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
	writeRPCStatus(w, http.StatusOK, resp)
}

func writeRPCStatus(w http.ResponseWriter, status int, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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
