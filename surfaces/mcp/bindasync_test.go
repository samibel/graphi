package mcp

import (
	"context"
	"encoding/json"
	"strings"
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
