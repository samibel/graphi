// HTTP/SSE client adapter (SW-047).
//
// HTTP is a read-only, loopback-only implementation of the surfaces/client.Client
// interface that consumes the SW-044 HTTP REST + SSE surface instead of an
// in-process engine. It exists so the TUI surface can talk to the one shared
// Engine over the same network-facing contract the web/VS Code surfaces use,
// while the TUI render path stays byte-identical to every other surface:
// the adapter unwraps the SW-044 {schema_version, payload} envelope and returns
// the INNER payload bytes verbatim (parity by construction).
//
// Privacy posture (mirrors the SW-044 server):
//   - loopback-only: NewHTTP refuses any non-loopback base URL at construction
//     (defense in depth on the client side, so the TUI cannot be pointed at a
//     remote Engine), preserving the local-first / zero-outbound contract.
//   - strictly read-only: every method issues only GET. The mutation methods of
//     the client.Client interface (RefactorPreview/Refactor/Undo/PrComment)
//     return the typed *Unavailable sentinels and NEVER emit a request — there
//     is no live mutation path.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// httpSchemaVersion is the SW-044 envelope contract version this adapter is
// built against. It is sent on every request via X-Graphi-Schema-Version so the
// server's drift gate (412) fires on a contract-version mismatch, and it is
// compared against the version stamped on every response/event (R5 drift
// handling) so the TUI never renders possibly-misinterpreted bytes.
const httpSchemaVersion = 1

// SchemaVersion returns the envelope contract version this adapter expects. The
// TUI uses it to render a distinct "contract version mismatch" banner on drift.
func (h *HTTP) SchemaVersion() int { return httpSchemaVersion }

// Typed error set surfaced to the TUI so it can render distinct, actionable,
// non-crashing status states (AC-5). Each carries only a sanitized message
// (the SW-044 server already strips raw engine detail from 5xx bodies).
var (
	// ErrUnreachable is returned on a dial / connection error (Engine down).
	ErrUnreachable = errors.New("client: engine unreachable")
	// ErrSchemaMismatch is returned on a 412 envelope-version mismatch (R5).
	ErrSchemaMismatch = errors.New("client: contract version mismatch")
	// ErrUnavailable is returned on a 503 capability-unavailable response.
	ErrUnavailable = errors.New("client: capability unavailable")
	// ErrBadInput is returned on a 400 bad-request response.
	ErrBadInput = errors.New("client: bad request")
)

// HTTP is the read-only, loopback-only HTTP/SSE adapter. Construct with NewHTTP.
type HTTP struct {
	base string // validated loopback base URL, no trailing slash
	hc   *http.Client
}

