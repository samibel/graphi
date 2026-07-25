package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func toolsCallSearch(t *testing.T, id string) rpcRequest {
	t.Helper()
	return rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(id),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"search","arguments":{"symbol":"Hello"}}`),
	}
}

// TestSessionBinding_ColdIndexDoesNotStallInitialize pins the anti-spiral
// contract: a binder that takes arbitrarily long (a cold FULL index of a big
// workspace) must not stall initialize past the bind grace — clients kill and
// restart a server whose initialize times out, aborting and restarting the
// index every round. While the binder runs, tools fail closed with a
// retryable "still indexing" message; once it lands, tools serve normally.
func TestSessionBinding_ColdIndexDoesNotStallInitialize(t *testing.T) {
	release := make(chan struct{})
	server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
		select {
		case <-release:
			return Binding{Client: allToolsClient{}}, nil
		case <-ctx.Done():
			return Binding{}, ctx.Err()
		}
	}, WithBindGrace(20*time.Millisecond))
	defer server.Close()

	ctx := context.Background()
	start := time.Now()
	resp, _, _ := server.handle(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error while indexing: %+v", resp.Error)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("initialize stalled %v on a cold index; must answer within the bind grace", elapsed)
	}

	resp, _, _ = server.handle(ctx, toolsCallSearch(t, "2"))
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "still indexing") {
		t.Fatalf("tool during indexing must fail closed with a retryable message, got %+v", resp)
	}

	// tools/list must NOT fail closed while the index runs: clients list tools
	// once at session start and treat an error as a dead server. The static
	// profile catalog is served optimistically instead.
	resp, _, _ = server.handle(ctx, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("10"), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list during indexing must serve the optimistic catalog, got %+v", resp.Error)
	}
	if tools := resp.Result.(map[string]any)["tools"].([]map[string]any); len(tools) == 0 {
		t.Fatal("optimistic tools/list catalog is empty")
	}

	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, _, _ = server.handle(ctx, toolsCallSearch(t, "3"))
		if resp.Error == nil && len(resp.Result.(map[string]any)) != 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never became ready after the binder finished: %+v", resp.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSessionBinding_ToolsListChangedAfterBind pins the client-visible half
// of the optimistic catalog: a client that listed tools while the cold index
// was still running holds the static profile catalog, so once the binding
// lands the server must emit notifications/tools/list_changed on the same
// stdio stream — clients list tools exactly once at startup and would
// otherwise never converge on the bound (capability-narrowed) catalog.
func TestSessionBinding_ToolsListChangedAfterBind(t *testing.T) {
	release := make(chan struct{})
	server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
		select {
		case <-release:
			return Binding{Client: allToolsClient{}}, nil
		case <-ctx.Done():
			return Binding{}, ctx.Err()
		}
	}, WithBindGrace(0))
	defer server.Close()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(context.Background(), inR, outW)
		_ = outW.Close()
		serveErr <- err
	}()
	writeLine := func(line string) {
		t.Helper()
		if _, err := io.WriteString(inW, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	scanner := bufio.NewScanner(outR)
	readLine := func() string {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("stdio stream ended early: %v", scanner.Err())
		}
		return scanner.Text()
	}

	writeLine(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"file:///fixture/repo"}}`)
	if line := readLine(); !strings.Contains(line, `"protocolVersion"`) {
		t.Fatalf("unexpected initialize response: %s", line)
	}
	writeLine(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if line := readLine(); strings.Contains(line, `"error"`) || !strings.Contains(line, `"tools"`) {
		t.Fatalf("tools/list during indexing must serve the optimistic catalog: %s", line)
	}
	close(release)
	if line := readLine(); !strings.Contains(line, `"method":"notifications/tools/list_changed"`) {
		t.Fatalf("expected notifications/tools/list_changed after bind, got: %s", line)
	}
	writeLine(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if line := readLine(); strings.Contains(line, `"error"`) || !strings.Contains(line, `"tools"`) {
		t.Fatalf("bound tools/list failed: %s", line)
	}
	_ = inW.Close()
	if err := <-serveErr; err != nil {
		t.Fatal(err)
	}
}

// TestSessionBinding_BindStatusRendersLiveDetail pins the WithBindStatus
// seam: while a bind is in flight, the retryable -32002 renders the
// composition root's live detail between the stable prefix and suffix; an
// empty detail falls back to the historical static message, so servers
// without the seam (and clients matching the old text) see byte-identical
// errors.
func TestSessionBinding_BindStatusRendersLiveDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		detail string
		want   string
	}{
		{
			name:   "live detail",
			detail: "indexing /work/mono: parse 1234/5678 files (3m10s elapsed)",
			want:   "repository is not bound: indexing /work/mono: parse 1234/5678 files (3m10s elapsed); retry in a moment",
		},
		{
			name:   "empty detail falls back to the static message",
			detail: "",
			want:   "repository is not bound: the session is still indexing the repository; retry in a moment",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
				<-ctx.Done()
				return Binding{}, ctx.Err()
			}, WithBindGrace(0), WithBindStatus(func() string { return tc.detail }))
			defer server.Close()

			resp, _, _ := server.handle(context.Background(), rpcRequest{
				JSONRPC: "2.0",
				ID:      json.RawMessage("1"),
				Method:  "initialize",
				Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
			})
			if resp.Error != nil {
				t.Fatalf("initialize error: %+v", resp.Error)
			}
			resp, _, _ = server.handle(context.Background(), toolsCallSearch(t, "2"))
			if resp.Error == nil {
				t.Fatal("tools/call during an in-flight bind must fail closed")
			}
			if resp.Error.Code != -32002 {
				t.Fatalf("error code = %d, want -32002", resp.Error.Code)
			}
			if resp.Error.Message != tc.want {
				t.Fatalf("error message = %q, want %q", resp.Error.Message, tc.want)
			}
		})
	}
}

