package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
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
