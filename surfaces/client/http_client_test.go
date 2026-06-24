package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
)

// seededStore returns an in-memory store with one node so queries resolve to a
// deterministic, non-error result for the parity assertion.
func seededStore(t *testing.T) graphstore.Graphstore {
	t.Helper()
	st := graphstore.NewMemStore()
	n, err := model.NewNode("function", "pkg.Foo", "foo.go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	if err := st.PutNode(context.Background(), n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	return st
}

// newRealServer spins up the genuine SW-044 handler over loopback httptest.
func newRealServer(t *testing.T, st graphstore.Graphstore) *httptest.Server {
	t.Helper()
	c := client.NewDirect(query.New(st), search.New(st))
	srv := httptest.NewServer(httpsrv.New(c, observe.New()).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func symbolID(t *testing.T, st graphstore.Graphstore) string {
	t.Helper()
	n, err := model.NewNode("function", "pkg.Foo", "foo.go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return string(n.ID())
}

// AC-2: the adapter's envelope-unwrapped bytes are byte-identical to what the
// in-process Direct client returns for the same query — parity by construction.
func TestHTTP_ParityWithDirect(t *testing.T) {
	st := seededStore(t)
	srv := newRealServer(t, st)
	sym := symbolID(t, st)

	direct := client.NewDirect(query.New(st), search.New(st))
	adapter, err := client.NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	ctx := context.Background()

	wantQ, err := direct.Query(ctx, "callers", sym, 0)
	if err != nil {
		t.Fatalf("direct.Query: %v", err)
	}
	gotQ, err := adapter.Query(ctx, "callers", sym, 0)
	if err != nil {
		t.Fatalf("adapter.Query: %v", err)
	}
	if string(gotQ) != string(wantQ) {
		t.Fatalf("query parity broken:\n direct=%s\nadapter=%s", wantQ, gotQ)
	}

	wantS, _ := direct.Search(ctx, "Foo", 10)
	gotS, err := adapter.Search(ctx, "Foo", 10)
	if err != nil {
		t.Fatalf("adapter.Search: %v", err)
	}
	if string(gotS) != string(wantS) {
		t.Fatalf("search parity broken:\n direct=%s\nadapter=%s", wantS, gotS)
	}
}

// AC-5: each failure mode maps to a distinct typed error with no panic.
func TestHTTP_TypedErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"bad_request", http.StatusBadRequest, `{"schema_version":1,"error":{"code":"bad_request","message":"symbol required"}}`, client.ErrBadInput},
		{"schema_mismatch", http.StatusPreconditionFailed, `{"schema_version":1,"error":{"code":"schema_mismatch","message":"schema version mismatch"}}`, client.ErrSchemaMismatch},
		{"unavailable", http.StatusServiceUnavailable, `{"schema_version":1,"error":{"code":"unavailable","message":"capability unavailable"}}`, client.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			adapter, err := client.NewHTTP(srv.URL)
			if err != nil {
				t.Fatalf("NewHTTP: %v", err)
			}
			_, err = adapter.Query(context.Background(), "callers", "x", 0)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// AC-5: a fully-down server (dial error) maps to ErrUnreachable, no panic.
func TestHTTP_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // now nothing is listening
	adapter, err := client.NewHTTP(addr)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	_, err = adapter.Query(context.Background(), "callers", "x", 0)
	if !errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("got %v, want ErrUnreachable", err)
	}
}

// R5: a server stamping a different envelope version is treated as a mismatch.
func TestHTTP_SchemaVersionDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":999,"payload":{}}`))
	}))
	defer srv.Close()
	adapter, _ := client.NewHTTP(srv.URL)
	_, err := adapter.Query(context.Background(), "callers", "x", 0)
	if !errors.Is(err, client.ErrSchemaMismatch) {
		t.Fatalf("got %v, want ErrSchemaMismatch on value drift", err)
	}
}

// AC-6 / local-first: a non-loopback base URL is refused at construction.
func TestHTTP_RejectsNonLoopback(t *testing.T) {
	for _, bad := range []string{"http://example.com:8080", "http://10.0.0.1:8080", "http://8.8.8.8"} {
		if _, err := client.NewHTTP(bad); err == nil {
			t.Fatalf("NewHTTP(%q) should have failed (non-loopback)", bad)
		}
	}
	for _, good := range []string{"http://127.0.0.1:8080", "http://localhost:9000", "http://[::1]:8080"} {
		if _, err := client.NewHTTP(good); err != nil {
			t.Fatalf("NewHTTP(%q) should succeed: %v", good, err)
		}
	}
}