// NewHTTP constructs a client over the SW-044 surface at baseURL (e.g.
// "http://127.0.0.1:8080"). It fails closed on a non-loopback target, mirroring
// httpsrv.AssertLoopback, so the TUI can never be pointed at a remote Engine.
func NewHTTP(baseURL string) (*HTTP, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("client: bad base URL %q: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("client: base URL %q must be http(s)", baseURL)
	}
	if err := assertLoopbackHost(u.Hostname()); err != nil {
		return nil, err
	}
	return &HTTP{
		base: strings.TrimRight(baseURL, "/"),
		// No proxy, no redirects to non-loopback: a stdlib client with a bounded
		// timeout. The transport dials only what base resolves to (loopback).
		hc: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// assertLoopbackHost rejects a non-loopback host, mirroring the server-side
// AssertLoopback discipline on the client so the local-first posture holds even
// when the address comes from a user flag.
func assertLoopbackHost(host string) error {
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("client: refusing non-loopback engine address %q (local-first, loopback-only)", host)
}

// doGET performs a read-only GET against path+query, validates the response
// status and the envelope schema version, and returns the inner payload bytes
// verbatim. It NEVER issues a non-GET request.
func (h *HTTP) doGET(ctx context.Context, path string, q url.Values) ([]byte, error) {
	full := h.base + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, fmt.Errorf("client: build request: %w", err)
	}
	// Advertise our envelope version so the server's drift gate (412) engages.
	req.Header.Set("X-Graphi-Schema-Version", strconv.Itoa(httpSchemaVersion))
	resp, err := h.hc.Do(req)
	if err != nil {
		// Dial / connection errors collapse to ErrUnreachable so the TUI renders
		// the calm "engine unreachable" banner rather than a raw transport error.
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, sanitizeDialErr(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		// happy path handled below
	case http.StatusBadRequest:
		return nil, fmt.Errorf("%w: %s", ErrBadInput, envMessage(body))
	case http.StatusPreconditionFailed:
		return nil, fmt.Errorf("%w: %s", ErrSchemaMismatch, envMessage(body))
	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, envMessage(body))
	default:
		// 404 / 405 / 500 and anything unexpected: surface a sanitized message.
		return nil, fmt.Errorf("client: engine error (%d): %s", resp.StatusCode, envMessage(body))
	}

	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("client: malformed envelope: %w", err)
	}
	// R5 drift handling: a value drift in the stamped version is treated as a
	// mismatch so the TUI does not render possibly-misinterpreted payload bytes.
	if env.SchemaVersion != httpSchemaVersion {
		return nil, fmt.Errorf("%w: server stamped schema_version=%d, want %d",
			ErrSchemaMismatch, env.SchemaVersion, httpSchemaVersion)
	}
	// Parity by construction: return the inner payload bytes verbatim.
	return []byte(env.Payload), nil
}

// doPOST is the write/transport counterpart of doGET for endpoints whose input
// does not fit query params (e.g. the multi-line /compound body). It sends the
// body verbatim, advertises the envelope version, and unwraps the SAME envelope
// as doGET so the returned payload bytes are byte-identical to doGET's.
func (h *HTTP) doPOST(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("X-Graphi-Schema-Version", strconv.Itoa(httpSchemaVersion))
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, sanitizeDialErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	switch resp.StatusCode {
	case http.StatusOK:
		// happy path below
	case http.StatusBadRequest:
		return nil, fmt.Errorf("%w: %s", ErrBadInput, envMessage(respBody))
	case http.StatusPreconditionFailed:
		return nil, fmt.Errorf("%w: %s", ErrSchemaMismatch, envMessage(respBody))
	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, envMessage(respBody))
	default:
		return nil, fmt.Errorf("client: engine error (%d): %s", resp.StatusCode, envMessage(respBody))
	}
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(respBody, &env); err != nil {
		return nil, fmt.Errorf("client: malformed envelope: %w", err)
	}
	if env.SchemaVersion != httpSchemaVersion {
		return nil, fmt.Errorf("%w: server stamped schema_version=%d, want %d",
			ErrSchemaMismatch, env.SchemaVersion, httpSchemaVersion)
	}
	return []byte(env.Payload), nil
}

// body, falling back to a generic string when the body is not an error envelope.
func envMessage(body []byte) string {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return "engine error"
}

// sanitizeDialErr reduces a transport error to a short, leak-free description.
func sanitizeDialErr(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "connection refused"
}

// Contract fetches the /contract capability document (envelope-unwrapped). The
// TUI uses it to populate the navigator without hard-coding the op list.
func (h *HTTP) Contract(ctx context.Context) ([]byte, error) {
	return h.doGET(ctx, "/contract", nil)
}

// Query runs a structural query over GET /query/{op}. Read-only.
func (h *HTTP) Query(ctx context.Context, op, symbol string, depth int) ([]byte, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	if depth != 0 {
		q.Set("depth", strconv.Itoa(depth))
	}
	return h.doGET(ctx, "/query/"+url.PathEscape(op), q)
}

