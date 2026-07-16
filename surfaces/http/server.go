// Package http is graphi's read-only HTTP REST + SSE surface over the shared
// engine. It holds no query/analysis logic of its own: every REST handler
// delegates to the same surfaces/client.Client seam used by the CLI, MCP, and
// daemon surfaces, so answers and provenance are byte-identical across surfaces
// (parity by construction). The one engine dependency beyond client.Client is
// the engine/observe broker, which the SSE handler subscribes to for streamed
// freshness events.
//
// Layering: surfaces. It imports surfaces/client (the contract) and engine/observe
// (the event source). It is wired from cmd/graphi, never the reverse. It is
// read-only and local-first: it binds loopback only and makes zero outbound
// network connections.
//
// # Two-version model (do not conflate)
//
// SchemaVersion (this package) versions the HTTP *envelope* contract — the wire
// shape consumed by TS/web clients (the {schema_version, payload} wrapper, the
// error envelope, the SSE frame, the /contract descriptors). The engine's
// EP-002 *payload* schema, embedded verbatim inside envelope.Payload, versions
// the analyzer/query result shapes independently. AC-3's drift gate (R5)
// negotiates the ENVELOPE version: the request header X-Graphi-Schema-Version is
// optional, the response version stamp is mandatory.
//
// # Status taxonomy (AC-5, stable across REST and SSE)
//
//	200 ok · 400 bad input · 403 Labs route fail-closed or rejected Host/Origin
//	(SW-112 / SAFE-01; GRAPHI_HTTP_LABS=1 opts in to Labs) · 404 unknown
//	route/resource · 405 mutating verb ·
//	412 schema-version mismatch · 413 request body too large · 503 capability unavailable
//	(ErrSearchUnavailable / ErrAnalysisUnavailable) · 500 sanitized unexpected.
//
// 5xx bodies carry only a generic message + a stable error code; the raw engine
// error is logged locally and NEVER written to the client.
//
// # SSE framing (AC-2)
//
// /events is a freshness firehose (no per-operation lifecycle: query/search/
// analyze are unary). Frames carry event:<type> + id:<monotonic-per-connection>
// + data:<json>. A version-stamped event:ready handshake is the first frame; a
// terminal event:bye frame is emitted on clean teardown (broker close / server
// shutdown); an event:error frame (shared error envelope) is emitted on a stream
// error. There is no per-operation progressive stream in this surface.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/wiki"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/http/webui"
	"github.com/samibel/graphi/surfaces/mcp"
)

// SchemaVersion is the envelope contract version stamped on every response and
// enforced (on request) via X-Graphi-Schema-Version. It versions the HTTP
// envelope shape consumed by the TS/React web client (SW-040) and VS Code
// extension (SW-043); the engine's internal payload versioning is separate.
const SchemaVersion = 1

const (
	// maxRequestBodyBytes is shared by every route on this HTTP surface. The
	// body-limit middleware reads at most one byte beyond it (via
	// http.MaxBytesReader), so oversized bodies are rejected instead of being
	// silently truncated into a different request.
	maxRequestBodyBytes int64 = 1 << 20

	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 30 * time.Second
	httpIdleTimeout       = 2 * time.Minute
	httpShutdownTimeout   = 5 * time.Second
)

// queryOps is the allow-list of structural query operations the REST surface
// accepts. It is DERIVED from engine/query.Operations so the HTTP surface can
// never drift from the engine's canonical operation set: adding an operation to
// query.Operations (e.g. the EP-011 hierarchy ops) automatically exposes it
// here with byte-identical semantics. The HTTP layer never invents new query
// semantics.
var queryOps = func() map[string]struct{} {
	m := make(map[string]struct{}, len(query.Operations))
	for _, op := range query.Operations {
		m[op] = struct{}{}
	}
	return m
}()

// streamDescriptors enumerates the SSE event types a client may observe on
// /events, for capability negotiation via /contract. "ingest-completed" is the
// freshness event published by the ingest producer; "ingest-progress" is the
// throttled per-phase progress event of a running full ingest; ready/bye/error
// are the framing events this surface emits (handshake, terminal, stream error).
var streamDescriptors = []string{"ingest-completed", "ingest-progress", "ready", "bye", "error"}

// Server is the read-only HTTP REST + SSE surface. Construct with New and serve
// via ListenAndServe (loopback) or Serve (custom listener, for tests).
type Server struct {
	client client.Client
	stable client.StableClient
	broker *observe.Broker

	// Optional read-only store for serving the self-generated wiki (SW-041).
	// When set, /wiki and /wiki/c/{id} are enabled; nil disables them (404).
	store graphstore.Graphstore

	// analyzers is the optional list of analyzer names exposed by /contract for
	// capability negotiation (AC-6). It is injected from cmd/graphi (which owns
	// the analysis.Service) so the http package never imports engine/analysis,
	// preserving the surface→engine layering. Empty when not wired.
	analyzers []string

	// labsEnabled gates the non-Stable ("Labs") routes — the PR/forge, memory,
	// distill and skillgen endpoints SCOPE-01 classifies outside the frozen
	// stable set. It is FALSE by default (SW-112 / SAFE-01 fail-closed): the
	// routes stay registered (the wire surface is pinned by the SW-110 route
	// snapshot) but answer 403 until the operator opts in by exporting
	// GRAPHI_HTTP_LABS=1 for the server process.
	labsEnabled bool

	wikiOnce      sync.Once
	wikiErr       error
	wikiGenerated wiki.Wiki
}

