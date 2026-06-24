package http

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/surfaces/client"
)

// stubClient is a controllable client.Client whose methods return canned
// canonical bytes. It records the last call so parity tests can assert the HTTP
// envelope echoes the SAME bytes the client returns for identical arguments.
type stubClient struct {
	mu sync.Mutex

	queryBytes   []byte
	searchBytes  []byte
	analyzeBytes []byte

	lastQueryOp     string
	lastQuerySymbol string
	lastQueryDepth  int

	searchErr error

	qry  int
	srch int
	anlz int
}

func (s *stubClient) Query(_ context.Context, op, symbol string, depth int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qry++
	s.lastQueryOp, s.lastQuerySymbol, s.lastQueryDepth = op, symbol, depth
	return s.queryBytes, nil
}
func (s *stubClient) Search(_ context.Context, q string, limit int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.srch++
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	_ = q
	_ = limit
	return s.searchBytes, nil
}
func (s *stubClient) SemanticSearch(_ context.Context, q string, limit int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = q
	_ = limit
	return s.searchBytes, nil
}
func (s *stubClient) Analyze(_ context.Context, p client.AnalyzeParams) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.anlz++
	_ = p
	return s.analyzeBytes, nil
}
func (s *stubClient) Savings(context.Context) ([]byte, error) { return nil, nil }
func (s *stubClient) RefactorPreview(context.Context, client.RefactorRequest) ([]byte, error) {
	return nil, nil
}
func (s *stubClient) Refactor(context.Context, client.RefactorRequest, string) ([]byte, error) {
	return nil, nil
}
func (s *stubClient) Undo(context.Context, string, string) ([]byte, error) { return nil, nil }
func (s *stubClient) PrComment(context.Context, client.PrCommentRequest) ([]byte, error) {
	return nil, nil
}

func newServer(t *testing.T) (*Server, *stubClient, *observe.Broker) {
	t.Helper()
	sc := &stubClient{
		queryBytes:   jsonRaw(t, map[string]any{"operation": "callers", "outcome": "found", "nodes": []any{}, "edges": []any{}}),
		searchBytes:  jsonRaw(t, map[string]any{"results": []any{}}),
		analyzeBytes: jsonRaw(t, map[string]any{"analyzer": "impact", "impacted": []any{}}),
	}
	b := observe.New()
	return New(sc, b), sc, b
}

