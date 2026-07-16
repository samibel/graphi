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
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

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
}

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
// rootUri/inline roots bind synchronously during initialize, roots-capable
// clients are queried with roots/list after initialized, and only clients that
// advertise neither use the binder's nil-roots fallback.
func NewServerWithBinder(bind BindFunc, opts ...ServerOption) *Server {
	s := &Server{binder: bind}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Close releases the currently bound repository session exactly once.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.dispatch.Lock()
		defer s.dispatch.Unlock()
		s.mu.Lock()
		s.closed = true
		cleanup := s.cleanup
		s.cleanup = nil
		s.bound.Store(nil)
		s.mu.Unlock()
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

type protocolRoot struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

type initializeParams struct {
	RootURI      string                     `json:"rootUri"`
	Roots        json.RawMessage            `json:"roots"`
	Capabilities map[string]json.RawMessage `json:"capabilities"`
}

// initialize captures repository hints before constructing a Runtime. A
// roots-capable client without an inline root is intentionally left unbound
// until its post-initialize roots/list response arrives; tools fail closed in
// that short protocol window.
func (s *Server) initialize(ctx context.Context, raw json.RawMessage, supportsServerRequests bool) *rpcError {
	var p initializeParams
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p); err != nil {
			return &rpcError{Code: -32602, Message: "invalid initialize params"}
		}
	}
	_, rootsCapable := p.Capabilities["roots"]
	binder := s.binder

	var (
		roots    []string
		supplied bool
	)
	if binder != nil {
		var err error
		roots, supplied, err = initializeRoots(p)
		if err != nil {
			return &rpcError{Code: -32602, Message: err.Error()}
		}
		if rootsCapable && !supplied && !supportsServerRequests {
			return &rpcError{
				Code:    -32602,
				Message: "streamable HTTP repository binding requires rootUri or inline roots during initialize; roots/list discovery is available only on a bidirectional transport",
			}
		}
	}

	s.mu.Lock()
	s.initialized = true
	s.rootsCapable = rootsCapable
	s.mu.Unlock()
	if binder == nil {
		return nil // explicitly bound -db/-daemon and in-process test servers
	}
	if supplied {
		if err := s.bind(ctx, roots); err != nil {
			return &rpcError{Code: -32002, Message: "repository binding failed: " + err.Error()}
		}
		return nil
	}
	if rootsCapable {
		return nil // requestRootsIfNeeded runs after notifications/initialized
	}
	if err := s.bind(ctx, nil); err != nil {
		return &rpcError{Code: -32002, Message: "repository binding failed: " + err.Error()}
	}
	return nil
}

func initializeRoots(p initializeParams) ([]string, bool, error) {
	var roots []protocolRoot
	supplied := false
	if strings.TrimSpace(p.RootURI) != "" {
		supplied = true
		roots = append(roots, protocolRoot{URI: p.RootURI})
	}
	if len(p.Roots) != 0 && string(p.Roots) != "null" {
		supplied = true
		var inline []protocolRoot
		if err := json.Unmarshal(p.Roots, &inline); err != nil {
			return nil, true, fmt.Errorf("invalid initialize roots: %w", err)
		}
		roots = append(roots, inline...)
	}
	if !supplied {
		return nil, false, nil
	}
	paths, err := rootPaths(roots)
	return paths, true, err
}

// rootPaths validates MCP roots and converts local file URIs at the transport
// boundary. Non-file/network roots are rejected rather than accidentally being
// interpreted relative to the server process cwd.
func rootPaths(roots []protocolRoot) ([]string, error) {
	paths := make([]string, 0, len(roots)) // non-nil: an empty root set is authoritative
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		path, err := localRootPath(root.URI)
		if err != nil {
			return nil, fmt.Errorf("invalid repository root %q: %w", root.URI, err)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths, nil
}

func localRootPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty URI")
	}
	// Validate Windows path syntax even on Unix hosts. filepath.IsAbs is
	// intentionally OS-specific and would otherwise accept //host/share as a
	// local POSIX path on Unix while the same MCP request becomes a UNC/network
	// access when the server runs on Windows. This is syntax-only: it runs before
	// the binder and therefore before any Lstat/open operation.
	if err := rejectNonLocalWindowsRoot(raw); err != nil {
		return "", err
	}
	// Accept an absolute native path as a compatibility input. URI-aware MCP
	// clients use file://; relative paths are never accepted.
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("unsupported URI scheme %q (want file)", u.Scheme)
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", fmt.Errorf("non-local file URI host %q", u.Host)
	}
	path := u.Path
	if path == "" && u.Opaque != "" {
		path = u.Opaque
	}
	// url.Parse decodes escaped path bytes into Path/Opaque. Revalidate that
	// decoded value so file:////host/share and percent-encoded UNC/device forms
	// cannot cross the transport boundary.
	if err := rejectNonLocalWindowsRoot(path); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	path = filepath.FromSlash(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("file URI does not contain an absolute local path")
	}
	return filepath.Clean(path), nil
}

