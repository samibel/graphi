// Package mcp is the MCP stdio surface over the shared surface client.
//
// It speaks a minimal JSON-RPC 2.0 protocol over stdin/stdout using ONLY the Go
// standard library (encoding/json + bufio) — no external MCP SDK, no CGo, and
// zero outbound network activity (local-first contract). It exposes structural
// queries and search as MCP tools and dispatches every call to the SAME shared
// client, then returns the canonical serialized bytes. The serialized result is
// therefore byte-identical to the CLI for identical inputs (MCP↔CLI parity).
//
// Layering: mcp is a surface. It imports surfaces/client only and holds no
// query/traversal/ordering/serialization logic of its own.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// protocolVersion is the MCP protocol version this stdio handler reports.
const protocolVersion = "2024-11-05"

// --- JSON-RPC 2.0 envelopes (stdlib encoding/json only) ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent for notifications
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
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

// rpcServerRequest is an MCP server→client request. Roots-capable clients are
// queried only after the initialized notification, as required by the MCP
// lifecycle; their response is consumed by the same stdio session loop.
type rpcServerRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
}

// Binding is one repository-scoped client plus its owned cleanup. The MCP
// surface does not know how stores, ingesters, or sidecars are composed; cmd's
// runtime composition root supplies that implementation.
type Binding struct {
	Client client.Client
	Close  func()
}

// BindFunc creates a repository-scoped binding. roots is authoritative when
// non-nil (rootUri, inline roots, or roots/list); nil asks the composition root
// to use its configured fallback such as the process cwd.
type BindFunc func(ctx context.Context, roots []string) (Binding, error)

type boundClient struct {
	client client.Client
	stable client.StableClient
}

// ServerOption configures an MCP server profile without widening the default
// surface. Options are explicit at construction so Labs cannot be enabled by a
// request payload or ambient client input.
type ServerOption func(*Server)

// WithLabs opts a server into the maximal capability-gated Labs catalog. The
// default profile exposes only StableMCPToolNames.
func WithLabs() ServerOption {
	return func(s *Server) { s.labs = true }
}

// Server is the MCP stdio handler bound to a shared surface client.
type Server struct {
	// bound is atomically replaced as MCP roots bind or change. Stable and Labs
	// views always refer to the same client instance, preserving CAP-01 while
	// allowing one server process to defer repository choice until initialize.
	bound atomic.Pointer[boundClient]
	labs  bool

	catalogMu      sync.Mutex
	catalogBinding *boundClient
	catalog        []map[string]any

	dispatch       sync.RWMutex // prevents a roots rebind from closing an in-flight tool client
	mu             sync.Mutex
	binder         BindFunc
	cleanup        func()
	bindErr        error
	initialized    bool
	rootsCapable   bool
	awaitingRoots  bool
	rootsRequestID string
	rootsSequence  uint64
	closed         bool
	closeOnce      sync.Once

	// Async binding state (stdio only). A cold zero-config session runs a FULL
	// repository index inside the binder; running that synchronously inside
	// initialize stalled the client past its startup timeout, and clients
	// respond by killing and restarting the server — each restart aborting and
	// restarting the index (the kill/re-index spiral). bindInFlight marks a
	// binder running off the protocol loop; bindDone is closed when that
	// attempt finishes; bindCancel aborts its ingest on Close/roots-change;
	// bindGen invalidates a stale attempt's result (its Binding is closed and
	// discarded, never stored). bindGrace is how long lifecycle requests wait
	// for the binder before answering without it — long enough that a warm
	// store keeps the pre-async synchronous behavior, short enough that a cold
	// full index can never stall the protocol.
	bindInFlight bool
	bindDone     chan struct{}
	bindCancel   context.CancelFunc
	bindGen      uint64
	bindGrace    time.Duration
}

// defaultBindGrace keeps warm sessions synchronous (a drift-checked store
// binds in well under this) while bounding how long any protocol request can
// stall on a cold full index.
const defaultBindGrace = 2 * time.Second

// NewServer constructs an MCP server over an in-process query service.
// If searchSvc is non-nil, the search tool is also advertised.
func NewServer(q *query.Service, searchSvc *search.Service, opts ...ServerOption) *Server {
	return NewServerWithClient(client.NewDirect(q, searchSvc), opts...)
}

// NewServerWithClient constructs an MCP server over an arbitrary client
// (in-process or daemon). The stable view is the same client, narrowed to the
// consumer-owned ports (CAP-01).
func NewServerWithClient(c client.Client, opts ...ServerOption) *Server {
	s := &Server{}
	for _, opt := range opts {
		opt(s)
	}
	s.bound.Store(&boundClient{client: c, stable: client.AsStable(c)})
	return s
}