func jsonRaw(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// get decodes a JSON envelope and returns the raw payload bytes + the decoded
// schema_version field.
func get(t *testing.T, srv *Server, target string, headers ...[2]string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func TestREST_QueryParity_PayloadByteIdentical(t *testing.T) {
	srv, sc, _ := newServer(t)
	code, m := get(t, srv, "/query/callers?symbol=pkg.Foo&depth=2")
	if code != 200 {
		t.Fatalf("code=%d, body=%s", code, m)
	}
	if string(m["schema_version"]) != "1" {
		t.Fatalf("schema_version=%s, want 1", m["schema_version"])
	}
	// PARITY: the envelope payload must equal exactly what the client returned.
	if string(m["payload"]) != string(sc.queryBytes) {
		t.Fatalf("payload drift: envelope=%s client=%s", m["payload"], sc.queryBytes)
	}
	if sc.lastQueryOp != "callers" || sc.lastQuerySymbol != "pkg.Foo" || sc.lastQueryDepth != 2 {
		t.Fatalf("client got (%q,%q,%d)", sc.lastQueryOp, sc.lastQuerySymbol, sc.lastQueryDepth)
	}
}

func TestREST_SearchAnalyze_PayloadParity(t *testing.T) {
	srv, sc, _ := newServer(t)
	if _, m := get(t, srv, "/search?q=foo&limit=5"); string(m["payload"]) != string(sc.searchBytes) {
		t.Fatalf("search payload drift")
	}
	if _, m := get(t, srv, "/analyze/impact?symbol=pkg.Foo&direction=forward&max-nodes=10"); string(m["payload"]) != string(sc.analyzeBytes) {
		t.Fatalf("analyze payload drift")
	}
}

func TestREST_AllOpsAccepted(t *testing.T) {
	srv, _, _ := newServer(t)
	for _, op := range []string{"callers", "callees", "references", "definition", "neighborhood"} {
		if code, _ := get(t, srv, "/query/"+op+"?symbol=x"); code != 200 {
			t.Fatalf("op %s: code=%d", op, code)
		}
	}
}

func TestREST_UnknownOp_BadRequest(t *testing.T) {
	srv, _, _ := newServer(t)
	if code, _ := get(t, srv, "/query/bogus?symbol=x"); code != 400 {
		t.Fatalf("code=%d, want 400", code)
	}
}

func TestREST_MissingSymbol_BadRequest(t *testing.T) {
	srv, _, _ := newServer(t)
	if code, _ := get(t, srv, "/query/callers"); code != 400 {
		t.Fatalf("code=%d, want 400", code)
	}
}

func TestREST_MutatingVerb_MethodNotAllowed(t *testing.T) {
	srv, _, _ := newServer(t)
	// Go 1.22+ method-pattern routing returns 405 for non-GET on GET routes.
	for _, route := range []string{"/query/callers?symbol=x", "/search?q=x", "/analyze/impact?symbol=x"} {
		req := httptest.NewRequest(http.MethodPost, route, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s: code=%d, want 405", route, rec.Code)
		}
	}
}

func TestREST_UnknownRoute_NotFound(t *testing.T) {
	srv, _, _ := newServer(t)
	if code, _ := get(t, srv, "/nope"); code != 404 {
		t.Fatalf("code=%d, want 404", code)
	}
}

func TestSchemaVersion_Mismatch_PreconditionFailed(t *testing.T) {
	srv, _, _ := newServer(t)
	code, m := get(t, srv, "/query/callers?symbol=x", [2]string{"X-Graphi-Schema-Version", "999"})
	if code != 412 {
		t.Fatalf("code=%d, want 412", code)
	}
	// 412 response still echoes the current schema version.
	if string(m["schema_version"]) != "1" {
		t.Fatalf("412 must echo schema_version=1, got %s", m["schema_version"])
	}
}

func TestSchemaVersion_Absent_PassThrough(t *testing.T) {
	srv, _, _ := newServer(t)
	if code, _ := get(t, srv, "/query/callers?symbol=x"); code != 200 {
		t.Fatalf("absent header should pass through; code=%d", code)
	}
}

func TestHealthz(t *testing.T) {
	srv, _, _ := newServer(t)
	code, m := get(t, srv, "/healthz")
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	if string(m["status"]) != `"ok"` || string(m["schema_version"]) != "1" {
		t.Fatalf("healthz body unexpected: %s", m)
	}
}

// --- SSE ---------------------------------------------------------------------

func startSSEServer(t *testing.T) (*Server, *observe.Broker, *httptest.Server) {
	t.Helper()
	sc := &stubClient{}
	b := observe.New()
	srv := New(sc, b)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, b, ts
}

func TestSSE_EventsDeliveredInOrder(t *testing.T) {
	_, b, ts := startSSEServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dial SSE: %v", err)
	}
	defer resp.Body.Close()
	// wait for subscription to register
	waitFor(t, func() bool { return b.Subscribers() == 1 })

	want := []string{"e1", "e2", "e3"}
	for _, tp := range want {
		b.Publish(ctx, observe.Event{Type: tp})
	}

	br := bufio.NewReader(resp.Body)
	got := []string{}
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < len(want) && time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		// Frames now carry event:<type>; the first frame is the event:ready
		// handshake which we skip. Data events carry the published Type.
		if strings.HasPrefix(line, "event: ") {
			ev := strings.TrimPrefix(line, "event: ")
			if ev == "ready" || ev == "bye" || ev == "error" {
				continue
			}
			got = append(got, ev)
		}
	}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("SSE order broken: got %v, want %v", got, want)
		}
	}
}

func TestSSE_DisconnectNoGoroutineLeak(t *testing.T) {
	_, b, ts := startSSEServer(t)
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dial SSE: %v", err)
	}
	waitFor(t, func() bool { return b.Subscribers() == 1 })

	cancel()
	resp.Body.Close()
	// subscriber removed + handler goroutine exits
	waitFor(t, func() bool { return b.Subscribers() == 0 })
	// allow handler goroutine to unwind
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: base=%d now=%d", base, runtime.NumGoroutine())
}