// LabsEnvVar is the explicit operator opt-in for the Labs HTTP routes (SW-112 /
// SAFE-01). Unset or any value other than "1" keeps them fail-closed (403).
const LabsEnvVar = "GRAPHI_HTTP_LABS"

// New constructs a Server over the given client and (optionally) an event broker.
// A nil broker disables the /events SSE endpoint (it returns 503). The Labs
// routes are fail-closed unless GRAPHI_HTTP_LABS=1 is exported (read once here).
func New(c client.Client, b *observe.Broker) *Server {
	return &Server{client: c, stable: client.AsStable(c), broker: b, labsEnabled: os.Getenv(LabsEnvVar) == "1"}
}

// WithWiki enables the self-generated wiki (SW-041) by attaching a read-only
// graphstore the wiki is generated from. The wiki is generated once on first
// /wiki access and cached for the server's lifetime (deterministic + stable for
// a process). Returns the receiver for chaining.
func (s *Server) WithWiki(store graphstore.Graphstore) *Server {
	s.store = store
	return s
}

// WithDescriptors injects the analyzer names exposed by the /contract metadata
// endpoint (AC-6) for client capability negotiation. cmd/graphi passes the
// analysis.Service.Names() set here so the http package stays free of an
// engine/analysis import. A nil/empty list simply omits analyzers from
// /contract. Returns the receiver for chaining.
func (s *Server) WithDescriptors(analyzers []string) *Server {
	s.analyzers = analyzers
	return s
}

// envelope wraps every data response so consumers can detect contract drift.
// Payload carries the engine's canonical serialized bytes verbatim (as
// json.RawMessage) so the wire bytes are byte-identical to MCP/CLI output.
type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// errBody is the inner structured error of errorEnvelope: a stable machine code
// plus a safe, sanitized human message (never a raw engine error).
type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorEnvelope is the single error shape shared by every REST error response
// and the SSE event:error frame (AC-5). It is version-stamped like the success
// envelope so clients can detect contract drift on the error path too.
type errorEnvelope struct {
	SchemaVersion int     `json:"schema_version"`
	Error         errBody `json:"error"`
}

// mapError classifies an engine/daemon error into a documented HTTP status, a
// stable machine code, and a SANITIZED client-safe message. Unexpected errors
// collapse to 500 / "internal" / "internal error" — the raw err is NEVER
// returned to the client (it is logged by the caller); this is the AC-5
// no-leak guarantee. Capability-unavailable errors map to 503.
func mapError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, client.ErrSearchUnavailable),
		errors.Is(err, client.ErrAnalysisUnavailable),
		errors.Is(err, client.ErrMemoryUnavailable),
		errors.Is(err, client.ErrDistillUnavailable),
		errors.Is(err, client.ErrSkillGenUnavailable):
		return http.StatusServiceUnavailable, "unavailable", "capability unavailable"
	default:
		return http.StatusInternalServerError, "internal", "internal error"
	}
}

// route is one registered HTTP route: the wire-visible method+pattern and the
// handler serving it. The route table is the SINGLE SOURCE OF TRUTH the router
// and the SW-110 route snapshot both read, so the advertised HTTP surface cannot
// drift from what a scope change reviews as an intentional diff (TEST-01 AC3).
type route struct {
	Pattern    string // Go 1.22 ServeMux "METHOD /path" pattern
	capability capabilityResolver
	handler    http.HandlerFunc
}

// capabilityResolver maps one concrete HTTP request to the canonical product
// operation it invokes. An empty result marks transport/infrastructure behavior
// such as health, contract negotiation, the plain event stream, or the SPA.
// Every route supplies a resolver, including mixed routes such as
// /query/{op}, /analyze/{analyzer}, and /events?analyzer=.
type capabilityResolver func(*http.Request) string

func infrastructureCapability(*http.Request) string { return "" }

func fixedCapability(name string) capabilityResolver {
	return func(*http.Request) string { return name }
}

func queryCapability(r *http.Request) string {
	op := r.PathValue("op")
	if _, known := queryOps[op]; !known {
		// Preserve the documented 400 for an unknown query operation. Known
		// hierarchy operations are resolved below and therefore fail closed.
		return ""
	}
	return op
}

func analyzerCapability(r *http.Request) string { return r.PathValue("analyzer") }

func eventCapability(r *http.Request) string { return r.URL.Query().Get("analyzer") }

func isLabsCapability(name string) bool {
	return name != "" && !mcp.IsStableOperation(name)
}

// capabilityGuard fail-closes every request that resolves to an operation
// outside the frozen StableOperations manifest (SW-112 / SAFE-01). Guarding is
// applied centrally while registering the route table, so adding a route cannot
// accidentally bypass protection by forgetting a hand-written wrapper.
func (s *Server) capabilityGuard(resolve capabilityResolver, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		capability := resolve(r)
		if isLabsCapability(capability) && !s.labsEnabled {
			writeErr(w, http.StatusForbidden, "labs_disabled",
				"this Labs route is outside the stable capability set and is disabled by default (SAFE-01); export "+LabsEnvVar+"=1 to opt in")
			return
		}
		next(w, r)
	}
}

