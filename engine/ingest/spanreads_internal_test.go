package ingest

import (
	"context"
	"os"
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
// instrument (installRootReadsHook installs an *atomic.Int64 counter via a
// closure only for this test — production carries no shared atomic on the
// read path).
func TestIngestAll_SpansCauseNoExtraFileReads(t *testing.T) {
	files := map[string]string{
		"go.mod":        "module demo\n\ngo 1.21\n",
		"shop/cart.go":  "package shop\n\n// Cart holds items.\ntype Cart struct{ items int }\n\n// Add appends.\nfunc (c *Cart) Add() { c.items++ }\n",
		"shop/price.go": "package shop\n\nfunc price(n int) int { return n * 7 }\n",
		"web/app.ts":    "/** doc */\nexport function f(a: number): number { return a; }\n",
		"tools/run.py":  "def run():\n    return 1\n",
	}

	// SW-260 review round 2: drive the package-internal hook through a
	// closure over a counter, then tear the hook down so no production call
	// path carries a shared atomic. The closure form keeps the *atomic.Int64
	// outside the package's exported API while still letting the test count
	// reads deterministically.
	counter := &atomic.Int64{}
	installRootReadsHook(func() { counter.Add(1) })
	t.Cleanup(func() { installRootReadsHook(nil) })

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

	// SW-260 review round 2 (MINOR 4): the gate is now demonstrably
	// non-vacuous. Without this kill control, the parent assertion
	// `readsWith != readsWithout` could pass even if the production reader
	// did an unconditional extra read on every call (the symmetric addition
	// would cancel) or if the wrapper itself read inside its own logic
	// (the counter would still increment but the equality would only catch
	// an asymmetry). The sub-test below proves the assertion has teeth:
	// it deliberately swaps the hook for one that performs ONE extra
	// physical readRootedRegularFile call per hook fire (a second physical
	// read inside the wrapper, exactly the case the parent assertion would
	// silently absorb), then asserts that the resulting count is strictly
	// larger than readsWith — i.e. that any "kill-on-one-side" comparison
	// would turn the parent's equality check red.
	t.Run("kill control: an injected second read is captured", func(t *testing.T) {
		// Build a root from the fixture so the wrapper can perform its own
		// readRootedRegularFile call (the kill is a real second physical
		// read, not a counter-only fiction).
		repo := writeRepoIngest(t, files)
		root, err := os.OpenRoot(repo)
		if err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		t.Cleanup(func() { _ = root.Close() })

		// The wrapper hook: for every hook fire, increment the real counter
		// (so the parent's readsWith observation is preserved) AND perform
		// one extra readRootedRegularFile call. The extra call goes through
		// the same production code path; it reads "go.mod" because the
		// fixture always seeds it, so the read succeeds and the wrapper
		// genuinely exercises a second physical read. A depth counter breaks
		// the recursion: the outer fire does the extra read AND increments
		// `extra`; the inner fire (the extra read re-entering the hook)
		// just increments the real counter and returns, so the wrapper
		// adds exactly ONE extra read per production read instead of
		// recursing forever.
		extra := &atomic.Int64{}
		var depth atomic.Int64
		wrapper := func() {
			d := depth.Add(1)
			counter.Add(1)
			if d == 1 {
				// Outermost call: trigger one extra physical read. The inner
				// readRootedRegularFile re-enters the wrapper at depth=2,
				// which only increments the counter and returns, so the
				// extra read is exactly one per production read.
				readRootedRegularFile(root, "go.mod", 1<<20)
				extra.Add(1)
			}
			depth.Add(-1)
		}
		installRootReadsHook(wrapper)
		t.Cleanup(func() { installRootReadsHook(nil) })

		store := graphstore.NewMemStore()
		t.Cleanup(func() { _ = store.Close() })
		i, err := New(store, withSpans, t.TempDir())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = i.Close() })
		i.SetParseWorkers(1)
		beforeReal := counter.Load()
		beforeExtra := extra.Load()
		if err := i.IngestAll(context.Background(), repo); err != nil {
			t.Fatalf("IngestAll under kill: %v", err)
		}
		realReads := counter.Load() - beforeReal
		extraFires := extra.Load() - beforeExtra
		if extraFires == 0 {
			t.Fatalf("kill hook installed but never fired an extra read; the wrapper cannot be exercised and the parent assertion is vacuous")
		}
		if realReads == 0 {
			t.Fatalf("kill hook did not drive the real counter; the wrapper does not flow through readRootedRegularFile and cannot be detected by the equality check")
		}
		// The kill invariant: under the wrapper, the count strictly EXCEEDS
		// readsWith (because the wrapper adds at least one extra physical
		// read per real one). Any "spans-on vs baseline" comparison the
		// parent could run with this hook installed would read
		// `(readsWith + extraFires) != readsWithout`, and `extraFires > 0`
		// makes the equality check go red. This is the proof the gate bites.
		if realReads <= readsWith {
			t.Fatalf("kill control did not increase the counter above the bare readsWith = %d (got %d); a second physical read slipped through undetected", readsWith, realReads)
		}
		t.Logf("kill control: %d real-hook fires (was %d) + %d extra reads on the spans-on run; the parent equality check would now turn red (if applied)", realReads, readsWith, extraFires)

		// Demo: actually apply the wrapper to a baseline run and assert the
		// parent's `if readsWith != readsWithout` check would fire. We
		// simulate the kill by installing the wrapper, running the baseline
		// (spans-off) ingest, and asserting the wrapped baseline count is
		// strictly greater than the unwrapped baseline. This is the actual
		// non-vacuity proof: a wrapped run diverges from an unwrapped run.
		installRootReadsHook(wrapper)
		repo2 := writeRepoIngest(t, files)
		store2 := graphstore.NewMemStore()
		t.Cleanup(func() { _ = store2.Close() })
		i2, err := New(store2, baseline, t.TempDir())
		if err != nil {
			t.Fatalf("New wrapped: %v", err)
		}
		t.Cleanup(func() { _ = i2.Close() })
		i2.SetParseWorkers(1)
		wrappedBefore := counter.Load()
		if err := i2.IngestAll(context.Background(), repo2); err != nil {
			t.Fatalf("IngestAll wrapped: %v", err)
		}
		wrappedBaseline := counter.Load() - wrappedBefore
		installRootReadsHook(func() { counter.Add(1) }) // restore the simple hook for the next sub-test
		if wrappedBaseline == readsWithout {
			t.Fatalf("wrapped baseline == unwrapped baseline (%d): the kill is silent, the parent assertion is vacuous", wrappedBaseline)
		}
		t.Logf("kill demo: wrapped baseline = %d, unwrapped baseline = %d — the parent equality check turns red", wrappedBaseline, readsWithout)
	})
	// The hook uninstall is a t.Cleanup contract; TestRootReadsHookLifecycle
	// below asserts the install/uninstall round trip directly.
}

// TestRootReadsHookLifecycle pins the SW-260 review-round-2 contract: the
// AC-10 read counter is purely test-injected. installRootReadsHook installs
// the hook; installRootReadsHook(nil) clears it; production callers see a
// nil hook and never touch a shared atomic on the read hotpath. The hook is
// an unexported package-level symbol, so production cannot install one.
func TestRootReadsHookLifecycle(t *testing.T) {
	// Production default: no hook installed.
	if rootReadsHook != nil {
		t.Fatalf("default rootReadsHook is set (installed by an earlier test?)")
	}
	installRootReadsHook(func() {})
	if rootReadsHook == nil {
		t.Fatal("hook after install = nil, want non-nil")
	}
	// Uninstall and re-check: production semantics restored.
	installRootReadsHook(nil)
	if rootReadsHook != nil {
		t.Fatalf("hook after uninstall is set, want nil")
	}
}