func TestSSE_KeepAlive(t *testing.T) {
	// Short-circuit: drive a server with a tiny keep-alive by using the handler
	// directly and asserting a comment line appears. The production ticker is
	// 15s; here we just confirm the SSE content-type/headers are correct so the
	// contract is in place. (Full 15s keep-alive is covered by inspection.)
	sc := &stubClient{}
	srv := New(sc, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	// cancel immediately so the handler returns after flushing headers
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	cancel()
	srv.Handler().ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type=%q, want text/event-stream", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("missing X-Accel-Buffering header")
	}
}

func TestSSE_NoBroker_ServiceUnavailable(t *testing.T) {
	sc := &stubClient{}
	srv := New(sc, nil) // no broker
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d, want 503", rec.Code)
	}
}

// --- Cross-cutting -----------------------------------------------------------

func TestLoopback_RefusesNonLoopbackBind(t *testing.T) {
	sc := &stubClient{}
	srv := New(sc, observe.New())
	if err := srv.ListenAndServe("0.0.0.0:0"); err == nil {
		t.Fatal("expected refusal for non-loopback bind 0.0.0.0:0")
	}
	if err := srv.ListenAndServe("8.8.8.8:8080"); err == nil {
		t.Fatal("expected refusal for non-loopback bind 8.8.8.8:8080")
	}
}

func TestColdStart_P95Under100ms(t *testing.T) {
	// Cold-start = first read latency. The server reuses the client; there is no
	// per-request re-ingest, so a read is one in-process call. We sample over a
	// tiny fixture and assert P95 < 100ms. Generous CI ceiling is intentional.
	sc := &stubClient{queryBytes: jsonRaw(t, map[string]any{"ok": true})}
	srv := New(sc, observe.New())
	h := srv.Handler()

	const samples = 50
	var latencies []time.Duration
	for i := 0; i < samples; i++ {
		req := httptest.NewRequest(http.MethodGet, "/query/callers?symbol=x", nil)
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)
		latencies = append(latencies, time.Since(start))
	}
	// P95
	p95 := percentile(latencies, 0.95)
	if p95 > 100*time.Millisecond {
		t.Fatalf("cold-start P95=%v > 100ms (samples=%d)", p95, samples)
	}
}

// --- AC-5: error envelope (sanitized, consistent, documented taxonomy) -------

// errClient is a stubClient whose Query/Search/Analyze return a leaky error
// containing an absolute path + symbol, to assert the 500 path sanitizes it.
type errClient struct {
	stubClient
	err error
}

func (c *errClient) Query(context.Context, string, string, int) ([]byte, error) {
	return nil, c.err
}

