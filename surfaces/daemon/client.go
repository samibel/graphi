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

// Compound implements client.Client (EP-011 G1).
func (c *DaemonClient) Compound(ctx context.Context, queryText string) ([]byte, error) {
	return c.request(ctx, "compound", compoundParams{Query: queryText})
}

// Search implements client.Client.
func (c *DaemonClient) Search(ctx context.Context, q string, limit int) ([]byte, error) {
	return c.request(ctx, "search", searchParams{Query: q, Limit: limit})
}

// SearchAST implements client.Client (SW-085). It forwards the JSON AstPattern to
// the daemon's search_ast RPC; the daemon-side handler is the same Direct client,
// so the returned bytes are the canonical query.Marshal output — byte-identical to
// the in-process and HTTP surfaces.
func (c *DaemonClient) SearchAST(ctx context.Context, patternJSON string, limit int) ([]byte, error) {
	return c.request(ctx, "search_ast", searchASTParams{Pattern: patternJSON, Limit: limit})
}

// FindClones implements client.Client (SW-085). It forwards the JSON CloneConfig
// to the daemon's find_clones RPC; the daemon-side handler returns the canonical
// query.MarshalCloneResult bytes for byte-identical parity.
func (c *DaemonClient) FindClones(ctx context.Context, configJSON string) ([]byte, error) {
	return c.request(ctx, "find_clones", findClonesParams{Config: configJSON})
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

// Stop sends a shutdown request to a running daemon and waits for its ack.
// Unlike request, it dials directly instead of going through connect's
// auto-start path: a daemon that isn't listening should report "not running",
// not be spun up just to be killed immediately.
func (c *DaemonClient) Stop(ctx context.Context) error {
	conn, err := c.dial()
	if err != nil {
		return fmt.Errorf("daemon not running at %s", c.socketPath)
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	enc := json.NewEncoder(conn)
	if err := enc.Encode(request{Method: "stop", Params: mustJSON(nil)}); err != nil {
		return fmt.Errorf("daemon: send stop: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("daemon: no response")
	}
	var resp response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("daemon: decode response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon: %s", resp.Error)
	}
	return nil
}

// Analyze implements client.Client. SW-104 wires the daemon analysis RPC: it
// forwards the transport-agnostic AnalyzeParams to the daemon's "analyze" method,
// whose handler is the same in-process Direct client used by the cold surfaces,
// so the returned bytes are the canonical analysis.Marshal output —
// byte-identical to the CLI/MCP/HTTP/SSE surfaces (parity by construction).
func (c *DaemonClient) Analyze(ctx context.Context, p client.AnalyzeParams) ([]byte, error) {
	return c.request(ctx, "analyze", analyzeParams(p))
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

// Memory implements client.Client. The daemon memory RPC is not yet wired;
// returns ErrMemoryUnavailable.
func (c *DaemonClient) Memory(ctx context.Context, req client.MemoryRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrMemoryUnavailable
}

// Distill implements client.Client. The daemon distill RPC is not yet wired;
// returns ErrDistillUnavailable.
func (c *DaemonClient) Distill(ctx context.Context, req client.DistillRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrDistillUnavailable
}

// SkillGen implements client.Client. The daemon skillgen RPC is not yet wired;
// returns ErrSkillGenUnavailable.
func (c *DaemonClient) SkillGen(ctx context.Context, req client.SkillGenRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrSkillGenUnavailable
}

// Brief implements client.Client. The daemon agent_brief RPC is not yet wired;
// returns ErrBriefUnavailable.
func (c *DaemonClient) Brief(ctx context.Context, topic string) ([]byte, []byte, error) {
	_, _ = ctx, topic
	return nil, nil, client.ErrBriefUnavailable
}

// Diagnose returns ErrDiagnosticUnavailable until a daemon diagnostics RPC is
// added (mirrors the analysis/edit "unavailable until wired" precedent).
func (c *DaemonClient) Diagnose(ctx context.Context, kinds []string, opts client.DiagnoseOptions) ([]byte, error) {
	_, _ = ctx, kinds
	return nil, client.ErrDiagnosticUnavailable
}

// Inline returns ErrEditUnavailable until a daemon edit RPC is added.
func (c *DaemonClient) Inline(ctx context.Context, req client.InlineRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrEditUnavailable
}

// SafeDelete returns ErrEditUnavailable until a daemon edit RPC is added.
func (c *DaemonClient) SafeDelete(ctx context.Context, req client.SafeDeleteRequest) ([]byte, error) {
	_, _ = ctx, req
	return nil, client.ErrEditUnavailable
}

// SocketPath returns the configured socket path.
func (c *DaemonClient) SocketPath() string { return c.socketPath }

// IsAlive reports whether a daemon is listening on the given Unix socket. It is
// a UNIX-ONLY liveness probe: it dials with net.DialTimeout("unix", ...) and
// NEVER opens a TCP/network connection. A successful dial means a server is
// accepting on the socket; any error (no socket, refused, timeout) → false. The
// connection is closed immediately. Used by the cmd default-discovery path to
// decide whether to route through a running daemon.
func IsAlive(socket string) bool {
	if socket == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

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

// ListPRs returns ErrForgeUnavailable until a daemon forge-enumeration RPC is
// added (SW-105). The forge PR-enumeration boundary is wired only on the
// in-process Direct client today (mirrors the analysis/edit/review
// "unavailable until wired" precedent).
func (c *DaemonClient) ListPRs(ctx context.Context) ([]byte, error) {
	return nil, client.ErrForgeUnavailable
}

// TriagePRs returns ErrForgeUnavailable until a daemon forge-enumeration RPC is
// added (SW-105).
func (c *DaemonClient) TriagePRs(ctx context.Context) ([]byte, error) {
	return nil, client.ErrForgeUnavailable
}

// ConflictsPRs returns ErrForgeUnavailable until a daemon forge-enumeration RPC is
// added (SW-106). Mirrors the ListPRs/TriagePRs "unavailable until wired" rule.
func (c *DaemonClient) ConflictsPRs(ctx context.Context) ([]byte, error) {
	return nil, client.ErrForgeUnavailable
}

// SuggestReviewers returns ErrAnalysisUnavailable until a daemon suggest-reviewers
// RPC is added (SW-107). The analyzer is wired only on the in-process Direct
// client today (mirrors the analysis "unavailable until wired" precedent).
func (c *DaemonClient) SuggestReviewers(ctx context.Context, diff string) ([]byte, error) {
	return nil, client.ErrAnalysisUnavailable
}

// CompareBranches returns ErrCompareUnavailable until a daemon branch-state
// materialization RPC is added (SW-107). The materializer is wired only on the
// in-process Direct client today.
func (c *DaemonClient) CompareBranches(ctx context.Context, baseRef, headRef string) ([]byte, error) {
	return nil, client.ErrCompareUnavailable
}

// CritiqueReview returns ErrAnalysisUnavailable until a daemon critique-review RPC
// is added (SW-108). The analyzer + review-fetch boundary are wired only on the
// in-process Direct client today (mirrors the analysis "unavailable until wired"
// precedent).
func (c *DaemonClient) CritiqueReview(ctx context.Context, prNumber int, diff, reviewJSON string) ([]byte, error) {
	return nil, client.ErrAnalysisUnavailable
}
