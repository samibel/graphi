package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// DaemonClient connects to a graphi daemon over a Unix domain socket,
// auto-starting the daemon if it is not already listening.
type DaemonClient struct {
	socketPath string
	binaryPath string
}

// NewClient constructs a daemon client for socketPath. binaryPath is the
// graphi binary used for auto-start; empty means os.Args[0].
func NewClient(socketPath, binaryPath string) *DaemonClient {
	if binaryPath == "" {
		binaryPath = os.Args[0]
	}
	return &DaemonClient{socketPath: socketPath, binaryPath: binaryPath}
}

// Ensure DaemonClient satisfies the surfaces/client.Client contract.
var _ client.Client = (*DaemonClient)(nil)

// connect returns a net.Conn to the daemon, auto-starting it if necessary.
func (c *DaemonClient) connect(ctx context.Context) (net.Conn, error) {
	conn, err := c.dial()
	if err == nil {
		return conn, nil
	}
	// Auto-start: try to spawn the daemon and wait for the socket.
	if err := c.startDaemon(ctx); err != nil {
		return nil, fmt.Errorf("daemon: auto-start failed: %w", err)
	}
	return c.dialWithRetry(ctx, 20, 100*time.Millisecond)
}

func (c *DaemonClient) dial() (net.Conn, error) {
	return net.Dial("unix", c.socketPath)
}

func (c *DaemonClient) dialWithRetry(ctx context.Context, attempts int, delay time.Duration) (net.Conn, error) {
	for i := 0; i < attempts; i++ {
		conn, err := c.dial()
		if err == nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("daemon: socket %s did not appear after auto-start", c.socketPath)
}

func (c *DaemonClient) startDaemon(ctx context.Context) error {
	if c.binaryPath == "" {
		return fmt.Errorf("no daemon binary configured")
	}
	cmd := exec.CommandContext(ctx, c.binaryPath, "daemon", "start", "-socket", c.socketPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach; the daemon daemonizes itself by running until Stop.
	return nil
}

// request sends a method call to the daemon and returns the raw body bytes.
func (c *DaemonClient) request(ctx context.Context, method string, params any) ([]byte, error) {
	conn, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(request{Method: method, Params: mustJSON(params)}); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return nil, fmt.Errorf("daemon: no response")
	}
	var resp response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("daemon: decode response: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon: %s", resp.Error)
	}
	return resp.Body, nil
}

// Query implements client.Client.
func (c *DaemonClient) Query(ctx context.Context, op, symbol string, depth int) ([]byte, error) {
	return c.request(ctx, "query", queryParams{Op: op, Symbol: symbol, Depth: depth})
}

// Search implements client.Client.
func (c *DaemonClient) Search(ctx context.Context, q string, limit int) ([]byte, error) {
	return c.request(ctx, "search", searchParams{Query: q, Limit: limit})
}

// SemanticSearch implements client.Client. The daemon semantic RPC is not yet
// wired; until it is, the daemon client returns the canonical typed Unavailable
// graceful-skip response (Available=false) — NOT an error — so the unconfigured
// bytes stay byte-identical to the in-process and HTTP surfaces (SW-059 parity).
func (c *DaemonClient) SemanticSearch(ctx context.Context, q string, limit int) ([]byte, error) {
	_, _ = ctx, limit
	return search.MarshalSemantic(search.SemanticResponse{
		Query:     q,
		Available: false,
		Reason:    search.UnavailableReason,
		Hits:      []search.SemanticHit{},
	})
}

// Savings implements client.Client. It forwards a savings readout request to
// the daemon.
func (c *DaemonClient) Savings(ctx context.Context) ([]byte, error) {
	return c.request(ctx, "savings", nil)
}

// Analyze implements client.Client. The daemon analysis RPC is not yet wired
// (SW-022 ships the in-process analysis path that both MCP stdio and CLI direct
// mode use); until it is added, the daemon client reports the capability as
// unavailable rather than fabricating a result. Query/search/savings are
// unaffected.
func (c *DaemonClient) Analyze(ctx context.Context, p client.AnalyzeParams) ([]byte, error) {
	_ = ctx
	_ = p
	return nil, client.ErrAnalysisUnavailable
}

// RefactorPreview implements client.Client. The daemon edit RPC is not yet wired
// (SW-038 ships the in-process edit path that MCP stdio and CLI direct mode use);
// until it is added, the daemon client reports the capability as unavailable
// rather than fabricating a mutation. Query/search/savings/analysis are unaffected.
func (c *DaemonClient) RefactorPreview(ctx context.Context, req client.RefactorRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrEditUnavailable
}

// Refactor implements client.Client. Not yet wired over the daemon RPC; see
// RefactorPreview.
func (c *DaemonClient) Refactor(ctx context.Context, req client.RefactorRequest, actor string) ([]byte, error) {
	_, _, _ = ctx, req, actor
	return nil, client.ErrEditUnavailable
}

// Undo implements client.Client. Not yet wired over the daemon RPC; see
// RefactorPreview.
func (c *DaemonClient) Undo(ctx context.Context, undoToken, actor string) ([]byte, error) {
	_, _, _ = ctx, undoToken, actor
	return nil, client.ErrEditUnavailable
}

// PrComment implements client.Client. The daemon review RPC is not yet wired
// (SW-042 ships the in-process review path that MCP stdio and CLI direct mode
// use; SW-043 wires the real host + GitHub Action); until it is added, the
// daemon client reports the capability as unavailable rather than fabricating a
// publish. Query/search/savings/analysis/edit are unaffected.
func (c *DaemonClient) PrComment(ctx context.Context, req client.PrCommentRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrReviewUnavailable
}

// SocketPath returns the configured socket path.
func (c *DaemonClient) SocketPath() string { return c.socketPath }

// DefaultSocketPath returns a project-local default socket path in the user's
// home directory. Production code may override this.
func DefaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".graphi.sock")
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