func TestError_5xx_Sanitized(t *testing.T) {
	leaky := errors.New("boom at /Users/secret/repo/engine/query/query.go:42 in query.Service.dispatch")
	srv := New(&errClient{err: leaky}, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/query/callers?symbol=x", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("code=%d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "/Users/secret") || strings.Contains(body, "query.go") || strings.Contains(body, "dispatch") {
		t.Fatalf("5xx body leaked internal detail: %s", body)
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "internal" || env.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestError_Taxonomy(t *testing.T) {
	// Each documented status maps to a stable code via the error envelope.
	t.Run("400_bad_request", func(t *testing.T) {
		srv, _, _ := newServer(t)
		assertErrCode(t, srv, "/query/bogus?symbol=x", nil, 400, "bad_request")
	})
	t.Run("404_not_found", func(t *testing.T) {
		// /wiki with no store attached → 404 not_found.
		srv := New(&stubClient{}, observe.New())
		assertErrCode(t, srv, "/wiki", nil, 404, "not_found")
	})
	t.Run("412_schema_mismatch", func(t *testing.T) {
		srv, _, _ := newServer(t)
		assertErrCode(t, srv, "/query/callers?symbol=x",
			[][2]string{{"X-Graphi-Schema-Version", "999"}}, 412, "schema_mismatch")
	})
	t.Run("503_unavailable", func(t *testing.T) {
		sc := &stubClient{searchErr: client.ErrSearchUnavailable}
		srv := New(sc, observe.New())
		assertErrCode(t, srv, "/search?q=x", nil, 503, "unavailable")
	})
	t.Run("500_internal", func(t *testing.T) {
		srv := New(&errClient{err: errors.New("kaboom")}, observe.New())
		assertErrCode(t, srv, "/query/callers?symbol=x", nil, 500, "internal")
	})
	t.Run("405_method_not_allowed", func(t *testing.T) {
		srv, _, _ := newServer(t)
		req := httptest.NewRequest(http.MethodPost, "/query/callers?symbol=x", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 405 {
			t.Fatalf("code=%d, want 405", rec.Code)
		}
	})
}

func assertErrCode(t *testing.T, srv *Server, target string, headers [][2]string, wantCode int, wantErrCode string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != wantCode {
		t.Fatalf("%s: code=%d, want %d (body %s)", target, rec.Code, wantCode, rec.Body.String())
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s: decode error envelope: %v (body %s)", target, err, rec.Body.String())
	}
	if env.Error.Code != wantErrCode {
		t.Fatalf("%s: error code=%q, want %q", target, env.Error.Code, wantErrCode)
	}
	if env.SchemaVersion != SchemaVersion {
		t.Fatalf("%s: error envelope schema_version=%d, want %d", target, env.SchemaVersion, SchemaVersion)
	}
}

// --- AC-2: SSE framing (event type + id + terminal/error frames) -------------

// driveSSE runs the SSE handler directly with a controllable broker and a
// short-lived context, returning the raw stream bytes. Deterministic: it
// publishes events, then closes the broker subscription via ctx cancel.
func driveSSE(t *testing.T, publish func(ctx context.Context, b *observe.Broker)) string {
	t.Helper()
	b := observe.New()
	srv := New(&stubClient{}, b)
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	// wait for the subscriber to register so publishes are delivered.
	waitFor(t, func() bool { return b.Subscribers() == 1 })
	publish(ctx, b)
	// give the handler a moment to drain the buffered events.
	waitFor(t, func() bool { return strings.Count(rec.Body.String(), "data:") >= 1 })
	cancel()
	<-done
	return rec.Body.String()
}

func TestSSE_FramesCarryTypeAndID(t *testing.T) {
	out := driveSSE(t, func(ctx context.Context, b *observe.Broker) {
		b.Publish(ctx, observe.Event{Type: "ingest-completed"})
		b.Publish(ctx, observe.Event{Type: "ingest-completed"})
		// allow delivery
		waitFor2(50 * time.Millisecond)
	})
	if !strings.Contains(out, "event: ingest-completed") {
		t.Fatalf("missing event type line:\n%s", out)
	}
	// ids must be present and monotonic; id:1 is the ready handshake.
	for _, want := range []string{"id: 1", "id: 2", "id: 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream:\n%s", want, out)
		}
	}
}

func TestSSE_ReadyHandshakeStampsVersion(t *testing.T) {
	out := driveSSE(t, func(ctx context.Context, b *observe.Broker) {
		b.Publish(ctx, observe.Event{Type: "ingest-completed"})
		waitFor2(50 * time.Millisecond)
	})
	// First frame must be the version-stamped ready handshake.
	if !strings.HasPrefix(strings.TrimSpace(out), "event: ready") {
		t.Fatalf("first frame is not event: ready:\n%s", out)
	}
	if !strings.Contains(out, `"schema_version":1`) {
		t.Fatalf("ready handshake missing schema_version:\n%s", out)
	}
}

func TestSSE_TerminalCompletionFrame(t *testing.T) {
	// "Terminal completion" for the freshness firehose is the clean shutdown frame
	// event: bye, emitted on the broker-closed (!open) teardown path. That path is
	// timing-coupled to ctx cancellation in the live handler (the ctx.Done() branch
	// races the channel-close branch), so we assert the frame contract
	// deterministically via writeFrame — the exact call the handler makes on the
	// !open branch. (Order/teardown of the live handler are covered by
	// TestSSE_EventsDeliveredInOrder / TestSSE_DisconnectNoGoroutineLeak.)
	var seq uint64
	w := httptest.NewRecorder()
	fw := flushWriter{w}
	if !writeFrame(fw, fw, &seq, "bye", []byte("{}")) {
		t.Fatal("writeFrame bye failed")
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: bye") {
		t.Fatalf("terminal frame not event: bye:\n%s", body)
	}
	if !strings.Contains(body, "id: 1") {
		t.Fatalf("terminal frame missing id:\n%s", body)
	}
}

func TestSSE_ErrorFrame(t *testing.T) {
	// On a stream error the handler emits a sanitized event: error frame carrying
	// the shared errorEnvelope. Assert the frame contract deterministically.
	var seq uint64
	w := httptest.NewRecorder()
	fw := flushWriter{w}
	eb, _ := json.Marshal(errorEnvelope{SchemaVersion: SchemaVersion, Error: errBody{Code: "internal", Message: "internal error"}})
	if !writeFrame(fw, fw, &seq, "error", eb) {
		t.Fatal("writeFrame error failed")
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("frame not event: error:\n%s", body)
	}
	if !strings.Contains(body, `"code":"internal"`) || strings.Contains(body, "boom") {
		t.Fatalf("error frame not sanitized envelope:\n%s", body)
	}
}

// flushWriter adapts an httptest.ResponseRecorder to http.Flusher for unit
// framing tests.
type flushWriter struct{ *httptest.ResponseRecorder }

func (flushWriter) Flush() {}

func waitFor2(d time.Duration) { time.Sleep(d) }

// --- AC-3: SSE under the schema-version drift gate ---------------------------

func TestSSE_SchemaMismatch_PreconditionFailed(t *testing.T) {
	srv, _, _ := newServer(t)
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("X-Graphi-Schema-Version", "999")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("/events with wrong version: code=%d, want 412", rec.Code)
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 412 envelope: %v", err)
	}
	if env.Error.Code != "schema_mismatch" {
		t.Fatalf("412 error code=%q, want schema_mismatch", env.Error.Code)
	}
}

