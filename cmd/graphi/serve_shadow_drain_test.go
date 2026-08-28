package main

// SW-245 review MINOR-2 — `graphi http` builds no Runtime, so nothing on that
// surface reaches Runtime.Close's drain. It dispatches through the same canary
// seam as MCP, though, so on the shipped `shadow` default it queues deferred
// comparisons like any other surface. runHTTP therefore drains them itself, on
// a defer registered AFTER the store's so it runs BEFORE it.

import (
	"context"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// httpDrainFixture is a Direct client that can answer dead_code, wrapped so the
// DEFERRED pass can be pinned. The two passes are told apart the way the seam
// arranges them: the caller dispatches with a cancellable context and the
// worker runs on context.WithoutCancel, so a nil Done() channel is the worker.
type httpDrainFixture struct {
	client.Client
	entered chan struct{}
	gate    chan struct{}
}

func (f *httpDrainFixture) DeadCode(ctx context.Context, p client.DeadCodeParams) ([]byte, error) {
	if ctx.Done() == nil {
		f.entered <- struct{}{}
		<-f.gate
	}
	return f.Client.DeadCode(ctx, p)
}

func (f *httpDrainFixture) release() {
	select {
	case <-f.gate:
	default:
		close(f.gate)
	}
}

func newHTTPDrainFixture(t *testing.T) *httpDrainFixture {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	for i := 0; i < 4; i++ {
		n, err := model.NewNode("function", "p.F"+string(rune('a'+i)), "p/f.go", i+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	inner := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	return &httpDrainFixture{Client: inner, entered: make(chan struct{}, 4), gate: make(chan struct{})}
}

// TestSW245_HTTPExitDrainsTheDeferredComparisons pins AC-3's exit guarantee on
// the surface that has no Runtime to close.
//
// Without the drain the process would return from runHTTP with comparisons
// still queued and then close the store underneath them — which is worse than
// losing them, because a comparison that runs against a closing store
// manufactures a false "error-presence" divergence out of the shutdown.
func TestSW245_HTTPExitDrainsTheDeferredComparisons(t *testing.T) {
	client.ResetCanaryMismatches()
	t.Cleanup(client.ResetCanaryMismatches)

	previous := client.CanaryModeDefault()
	if err := client.SetCanaryModeDefault(client.CanaryModeShadow); err != nil {
		t.Fatalf("SetCanaryModeDefault: %v", err)
	}
	t.Cleanup(func() {
		if err := client.SetCanaryModeDefault(previous); err != nil {
			t.Fatalf("restore canary mode: %v", err)
		}
	})

	fixture := newHTTPDrainFixture(t)
	defer fixture.release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := client.DispatchOperation(ctx, fixture, &client.DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the deferred comparison never started")
	}
	if pending := client.CanaryShadowPending(); pending == 0 {
		t.Fatal("nothing was outstanding, so this test would pass vacuously")
	}

	drained := make(chan struct{})
	go func() { drainCanaryShadowForExit(); close(drained) }()
	select {
	case <-drained:
		t.Fatal("the HTTP exit path returned while a comparison was still in flight — " +
			"the store would close underneath it")
	case <-time.After(200 * time.Millisecond):
	}

	fixture.release()
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("the HTTP exit drain never returned")
	}
	if pending := client.CanaryShadowPending(); pending != 0 {
		t.Fatalf("%d comparison(s) still outstanding after the exit drain", pending)
	}
	if skipped, reasons := client.CanarySkipped(); skipped != 0 {
		t.Fatalf("the exit drain abandoned %d comparison(s) %v", skipped, reasons)
	}
}