// routes returns the ordered route table this surface serves. Handler registers
// exactly these in order; the SPA catch-all is LAST and least specific so every
// explicit API/SSE/wiki route wins (Go 1.22 precedence). Labs routes (PR/forge,
// memory, distill, skillgen, hierarchy/compound/pattern/semantic queries,
// non-impact analyzers, analyzer-over-SSE, and community wiki) are classified
// here and guarded centrally by Handler.
func (s *Server) routes() []route {
	return []route{
		{"GET /healthz", infrastructureCapability, s.handleHealth},
		{"GET /contract", infrastructureCapability, s.handleContract},
		{"GET /query/{op}", queryCapability, s.schemaGuard(s.handleQuery)},
		{"POST /compound", fixedCapability("compound"), s.schemaGuard(s.handleCompound)},
		{"POST /query-ast", fixedCapability("search_ast"), s.schemaGuard(s.handleSearchAST)},
		{"POST /find-clones", fixedCapability("find_clones"), s.schemaGuard(s.handleFindClones)},
		{"GET /search", fixedCapability("search"), s.schemaGuard(s.handleSearch)},
		{"GET /search/semantic", fixedCapability("search_semantic"), s.schemaGuard(s.handleSemanticSearch)},
		{"GET /analyze/{analyzer}", analyzerCapability, s.schemaGuard(s.handleAnalyze)},
		{"GET /prs", fixedCapability("list_prs"), s.schemaGuard(s.handleListPRs)},
		{"GET /prs/triage", fixedCapability("triage_prs"), s.schemaGuard(s.handleTriagePRs)},
		{"GET /prs/conflicts", fixedCapability("conflicts_prs"), s.schemaGuard(s.handleConflictsPRs)},
		{"GET /prs/suggest-reviewers", fixedCapability("suggest_reviewers"), s.schemaGuard(s.handleSuggestReviewers)},
		{"GET /branches/compare", fixedCapability("compare_branches"), s.schemaGuard(s.handleCompareBranches)},
		{"GET /reviews/critique", fixedCapability("critique_review"), s.schemaGuard(s.handleCritiqueReview)},
		{"POST /memory", fixedCapability("memory"), s.schemaGuard(s.handleMemory)},
		{"POST /distill", fixedCapability("distill"), s.schemaGuard(s.handleDistill)},
		{"POST /skillgen", fixedCapability("skillgen"), s.schemaGuard(s.handleSkillGen)},
		{"GET /events", eventCapability, s.schemaGuard(s.handleSSE)},
		{"GET /wiki", fixedCapability("communities"), s.handleWikiIndex},
		{"GET /wiki/c/{id}", fixedCapability("communities"), s.handleWikiPage},
		// SPA catch-all (SW-066). Registered LAST and matched LEAST specifically:
		// Go 1.22 ServeMux routes the explicit patterns above ahead of "GET /", so
		// every existing API/SSE/wiki route is byte-identical and the SPA only sees
		// paths none of them claim. It is served over this same loopback surface.
		{"GET /", infrastructureCapability, s.spaHandler()},
	}
}

// RoutePatterns returns the ordered ServeMux patterns this surface registers. It
// is the descriptive projection the SW-110 route-set snapshot pins; it registers
// nothing and has no side effects.
func (s *Server) RoutePatterns() []string {
	rs := s.routes()
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Pattern
	}
	return out
}

// Handler returns the routed http.Handler. Routing uses Go 1.22+ method-pattern
// matching: non-GET methods on data routes return 405 automatically, and unknown
// paths return 404.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.routes() {
		mux.HandleFunc(r.Pattern, s.capabilityGuard(r.capability, r.handler))
	}
	return requestSecurityGuard(limitRequestBody(mux))
}