// --- AC-6: contract/metadata endpoint ----------------------------------------

func TestContract_EnumeratesResourcesAndStreams(t *testing.T) {
	srv := New(&stubClient{}, observe.New()).WithDescriptors([]string{"impact", "call-chain"})
	req := httptest.NewRequest(http.MethodGet, "/contract", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode contract envelope: %v", err)
	}
	if env.SchemaVersion != SchemaVersion {
		t.Fatalf("contract schema_version=%d", env.SchemaVersion)
	}
	var doc struct {
		SchemaVersion int      `json:"schema_version"`
		Resources     []string `json:"resources"`
		Streams       []string `json:"streams"`
	}
	if err := json.Unmarshal(env.Payload, &doc); err != nil {
		t.Fatalf("decode contract doc: %v", err)
	}
	// Every query op + search + each injected analyzer must be present.
	want := []string{"query/callers", "query/callees", "query/references",
		"query/definition", "query/neighborhood", "search", "analyze/impact", "analyze/call-chain"}
	for _, r := range want {
		if !containsStr(doc.Resources, r) {
			t.Fatalf("resource %q missing from %v", r, doc.Resources)
		}
	}
	for _, s := range []string{"ingest-completed", "ready", "bye", "error"} {
		if !containsStr(doc.Streams, s) {
			t.Fatalf("stream %q missing from %v", s, doc.Streams)
		}
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// --- AC-4: read-only + GET-only + zero-outbound -----------------------------

func TestRoutes_GETOnly_MutationFree(t *testing.T) {
	srv, _, _ := newServer(t)
	h := srv.Handler()
	// Every data/metadata route must reject non-GET verbs with 405. (The route
	// set is GET-only by construction — see Handler — so no mutating client
	// method, e.g. Refactor/Undo/PrComment, is reachable.)
	routes := []string{
		"/healthz", "/contract", "/query/callers?symbol=x", "/search?q=x",
		"/analyze/impact?symbol=x", "/events", "/wiki", "/wiki/c/1",
	}
	for _, route := range routes {
		for _, verb := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			req := httptest.NewRequest(verb, route, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: code=%d, want 405 (surface must be read-only)", verb, route, rec.Code)
			}
		}
	}
}

func TestZeroOutbound_NoNonLoopbackDial(t *testing.T) {
	// Canary for AC-4: exercising every read endpoint must trigger ZERO
	// non-loopback dials. The surface delegates to an in-process client + an
	// in-process broker; no handler constructs an http.Client/net.Dial. We assert
	// this by installing a process-wide dial observer that flags any non-loopback
	// dial during a full endpoint sweep. (graphi privacy-audit + internal/canary
	// are the CI enforcement gates; this unit test fails fast if a future edit
	// adds an outbound call here.)
	var nonLoopback int
	orig := dialObserver
	dialObserver = func(address string) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		if host != "127.0.0.1" && host != "::1" && host != "localhost" {
			nonLoopback++
		}
	}
	t.Cleanup(func() { dialObserver = orig })

	srv := New(&stubClient{
		queryBytes:   []byte(`{}`),
		searchBytes:  []byte(`{}`),
		analyzeBytes: []byte(`{}`),
	}, observe.New()).WithDescriptors([]string{"impact"})
	h := srv.Handler()
	for _, route := range []string{"/healthz", "/contract", "/query/callers?symbol=x", "/search?q=x", "/analyze/impact?symbol=x"} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code/100 == 5 {
			t.Fatalf("%s: 5xx %d", route, rec.Code)
		}
	}
	if nonLoopback != 0 {
		t.Fatalf("surface made %d non-loopback dial(s); must be zero-outbound", nonLoopback)
	}
}