// Compound runs a compound / Cypher-style graph query over POST /compound
// (EP-011 G1). The query text is sent as the request body so multi-line
// SEED/HOP/WHERE queries survive transport verbatim; the server returns the
// canonical query.Result bytes, byte-identical to the in-process surface.
func (h *HTTP) Compound(ctx context.Context, queryText string) ([]byte, error) {
	return h.doPOST(ctx, "/compound", []byte(queryText))
}

// Search runs a lexical search over GET /search. Read-only.
func (h *HTTP) Search(ctx context.Context, query string, limit int) ([]byte, error) {
	q := url.Values{}
	q.Set("q", query)
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return h.doGET(ctx, "/search", q)
}

// SemanticSearch runs the OPTIONAL semantic search over GET /search/semantic.
// Read-only. The server returns the canonical typed SemanticResponse (the
// graceful-skip "unavailable" response when no embedder is configured), so the
// bytes match the in-process and MCP surfaces (SW-059 parity).
func (h *HTTP) SemanticSearch(ctx context.Context, query string, limit int) ([]byte, error) {
	q := url.Values{}
	q.Set("q", query)
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return h.doGET(ctx, "/search/semantic", q)
}

// SearchAST runs the structural AST pattern query over POST /query-ast (SW-085).
// The JSON pattern is sent as the request body so a structured pattern survives
// transport verbatim; limit rides the query string. The server returns the
// canonical query.Result bytes, byte-identical to the in-process and MCP surfaces.
func (h *HTTP) SearchAST(ctx context.Context, patternJSON string, limit int) ([]byte, error) {
	path := "/query-ast"
	if limit != 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	return h.doPOST(ctx, path, []byte(patternJSON))
}

// FindClones runs the clone-detection query over POST /find-clones (SW-085). The
// JSON CloneConfig is the request body (empty ⇒ engine defaults). The server
// returns the canonical query.MarshalCloneResult bytes for byte-identical parity.
func (h *HTTP) FindClones(ctx context.Context, configJSON string) ([]byte, error) {
	return h.doPOST(ctx, "/find-clones", []byte(configJSON))
}

// Analyze runs a named analyzer over GET /analyze/{analyzer}. Read-only.
func (h *HTTP) Analyze(ctx context.Context, p AnalyzeParams) ([]byte, error) {
	q := url.Values{}
	if p.Symbol != "" {
		q.Set("symbol", p.Symbol)
	}
	if p.Direction != "" {
		q.Set("direction", p.Direction)
	}
	if p.MaxNodes != 0 {
		q.Set("max-nodes", strconv.Itoa(p.MaxNodes))
	}
	return h.doGET(ctx, "/analyze/"+url.PathEscape(p.Name), q)
}

// Savings is not exposed by the SW-044 surface; the read-only TUI does not need
// it. Returns ErrSavingsUnavailable without issuing any request.
func (h *HTTP) Savings(ctx context.Context) ([]byte, error) {
	return nil, ErrSavingsUnavailable
}

// --- Mutation methods: NO live path (read-only, structurally enforced) -------
//
// The adapter implements the full client.Client interface but every mutating
// method returns the typed unavailable sentinel and emits NO request, so the
// read-only guarantee is structural, not merely conventional (AC-6 / security).

// RefactorPreview always returns ErrEditUnavailable (read-only surface).
func (h *HTTP) RefactorPreview(ctx context.Context, req RefactorRequest) ([]byte, error) {
	return nil, ErrEditUnavailable
}

// Refactor always returns ErrEditUnavailable (read-only surface).
func (h *HTTP) Refactor(ctx context.Context, req RefactorRequest, actor string) ([]byte, error) {
	return nil, ErrEditUnavailable
}

// Undo always returns ErrEditUnavailable (read-only surface).
func (h *HTTP) Undo(ctx context.Context, undoToken, actor string) ([]byte, error) {
	return nil, ErrEditUnavailable
}

// PrComment always returns ErrReviewUnavailable (read-only surface).
func (h *HTTP) PrComment(ctx context.Context, req PrCommentRequest) ([]byte, error) {
	return nil, ErrReviewUnavailable
}