// rejectNonLocalWindowsRoot rejects syntax that Windows interprets as a UNC,
// network share, Win32 device namespace, or NT object-manager namespace. It is
// deliberately independent of runtime.GOOS so the security boundary is
// testable on every CI host and behaves identically before platform-specific
// path cleaning.
func rejectNonLocalWindowsRoot(raw string) error {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && isWindowsSeparator(raw[0]) && isWindowsSeparator(raw[1]) {
		return errors.New("UNC, network-share, and Windows device roots are not allowed")
	}
	// Windows accepts both separators. Normalize before namespace matching so
	// decoded URI paths such as /??/UNC/host/share cannot turn into a device/UNC
	// path only after filepath.FromSlash below.
	lower := strings.ToLower(strings.ReplaceAll(raw, "/", `\`))
	for _, namespace := range []string{
		`\??\`,
		`\device\`,
		`\global??\`,
		`\dosdevices\`,
		`\systemroot\`,
	} {
		if strings.HasPrefix(lower, namespace) {
			return errors.New("Windows device namespace roots are not allowed")
		}
	}
	return nil
}

func isWindowsSeparator(b byte) bool {
	return b == '/' || b == '\\'
}

func (s *Server) bind(ctx context.Context, roots []string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("MCP session is closed")
	}
	binder := s.binder
	s.awaitingRoots = false
	s.bindErr = nil
	s.mu.Unlock()
	if binder == nil {
		if s.bound.Load() != nil {
			return nil
		}
		return errors.New("no repository binder configured")
	}

	binding, err := binder(ctx, roots)
	if err == nil && binding.Client == nil {
		err = errors.New("repository binder returned no client")
	}
	if err != nil {
		s.failBinding(err)
		return err
	}
	if binding.Close == nil {
		binding.Close = func() {}
	}

	s.dispatch.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.dispatch.Unlock()
		binding.Close()
		return errors.New("MCP session is closed")
	}
	oldCleanup := s.cleanup
	s.cleanup = binding.Close
	s.bindErr = nil
	s.awaitingRoots = false
	s.bound.Store(&boundClient{client: binding.Client, stable: client.AsStable(binding.Client)})
	s.mu.Unlock()
	s.dispatch.Unlock()
	if oldCleanup != nil {
		oldCleanup()
	}
	return nil
}

func (s *Server) failBinding(err error) {
	s.dispatch.Lock()
	s.mu.Lock()
	oldCleanup := s.cleanup
	s.cleanup = nil
	s.bindErr = err
	s.awaitingRoots = false
	s.bound.Store(nil)
	s.mu.Unlock()
	s.dispatch.Unlock()
	if oldCleanup != nil {
		oldCleanup()
	}
}

func (s *Server) requestRootsIfNeeded() *rpcServerRequest {
	s.dispatch.Lock()
	s.mu.Lock()
	if s.closed || !s.rootsCapable || s.awaitingRoots || s.bound.Load() != nil {
		s.mu.Unlock()
		s.dispatch.Unlock()
		return nil
	}
	s.rootsSequence++
	s.rootsRequestID = fmt.Sprintf("graphi-roots-%d", s.rootsSequence)
	s.awaitingRoots = true
	id := s.rootsRequestID
	s.mu.Unlock()
	s.dispatch.Unlock()
	return &rpcServerRequest{JSONRPC: "2.0", ID: id, Method: "roots/list"}
}

// invalidateRootsChanged preserves the one-repository-per-process contract.
// Serving the old graph after a client changes workspaces would be incorrect;
// silently hopping repositories would change the session identity. Close the
// old Runtime and require the MCP client to restart this stdio session.
func (s *Server) invalidateRootsChanged() {
	s.dispatch.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.dispatch.Unlock()
		return
	}
	cleanup := s.cleanup
	s.cleanup = nil
	s.awaitingRoots = false
	s.bindErr = errors.New("client repository roots changed; restart the MCP session")
	s.bound.Store(nil)
	s.mu.Unlock()
	s.dispatch.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func (s *Server) handleRootsResponse(ctx context.Context, req rpcRequest) {
	var id string
	if err := json.Unmarshal(req.ID, &id); err != nil {
		return // not one of our string request IDs
	}
	s.mu.Lock()
	if !s.awaitingRoots || id != s.rootsRequestID {
		s.mu.Unlock()
		return
	}
	s.awaitingRoots = false
	s.mu.Unlock()
	if req.Error != nil {
		s.failBinding(fmt.Errorf("roots/list failed (%d): %s", req.Error.Code, req.Error.Message))
		return
	}
	var result struct {
		Roots []protocolRoot `json:"roots"`
	}
	if err := json.Unmarshal(req.Result, &result); err != nil {
		s.failBinding(fmt.Errorf("invalid roots/list result: %w", err))
		return
	}
	paths, err := rootPaths(result.Roots)
	if err != nil {
		s.failBinding(err)
		return
	}
	_ = s.bind(ctx, paths) // bind records the error for the next client request
}

func (s *Server) repositoryUnavailable() *rpcError {
	if s.bound.Load() != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	message := "repository is not bound"
	switch {
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

// callParams is the tools/call params shape: a tool name plus its arguments.
type callParams struct {
	Name      string `json:"name"`
	Arguments struct {
		Symbol    string `json:"symbol"`
		Target    string `json:"target"`
		Concept   string `json:"concept"`
		Depth     *int   `json:"depth"`
		Analyzer  string `json:"analyzer"`
		Direction string `json:"direction"`
		MaxNodes  *int   `json:"max_nodes"`
		// SW-039 pr-risk scorer arguments (local-first; no remote fetch).
		Diff       string `json:"diff"`
		Provenance string `json:"provenance"`
		// SW-042 sticky PR-comment + merge-gate arguments (local-first).
		PR            string `json:"pr"`
		GateEnabled   bool   `json:"gate_enabled"`
		GateThreshold *int   `json:"gate_threshold"`
		Publish       bool   `json:"publish"`
		// SW-038 edit/refactor + undo arguments.
		Kind            string `json:"kind"`
		TargetSymbol    string `json:"target_symbol"`
		OldName         string `json:"old_name"`
		NewName         string `json:"new_name"`
		DestinationFile string `json:"destination_file"`
		UndoToken       string `json:"undo_token"`
		Actor           string `json:"actor"`
		// EP-011 G1 compound query text (SEED/HOP/WHERE/MAXDEPTH).
		Query string `json:"query"`
		// SW-085 pattern-query arguments: search_ast JSON pattern + limit, and the
		// find_clones JSON config.
		Pattern string `json:"pattern"`
		Limit   *int   `json:"limit"`
		Config  string `json:"config"`
		// EP-012 memory arguments.
		Op           string   `json:"op"`
		Scope        string   `json:"scope"`
		Notebook     string   `json:"notebook"`
		Tags         []string `json:"tags"`
		Payload      string   `json:"payload"`
		MemID        string   `json:"mem_id"`
		Source       string   `json:"source"`
		Confidence   string   `json:"confidence"`
		Evidence     string   `json:"evidence"`
		ExportToPath string   `json:"export_to_path"`
		// EP-012 distill arguments.
		SessionID      string        `json:"session_id"`
		Turns          []client.Turn `json:"turns"`
		Decisions      []string      `json:"decisions"`
		Risks          []string      `json:"risks"`
		OpenQuestions  []string      `json:"open_questions"`
		FileReferences []string      `json:"file_references"`
		// EP-012 skillgen arguments.
		Name         string             `json:"name"`
		Trigger      string             `json:"trigger"`
		Description  string             `json:"description"`
		SkillInputs  []string           `json:"skill_inputs"`
		SkillOutputs []string           `json:"skill_outputs"`
		SkillSteps   []client.SkillStep `json:"skill_steps"`

		// SW-107 compare_branches base/head branch refs (suggest_reviewers reuses the
		// shared `diff` argument above).
		Base string `json:"base"`
		Head string `json:"head"`

		// SW-108 critique_review: the PR number to fetch the existing review for (when
		// no inline review is supplied) and an inline existing-review JSON string. The
		// touched set reuses the shared `diff` argument above. The review is structured
		// at the surface; the engine never parses a raw blob or fetches.
		PRNumber int    `json:"pr_number"`
		Review   string `json:"review"`
	} `json:"arguments"`
}

// mcpActor is the default actor recorded for edits initiated via the MCP surface
// when the caller supplies none (Scope decision 6: actor is per-surface,
// recorded, excluded from the AC-4 parity comparable subset).
const mcpActor = "mcp"

func (s *Server) toolsCall(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	if !s.toolAdvertised(p.Name) {
		message := fmt.Sprintf("tool not available: %s", p.Name)
		if !s.labs && !IsStableMCPTool(p.Name) {
			message += " (the default MCP profile is Stable; restart with -labs to opt in to Labs tools)"
		}
		return nil, &rpcError{Code: -32602, Message: message}
	}

	if p.Name == ToolSearch {
		return s.searchCall(ctx, p)
	}
	if p.Name == ToolImpact {
		return s.impactCall(ctx, p)
	}
	if p.Name == ToolSearchSemantic {
		return s.semanticSearchCall(ctx, p)
	}
	if p.Name == ToolSavings {
		return s.savingsCall(ctx)
	}
	if p.Name == ToolAnalyze {
		return s.analysisCall(ctx, p)
	}
	// SW-038 edit/refactor command surface (thin transport over the shared client).
	switch p.Name {
	case ToolRefactorPreview:
		return s.refactorPreviewCall(ctx, p)
	case ToolRefactor:
		return s.refactorCall(ctx, p)
	case ToolUndo:
		return s.undoCall(ctx, p)
	}
	// SW-042 sticky PR-comment writer + optional risk-threshold merge gate.
	if p.Name == ToolPrComment {
		return s.prCommentCall(ctx, p)
	}
	// EP-011 G1 compound query.
	if p.Name == ToolCompound {
		return s.compoundCall(ctx, p)
	}
	// SW-085 pattern-query singletons.
	switch p.Name {
	case ToolSearchAST:
		return s.searchASTCall(ctx, p)
	case ToolFindClones:
		return s.findClonesCall(ctx, p)
	}
	// EP-012 agent memory & skills.
	switch p.Name {
	case ToolMemory:
		return s.memoryCall(ctx, p)
	case ToolDistill:
		return s.distillCall(ctx, p)
	case ToolSkillGen:
		return s.skillGenCall(ctx, p)
	}
	// EP-018 multi-PR triage suite (SW-105): list_prs (read-only forge enumeration)
	// and triage_prs (single-pass graph-derived ranking). Both ride the shared
	// client seam, so the bytes are byte-identical across surfaces.
	switch p.Name {
	case ToolListPRs:
		return s.listPRsCall(ctx)
	case ToolTriagePRs:
		return s.triagePRsCall(ctx)
	case ToolConflictsPRs:
		return s.conflictsPRsCall(ctx)
	}
	// EP-018 SW-107: suggest_reviewers (ranked candidate reviewers from the touched
	// set) and compare_branches (graph-level diff of two branch states). Both ride
	// the shared client seam, so the bytes are byte-identical across surfaces.
	switch p.Name {
	case ToolSuggestReviewers:
		return s.suggestReviewersCall(ctx, p)
	case ToolCompareBranches:
		return s.compareBranchesCall(ctx, p)
	}
	// EP-018 SW-108 (capstone): critique_review (graph-evidence critique of an existing
	// review). Rides the shared client seam, so the bytes are byte-identical across
	// surfaces.
	if p.Name == ToolCritiqueReview {
		return s.critiqueReviewCall(ctx, p)
	}
	// EP-005 deep-analysis tools (SW-033): each dedicated tool routes through
	// the generic analysis dispatch by injecting its analyzer name.
	if deepAnalyzerName, ok := deepAnalyzerTools[p.Name]; ok {
		p.Arguments.Analyzer = deepAnalyzerName
		return s.analysisCall(ctx, p)
	}

	// EP-020 agent-first task tools (SW-115 / SW-116 / SW-117) plus EP-024 (SW-134).
	// Catalog filtering has already established that this binding supports them.
	switch p.Name {
	case ToolExplainSymbol:
		return s.explainSymbolCall(ctx, p)
	case ToolRelatedFiles:
		return s.relatedFilesCall(ctx, p)
	case ToolChangeRisk:
		return s.changeRiskCall(ctx, p)
	case ToolAgentBrief:
		return s.agentBriefCall(ctx, p)
	}

	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	depth := 1
	if p.Arguments.Depth != nil {
		depth = *p.Arguments.Depth
	}

	b, err := s.stableClient().Query(ctx, p.Name, p.Arguments.Symbol, depth)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}

	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// compoundCall runs a compound / Cypher-style graph query (EP-011 G1). The
// query text is the single `query` argument; the result bytes are the canonical
// query.Result, byte-identical to every fixed query across surfaces.
func (s *Server) compoundCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Query == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: query"}
	}
	b, err := s.client().Compound(ctx, p.Arguments.Query)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// memoryCall runs an EP-012 memory operation through the shared client and
// returns the canonical serialized MemoryResponse.
func (s *Server) memoryCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Op == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: op"}
	}
	b, err := s.client().Memory(ctx, client.MemoryRequest{
		Op:           p.Arguments.Op,
		Scope:        p.Arguments.Scope,
		Notebook:     p.Arguments.Notebook,
		Tags:         p.Arguments.Tags,
		Payload:      p.Arguments.Payload,
		ID:           p.Arguments.MemID,
		Kind:         p.Arguments.Kind,
		Source:       p.Arguments.Source,
		Confidence:   p.Arguments.Confidence,
		Evidence:     p.Arguments.Evidence,
		Limit:        derefInt(p.Arguments.Limit),
		ExportToPath: p.Arguments.ExportToPath,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// distillCall runs EP-012 session distillation through the shared client and
// returns the canonical serialized DistillResponse.
func (s *Server) distillCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.SessionID == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: session_id"}
	}
	b, err := s.client().Distill(ctx, client.DistillRequest{
		SessionID:      p.Arguments.SessionID,
		Turns:          p.Arguments.Turns,
		Decisions:      p.Arguments.Decisions,
		Risks:          p.Arguments.Risks,
		OpenQuestions:  p.Arguments.OpenQuestions,
		FileReferences: p.Arguments.FileReferences,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// skillGenCall runs EP-012 deterministic skill generation through the shared
// client and returns the canonical serialized SkillGenResponse.
func (s *Server) skillGenCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Name == "" || p.Arguments.Trigger == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required arguments: name and trigger"}
	}
	b, err := s.client().SkillGen(ctx, client.SkillGenRequest{
		Name:        p.Arguments.Name,
		Trigger:     p.Arguments.Trigger,
		Description: p.Arguments.Description,
		Inputs:      p.Arguments.SkillInputs,
		Outputs:     p.Arguments.SkillOutputs,
		Steps:       p.Arguments.SkillSteps,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

func (s *Server) searchCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: query"}
	}
	limit := search.DefaultResultLimit
	if p.Arguments.Depth != nil && *p.Arguments.Depth > 0 {
		limit = *p.Arguments.Depth
	}
	b, err := s.stableClient().Search(ctx, p.Arguments.Symbol, limit)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// semanticSearchCall dispatches the OPTIONAL semantic-search tool (SW-059). It
// returns the canonical serialized SemanticResponse from the shared client. When
// no embedder is configured it cleanly reports the typed graceful-skip
// "unavailable" response (Available=false) WITHOUT an error — byte-identical to
// the CLI and HTTP surfaces (parity by construction through the single client
// seam).
func (s *Server) semanticSearchCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: query"}
	}
	limit := search.DefaultResultLimit
	if p.Arguments.Depth != nil && *p.Arguments.Depth > 0 {
		limit = *p.Arguments.Depth
	}
	b, err := s.client().SemanticSearch(ctx, p.Arguments.Symbol, limit)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// searchASTCall dispatches the structural AST pattern query (SW-082 / SW-085)
// through the shared client. The JSON pattern rides the `pattern` argument and the
// optional `limit` bounds results; the returned bytes are the canonical
// query.Marshal output, byte-identical to the CLI and HTTP surfaces. A malformed
// pattern surfaces the engine's typed error as a JSON-RPC error (no new shape).
func (s *Server) searchASTCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Pattern == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: pattern"}
	}
	limit := 0
	if p.Arguments.Limit != nil {
		limit = *p.Arguments.Limit
	}
	b, err := s.client().SearchAST(ctx, p.Arguments.Pattern, limit)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// findClonesCall dispatches the clone-detection query (SW-083 / SW-085) through the
// shared client. The optional JSON config rides the `config` argument (empty ⇒
// engine defaults); the returned bytes are the canonical query.MarshalCloneResult
// output for byte-identical parity.
func (s *Server) findClonesCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.client().FindClones(ctx, p.Arguments.Config)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// savingsCall dispatches the savings readout tool (SW-020). It returns the
// canonical structured readout (per-call/session/cumulative USD + cap flags) so
// the MCP readout stays byte-identical to the CLI for the same ledger state.
func (s *Server) savingsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().Savings(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// analysisCall dispatches a named analyzer (SW-022) through the shared client.
// It holds no analysis logic: it builds AnalyzeParams, calls client.Client.Analyze,
// and returns the canonical serialized bytes (byte-identical to the CLI for the
// same inputs, preserving MCP<->CLI parity).
func (s *Server) analysisCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Analyzer == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: analyzer"}
	}
	// The pr-risk scorer (SW-039) is diff-driven, not symbol-driven: it requires
	// a diff argument and accepts no symbol. The SW-104 EP-017 operations
	// (communities, watcher-status, notebook-ingest, taint-query) are whole-graph /
	// status operations needing no symbol (shared client.AnalyzerSymbolOptional rule,
	// identical to the CLI). Every other analyzer requires a symbol.
	switch {
	case p.Arguments.Analyzer == "pr-risk" || p.Arguments.Analyzer == "pr-signals" || p.Arguments.Analyzer == "pr-questions":
		if p.Arguments.Diff == "" {
			return nil, &rpcError{Code: -32602, Message: "missing required argument: diff"}
		}
	case client.AnalyzerSymbolOptional(p.Arguments.Analyzer):
		// no required symbol argument
	case p.Arguments.Symbol == "":
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	// Direction passes through verbatim; the ENGINE owns the default (empty →
	// reverse = dependents/blast radius since the v0.1.3 direction fix). A
	// surface-side fallback here would silently shadow that single source of
	// truth.
	direction := p.Arguments.Direction
	maxNodes := 0
	if p.Arguments.MaxNodes != nil {
		maxNodes = *p.Arguments.MaxNodes
	}
	b, err := s.client().Analyze(ctx, client.AnalyzeParams{
		Name:       p.Arguments.Analyzer,
		Symbol:     p.Arguments.Symbol,
		Target:     p.Arguments.Target,
		Concept:    p.Arguments.Concept,
		Direction:  direction,
		MaxNodes:   maxNodes,
		Diff:       p.Arguments.Diff,
		Provenance: p.Arguments.Provenance,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// impactCall is the dedicated stable analyzer port. The wire input has no
// analyzer selector and any injected analyzer field is ignored: this path can
// dispatch only the frozen "impact" operation through StableClient.Impact.
func (s *Server) impactCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	maxNodes := 0
	if p.Arguments.MaxNodes != nil {
		maxNodes = *p.Arguments.MaxNodes
	}
	b, err := s.stableClient().Impact(ctx, client.ImpactParams{
		Symbol:    p.Arguments.Symbol,
		Direction: p.Arguments.Direction,
		MaxNodes:  maxNodes,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// prCommentCall (SW-042) renders the assembled PR-review findings into one sticky
// Markdown comment and evaluates the optional risk-threshold merge gate through
// the shared client, returning the canonical serialized PublishResult. It holds
// no engine logic: it builds a PrCommentRequest and calls client.Client.PrComment,
// so MCP and CLI emit byte-identical output for the same inputs (parity). The
// diff is diff-driven (required); the default is an offline dry-run.
func (s *Server) prCommentCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Diff == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: diff"}
	}
	threshold := 700
	if p.Arguments.GateThreshold != nil {
		threshold = *p.Arguments.GateThreshold
	}
	provenance := p.Arguments.Provenance
	if provenance == "" {
		provenance = "summary"
	}
	b, err := s.client().PrComment(ctx, client.PrCommentRequest{
		PR:            p.Arguments.PR,
		Diff:          p.Arguments.Diff,
		Provenance:    provenance,
		GateEnabled:   p.Arguments.GateEnabled,
		GateThreshold: threshold,
		Publish:       p.Arguments.Publish,
	})
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}, nil
}

// refactorRequest builds the transport-agnostic request from the MCP arguments.
// TargetSymbol falls back to Symbol so callers can use either field name.
func refactorRequest(p callParams) client.RefactorRequest {
	target := p.Arguments.TargetSymbol
	if target == "" {
		target = p.Arguments.Symbol
	}
	return client.RefactorRequest{
		Kind:            p.Arguments.Kind,
		TargetSymbol:    target,
		OldName:         p.Arguments.OldName,
		NewName:         p.Arguments.NewName,
		DestinationFile: p.Arguments.DestinationFile,
	}
}

// refactorPreviewCall (SW-038) returns the EP-004 impact set BEFORE mutation
// (AC-1) by delegating to the shared client.RefactorPreview. No engine logic.
func (s *Server) refactorPreviewCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.client().RefactorPreview(ctx, refactorRequest(p))
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// refactorCall (SW-038) commits a refactor through the shared client and returns
// the canonical change record. The actor defaults to "mcp" unless supplied. No
// engine logic — the surface only marshals inputs into a RefactorRequest.
func (s *Server) refactorCall(ctx context.Context, p callParams) (any, *rpcError) {
	actor := p.Arguments.Actor
	if actor == "" {
		actor = mcpActor
	}
	b, err := s.client().Refactor(ctx, refactorRequest(p), actor)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// undoCall (SW-038) reverses an applied edit by its undo token and returns the
// canonical reversal change record. No engine logic.
func (s *Server) undoCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.UndoToken == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: undo_token"}
	}
	actor := p.Arguments.Actor
	if actor == "" {
		actor = mcpActor
	}
	b, err := s.client().Undo(ctx, p.Arguments.UndoToken, actor)
	if err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}
	return textResult(b), nil
}

// listPRsCall (SW-105) enumerates open PRs through the read-only forge boundary
// and returns the canonical serialized forge.PRList. It holds no engine logic and
// performs no scoring — pure metadata enumeration through the shared client.
func (s *Server) listPRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().ListPRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// triagePRsCall (SW-105) returns the single-pass graph-derived ranked PR triage
// through the shared client (forge enumeration → zero-egress engine analyzer →
// shared encoder), so the ranked TriageReport is byte-identical across surfaces.
func (s *Server) triagePRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().TriagePRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// conflictsPRsCall (SW-106) returns the inter-PR conflict report through the
// shared client (forge enumeration → zero-egress engine analyzer → shared
// encoder), so the pairwise ConflictReport is byte-identical across surfaces.
func (s *Server) conflictsPRsCall(ctx context.Context) (any, *rpcError) {
	b, err := s.client().ConflictsPRs(ctx)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// suggestReviewersCall (SW-107) returns the ranked candidate-reviewer report
// through the shared client (touched-set resolution → zero-egress engine analyzer →
// shared encoder), so the ReviewerReport is byte-identical across surfaces. The
// `diff` argument is the local-first PR diff / line-oriented ref string.
func (s *Server) suggestReviewersCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.client().SuggestReviewers(ctx, p.Arguments.Diff)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// compareBranchesCall (SW-107) returns the graph-level branch diff through the
// shared client (base/head materialized above the surface boundary → zero-egress
// engine analyzer → shared encoder), so the BranchDiffReport is byte-identical
// across surfaces. The `base`/`head` arguments are branch refs.
func (s *Server) compareBranchesCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.client().CompareBranches(ctx, p.Arguments.Base, p.Arguments.Head)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// critiqueReviewCall (SW-108) returns the graph-evidence critique of an existing
// review through the shared client (surface review fetch / inline review → zero-egress
// engine analyzer → shared encoder), so the CritiqueReport is byte-identical across
// surfaces. The `pr_number` selects the review to fetch; `review` supplies an inline
// review JSON (takes precedence); `diff` is the PR's touched set.
func (s *Server) critiqueReviewCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, err := s.client().CritiqueReview(ctx, p.Arguments.PRNumber, p.Arguments.Diff, p.Arguments.Review)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// textResult wraps canonical serialized bytes in the MCP tool-result envelope.
func textResult(b []byte) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// agentBriefCall (SW-134) returns a bounded, cited task-start context packet
// in the C1 contract shape, plus a Markdown rendering in a fenced JSON block.
// It rides the shared client seam so MCP and CLI emit the same canonical bytes
// (and both see the graph/memory-backed content when those services are wired).
func (s *Server) agentBriefCall(ctx context.Context, p callParams) (any, *rpcError) {
	b, md, err := s.stableClient().Brief(ctx, p.Arguments.Symbol)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	// Dual-format delivery: human-readable Markdown with fenced canonical JSON.
	text := string(md) + "\n\n```json\n" + string(b) + "\n```\n"
	return textResult([]byte(text)), nil
}

// explainSymbolCall (SW-115) returns a compact symbol-identity summary in the C1
// contract shape. It rides the shared client seam (engine/agenttools/explain
// behind it) so MCP and CLI emit the same canonical bytes.
func (s *Server) explainSymbolCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Symbol == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: symbol"}
	}
	b, err := s.stableClient().ExplainSymbol(ctx, p.Arguments.Symbol, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// relatedFilesCall (SW-116) returns a deterministically ranked read-first file
// list in the C1 contract shape.
func (s *Server) relatedFilesCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Target == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: target"}
	}
	b, err := s.stableClient().RelatedFiles(ctx, p.Arguments.Target, p.Arguments.Direction, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// changeRiskCall (SW-117) returns a change-risk evaluation in the C1 contract
// shape.
func (s *Server) changeRiskCall(ctx context.Context, p callParams) (any, *rpcError) {
	if p.Arguments.Target == "" && p.Arguments.Diff == "" {
		return nil, &rpcError{Code: -32602, Message: "missing required argument: target or diff"}
	}
	b, err := s.stableClient().ChangeRisk(ctx, p.Arguments.Target, p.Arguments.Diff, derefInt(p.Arguments.Limit))
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return textResult(b), nil
}

// deepAnalyzerTools maps dedicated EP-005 MCP tool names → their analysis
// dispatcher name so each tool name routes through analysisCall after injecting
// the correct analyzer. The map is package-level so both toolsCall routing and
// toolDescriptors advertising can share a single source of truth.
var deepAnalyzerTools = map[string]string{
	ToolAnalyzeTaint:       "taint",
	ToolAnalyzePDG:         "pdg",
	ToolAnalyzeInterproc:   "interproc",
	ToolAnalyzeContracts:   "contracts",
	ToolAnalyzeGitHistory:  "git-history",
	ToolAnalyzePrRisk:      "pr-risk",
	ToolAnalyzePrSignals:   "pr-signals",
	ToolAnalyzePrQuestions: "pr-questions",
}

// deepAnalyzerDescriptors defines the MCP tool schema for each EP-005 deep
// analyzer. Each entry is appended verbatim to the tools/list response when
// the analysis service is available.
var deepAnalyzerDescriptors = []map[string]any{
	{
		"name":        ToolAnalyzeTaint,
		"description": "flow-sensitive taint analysis: finds source-to-sink data-flow paths through the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "traversal direction: reverse (dependents/blast radius — the default) | forward (dependencies)"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzePDG,
		"description": "program dependence graph: computes data-dependence and control-dependence edges via reaching-definitions and post-dominance",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeInterproc,
		"description": "interprocedural analysis: Sharir-Pnueli fixpoint solver that computes procedure summaries over the call graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeContracts,
		"description": "contract drift detection: finds producer/consumer contracts and detects structural drift between linked API surfaces",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeGitHistory,
		"description": "git-history signal analysis: computes churn scores, bus-factor risks, and co-change groups from commit history",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzePrRisk,
		"description": "risk-scored PR diff (SW-039): maps changed nodes onto the graph and combines EP-004 impact with EP-005 taint signals into a deterministic, versioned per-region risk record. Local-first: diff is a unified-diff string or simple ref form; NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
	{
		"name":        ToolAnalyzePrSignals,
		"description": "hub/bridge/surprise graph signals on PR-changed code (SW-040): annotates each changed node with hub (high fan-in/out over a configurable threshold), bridge (articulation point / cut-vertex between modules), and surprise (rarely-modified or unexpectedly-coupled region) signals. Consumes EP-004 metrics + EP-005 PDG/git-history; never recomputes centrality. Local-first: diff is a unified-diff string or simple ref form; NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
	{
		"name":        ToolAnalyzePrQuestions,
		"description": "deterministic, no-LLM reviewer questions from graph findings on PR-changed code (SW-041): applies a fixed rule/template set to the consumed SW-039 risk scores and SW-040 hub/bridge/surprise signals to emit targeted reviewer questions. Each question carries a non-empty evidence reference to the triggering node/edge/signal; identical input yields byte-identical output. Consumes the two sibling reports; never recomputes scoring or signals. Local-first: diff is a unified-diff string or simple ref form; NO LLM, NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
}

// toolDescriptors returns the immutable catalog for the current repository
// binding and selected profile. The cache also gives tools/call the exact same
// allow-list as tools/list without executing client operations during discovery.
func (s *Server) toolDescriptors() []map[string]any {
	binding := s.bound.Load()
	if binding == nil {
		return nil
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if s.catalogBinding == binding && s.catalog != nil {
		return s.catalog
	}
	var tools []map[string]any
	if s.labs {
		tools = maximalToolDescriptors()
	} else {
		tools = stableToolDescriptors()
	}
	tools = filterSupportedToolDescriptors(binding.client, tools)
	s.catalogBinding = binding
	s.catalog = tools
	return tools
}

// filterSupportedToolDescriptors applies the bound client's optional,
// side-effect-free capability report after the Stable/Labs profile has built
// its normal catalog. This is a second, binding-specific boundary: profile
// membership says whether graphi promises/allows a tool, while capability
// reporting says whether this concrete transport can actually execute it.
// Clients without a reporter retain the historical catalog contract.
func filterSupportedToolDescriptors(c client.Client, tools []map[string]any) []map[string]any {
	reporter, ok := c.(client.CapabilityReporter)
	if !ok {
		return tools
	}
	filtered := make([]map[string]any, 0, len(tools))
	for _, descriptor := range tools {
		name, ok := descriptor["name"].(string)
		if !ok || name == "" || !reporter.SupportsCapability(name) {
			continue
		}
		filtered = append(filtered, descriptor)
	}
	return filtered
}

// toolAdvertised is the dispatch-side half of the profile boundary: a caller
// cannot invoke a tool omitted from this binding's tools/list response.
func (s *Server) toolAdvertised(name string) bool {
	for _, descriptor := range s.toolDescriptors() {
		if descriptor["name"] == name {
			return true
		}
	}
	return false
}

// stableToolDescriptors is deliberately static and side-effect free. The
// shipped Runtime wires every stable port, so its default profile is exactly
// StableOperations minus lifecycle-only index; partially wired bindings are
// narrowed later through CapabilityReporter.
func stableToolDescriptors() []map[string]any {
	tools := make([]map[string]any, 0, len(StableOperations)-1)
	for _, op := range query.Operations {
		if !IsStableMCPTool(op) {
			continue
		}
		props := map[string]any{
			"symbol": map[string]any{"type": "string", "description": "symbol (node) id to query"},
		}
		if op == query.OpNeighborhood {
			props["depth"] = map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("hop depth (clamped to MaxNeighborhoodDepth=%d)", query.MaxNeighborhoodDepth),
			}
		}
		tools = append(tools, map[string]any{
			"name":        op,
			"description": "structural query: " + op,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": []string{"symbol"}},
			"annotations": readOnlyToolAnnotations(),
		})
	}
	tools = append(tools,
		map[string]any{
			"name":        ToolSearch,
			"description": "lexical and symbol search over the indexed graph",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "search query (symbol token or free-text)"},
					"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
				},
				"required": []string{"symbol"},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		impactToolDescriptor(),
		map[string]any{
			"name":        ToolExplainSymbol,
			"description": "return a compact, cited symbol identity and immediate neighborhood",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "qualified id, file:line anchor, or bare name"},
					"limit":  map[string]any{"type": "integer", "description": "maximum returned items"},
				},
				"required": []string{"symbol"},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		map[string]any{
			"name":        ToolRelatedFiles,
			"description": "return a deterministically ranked read-first file list around an anchor",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target":    map[string]any{"type": "string", "description": "symbol id, file path, or diff anchor"},
					"direction": map[string]any{"type": "string", "description": "dependencies | dependents | both"},
					"limit":     map[string]any{"type": "integer", "description": "maximum returned files"},
				},
				"required": []string{"target"},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		map[string]any{
			"name":        ToolChangeRisk,
			"description": "return an evidence-based change-risk assessment for a target or diff",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{"type": "string", "description": "symbol id or file path"},
					"diff":   map[string]any{"type": "string", "description": "unified diff or line-oriented refs"},
					"limit":  map[string]any{"type": "integer", "description": "maximum returned items"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		map[string]any{
			"name":        ToolAgentBrief,
			"description": "return a bounded, cited task-start context packet",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "optional topic: symbol, path, or subsystem"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		},
	)
	return tools
}

func impactToolDescriptor() map[string]any {
	return map[string]any{
		"name":        ToolImpact,
		"description": "stable impact analysis: traverse forward dependencies or reverse dependents/blast radius",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "reverse (default blast radius) | forward (dependencies)"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	}
}

// maximalToolDescriptors builds the complete Stable+Labs descriptor registry
// without consulting the bound client. It must remain pure: tools/list is
// discovery, not permission to dial a daemon, auto-start a process, enumerate a
// forge, or execute an analyzer. toolDescriptors applies the optional, pure
// CapabilityReporter filter after this registry is complete. A third-party
// Client without that optional reporter retains the full Client-contract Labs
// catalog for backwards compatibility.
func maximalToolDescriptors() []map[string]any {
	tools := make([]map[string]any, 0, len(query.Operations)+2)
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
	tools = append(tools, map[string]any{
		"name":        ToolSearch,
		"description": "lexical and symbol search over the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "search query (symbol token or free-text)"},
				"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
			},
			"required": []string{"symbol"},
		},
	})
	// Optional semantic search (SW-059). Advertised whenever the search tool is —
	// it is always callable through the client and cleanly reports "unavailable"
	// (typed graceful-skip) when no embedder is configured.
	tools = append(tools, map[string]any{
		"name":        ToolSearchSemantic,
		"description": "optional semantic (embedding) search over the indexed graph; reports 'unavailable' cleanly when no embedder is configured (OFF by default)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "semantic search query (free-text)"},
				"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
			},
			"required": []string{"symbol"},
		},
	})
	// SW-085 pattern-query tools. They ride the in-process query.Service and reuse
	// the canonical engine serializers; CapabilityReporter independently filters
	// them from bindings without that service. Per AC4 they carry the explicit
	// annotation set: read-only, idempotent, non-destructive, closed-world.
	tools = append(tools, map[string]any{
		"name":        ToolSearchAST,
		"description": "structural AST pattern search (SW-082): match nodes by kind/name/parent_kind; returns node identity + parent context only, never a file body",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "JSON AstPattern, e.g. {\"kind\":\"function\",\"name\":{\"regex\":\"^handle_\"}}"},
				"limit":   map[string]any{"type": "integer", "description": "maximum number of matches (applied after the canonical sort)"},
			},
			"required": []string{"pattern"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolFindClones,
		"description": "clone-group detection (SW-083): reports exact/renamed/structural clone groups derived from the AST edge sets; deterministic and bounded by max_groups",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"config": map[string]any{"type": "string", "description": "optional JSON CloneConfig (threshold, max_groups, clone_kinds, min_edges); empty uses engine defaults"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// Savings readout (SW-020). Binding-specific availability is filtered later.
	tools = append(tools, map[string]any{
		"name":        ToolSavings,
		"description": "token-savings ledger readout: per-call / per-session / cumulative USD with anti-gaming cap flags",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	})
	// Analyzers (SW-022). Binding-specific availability is filtered later.
	tools = append(tools, impactToolDescriptor(), map[string]any{
		"name":        ToolAnalyze,
		"description": "run a named graph analyzer (e.g. impact forward/reverse blast-radius reachability) over the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"analyzer":  map[string]any{"type": "string", "description": "analyzer name (e.g. impact)"},
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "traversal direction for directional analyzers: reverse (dependents/blast radius — the default) | forward (dependencies)"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"analyzer", "symbol"},
		},
	})
	// EP-005 (SW-033): include one dedicated tool per deep analyzer.
	tools = append(tools, deepAnalyzerDescriptors...)
	// SW-038 edit/refactor command surface.
	tools = append(tools, editToolDescriptors...)
	// SW-042 sticky PR-comment + merge-gate surface.
	tools = append(tools, map[string]any{
		"name":        ToolPrComment,
		"description": "render the assembled PR-review findings (risk + hub/bridge/surprise signals + reviewer questions) into one sticky Markdown comment and evaluate the optional risk-threshold merge gate; offline dry-run by default",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":           map[string]any{"type": "string", "description": "local-first unified-diff or simple ref string (required)"},
				"pr":             map[string]any{"type": "string", "description": "PR reference rendered in the comment header (e.g. owner/repo#42)"},
				"provenance":     map[string]any{"type": "string", "description": "evidence redaction level: summary (default; safe for public comments) | full"},
				"gate_enabled":   map[string]any{"type": "boolean", "description": "enable the optional risk-threshold merge gate"},
				"gate_threshold": map[string]any{"type": "integer", "description": "risk threshold in fixed-point units (1/1000) the worst region must EXCEED to BLOCK (default 700)"},
				"publish":        map[string]any{"type": "boolean", "description": "upsert the sticky comment through the host (default false: offline dry-run, render+gate only)"},
			},
			"required": []string{"diff"},
		},
	})
	// EP-011 G1 compound query (singleton descriptor; input is query text).
	tools = append(tools, map[string]any{
		"name":        ToolCompound,
		"description": "compound / Cypher-style graph query composing traversals, filters, and projections in one request (SEED/HOP/WHERE/MAXDEPTH text form)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "compound query text: SEED <id> then HOP <in|out|both> [<kind>] lines, optional WHERE KIND <kind>"},
			},
			"required": []string{"query"},
		},
	})
	// EP-012 agent memory & skills. Binding-specific availability is filtered later.
	tools = append(tools, map[string]any{
		"name":        ToolMemory,
		"description": "scoped agent memory: store, recall, forget, list, or export notes in scopes and notebooks with provenance",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op":             map[string]any{"type": "string", "description": "operation: store | recall | forget | list | export"},
				"scope":          map[string]any{"type": "string", "description": "memory scope"},
				"notebook":       map[string]any{"type": "string", "description": "memory notebook"},
				"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "tags for store"},
				"payload":        map[string]any{"type": "string", "description": "payload for store"},
				"mem_id":         map[string]any{"type": "string", "description": "entry id for forget or overwrite"},
				"kind":           map[string]any{"type": "string", "description": "entry kind for store: architecture | command | convention | decision | risk | dependency | workflow"},
				"source":         map[string]any{"type": "string", "description": "provenance source for store"},
				"confidence":     map[string]any{"type": "string", "description": "confirmed | derived | heuristic"},
				"evidence":       map[string]any{"type": "string", "description": "optional file:line citation"},
				"limit":          map[string]any{"type": "integer", "description": "max entries for list"},
				"export_to_path": map[string]any{"type": "string", "description": "REJECTED (SAFE-01): the transport never writes server-side files; export returns the payload in the response's `export` field — omit this argument"},
			},
			"required": []string{"op"},
		},
	})
	tools = append(tools, map[string]any{
		"name":        ToolDistill,
		"description": "deterministic, non-LLM session distillation: compress a session trace into a reusable artifact",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id":      map[string]any{"type": "string", "description": "session identifier"},
				"decisions":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"risks":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"open_questions":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"file_references": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"session_id"},
		},
	})
	tools = append(tools, map[string]any{
		"name":        ToolSkillGen,
		"description": "deterministic, non-LLM skill generation: turn a repeatable procedure into a Markdown skill artifact",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "description": "skill name"},
				"trigger":     map[string]any{"type": "string", "description": "skill trigger phrase"},
				"description": map[string]any{"type": "string", "description": "skill description"},
			},
			"required": []string{"name", "trigger"},
		},
	})
	// EP-018 multi-PR triage suite (SW-105).
	tools = append(tools, map[string]any{
		"name":        ToolListPRs,
		"description": "list open pull requests of the configured repo with read-only forge metadata (number, title, author, base/head refs, head SHA, changed files, additions/deletions, mergeable). Discovery/metadata ONLY — no graph scoring, no comment posting. The forge enumeration is the suite's only outbound path.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	})
	tools = append(tools, map[string]any{
		"name":        ToolTriagePRs,
		"description": "single-pass graph-derived PR triage: enumerate open PRs, then rank them by blast radius, touched high-centrality nodes, ownership concentration, churn, and test-coverage-of-touched-code, folded into a fixed-integer composite. Deterministic total order (composite DESC, PR number ASC). Scoring is a zero-egress pass over the local graph; the forge is touched only for enumeration.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolConflictsPRs,
		"description": "inter-PR conflict detection: enumerate open PRs, then report which PR PAIRS collide over the local graph — textual overlap (overlapping changed line ranges in the same file), shared file/symbol/high-centrality node, and the asymmetric contract-dependency case (one PR mutates a contract that another PR's changed entities depend on via graph edges, flagged even with NO textual overlap). Deterministic pairwise report (pairs by ascending PR number, canonical within-pair entity order). Detection is a zero-egress pass over the local graph; the forge is touched only for enumeration.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-018 SW-107: suggest_reviewers.
	tools = append(tools, map[string]any{
		"name":        ToolSuggestReviewers,
		"description": "suggest reviewers for a change: resolve the touched symbol/file set from a local-first PR diff (or line-oriented refs), then rank candidate reviewers from graph ownership + recency-decayed churn over the touched files plus affected-subgraph proximity (callers/callees/contract neighbors) of the touched symbols. Each candidate carries a transparent per-signal breakdown (ownership/recency-decayed-churn/subgraph-proximity) with honest file-vs-symbol granularity labels. Deterministic total order (composite DESC, reviewer identity ASC). Zero-egress pass over the local graph + git history.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff": map[string]any{"type": "string", "description": "unified diff or line-oriented refs (path:name | path#Lline | node id) of the change"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-018 SW-107: compare_branches.
	tools = append(tools, map[string]any{
		"name":        ToolCompareBranches,
		"description": "compare two branches at the GRAPH level: given two branch refs (states materialized above the surface boundary), report the structured diff of entities/symbols/contracts added/removed/changed plus edges added/removed and entities moved across files — keyed by stable canonical graph identity (NodeId), not line ranges. Detects signature/contract changes (a contract node whose dependency surface changed) and correlates moves by path-independent symbol identity. Deterministic per-group order. Zero-egress pure local set-diff; the engine never resolves a git ref.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"base": map[string]any{"type": "string", "description": "base branch ref"},
				"head": map[string]any{"type": "string", "description": "head branch ref"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-018 SW-108 (capstone): critique_review.
	tools = append(tools, map[string]any{
		"name":        ToolCritiqueReview,
		"description": "critique an EXISTING PR review against the knowledge graph: replay the single-PR risk/blast-radius/centrality/taint signals as a ground-truth oracle over the PR's touched set, then emit a structured, graph-evidence-grounded critique with three item types — gap (a high-risk touched entity the review never mentioned: blast-radius count + centrality + contributing edge kinds + taint provenance), over_flag (a review-flagged entity the graph shows is a low-centrality leaf below the risk threshold), and unsupported_claim (a review comment asserting impact to an anchorable target with NO connecting graph edge). Comment→entity matching is DETERMINISTIC anchoring (file:line/symbol → NodeId); unanchorable comments/claims are counted in an honest unanchored tally, never guessed. NO LLM prose. Deterministic total order (type → entity NodeId → review-anchor). The review is fetched at the surface boundary (or supplied inline); the critique itself is a zero-egress pass over the local graph.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pr_number": map[string]any{"type": "integer", "description": "PR number to fetch the existing review for (when no inline review is supplied)"},
				"diff":      map[string]any{"type": "string", "description": "the PR's touched set: unified diff or line-oriented refs (path:name | path#Lline | node id)"},
				"review":    map[string]any{"type": "string", "description": "inline existing-review JSON ({verdict, comments:[{id,path,line,symbol,claim_targets}]}); takes precedence over the surface fetch"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-020 agent-first task tools (SW-115 / SW-116 / SW-117) plus EP-024 (SW-134). Advertised
	// unconditionally: they require only the engine/agenttools packages, not a
	// separate service. Each descriptor uses the hardened six-facet
	// template (purpose, when-to-use, when-not-to-use, input shape, read-only,
	// partial-possible) and carries explicit read-only annotations.
	tools = append(tools, map[string]any{
		"name":        ToolExplainSymbol,
		"description": "explain_symbol: return a compact, cited symbol-identity summary (qualified name, kind, declaring file:line, direct callers/callees). Purpose: answer 'what is this symbol?' in one call. When to use: the agent has a symbol reference and needs identity + immediate neighborhood without reading source. When NOT to use: for broad 'what should I read first?' questions (use related_files) or risk scoring (use change_risk). Input shape: a single symbol reference (qualified id, file:line, or bare name). Read-only: true. Partial results possible: neighbor lists may truncate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "symbol reference: qualified id, file:line anchor, or bare name"},
			},
			"required": []string{"symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolRelatedFiles,
		"description": "related_files: return a deterministically ranked 'read these first' file list for a symbol, file, or diff anchor. Purpose: answer 'what should I read first?' in one call. When to use: the agent needs a scoped, evidence-backed file list before editing or reviewing. When NOT to use: for a single symbol's identity (use explain_symbol) or for risk scoring (use change_risk). Input shape: a single anchor plus optional direction (dependencies | dependents | both). Read-only: true. Partial results possible: ranked file list may truncate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "description": "anchor: symbol id, file path, or diff line-oriented refs"},
				"direction": map[string]any{"type": "string", "description": "dependencies | dependents | both (default)"},
			},
			"required": []string{"target"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolChangeRisk,
		"description": "change_risk: return an evidence-based low/medium/high/unknown risk assessment for a symbol, file, or diff target. Purpose: answer 'how risky is it to touch this?' in one call. When to use: before proposing or reviewing a change, to gauge blast radius and coverage. When NOT to use: when you only need a file list (use related_files) or a symbol summary (use explain_symbol). Input shape: a target symbol/file or a local-first diff. Read-only: true. Partial results possible: evidence may be truncated, and the tool returns unknown rather than guessing.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "symbol id or file path to evaluate"},
				"diff":   map[string]any{"type": "string", "description": "local-first unified diff or line-oriented refs (alternative to target)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-024 agent_brief: bounded task-start context packet.
	tools = append(tools, map[string]any{
		"name":        ToolAgentBrief,
		"description": "agent_brief: return a bounded, cited task-start context packet (project identity, start-here files, key symbols, known facts, hotspots, suggested next MCP calls) in Markdown with embedded canonical JSON. Purpose: give an agent a scoped, cited starting context without reading source blindly. When to use: at the beginning of a task or when entering a new subsystem. When NOT to use: when you already have a specific symbol to explain (use explain_symbol) or a file list to read (use related_files). Input shape: optional topic (symbol, path, or subsystem). Read-only: true. Partial results possible: sections may be empty if underlying analyzers are not yet wired.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "optional topic: symbol id, file path, or subsystem name"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// Central stability-tier marking (single source: StableOperations in
	// tools.go) — every advertised tool outside the frozen 12-op stable set is
	// prefixed [labs]; descriptor literals never carry the tag by hand.
	return markLabs(tools)
}

// editToolDescriptors defines the MCP tool schema for the SW-038 edit/refactor
// command surface (refactor-preview, refactor, undo). Each routes through the
// shared client; the surface holds no engine logic.
var editToolDescriptors = []map[string]any{
	{
		"name":        ToolRefactorPreview,
		"description": "preview a graph-aware refactor: resolve the target via the query layer and return the EP-004 impact set (blast radius + planned edits) WITHOUT mutating",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":             map[string]any{"type": "string", "description": "refactor kind: rename|signature_change (extract|move are NOT implemented and fail closed with a typed error before any read or write — SAFE-01)"},
				"target_symbol":    map[string]any{"type": "string", "description": "resolved node id of the symbol to refactor"},
				"old_name":         map[string]any{"type": "string", "description": "current spelling of the symbol"},
				"new_name":         map[string]any{"type": "string", "description": "replacement spelling"},
				"destination_file": map[string]any{"type": "string", "description": "destination file (move only)"},
			},
			"required": []string{"kind", "target_symbol", "old_name", "new_name"},
		},
	},
	{
		"name":        ToolRefactor,
		"description": "apply a graph-aware refactor through the shared atomic edit saga and return an auditable change record (operation, target, before/after, actor, timestamp, undo token)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":             map[string]any{"type": "string", "description": "refactor kind: rename|signature_change (extract|move are NOT implemented and fail closed with a typed error before any read or write — SAFE-01)"},
				"target_symbol":    map[string]any{"type": "string", "description": "resolved node id of the symbol to refactor"},
				"old_name":         map[string]any{"type": "string", "description": "current spelling of the symbol"},
				"new_name":         map[string]any{"type": "string", "description": "replacement spelling"},
				"destination_file": map[string]any{"type": "string", "description": "destination file (move only)"},
				"actor":            map[string]any{"type": "string", "description": "request identity recorded on the change record (default \"mcp\")"},
			},
			"required": []string{"kind", "target_symbol", "old_name", "new_name"},
		},
	},
	{
		"name":        ToolUndo,
		"description": "reverse a previously applied edit by its undo token, restoring the prior graph + source and recording the reversal as its own auditable change record",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"undo_token": map[string]any{"type": "string", "description": "the undo token returned by a prior refactor"},
				"actor":      map[string]any{"type": "string", "description": "request identity recorded on the reversal record (default \"mcp\")"},
			},
			"required": []string{"undo_token"},
		},
	},
}
