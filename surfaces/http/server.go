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
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
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

// Server is the read-only HTTP REST + SSE surface. Construct with New and serve
// via ListenAndServe (loopback) or Serve (custom listener, for tests).
type Server struct {
	client client.Client
	broker *observe.Broker

	// Optional read-only store for serving the self-generated wiki (SW-041).
	// When set, /wiki and /wiki/c/{id} are enabled; nil disables them (404).
	store graphstore.Graphstore

	wikiOnce sync.Once
	wikiErr  error
	wikiGenerated  wiki.Wiki
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

// envelope wraps every data response so consumers can detect contract drift.
// Payload carries the engine's canonical serialized bytes verbatim (as
// json.RawMessage) so the wire bytes are byte-identical to MCP/CLI output.
type envelope struct {
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// Handler returns the routed http.Handler. Routing uses Go 1.22+ method-pattern
// matching: non-GET methods on data routes return 405 automatically, and unknown
// paths return 404.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /query/{op}", s.schemaGuard(s.handleQuery))
	mux.HandleFunc("GET /search", s.schemaGuard(s.handleSearch))
	mux.HandleFunc("GET /analyze/{analyzer}", s.schemaGuard(s.handleAnalyze))
	mux.HandleFunc("GET /events", s.handleSSE)
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

// schemaGuard rejects requests whose X-Graphi-Schema-Version header advertises an
// unsupported contract version (412 Precondition Failed), mirroring the EP-002
// schema-version drift gate. Absent header = no negotiation (pass through).
func (s *Server) schemaGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Graphi-Schema-Version"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n != SchemaVersion {
				writeErr(w, http.StatusPreconditionFailed,
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

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("op")
	if _, ok := queryOps[op]; !ok {
		writeErr(w, http.StatusBadRequest, "unknown query op: "+op)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "symbol required")
		return
	}
	depth := 0
	if d := r.URL.Query().Get("depth"); d != "" {
		v, err := strconv.Atoi(d)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad depth")
			return
		}
		depth = v
	}
	raw, err := s.client.Query(r.Context(), op, symbol, depth)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q required")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad limit")
			return
		}
		limit = v
	}
	raw, err := s.client.Search(r.Context(), q, limit)
	if err != nil {
		if errors.Is(err, client.ErrSearchUnavailable) {
			writeErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeEnvelope(w, raw)
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	analyzer := r.PathValue("analyzer")
	if analyzer == "" {
		writeErr(w, http.StatusBadRequest, "analyzer required")
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
			writeErr(w, http.StatusBadRequest, "bad max-nodes")
			return
		}
		p.MaxNodes = v
	}
	raw, err := s.client.Analyze(r.Context(), p)
	if err != nil {
		if errors.Is(err, client.ErrAnalysisUnavailable) {
			writeErr(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeEnvelope(w, raw)
}

// handleSSE streams broker events to the client as Server-Sent Events. It keeps
// the connection alive with comments, writes each event as a `data:` line, and
// tears down cleanly on client disconnect (the broker drops the subscriber via
// context cancellation — no goroutine or connection leak).
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if s.broker == nil {
		writeErr(w, http.StatusServiceUnavailable, "event stream unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ctx := r.Context()
	events := s.broker.Subscribe(ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ":keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-events:
			if !open {
				return // subscription ended (broker removed us)
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeEnvelope writes a successful data response: the engine bytes embedded
// verbatim inside the versioned envelope.
func writeEnvelope(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope{SchemaVersion: SchemaVersion, Payload: json.RawMessage(raw)})
}

// writeJSON writes a bare JSON object (used by /healthz).
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes a JSON error with the envelope schema version echoed.
func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg, "schema_version": SchemaVersion})
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
		writeErr(w, http.StatusNotFound, "wiki unavailable")
		return
	}
	writeMarkdown(w, doc.Index.Body)
}

// handleWikiPage serves one community page as Markdown. Unknown id → 404.
func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
	doc, err := s.wikiDoc()
	if err != nil {
		writeErr(w, http.StatusNotFound, "wiki unavailable")
		return
	}
	p, ok := doc.PageByID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown community")
		return
	}
	writeMarkdown(w, p.Body)
}

// writeMarkdown writes a Markdown body with the correct content type.
func writeMarkdown(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