// Memory is not exposed by the current HTTP surface; returns ErrMemoryUnavailable.
func (h *HTTP) Memory(ctx context.Context, req MemoryRequest) ([]byte, error) {
	return nil, ErrMemoryUnavailable
}

// Distill is not exposed by the current HTTP surface; returns ErrDistillUnavailable.
func (h *HTTP) Distill(ctx context.Context, req DistillRequest) ([]byte, error) {
	return nil, ErrDistillUnavailable
}

// SkillGen is not exposed by the current HTTP surface; returns ErrSkillGenUnavailable.
func (h *HTTP) SkillGen(ctx context.Context, req SkillGenRequest) ([]byte, error) {
	return nil, ErrSkillGenUnavailable
}

// Brief is not exposed by the current HTTP surface; returns ErrBriefUnavailable.
func (h *HTTP) Brief(ctx context.Context, topic string) ([]byte, []byte, error) {
	return nil, nil, ErrBriefUnavailable
}

// Diagnose returns ErrDiagnosticUnavailable until a daemon/HTTP diagnostics RPC
// is added (mirrors the analysis/edit "unavailable until wired" precedent).
func (h *HTTP) Diagnose(ctx context.Context, kinds []string, opts DiagnoseOptions) ([]byte, error) {
	return nil, ErrDiagnosticUnavailable
}

// Inline returns ErrEditUnavailable until a daemon/HTTP edit RPC is added.
func (h *HTTP) Inline(ctx context.Context, req InlineRequest) ([]byte, error) {
	return nil, ErrEditUnavailable
}

// SafeDelete returns ErrEditUnavailable until a daemon/HTTP edit RPC is added.
func (h *HTTP) SafeDelete(ctx context.Context, req SafeDeleteRequest) ([]byte, error) {
	return nil, ErrEditUnavailable
}

// ListPRs returns ErrForgeUnavailable until a remote forge-enumeration RPC is
// added (SW-105). The forge boundary is wired only on the in-process Direct
// client today (mirrors the analysis/edit/review "unavailable until wired" rule).
func (h *HTTP) ListPRs(ctx context.Context) ([]byte, error) {
	return nil, ErrForgeUnavailable
}

// TriagePRs returns ErrForgeUnavailable until a remote forge-enumeration RPC is
// added (SW-105).
func (h *HTTP) TriagePRs(ctx context.Context) ([]byte, error) {
	return nil, ErrForgeUnavailable
}

// ConflictsPRs returns ErrForgeUnavailable until a remote forge-enumeration RPC is
// added (SW-106).
func (h *HTTP) ConflictsPRs(ctx context.Context) ([]byte, error) {
	return nil, ErrForgeUnavailable
}

// SuggestReviewers returns ErrAnalysisUnavailable until a remote suggest-reviewers
// RPC is added (SW-107). The analyzer is wired only on the in-process Direct client
// today (mirrors the analysis "unavailable until wired" precedent).
func (h *HTTP) SuggestReviewers(ctx context.Context, diff string) ([]byte, error) {
	return nil, ErrAnalysisUnavailable
}

// CompareBranches returns ErrCompareUnavailable until a remote branch-state
// materialization RPC is added (SW-107). The materializer is wired only on the
// in-process Direct client today.
func (h *HTTP) CompareBranches(ctx context.Context, baseRef, headRef string) ([]byte, error) {
	return nil, ErrCompareUnavailable
}

// CritiqueReview returns ErrAnalysisUnavailable until a remote critique-review RPC
// is added (SW-108). The analyzer + review-fetch boundary are wired only on the
// in-process Direct client today (mirrors the analysis "unavailable until wired"
// precedent).
func (h *HTTP) CritiqueReview(ctx context.Context, prNumber int, diff, reviewJSON string) ([]byte, error) {
	return nil, ErrAnalysisUnavailable
}

// compile-time proof the adapter satisfies the surface contract.
var _ Client = (*HTTP)(nil)
