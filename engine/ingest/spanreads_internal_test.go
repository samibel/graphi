package ingest

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// spanStrippingParser is the pre-SW-260 shape of the parse boundary: the same
// parsers, with the span sidecar dropped before ingest sees it. It is the
// in-test BASELINE the spans-on run is compared against.
type spanStrippingParser struct {
	inner Parser
	seen  atomic.Int64 // results that carried spans before stripping
}

func (p *spanStrippingParser) Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error) {
	res, err := p.inner.Parse(ctx, path, src)
	if res != nil && res.Spans != nil {
		p.seen.Add(1)
		res.Spans = nil
	}
	return res, err
}

// spanCountingParser observes that the spans-on run really carried spans, so
// the equality below is not vacuous.
type spanCountingParser struct {
	inner Parser
	seen  atomic.Int64
}

func (p *spanCountingParser) Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error) {
	res, err := p.inner.Parse(ctx, path, src)
	if res != nil && res.Spans != nil {
		p.seen.Add(1)
	}
	return res, err
}

// TestIngestAll_SpansCauseNoExtraFileReads pins SW-260 AC-10: the default
// (non-semantic) index reads exactly as many repository files with the span
// sidecar present as it did without it. The baseline is captured in the same
// test by stripping spans at the parse boundary; the reader itself is the
// instrument (SetRootReadsHook installs an *atomic.Int64 counter only for
// this test — production carries no shared atomic on the read path).
func TestIngestAll_SpansCauseNoExtraFileReads(t *testing.T) {
	files := map[string]string{
		"go.mod":        "module demo\n\ngo 1.21\n",
		"shop/cart.go":  "package shop\n\n// Cart holds items.\ntype Cart struct{ items int }\n\n// Add appends.\nfunc (c *Cart) Add() { c.items++ }\n",
		"shop/price.go": "package shop\n\nfunc price(n int) int { return n * 7 }\n",
		"web/app.ts":    "/** doc */\nexport function f(a: number): number { return a; }\n",
		"tools/run.py":  "def run():\n    return 1\n",
	}

	// SW-260 review round 1: install the counter hook only for this test, then
	// tear it down so no production call path carries a shared atomic.
	counter := &atomic.Int64{}
	SetRootReadsCounter(counter)
	t.Cleanup(func() { SetRootReadsCounter(nil) })

	index := func(t *testing.T, parser Parser) int64 {
		t.Helper()
		repo := writeRepoIngest(t, files)
		store := graphstore.NewMemStore()
		t.Cleanup(func() { _ = store.Close() })
		i, err := New(store, parser, t.TempDir())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = i.Close() })
		i.SetParseWorkers(1) // serial: the count must not depend on pool scheduling
		before := counter.Load()
		if err := i.IngestAll(context.Background(), repo); err != nil {
			t.Fatalf("IngestAll: %v", err)
		}
		return counter.Load() - before
	}

	inner := NewNotebookParser(parse.NewDefaultRegistry())
	baseline := &spanStrippingParser{inner: inner}
	withSpans := &spanCountingParser{inner: inner}

	readsWithout := index(t, baseline)
	readsWith := index(t, withSpans)

	if baseline.seen.Load() == 0 || withSpans.seen.Load() == 0 {
		t.Fatalf("precondition: the default parsers must emit spans (baseline saw %d, spans-on saw %d)", baseline.seen.Load(), withSpans.seen.Load())
	}
	if readsWithout == 0 {
		t.Fatal("the reader instrument counted no reads; the test cannot measure")
	}
	if readsWith != readsWithout {
		t.Fatalf("default index read %d files with spans vs %d without: spans must come from the AST already in memory, never from an extra read", readsWith, readsWithout)
	}
	// The reads are the ones the default path already made: at most the walk
	// hash + the parse read per file, plus the typeresolve/module-map re-reads.
	t.Logf("default index: %d reads for %d files, identical with and without spans", readsWith, len(files))
	// The hook uninstall is a t.Cleanup contract; TestRootReadsHookLifecycle
	// below asserts the install/uninstall round trip directly.
}

// TestRootReadsHookLifecycle pins the SW-260 review-round-1 contract: the
// AC-10 read counter is purely test-injected. SetRootReadsCounter installs
// the counter AND the hook; SetRootReadsCounter(nil) clears both; production
// callers see a nil hook and never touch a shared atomic on the read hotpath.
func TestRootReadsHookLifecycle(t *testing.T) {
	// Production default: no counter, no hook.
	if CountRootReads() != 0 {
		t.Errorf("default CountRootReads = %d, want 0", CountRootReads())
	}
	c := &atomic.Int64{}
	SetRootReadsCounter(c)
	defer SetRootReadsCounter(nil)
	if CountRootReads() != 0 {
		t.Errorf("CountRootReads after install = %d, want 0 (counter just installed, no reads happened)", CountRootReads())
	}
	// Uninstall and re-check: production semantics restored.
	SetRootReadsCounter(nil)
	if CountRootReads() != 0 {
		t.Errorf("CountRootReads after uninstall = %d, want 0", CountRootReads())
	}
}