// TestSessionBinding_RebindWaitsForCancelledPredecessor pins the re-bind
// ordering fix: a roots-change cancels the in-flight attempt, but its binder
// may keep unwinding while it still holds the cross-process ingest lock. The
// replacement attempt must JOIN that predecessor instead of starting a second
// OpenSession that queues behind its own process's lock.
func TestSessionBinding_RebindWaitsForCancelledPredecessor(t *testing.T) {
	var (
		calls           atomic.Int32
		binder1Returned atomic.Bool
		overlap         atomic.Bool
	)
	release1 := make(chan struct{})
	server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
		switch calls.Add(1) {
		case 1:
			<-ctx.Done() // the roots change cancels this attempt...
			<-release1   // ...but the binder keeps unwinding (e.g. a held ingest lock)
			binder1Returned.Store(true)
			return Binding{}, ctx.Err()
		default:
			if !binder1Returned.Load() {
				overlap.Store(true)
			}
			return Binding{Client: allToolsClient{}}, nil
		}
	}, WithBindGrace(0))
	defer server.Close()

	ctx := context.Background()
	resp, _, _ := server.handle(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	server.handle(ctx, rpcRequest{JSONRPC: "2.0", Method: "notifications/roots/list_changed"})
	resp, _, _ = server.handle(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("re-initialize error: %+v", resp.Error)
	}

	// The replacement attempt must not run its binder while the predecessor is
	// still unwinding.
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("binder invoked %d times while the predecessor was still unwinding, want 1", got)
	}

	close(release1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, _, _ = server.handle(ctx, toolsCallSearch(t, "3"))
		if resp.Error == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never bound after the predecessor unwound: %+v", resp.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if overlap.Load() {
		t.Fatal("replacement binder ran before the cancelled predecessor returned")
	}
}

// TestSessionBinding_CancelDuringJoinResolvesAttempt pins publishBindFailure:
// an attempt cancelled while still JOINING its predecessor (its own binder
// never ran) must resolve the session to a bind error instead of leaving
// bindInFlight true — and therefore "still indexing" — forever.
func TestSessionBinding_CancelDuringJoinResolvesAttempt(t *testing.T) {
	var calls atomic.Int32
	release1 := make(chan struct{})
	server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
		calls.Add(1)
		<-ctx.Done()
		<-release1
		return Binding{}, ctx.Err()
	}, WithBindGrace(0))
	defer server.Close()
	defer close(release1)

	serveCtx, cancelServe := context.WithCancel(context.Background())
	resp, _, _ := server.handle(serveCtx, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	server.handle(serveCtx, rpcRequest{JSONRPC: "2.0", Method: "notifications/roots/list_changed"})
	resp, _, _ = server.handle(serveCtx, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("re-initialize error: %+v", resp.Error)
	}

	// Cancel the serve context while the replacement attempt is still joining
	// its predecessor; its binder must never run and the attempt must resolve.
	cancelServe()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, _, _ = server.handle(context.Background(), toolsCallSearch(t, "3"))
		if resp.Error != nil && !strings.Contains(resp.Error.Message, "still indexing") {
			if !strings.Contains(resp.Error.Message, "context canceled") {
				t.Fatalf("resolved bind error = %q, want a context cancellation", resp.Error.Message)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled joining attempt never resolved: %+v", resp.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("binder ran %d times, want 1 (the joining attempt's binder must never start)", got)
	}
}

// TestSessionBinding_CloseDiscardsLateBinding: a binding that lands after
// Close must be released and never adopted — otherwise a killed session
// leaks its Runtime (store, ingester) and serves over a closed session.
func TestSessionBinding_CloseDiscardsLateBinding(t *testing.T) {
	release := make(chan struct{})
	lateClosed := make(chan struct{})
	server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
		<-release
		return Binding{Client: allToolsClient{}, Close: func() { close(lateClosed) }}, nil
	}, WithBindGrace(0))

	resp, _, _ := server.handle(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	server.Close()
	close(release)
	select {
	case <-lateClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("late binding was not closed and discarded after Close")
	}
	if server.bound.Load() != nil {
		t.Fatal("closed session must never adopt a late binding")
	}
}

// TestSessionBinding_CloseCancelsInFlightIngest: closing the session must
// cancel the binder's context so a running full index stops burning CPU and
// memory for a session nobody is waiting on (the restart-spiral fuel).
func TestSessionBinding_CloseCancelsInFlightIngest(t *testing.T) {
	cancelled := make(chan struct{})
	server := NewServerWithBinder(func(ctx context.Context, _ []string) (Binding, error) {
		<-ctx.Done()
		close(cancelled)
		return Binding{}, ctx.Err()
	}, WithBindGrace(0))

	resp, _, _ := server.handle(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///fixture/repo"}`),
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	server.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the in-flight binder context")
	}
}
