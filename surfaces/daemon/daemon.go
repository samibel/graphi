// Package daemon implements graphi's local-first hot-index daemon.
//
// Layering: daemon is a surface package. It owns lifecycle, Unix domain socket
// transport, and request routing. Query/search logic is delegated to the shared
// engine services via the surfaces/client.Client contract.
//
// Security: the daemon binds only to a Unix domain socket with owner-only
// permissions (0600). It refuses TCP listeners and performs no outbound network
// activity.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/samibel/graphi/surfaces/client"
)

// Handler is the service side of Client. It matches the Client contract but is
// implemented by the daemon server using in-process services.
type Handler interface {
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	Search(ctx context.Context, q string, limit int) ([]byte, error)
	// Compound runs a compound / Cypher-style graph query (EP-011 G1).
	Compound(ctx context.Context, queryText string) ([]byte, error)
	// SearchAST runs the structural AST pattern query (SW-082 / SW-085).
	SearchAST(ctx context.Context, patternJSON string, limit int) ([]byte, error)
	// FindClones runs the clone-detection query (SW-083 / SW-085).
	FindClones(ctx context.Context, configJSON string) ([]byte, error)
	Savings(ctx context.Context) ([]byte, error)
	// Memory runs an EP-012 memory operation.
	Memory(ctx context.Context, req client.MemoryRequest) ([]byte, error)
	// Distill runs EP-012 session distillation.
	Distill(ctx context.Context, req client.DistillRequest) ([]byte, error)
	// SkillGen runs EP-012 skill generation.
	SkillGen(ctx context.Context, req client.SkillGenRequest) ([]byte, error)
}

// request is the JSON envelope sent over the Unix socket.
type request struct {
	Method string          `json:"method"` // "query" or "search"
	Params json.RawMessage `json:"params"`
}

type queryParams struct {
	Op     string `json:"op"`
	Symbol string `json:"symbol"`
	Depth  int    `json:"depth"`
}

type searchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// compoundParams carries a compound / Cypher-style graph query (EP-011 G1).
type compoundParams struct {
	Query string `json:"query"`
}

// searchASTParams carries a structural AST pattern query (SW-082 / SW-085). The
// pattern is the JSON AstPattern text; limit bounds the result set.
type searchASTParams struct {
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
}

// findClonesParams carries a clone-detection query (SW-083 / SW-085). The config
// is the JSON CloneConfig text (empty ⇒ engine defaults).
type findClonesParams struct {
	Config string `json:"config"`
}

type memoryParams client.MemoryRequest
type distillParams client.DistillRequest
type skillgenParams client.SkillGenRequest

// SW-096 control-plane params.
type trackParams struct {
	Root string `json:"root"`
}
type untrackParams struct {
	ID string `json:"id"`
}

// proxyParams forwards an inner surface request to the warm daemon-resident
// engine; the response body is byte-identical to what a cold surface would emit.
type proxyParams struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// response is the JSON envelope returned over the Unix socket.
type response struct {
	OK    bool   `json:"ok"`
	Body  []byte `json:"body,omitempty"`
	Error string `json:"error,omitempty"`
}

// Server is the hot-index daemon.
type Server struct {
	handler    Handler
	listener   net.Listener
	socketPath string
	ctl        *control // SW-096 control plane (track/untrack/reload/status)

	mu     sync.Mutex
	closed bool
}

// NewServer constructs a daemon server bound to the given handler.
func NewServer(handler Handler) *Server {
	return &Server{handler: handler, ctl: newControl(nil)}
}

// Start validates the socket path, creates a Unix listener with owner-only
// permissions, and begins serving requests.
func (s *Server) Start(socketPath string) error {
	socketPath = filepath.Clean(socketPath)

	if err := validateSocketPath(socketPath); err != nil {
		return err
	}
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		return fmt.Errorf("daemon: chmod socket: %w", err)
	}

	s.mu.Lock()
	s.listener = ln
	s.socketPath = socketPath
	s.mu.Unlock()

	go s.serve()
	return nil
}

