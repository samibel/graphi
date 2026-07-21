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
