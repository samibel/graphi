package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/samibel/graphi/surfaces/client"
)

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
	// A bidirectional (stdio) transport binds with the bounded grace wait — a
	// cold full index must never stall initialize into the client's startup
	// timeout. HTTP has no later exchange to surface a deferred bind error on,
	// so it keeps the synchronous wait.
	if supplied {
		return s.bind(ctx, roots, !supportsServerRequests)
	}
	if rootsCapable {
		return nil // requestRootsIfNeeded runs after notifications/initialized
	}
	return s.bind(ctx, nil, !supportsServerRequests)
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

// startBind launches the binder on its own goroutine and returns a channel
// closed when that attempt completes. The binder may run a cold full index
// for minutes; nothing it does may block the protocol loop, and nothing the
// protocol loop does may be required for it to finish (so waiting on the
// returned channel can never deadlock). A second call while an attempt is in
// flight joins that attempt instead of stacking another ingest.
func (s *Server) startBind(parent context.Context, roots []string) (<-chan struct{}, *rpcError) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, &rpcError{Code: -32002, Message: "MCP session is closed"}
	}
	if s.binder == nil {
		s.mu.Unlock()
		if s.bound.Load() != nil {
			return nil, nil
		}
		return nil, &rpcError{Code: -32002, Message: "no repository binder configured"}
	}
	if s.bindInFlight {
		done := s.bindDone
		s.mu.Unlock()
		return done, nil
	}
	s.bindInFlight = true
	s.bindErr = nil
	s.awaitingRoots = false
	gen := s.bindGen
	ctx, cancel := context.WithCancel(parent)
	s.bindCancel = cancel
	done := make(chan struct{})
	s.bindDone = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		defer cancel()
		s.runBind(ctx, gen, roots)
	}()
	return done, nil
}

// runBind executes one binding attempt and publishes its outcome — unless the
// session was closed or the roots changed while the binder ran (gen mismatch),
// in which case the late Binding is closed and discarded, never stored.
func (s *Server) runBind(ctx context.Context, gen uint64, roots []string) {
	binding, err := s.binder(ctx, roots)
	if err == nil && binding.Client == nil {
		err = errors.New("repository binder returned no client")
	}
	if binding.Close == nil {
		binding.Close = func() {}
	}

	s.dispatch.Lock()
	s.mu.Lock()
	if s.closed || gen != s.bindGen {
		s.mu.Unlock()
		s.dispatch.Unlock()
		if err == nil {
			binding.Close()
		}
		return
	}
	s.bindInFlight = false
	s.bindCancel = nil
	oldCleanup := s.cleanup
	if err != nil {
		s.cleanup = nil
		s.bindErr = err
		s.bound.Store(nil)
	} else {
		s.cleanup = binding.Close
		s.bindErr = nil
		s.bound.Store(&boundClient{client: binding.Client, stable: client.AsStable(binding.Client)})
	}
	s.mu.Unlock()
	s.dispatch.Unlock()
	if oldCleanup != nil {
		oldCleanup()
	}
}

// bind brings the session toward a bound repository without letting a slow
// binder stall the protocol. It waits up to the server's bind grace: a warm
// store finishes well inside it and keeps the historical synchronous contract
// (including a binding failure reported directly on this request), while a
// cold full index keeps preparing in the background — tool calls fail closed
// with a retryable "still indexing" message until it lands. sync forces an
// unbounded wait for transports without a bidirectional stream (HTTP), whose
// sessions have no later request to pick the error up from.
func (s *Server) bind(ctx context.Context, roots []string, sync bool) *rpcError {
	done, rerr := s.startBind(ctx, roots)
	if rerr != nil || done == nil {
		return rerr
	}
	if !sync {
		grace := s.bindGrace
		if grace <= 0 {
			return nil
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			return nil // still indexing; the session answers without stalling
		case <-ctx.Done():
			return nil
		}
	} else {
		select {
		case <-done:
		case <-ctx.Done():
			return &rpcError{Code: -32002, Message: "repository binding cancelled: " + ctx.Err().Error()}
		}
	}
	if s.bound.Load() != nil {
		return nil
	}
	s.mu.Lock()
	err := s.bindErr
	s.mu.Unlock()
	if err == nil {
		err = errors.New("repository binding did not complete")
	}
	return &rpcError{Code: -32002, Message: "repository binding failed: " + err.Error()}
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
	if s.closed || !s.rootsCapable || s.awaitingRoots || s.bindInFlight || s.bound.Load() != nil {
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
	// Invalidate any in-flight background binding: bump the generation so its
	// late result is discarded by runBind, and cancel it so a running ingest
	// stops instead of burning CPU for a session that must restart anyway.
	s.bindGen++
	cancel := s.bindCancel
	s.bindCancel = nil
	s.bindInFlight = false
	s.bindErr = errors.New("client repository roots changed; restart the MCP session")
	s.bound.Store(nil)
	s.mu.Unlock()
	s.dispatch.Unlock()
	if cancel != nil {
		cancel()
	}
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
	_ = s.bind(ctx, paths, false) // bind records the error for a later client request
}