// limitRequestBody enforces one hard limit before routing. Buffering once here
// covers both today's POST handlers and future routes; handlers receive a fresh
// reader containing the complete body only after the limit check succeeds.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}

		limited := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		body, err := io.ReadAll(limited)
		_ = limited.Close()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeErr(w, http.StatusRequestEntityTooLarge, "request_too_large",
					fmt.Sprintf("request body exceeds %d bytes", maxRequestBodyBytes))
				return
			}
			writeErr(w, http.StatusBadRequest, "bad_request", "cannot read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

// requestSecurityGuard protects the loopback HTTP surface against DNS
// rebinding and cross-origin browser access. Host must name localhost or a
// loopback IP. Browser requests that carry Origin must be exactly same-origin
// (scheme, normalized host, and port); requests without Origin remain valid for
// curl, the CLI HTTP client, IDEs, and other non-browser local consumers.
func requestSecurityGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		requestAuthority, err := parseHTTPAuthority(r.Host, scheme)
		if err != nil || !isLoopbackHTTPHost(requestAuthority.host) {
			writeErr(w, http.StatusForbidden, "invalid_host", "request host must be loopback or localhost")
			return
		}

		origins := r.Header.Values("Origin")
		if len(origins) > 1 {
			writeErr(w, http.StatusForbidden, "origin_forbidden", "cross-origin requests are not allowed")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			originAuthority, originScheme, err := parseHTTPOrigin(origin)
			if err != nil || originScheme != scheme || originAuthority != requestAuthority {
				writeErr(w, http.StatusForbidden, "origin_forbidden", "cross-origin requests are not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type httpAuthority struct {
	host string
	port string
}

func parseHTTPAuthority(raw, scheme string) (httpAuthority, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "/?#@") {
		return httpAuthority{}, errors.New("invalid authority")
	}

	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		switch {
		case net.ParseIP(raw) != nil:
			host = raw // bare IPv6, accepted defensively for in-process clients
		case !strings.Contains(raw, ":"):
			host = raw
		default:
			return httpAuthority{}, fmt.Errorf("invalid authority %q: %w", raw, err)
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return httpAuthority{}, errors.New("empty authority host")
	}
	if port == "" {
		port = defaultHTTPPort(scheme)
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return httpAuthority{}, errors.New("invalid authority port")
		}
		port = strconv.Itoa(n)
	}
	return httpAuthority{host: host, port: port}, nil
}

func parseHTTPOrigin(raw string) (httpAuthority, string, error) {
	if raw == "null" {
		return httpAuthority{}, "", errors.New("opaque origin")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return httpAuthority{}, "", errors.New("invalid origin")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return httpAuthority{}, "", errors.New("invalid origin scheme")
	}
	authority, err := parseHTTPAuthority(u.Host, scheme)
	if err != nil || !isLoopbackHTTPHost(authority.host) {
		return httpAuthority{}, "", errors.New("invalid origin authority")
	}
	return authority, scheme, nil
}

func defaultHTTPPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func isLoopbackHTTPHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// spaHandler serves the embedded single-page web UI at "/" (SW-066).
//
//   - If the UI was not embedded (default UI-free build, !webui.Enabled()), it
//     returns the static NoticeHTML page explaining how to get a bundled binary.
//   - Otherwise it serves static files from the embedded webui.FS: "/" → index.html,
//     an existing file path → that file (with FileServerFS content types), and any
//     other path → index.html (SPA history-mode fallback). All served responses
//     are 200.
//
// It is static-only (no wall-clock/rand) and makes no outbound connection — it
// reads exclusively from the in-binary embedded FS, preserving the loopback-only,
// zero-egress contract.
func (s *Server) spaHandler() http.HandlerFunc {
	if !webui.Enabled() {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(webui.NoticeHTML))
		}
	}
	fileServer := http.FileServerFS(webui.FS)
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			http.ServeFileFS(w, r, webui.FS, "index.html")
			return
		}
		if _, err := fs.Stat(webui.FS, p); err == nil {
			// Existing asset: let FileServerFS set content types / handle ranges.
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA history-mode fallback: unknown client-side route → index.html (200).
		http.ServeFileFS(w, r, webui.FS, "index.html")
	}
}

// ListenAndServe serves the HTTP surface on addr. Callers MUST pass a loopback
// address (e.g. "127.0.0.1:0" or "127.0.0.1:8080"); the zero-outbound,
// local-first contract binds loopback only. The call blocks until the server
// stops.
func (s *Server) ListenAndServe(addr string) error {
	if err := AssertLoopback(addr); err != nil {
		return err
	}
	return s.newHTTPServer(addr).ListenAndServe()
}

// Serve serves the HTTP surface on the given listener (tests). Blocks until the
// listener is closed.
func (s *Server) Serve(ln net.Listener) error {
	return s.ServeContext(context.Background(), ln)
}

// ServeContext serves until the listener fails or ctx is cancelled. Cancellation
// first attempts a bounded graceful shutdown; if active handlers do not drain in
// time, Close releases their connections before the method returns.
func (s *Server) ServeContext(ctx context.Context, ln net.Listener) error {
	if ctx == nil {
		ctx = context.Background()
	}
	httpServer := s.newHTTPServer("")
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	httpServer.BaseContext = func(net.Listener) context.Context { return requestCtx }
	// Shutdown does not cancel active request contexts by itself. Cancelling
	// them from its callback lets long-lived SSE handlers exit cleanly while the
	// bounded Shutdown call continues to drain the server.
	httpServer.RegisterOnShutdown(cancelRequests)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(ln) }()

	select {
	case err := <-serveErr:
		if ctx.Err() != nil && errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			if closeErr := httpServer.Close(); closeErr != nil {
				shutdownErr = errors.Join(shutdownErr, closeErr)
			}
		}
		err := <-serveErr
		if shutdownErr != nil {
			return fmt.Errorf("http: graceful shutdown: %w", shutdownErr)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (s *Server) newHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

// AssertLoopback rejects a non-loopback bind address, enforcing the zero-outbound
// local-first contract at the surface boundary. It is exported so callers that
// build their own listener (e.g. cmd/graphi runHTTP, which prints the bound
// address) can validate BEFORE net.Listen — the surface must never bind a
// non-loopback address regardless of which entry point constructs the listener.
func AssertLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("http: bad address %q: %w", addr, err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("http: refusing non-loopback bind %q (local-first, loopback-only)", addr)
	}
	return nil
}

