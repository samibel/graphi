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
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/wiki"
	"github.com/samibel/graphi/surfaces/client"
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