// NewServerWithBinder constructs an initially-unbound MCP server. The binder is
// invoked from the protocol lifecycle, never before initialize: legacy
// rootUri/inline roots bind during initialize, roots-capable clients are
// queried with roots/list after initialized, and only clients that advertise
// neither use the binder's nil-roots fallback. On the stdio transport the
// binder runs OFF the protocol loop: initialize waits at most bindGrace for
// it, so a cold full index prepares in the background instead of stalling the
// client into a kill/restart spiral; tools fail closed with a retryable
// "still indexing" error until the session is ready.
func NewServerWithBinder(bind BindFunc, opts ...ServerOption) *Server {
	s := &Server{binder: bind, bindGrace: defaultBindGrace}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithBindGrace overrides how long protocol requests wait for an in-flight
// repository binding before answering without it (test seam; 0 = fully
// asynchronous).
func WithBindGrace(d time.Duration) ServerOption {
	return func(s *Server) { s.bindGrace = d }
}

// Close releases the currently bound repository session exactly once. An
// in-flight background binding is cancelled (aborting its ingest); its late
// result is detected via s.closed and discarded by runBind.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.dispatch.Lock()
		defer s.dispatch.Unlock()
		s.mu.Lock()
		s.closed = true
		cleanup := s.cleanup
		s.cleanup = nil
		cancel := s.bindCancel
		s.bindCancel = nil
		s.bound.Store(nil)
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if cleanup != nil {
			cleanup()
		}
	})
}

// Serve runs the JSON-RPC read/dispatch/write loop until in reaches EOF. Each
// request line is a single JSON object (line-delimited framing); responses are
// written one JSON object per line. Notifications (no id) receive no response.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if ctx.Err() != nil {
		return nil
	}
	// A stdio scanner otherwise blocks forever after SIGTERM while the MCP
	// client keeps stdin open. When the reader is closeable (os.Stdin in the
	// shipped server), cancellation closes only that input endpoint, unblocking
	// Scan so the caller can run its normal deferred Runtime cleanup. EOF on an
	// uncancelled context is unchanged and never closes the input here.
	if closer, ok := in.(io.Closer); ok {
		stopClose := context.AfterFunc(ctx, func() { _ = closer.Close() })
		defer stopClose()
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	type scanEvent struct {
		line []byte
		err  error
		done bool
	}
	events := make(chan scanEvent)
	go func() {
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case events <- scanEvent{line: line}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case events <- scanEvent{err: scanner.Err(), done: true}:
		case <-ctx.Done():
		}
	}()
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	for {
		var event scanEvent
		select {
		case <-ctx.Done():
			return nil
		case event = <-events:
		}
		if event.done {
			return event.err
		}
		line := event.line
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
		resp, isNotification, outbound := s.handle(ctx, req)
		if isNotification {
			if outbound != nil {
				if err := enc.Encode(outbound); err != nil {
					return err
				}
			}
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
		if outbound != nil {
			if err := enc.Encode(outbound); err != nil {
				return err
			}
		}
	}
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (rpcResponse, bool, *rpcServerRequest) {
	return s.handleForTransport(ctx, req, true)
}

// handleForTransport keeps the protocol dispatcher shared while making one
// transport capability explicit. stdio can emit a roots/list server request on
// its bidirectional stream. The current HTTP handler has no request-associated
// SSE response stream, so it must reject an initialize exchange that would need
// that request instead of silently discarding it and leaving the session
// permanently unbound.
func (s *Server) handleForTransport(ctx context.Context, req rpcRequest, supportsServerRequests bool) (rpcResponse, bool, *rpcServerRequest) {
	isNotification := len(req.ID) == 0
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	// A JSON-RPC response has no method. Consume roots/list replies internally;
	// responses never receive responses of their own.
	if req.Method == "" && len(req.ID) != 0 {
		s.handleRootsResponse(ctx, req)
		return resp, true, nil
	}

	switch req.Method {
	case "initialize":
		if rerr := s.initialize(ctx, req.Params, supportsServerRequests); rerr != nil {
			resp.Error = rerr
			break
		}
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "graphi-query", "version": "1"},
		}
	case "notifications/initialized", "initialized":
		return resp, true, s.requestRootsIfNeeded()
	case "notifications/roots/list_changed", "roots/list_changed":
		s.invalidateRootsChanged()
		return resp, true, nil
	case "tools/list":
		s.dispatch.RLock()
		if rerr := s.repositoryUnavailable(); rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = map[string]any{"tools": s.toolDescriptors()}
		}
		s.dispatch.RUnlock()
	case "tools/call":
		s.dispatch.RLock()
		if unavailable := s.repositoryUnavailable(); unavailable != nil {
			resp.Error = unavailable
		} else {
			result, rerr := s.toolsCall(ctx, req.Params)
			if rerr != nil {
				resp.Error = rerr
			} else {
				resp.Result = result
			}
		}
		s.dispatch.RUnlock()
	default:
		resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	if isNotification {
		return resp, true, nil
	}
	return resp, false, nil
}

func (s *Server) repositoryUnavailable() *rpcError {
	if s.bound.Load() != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	message := "repository is not bound"
	switch {
	case s.bindInFlight:
		message += ": the session is still indexing the repository; retry in a moment"
	case s.awaitingRoots:
		message += ": waiting for the client's roots/list response"
	case s.bindErr != nil:
		message += ": " + s.bindErr.Error()
	case !s.initialized:
		message += ": initialize the MCP session first"
	}
	return &rpcError{Code: -32002, Message: message}
}

func (s *Server) client() client.Client {
	return s.bound.Load().client
}

// stableClient is the CAP-01 consumer-owned view of the same atomic binding:
// stable dispatch cannot reach a Labs-only method even when sessions rebind.
func (s *Server) stableClient() client.StableClient {
	return s.bound.Load().stable
}