// ListenLoopback asserts addr is loopback (AssertLoopback) and then binds a TCP
// listener on it. The bind lives in this loopback-only surface package — not in
// cmd — so the local-first contract and the single net.Listen egress surface
// stay inside the allowlisted surfaces/http boundary (the zero-telemetry canary
// allowlists this package, like surfaces/daemon and surfaces/client).
func ListenLoopback(addr string) (net.Listener, error) {
	if err := AssertLoopback(addr); err != nil {
		return nil, err
	}
	return net.Listen("tcp", addr)
}

// schemaGuard rejects requests whose X-Graphi-Schema-Version header advertises an
// unsupported contract version (412 Precondition Failed), mirroring the EP-002
// schema-version drift gate. Absent header = no negotiation (pass through).
func (s *Server) schemaGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Graphi-Schema-Version"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n != SchemaVersion {
				writeErr(w, http.StatusPreconditionFailed, "schema_mismatch",
					fmt.Sprintf("schema version mismatch: want %d", SchemaVersion))
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "schema_version": SchemaVersion})
}

// contractDoc is the capability-negotiation document served by /contract
// (AC-6). It enumerates the data resources (query ops + search + analyzers) and
// the SSE stream event types a client may consume, stamped with the envelope
// SchemaVersion so a TS/web/TUI/VS Code client can negotiate without
// hard-coding routes.
type contractDoc struct {
	SchemaVersion int      `json:"schema_version"`
	Resources     []string `json:"resources"`
	Streams       []string `json:"streams"`
}

// handleContract returns only resources that the current server profile will
// actually serve. The default profile therefore omits Labs query operations and
// analyzers instead of advertising capabilities that capabilityGuard will
// reject. Labs opt-in exposes the complete catalog. It is the runtime mirror of
// the hand-authored contract.schema.json (the Go↔TS single source of truth).
func (s *Server) handleContract(w http.ResponseWriter, r *http.Request) {
	resourceSet := make(map[string]struct{}, len(queryOps)+1+len(agentToolNames)+len(s.analyzers))
	add := func(operation, resource string) {
		if s.labsEnabled || !isLabsCapability(operation) {
			resourceSet[resource] = struct{}{}
		}
	}
	for op := range queryOps {
		add(op, "query/"+op)
	}
	add("search", "search")
	// EP-020 agent tools are always served (the client seam degrades to the
	// contract "unavailable" outcome when no graph services are wired).
	for _, t := range agentToolNames {
		add(t, "analyze/"+t)
	}
	for _, a := range s.analyzers {
		add(a, "analyze/"+a)
	}
	resources := make([]string, 0, len(resourceSet))
	for resource := range resourceSet {
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	doc := contractDoc{
		SchemaVersion: SchemaVersion,
		Resources:     resources,
		Streams:       append([]string(nil), streamDescriptors...),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("op")
	if _, ok := queryOps[op]; !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "unknown query op: "+op)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "symbol required")
		return
	}
	depth := 0
	if d := r.URL.Query().Get("depth"); d != "" {
		v, err := strconv.Atoi(d)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad depth")
			return
		}
		depth = v
	}
	raw, err := s.client.Query(r.Context(), op, symbol, depth)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleCompound runs a compound / Cypher-style graph query (EP-011 G1). The
// query text is the request body; the canonical query.Result payload is returned
// in the same envelope as /query, so compound and fixed-query results are
// byte-identical across CLI/MCP/HTTP/daemon.
func (s *Server) handleCompound(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read compound body")
		return
	}
	raw, err := s.client.Compound(r.Context(), string(body))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSearchAST runs the structural AST pattern query (SW-082 / SW-085). The
// JSON AstPattern is the request body and limit rides the query string; the
// canonical query.Result payload is returned in the SAME envelope as /query and
// /compound, so search_ast results are byte-identical across CLI/MCP/HTTP/daemon.
// A malformed pattern surfaces the engine's typed error through the shared
// sanitized-error path (no new surface error shape).
func (s *Server) handleSearchAST(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read query-ast body")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.SearchAST(r.Context(), string(body), limit)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleFindClones runs the clone-detection query (SW-083 / SW-085). The JSON
// CloneConfig is the request body (empty ⇒ engine defaults); the canonical
// query.CloneResult payload is returned in the same envelope for byte-identical
// parity across surfaces.
func (s *Server) handleFindClones(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "cannot read find-clones body")
		return
	}
	raw, err := s.client.FindClones(r.Context(), string(body))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "q required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.Search(r.Context(), q, limit)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSemanticSearch serves the OPTIONAL semantic search (SW-059). It embeds