// Stop shuts down the listener and removes the socket file.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return err
		}
	}
	if s.socketPath != "" {
		_ = os.Remove(s.socketPath)
	}
	return nil
}

// Addr returns the daemon's socket path.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.socketPath
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	ctx := context.Background()
	scanner := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(response{OK: false, Error: "invalid request"})
			continue
		}
		resp := s.dispatch(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return
		}
		// `daemon stop`: the ack has been written; now drain by tearing down the
		// listener + socket so a subsequent status reports not-running.
		if req.Method == "stop" {
			_ = s.Stop()
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req request) response {
	switch req.Method {
	case "query":
		var p queryParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid query params"}
		}
		b, err := s.handler.Query(ctx, p.Op, p.Symbol, p.Depth)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "search":
		var p searchParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid search params"}
		}
		b, err := s.handler.Search(ctx, p.Query, p.Limit)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "compound":
		var p compoundParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid compound params"}
		}
		b, err := s.handler.Compound(ctx, p.Query)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "search_ast":
		var p searchASTParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid search_ast params"}
		}
		b, err := s.handler.SearchAST(ctx, p.Pattern, p.Limit)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "find_clones":
		var p findClonesParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid find_clones params"}
		}
		b, err := s.handler.FindClones(ctx, p.Config)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "savings":
		b, err := s.handler.Savings(ctx)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "memory":
		var p memoryParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid memory params"}
		}
		b, err := s.handler.Memory(ctx, client.MemoryRequest(p))
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "distill":
		var p distillParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid distill params"}
		}
		b, err := s.handler.Distill(ctx, client.DistillRequest(p))
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "skillgen":
		var p skillgenParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid skillgen params"}
		}
		b, err := s.handler.SkillGen(ctx, client.SkillGenRequest(p))
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		return response{OK: true, Body: b}
	case "track":
		var p trackParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid track params"}
		}
		id, err := s.ctl.track(p.Root)
		if err != nil {
			return response{OK: false, Error: err.Error()}
		}
		b, _ := json.Marshal(map[string]string{"id": id})
		return response{OK: true, Body: b}
	case "untrack":
		var p untrackParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid untrack params"}
		}
		s.ctl.untrack(p.ID)
		return response{OK: true}
	case "reload":
		s.ctl.reload()
		b, _ := json.Marshal(s.ctl.status(s.Addr()))
		return response{OK: true, Body: b}
	case "status":
		b, _ := json.Marshal(s.ctl.status(s.Addr()))
		return response{OK: true, Body: b}
	case "proxy":
		var p proxyParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return response{OK: false, Error: "invalid proxy params"}
		}
		if p.Method == "proxy" {
			return response{OK: false, Error: "proxy cannot wrap proxy"}
		}
		// Forward to the warm engine via the same dispatch path → byte-identical
		// body to a cold surface invocation.
		return s.dispatch(ctx, request{Method: p.Method, Params: p.Params})
	case "stop":
		// The actual listener teardown happens in handleConn AFTER this response
		// is written, so the caller gets a clean ack before the socket closes.
		return response{OK: true}
	default:
		return response{OK: false, Error: "unknown method: " + req.Method}
	}
}

// validateSocketPath rejects paths outside the parent directory, symlinks, and
// unsafe parent directory permissions.
func validateSocketPath(socketPath string) error {
	abs, err := filepath.Abs(socketPath)
	if err != nil {
		return fmt.Errorf("daemon: abs socket path: %w", err)
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return errors.New("daemon: socket path cannot be a root directory")
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("daemon: stat socket parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("daemon: socket parent is a symlink")
	}
	// Reject world-writable parent directories.
	if info.Mode().Perm()&0o002 != 0 {
		return errors.New("daemon: socket parent directory is world-writable")
	}
	// Reject if socket path itself is a symlink.
	if fi, err := os.Lstat(abs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("daemon: socket path is a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("daemon: stat socket path: %w", err)
	}
	return nil
}