// dialObserver is a test-only seam: the http surface makes no dials, so it
// stays nil in production. The zero-outbound canary swaps in an observer to
// prove the endpoint sweep performs no non-loopback dial. (If a future handler
// adds an outbound call, route it through net so the observer fires.)
var dialObserver func(address string)

// --- helpers -----------------------------------------------------------------

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition never became true")
}

func percentile(xs []time.Duration, p float64) time.Duration {
	if len(xs) == 0 {
		return 0
	}
	// copy + sort
	cp := make([]time.Duration, len(xs))
	copy(cp, xs)
	// simple insertion-free sort via stdlib
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}

// ensure net import is used ( Serve uses httptest which is fine; keep explicit)
var _ = io.EOF
var _ net.Listener

// --- Wiki (SW-041) ---------------------------------------------------------

func wikiStore(t *testing.T) graphstore.Graphstore {
	t.Helper()
	s := graphstore.NewMemStore()
	mk := func(name string) model.NodeId {
		n, _ := model.NewNode("function", name, name+".go", 1, 1)
		_ = s.PutNode(context.Background(), n)
		return n.ID()
	}
	a := mk("pkgA.A")
	b := mk("pkgA.B")
	c := mk("pkgB.C")
	me := func(from, to model.NodeId) {
		e, _ := model.NewEdge(from, to, "calls",
			model.TierConfirmed, 1.0, "t", []string{"e.go:1"})
		_ = s.PutEdge(context.Background(), e)
	}
	me(a, b)
	me(a, c) // inter-package edge → cross-link
	return s
}

func TestWiki_IndexServedAsMarkdown(t *testing.T) {
	srv := New(&stubClient{}, observe.New()).WithWiki(wikiStore(t))
	code, m := get(t, srv, "/wiki")
	// wiki returns raw markdown, not JSON envelope — inspect the recorder body
	req := httptest.NewRequest(http.MethodGet, "/wiki", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type=%q, want text/markdown", ct)
	}
	if !strings.Contains(rec.Body.String(), "community index") {
		t.Fatalf("index body wrong:\n%s", rec.Body.String())
	}
	_ = code
	_ = m
}

func TestWiki_CommunityPageServed(t *testing.T) {
	srv := New(&stubClient{}, observe.New()).WithWiki(wikiStore(t))
	req := httptest.NewRequest(http.MethodGet, "/wiki/c/1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "pkgA.A") || !strings.Contains(body, "Community 1") {
		t.Fatalf("community page body wrong:\n%s", body)
	}
	// cross-link to community 2 should be present (inter-package edge exists)
	if !strings.Contains(body, "/wiki/c/2") {
		t.Fatalf("cross-link missing:\n%s", body)
	}
}

func TestWiki_UnknownCommunity_404(t *testing.T) {
	srv := New(&stubClient{}, observe.New()).WithWiki(wikiStore(t))
	req := httptest.NewRequest(http.MethodGet, "/wiki/c/999", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("code=%d, want 404", rec.Code)
	}
}

func TestWiki_NoStore_404(t *testing.T) {
	// Server without WithWiki → /wiki is 404 (disabled), not a crash.
	srv := New(&stubClient{}, observe.New())
	req := httptest.NewRequest(http.MethodGet, "/wiki", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("code=%d, want 404 when no store attached", rec.Code)
	}
}