// the engine's canonical SemanticResponse bytes verbatim, so the graceful-skip
// "unavailable" payload is byte-identical to the CLI and MCP surfaces. The
// graceful-skip path returns 200 with Available=false (NOT a 503), because "no
// embedder configured" is a normal, typed result — not a capability error.
func (s *Server) handleSemanticSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "q required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.SemanticSearch(r.Context(), q, limit)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	analyzer := r.PathValue("analyzer")
	if analyzer == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "analyzer required")
		return
	}
	// EP-020 agent tools ride the same read-only /analyze/{name} route but
	// dispatch through their dedicated client seams (shared engine logic, same
	// canonical bytes as CLI/MCP) instead of the generic analysis service.
	if handled := s.handleAgentTool(w, r, analyzer); handled {
		return
	}
	p := client.AnalyzeParams{
		Name:      analyzer,
		Symbol:    r.URL.Query().Get("symbol"),
		Direction: r.URL.Query().Get("direction"),
	}
	if mn := r.URL.Query().Get("max-nodes"); mn != "" {
		v, err := strconv.Atoi(mn)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad max-nodes")
			return
		}
		p.MaxNodes = v
	}
	var raw []byte
	var err error
	if analyzer == "impact" {
		// The only Stable analyzer route uses the selector-free StableClient seam;
		// arbitrary analyzer dispatch remains confined to the Labs branch below.
		raw, err = s.stable.Impact(r.Context(), client.ImpactParams{
			Symbol: p.Symbol, Direction: p.Direction, MaxNodes: p.MaxNodes,
		})
	} else {
		raw, err = s.client.Analyze(r.Context(), p)
	}
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// agentToolNames are the EP-020 agent tools served on the /analyze/{name}
// route and advertised in /contract. They dispatch through the dedicated
// client seams, not the generic analysis service.
var agentToolNames = []string{"agent_brief", "change_risk", "explain_symbol", "related_files"}

// handleAgentTool serves the EP-020 agent tools on the shared /analyze route.
// It returns false when name is not an agent tool so the generic analyzer
// dispatch proceeds. Read-only: every seam it calls is a GET-safe query.
func (s *Server) handleAgentTool(w http.ResponseWriter, r *http.Request, name string) bool {
	q := r.URL.Query()
	maxItems := 0
	if mi := q.Get("max-items"); mi != "" {
		v, err := strconv.Atoi(mi)
		if err != nil || v < 0 {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad max-items")
			return true
		}
		maxItems = v
	}
	var (
		raw []byte
		err error
	)
	switch name {
	case "explain_symbol":
		symbol := q.Get("symbol")
		if symbol == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "symbol required")
			return true
		}
		raw, err = s.client.ExplainSymbol(r.Context(), symbol, maxItems)
	case "related_files":
		target := q.Get("target")
		if target == "" {
			target = q.Get("symbol") // IDE convenience alias
		}
		if target == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "target required")
			return true
		}
		raw, err = s.client.RelatedFiles(r.Context(), target, q.Get("direction"), maxItems)
	case "change_risk":
		target := q.Get("target")
		if target == "" {
			target = q.Get("symbol")
		}
		if target == "" {
			writeErr(w, http.StatusBadRequest, "bad_request", "target required")
			return true
		}
		raw, err = s.client.ChangeRisk(r.Context(), target, "", maxItems)
	case "agent_brief":
		topic := q.Get("topic")
		if topic == "" {
			topic = q.Get("symbol")
		}
		raw, _, err = s.client.Brief(r.Context(), topic)
	default:
		return false
	}
	if err != nil {
		writeErrSanitized(w, err)
		return true
	}
	writeEnvelope(w, raw)
	return true
}

