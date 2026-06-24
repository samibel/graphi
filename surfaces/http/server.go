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
//	200 ok · 400 bad input · 404 unknown route/resource · 405 mutating verb ·
//	412 schema-version mismatch · 503 capability unavailable
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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/wiki"
	"github.com/samibel/graphi/surfaces/client"
)

// SchemaVersion is the envelope contract version stamped on every response and
// enforced (on request) via X-Graphi-Schema-Version. It versions the HTTP
// envelope shape consumed by the TS/React web client (SW-040) and VS Code
// extension (SW-043); the engine's internal payload versioning is separate.
const SchemaVersion = 1

// queryOps is the allow-list of structural query operations the REST surface
// accepts. It mirrors engine/query.Service's method set; the HTTP layer never
// invents new query semantics.
var queryOps = map[string]struct{}{
	"callers":      {},
	"callees":      {},
	"references":   {},
	"definition":   {},
	"neighborhood": {},
}

// streamDescriptors enumerates the SSE event types a client may observe on
// /events, for capability negotiation via /contract. "ingest-completed" is the
// freshness event published by the ingest producer; ready/bye/error are the
// framing events this surface emits (handshake, terminal, stream error).
var streamDescriptors = []string{"ingest-completed", "ready", "bye", "error"}

// Server is the read-only HTTP REST + SSE surface. Construct with New and serve
// via ListenAndServe (loopback) or Serve (custom listener, for tests).
type Server struct {
	client client.Client
	broker *observe.Broker

	// Optional read-only store for serving the self-generated wiki (SW-041).
	// When set, /wiki and /wiki/c/{id} are enabled; nil disables them (404).
	store graphstore.Graphstore

	// analyzers is the optional list of analyzer names exposed by /contract for
	// capability negotiation (AC-6). It is injected from cmd/graphi (which owns
	// the analysis.Service) so the http package never imports engine/analysis,
	// preserving the surface→engine layering. Empty when not wired.
	analyzers []string

	wikiOnce      sync.Once
	wikiErr       error
	wikiGenerated wiki.Wiki
}

// New constructs a Server over the given client and (optionally) an event broker.
// A nil broker disables the /events SSE endpoint (it returns 503).
func New(c client.Client, b *observe.Broker) *Server {
	return &Server{client: c, broker: b}
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
		errors.Is(err, client.ErrAnalysisUnavailable):
		return http.StatusServiceUnavailable, "unavailable", "capability unavailable"
	default:
		return http.StatusInternalServerError, "internal", "internal error"
	}
}

// Handler returns the routed http.Handler. Routing uses Go 1.22+ method-pattern
// matching: non-GET methods on data routes return 405 automatically, and unknown
// paths return 404.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /contract", s.handleContract)
	mux.HandleFunc("GET /query/{op}", s.schemaGuard(s.handleQuery))
	mux.HandleFunc("GET /search", s.schemaGuard(s.handleSearch))
	mux.HandleFunc("GET /search/semantic", s.schemaGuard(s.handleSemanticSearch))
	mux.HandleFunc("GET /analyze/{analyzer}", s.schemaGuard(s.handleAnalyze))
	mux.HandleFunc("GET /events", s.schemaGuard(s.handleSSE))
	mux.HandleFunc("GET /wiki", s.handleWikiIndex)
	mux.HandleFunc("GET /wiki/c/{id}", s.handleWikiPage)
	return mux
}

// ListenAndServe serves the HTTP surface on addr. Callers MUST pass a loopback
// address (e.g. "127.0.0.1:0" or "127.0.0.1:8080"); the zero-outbound,
// local-first contract binds loopback only. The call blocks until the server
// stops.
func (s *Server) ListenAndServe(addr string) error {
	if err := AssertLoopback(addr); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}

// Serve serves the HTTP surface on the given listener (tests). Blocks until the
// listener is closed.
func (s *Server) Serve(ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
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

// handleContract returns the contract/metadata document wrapped in the standard
// envelope. resources = sorted query ops + "search" + injected analyzer names;
// streams = the SSE event descriptors. It is the runtime mirror of the
// hand-authored contract.schema.json (the Go↔TS single source of truth).
func (s *Server) handleContract(w http.ResponseWriter, r *http.Request) {
	resources := make([]string, 0, len(queryOps)+1+len(s.analyzers))
	for op := range queryOps {
		resources = append(resources, "query/"+op)
	}
	sort.Strings(resources)
	resources = append(resources, "search")
	for _, a := range s.analyzers {
		resources = append(resources, "analyze/"+a)
	}
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

// writeFrame writes one SSE frame (event:<type>, id:<*seq incremented>,
// data:<payload>) and flushes. It returns false if the write failed (client
// gone), so the caller can stop streaming. The id counter is per-connection and
// monotonic.
func writeFrame(w http.ResponseWriter, flusher http.Flusher, seq *uint64, event string, data []byte) bool {
	*seq++
	if _, err := fmt.Fprintf(w, "event: %s\nid: %d\ndata: %s\n\n", event, *seq, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
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

// handleWikiIndex serves the wiki index page as Markdown (text/markdown).
func (s *Server) handleWikiIndex(w http.ResponseWriter, r *http.Request) {
	doc, err := s.wikiDoc()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "wiki unavailable")
		return
	}
	writeMarkdown(w, doc.Index.Body)
}

// handleWikiPage serves one community page as Markdown. Unknown id → 404.
func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
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