// AC-6: the adapter issues ONLY GET, and mutation methods emit no request.
func TestHTTP_ReadOnly_OnlyGET(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"payload":{}}`))
	}))
	defer srv.Close()
	adapter, _ := client.NewHTTP(srv.URL)
	ctx := context.Background()
	_, _ = adapter.Query(ctx, "callers", "x", 0)
	_, _ = adapter.Search(ctx, "x", 10)
	_, _ = adapter.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: "x"})
	_, _ = adapter.Contract(ctx)
	for _, m := range methods {
		if m != http.MethodGet {
			t.Fatalf("adapter issued non-GET request: %s", m)
		}
	}

	// Mutation methods return typed unavailable and issue NO request.
	before := len(methods)
	if _, err := adapter.Refactor(ctx, client.RefactorRequest{}, "actor"); !errors.Is(err, client.ErrEditUnavailable) {
		t.Fatalf("Refactor: got %v, want ErrEditUnavailable", err)
	}
	if _, err := adapter.RefactorPreview(ctx, client.RefactorRequest{}); !errors.Is(err, client.ErrEditUnavailable) {
		t.Fatalf("RefactorPreview: got %v, want ErrEditUnavailable", err)
	}
	if _, err := adapter.Undo(ctx, "tok", "actor"); !errors.Is(err, client.ErrEditUnavailable) {
		t.Fatalf("Undo: got %v, want ErrEditUnavailable", err)
	}
	if _, err := adapter.PrComment(ctx, client.PrCommentRequest{}); !errors.Is(err, client.ErrReviewUnavailable) {
		t.Fatalf("PrComment: got %v, want ErrReviewUnavailable", err)
	}
	if len(methods) != before {
		t.Fatalf("mutation methods issued %d HTTP request(s); want 0", len(methods)-before)
	}
}

// SSE: the real SW-044 handler emits ready then (on broker close) bye; assert
// the adapter parses frames into typed Events.
func TestHTTP_SubscribeEvents(t *testing.T) {
	st := seededStore(t)
	broker := observe.New()
	c := client.NewDirect(query.New(st), search.New(st))
	srv := httptest.NewServer(httpsrv.New(c, broker).Handler())
	defer srv.Close()

	adapter, _ := client.NewHTTP(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := adapter.SubscribeEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeEvents: %v", err)
	}
	// First frame is the version-stamped ready handshake.
	ev, ok := <-ch
	if !ok || ev.Type != "ready" {
		t.Fatalf("first event: got %+v ok=%v, want ready", ev, ok)
	}
	if !strings.Contains(ev.Data, "schema_version") {
		t.Fatalf("ready frame missing schema_version: %q", ev.Data)
	}
	// The server sends the "ready" frame BEFORE it calls broker.Subscribe (see
	// surfaces/http/server.go: writeFrame "ready", then broker.Subscribe), so the
	// ready handshake is NOT a sync point for the broker subscription. Wait for the
	// subscription to register, otherwise the loss-tolerant Publish below can race
	// ahead of it and be dropped (the flake this guards against).
	deadline := time.Now().Add(2 * time.Second)
	for broker.Subscribers() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("server did not subscribe to broker within 2s after ready handshake")
		}
		time.Sleep(time.Millisecond)
	}

	// Publish a freshness event; the adapter should surface it as a typed Event.
	broker.Publish(ctx, observe.Event{Type: "ingest-completed"})
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("event channel closed before ingest-completed arrived")
		}
		if ev.Type != "ingest-completed" {
			t.Fatalf("got %+v, want ingest-completed", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ingest-completed event")
	}
}

// SSE: a non-loopback base URL never even gets to subscribe.
func TestHTTP_SubscribeEvents_LoopbackOnly(t *testing.T) {
	if _, err := client.NewHTTP("http://" + url.PathEscape("evil.example") + ":80"); err == nil {
		t.Fatal("expected non-loopback rejection")
	}
}