// handleListPRs (SW-105) returns the read-only forge PR-enumeration metadata
// envelope. It delegates 100% to the shared client.ListPRs seam and embeds the
// canonical forge.PRList bytes verbatim, so the payload is byte-identical to the
// CLI/MCP surfaces. No graph scoring occurs.
func (s *Server) handleListPRs(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.ListPRs(r.Context())
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleTriagePRs (SW-105) returns the single-pass graph-derived ranked PR triage.
// It delegates to the shared client.TriagePRs seam (forge enumeration → zero-egress
// engine analyzer → shared encoder), so the ranked payload is byte-identical to the
// CLI/MCP surfaces.
func (s *Server) handleTriagePRs(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.TriagePRs(r.Context())
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleConflictsPRs (SW-106) returns the inter-PR conflict report. It delegates
// to the shared client.ConflictsPRs seam (forge enumeration → zero-egress engine
// analyzer → shared encoder), so the pairwise payload is byte-identical to the
// CLI/MCP surfaces.
func (s *Server) handleConflictsPRs(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.ConflictsPRs(r.Context())
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSuggestReviewers (SW-107) returns the ranked reviewer report. It delegates
// to the shared client.SuggestReviewers seam (touched-set resolution → zero-egress
// engine analyzer → shared encoder), so the payload is byte-identical to the
// CLI/MCP surfaces. The `diff` query parameter is the local-first PR diff / refs.
func (s *Server) handleSuggestReviewers(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.SuggestReviewers(r.Context(), r.URL.Query().Get("diff"))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleCompareBranches (SW-107) returns the graph-level branch diff. It delegates
// to the shared client.CompareBranches seam (base/head materialized above the
// surface boundary → zero-egress engine analyzer → shared encoder), so the payload
// is byte-identical to the CLI/MCP surfaces. The `base`/`head` query parameters are
// branch refs.
func (s *Server) handleCompareBranches(w http.ResponseWriter, r *http.Request) {
	raw, err := s.client.CompareBranches(r.Context(), r.URL.Query().Get("base"), r.URL.Query().Get("head"))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleCritiqueReview (SW-108, the EP-018 capstone) returns the graph-evidence
// critique of an existing PR review. It delegates to the shared client.CritiqueReview
// seam (surface review fetch / inline review → zero-egress engine analyzer → shared
// encoder), so the payload is byte-identical to the CLI/MCP surfaces. The `pr`
// parameter selects the review to fetch; `review` supplies an inline review JSON
// (takes precedence); `diff` is the PR's touched set.
func (s *Server) handleCritiqueReview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prNumber := 0
	if v := strings.TrimSpace(q.Get("pr")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErrSanitized(w, fmt.Errorf("http: invalid pr %q", v))
			return
		}
		prNumber = n
	}
	raw, err := s.client.CritiqueReview(r.Context(), prNumber, q.Get("diff"), q.Get("review"))
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleMemory runs an EP-012 memory operation. The request body is a JSON
// MemoryRequest; the response payload is the canonical MemoryResponse bytes
// (byte-identical to CLI/MCP output).
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	var req client.MemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	raw, err := s.client.Memory(r.Context(), req)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleDistill runs EP-012 session distillation. The request body is a JSON
// DistillRequest; the response payload is the canonical DistillResponse bytes.
func (s *Server) handleDistill(w http.ResponseWriter, r *http.Request) {
	var req client.DistillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	raw, err := s.client.Distill(r.Context(), req)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSkillGen runs EP-012 deterministic skill generation. The request body
// is a JSON SkillGenRequest; the response payload is the canonical
// SkillGenResponse bytes.
func (s *Server) handleSkillGen(w http.ResponseWriter, r *http.Request) {
	var req client.SkillGenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	raw, err := s.client.SkillGen(r.Context(), req)
	if err != nil {
		writeErrSanitized(w, err)
		return
	}
	writeEnvelope(w, raw)
}

// handleSSE streams broker events to the client as Server-Sent Events with
// stable framing (AC-2): each frame carries event:<type>, id:<monotonic
// per-connection seq>, and data:<json>. The first frame is a version-stamped
// event:ready handshake (R5: the response always stamps the envelope version,
// so a client that omits X-Graphi-Schema-Version still learns the server
// version). A terminal event:bye frame is emitted on clean teardown (broker
// close); an event:error frame (shared error envelope) is emitted on a stream
// marshal error. The connection is kept alive with :keep-alive comments and
// tears down cleanly on client disconnect (the broker drops the subscriber via
// context cancellation — no goroutine or connection leak).
//
// /events is wired behind schemaGuard (see Handler), so a client advertising a
// wrong X-Graphi-Schema-Version is rejected with 412 BEFORE the stream opens.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	// SW-104: when an `analyzer` query param is present, /events serves a one-shot
	// analysis frame whose payload is derived from the SHARED (*Direct).Analyze
	// path (canonical analysis.Marshal bytes), NOT re-serialized here. This routes
	// the four EP-017 operations through the same single dispatch + encoder as
	// every other surface, so the SSE analysis frame is byte-identical to the
	// CLI/MCP/HTTP/daemon envelope. Absent the param, /events is the freshness
	// firehose (unchanged).
	if analyzer := r.URL.Query().Get("analyzer"); analyzer != "" {
		s.handleSSEAnalyze(w, r, analyzer)
		return
	}
	if s.broker == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "event stream unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	var seq uint64
	// Version-stamped handshake frame (R5: mandatory response version stamp).
	if !writeFrame(w, flusher, &seq, "ready", []byte(fmt.Sprintf(`{"schema_version":%d}`, SchemaVersion))) {
		return
	}

	ctx := r.Context()
	events := s.broker.Subscribe(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnect: the response writer is gone; do not attempt a
			// terminal frame (it would error). The broker removes the subscriber.
			return
		case <-ticker.C:
			refreshWriteDeadline(w)
			if _, err := fmt.Fprintf(w, ":keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-events:
			if !open {
				// Broker closed the subscription (server shutdown / ctx cancel):
				// emit the terminal completion frame, then return.
				writeFrame(w, flusher, &seq, "bye", []byte("{}"))
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				// Surface the stream error as a sanitized event:error frame
				// (shared error envelope) instead of silently dropping it.
				eb, _ := json.Marshal(errorEnvelope{SchemaVersion: SchemaVersion, Error: errBody{Code: "internal", Message: "internal error"}})
				writeFrame(w, flusher, &seq, "error", eb)
				continue
			}
			if !writeFrame(w, flusher, &seq, ev.Type, data) {
				return
			}
		}
	}
}

// handleSSEAnalyze serves a one-shot analysis result over SSE (SW-104). The
// analysis payload is produced by the SHARED client.Analyze path (the same
// (*Direct).Analyze -> Service.Dispatch -> analysis.Marshal seam the REST
// /analyze handler and every other surface use), so the canonical bytes embedded
// in the `analysis` frame are byte-identical to the other surfaces' envelopes.
// The SSE adapter only frames those bytes; it holds NO analysis logic and does
// NOT re-serialize. Frame sequence: ready -> analysis -> bye.
func (s *Server) handleSSEAnalyze(w http.ResponseWriter, r *http.Request, analyzer string) {
	p := client.AnalyzeParams{
		Name:      analyzer,
		Symbol:    r.URL.Query().Get("symbol"),
		Direction: r.URL.Query().Get("direction"),
	}
	if mn := r.URL.Query().Get("max-nodes"); mn != "" {
		v, err := strconv.Atoi(mn)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "bad max-nodes")
			return
		}
		p.MaxNodes = v
	}
	raw, err := s.client.Analyze(r.Context(), p)
	if err != nil {
		// Emit the shared sanitized error envelope BEFORE switching to the SSE
		// content type, so an unavailable/failed analysis is a normal REST error.
		writeErrSanitized(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	var seq uint64
	if !writeFrame(w, flusher, &seq, "ready", []byte(fmt.Sprintf(`{"schema_version":%d}`, SchemaVersion))) {
		return
	}
	if !writeFrame(w, flusher, &seq, "analysis", raw) {
		return
	}
	writeFrame(w, flusher, &seq, "bye", []byte("{}"))
}

// writeFrame writes one SSE frame (event:<type>, id:<*seq incremented>,
// data:<payload>) and flushes. It returns false if the write failed (client
// gone), so the caller can stop streaming. The id counter is per-connection and
// monotonic.
func writeFrame(w http.ResponseWriter, flusher http.Flusher, seq *uint64, event string, data []byte) bool {
	refreshWriteDeadline(w)
	*seq++
	if _, err := fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event, *seq, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// refreshWriteDeadline keeps long-lived SSE connections compatible with the
// server-wide WriteTimeout while retaining a bounded deadline for each frame.
// Recorders and wrapper writers may not support deadlines; that is harmless.
func refreshWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(httpWriteTimeout))
}

// writeEnvelope writes a successful data response: the engine bytes embedded
// verbatim inside the versioned envelope. An encode failure is logged (the
// client may already have a partial 200) rather than silently swallowed.
func writeEnvelope(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(envelope{SchemaVersion: SchemaVersion, Payload: json.RawMessage(raw)}); err != nil {
		log.Printf("http: writeEnvelope encode: %v", err)
	}
}

// writeJSON writes a bare JSON object (used by /healthz).
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("http: writeJSON encode: %v", err)
	}
}

// writeErr writes the shared errorEnvelope (AC-5) with an explicit status, a
// stable machine code, and an already-safe message. Callers MUST NOT pass a raw
// engine error string here; use writeErrSanitized for engine/daemon errors so
// 5xx detail is mapped and never leaks.
func writeErr(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(errorEnvelope{
		SchemaVersion: SchemaVersion,
		Error:         errBody{Code: errCode, Message: msg},
	}); err != nil {
		log.Printf("http: writeErr encode: %v", err)
	}
}

// writeErrSanitized maps an engine/daemon error to its documented status + code
// + sanitized message (AC-5) and writes the shared errorEnvelope. For 500s the
// real error is logged locally and NEVER written to the client.
func writeErrSanitized(w http.ResponseWriter, err error) {
	status, code, msg := mapError(err)
	if status >= 500 {
		log.Printf("http: %d %s: %v", status, code, err) // detail stays local
	}
	writeErr(w, status, code, msg)
}

// --- Wiki (SW-041) ---------------------------------------------------------

// wikiDoc lazily generates and caches the self-generated wiki from the attached
// store. On a nil store it returns a zero-value Wiki with wikiErr set so the
// HTTP handlers can 404 consistently.
func (s *Server) wikiDoc() (wiki.Wiki, error) {
	s.wikiOnce.Do(func() {
		if s.store == nil {
			s.wikiErr = errors.New("wiki disabled (no store attached)")
			return
		}
		s.wikiGenerated, s.wikiErr = wiki.Generate(context.Background(), s.store)
	})
	return s.wikiGenerated, s.wikiErr
}

// serveWikiSPA reports whether the request is a browser DOCUMENT navigation
// (Accept: text/html) while the embedded UI is available; if so it serves the
// SPA shell and returns true. This mirrors the vite dev-server /wiki bypass:
// a deep link / page reload of /wiki* must land in the app (which then fetches
// the data with Accept: text/markdown), not show raw markdown bytes.
func serveWikiSPA(w http.ResponseWriter, r *http.Request) bool {
	if !webui.Enabled() || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	http.ServeFileFS(w, r, webui.FS, "index.html")
	return true
}

// handleWikiIndex serves the wiki index page as Markdown (text/markdown).
func (s *Server) handleWikiIndex(w http.ResponseWriter, r *http.Request) {
	if serveWikiSPA(w, r) {
		return
	}
	doc, err := s.wikiDoc()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "wiki unavailable")
		return
	}
	writeMarkdown(w, doc.Index.Body)
}

// handleWikiPage serves one community page as Markdown. Unknown id → 404.
func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
	if serveWikiSPA(w, r) {
		return
	}
	doc, err := s.wikiDoc()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "wiki unavailable")
		return
	}
	p, ok := doc.PageByID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "unknown community")
		return
	}
	writeMarkdown(w, p.Body)
}

// writeMarkdown writes a Markdown body with the correct content type.
func writeMarkdown(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
