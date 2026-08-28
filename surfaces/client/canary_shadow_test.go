package client

// SW-245 — the properties the deferral must not break.
//
// The latency evidence (AC-1) and the residual CPU/allocation accounting (AC-6)
// live in canary_latency_test.go beside the SW-242/SW-244 instruments they
// reuse. What is here is everything the move could silently cost: the caller's
// result (AC-2), the finding's durability (AC-3), and the coverage disclosure
// that keeps an observation count from being read as full coverage (AC-4).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// gatedCanaryStub is a canary client that answers the CALLER immediately and
// blocks the deferred comparison until the test releases it.
//
// It is what makes "the caller returned while the comparison was still running"
// an assertion rather than a hope: with the executor pass pinned open, any
// dispatch that returns has provably not waited for it.
//
// # How the two paths are told apart
//
// Both paths call the same method with the same arguments — that is the whole
// point of the comparison, so nothing in the request distinguishes them. The
// CONTEXT does: deferCanaryComparison hands the worker
// context.WithoutCancel(callerCtx), whose Done channel is nil by construction,
// while every dispatch below passes a cancellable context whose Done channel is
// not. So `ctx.Done() == nil` identifies the deferred pass exactly, and the
// discriminator doubles as a standing check that the worker's context really is
// detached — if a future change handed the caller's own context through,
// the gate would stop firing and these tests would fail rather than quietly
// stop testing anything.
type gatedCanaryStub struct {
	Client
	gate    chan struct{}
	entered chan struct{}

	mu    sync.Mutex
	calls int
}

func newGatedCanaryStub() *gatedCanaryStub {
	return &gatedCanaryStub{gate: make(chan struct{}), entered: make(chan struct{}, 4*shadowQueueCapacity)}
}

func (s *gatedCanaryStub) DeadCode(ctx context.Context, _ DeadCodeParams) ([]byte, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if ctx.Done() != nil {
		// The caller's own pass. It is the caller's answer and must never block.
		return []byte(`{"legacy":true}`), nil
	}
	s.entered <- struct{}{}
	<-s.gate
	return []byte(`{"legacy":true}`), nil
}

// gatedContext is the cancellable context the tests dispatch with, so the stub
// above can tell the caller's pass from the deferred one.
func gatedContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// release opens the gate exactly once, however the test got here.
func (s *gatedCanaryStub) release() {
	select {
	case <-s.gate:
	default:
		close(s.gate)
	}
}

func (s *gatedCanaryStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestSW245_ShadowReturnsWithoutWaitingForTheComparison is AC-2's new half.
//
// AC-2 already had "the caller receives the legacy bytes" (TestCanary_
// ShadowReturnsTheLegacyResult). What SW-245 adds is the clause that the result
// is "not affected by whether the comparison has completed" — including the case
// the story names explicitly, where the comparison is still IN FLIGHT when the
// caller returns. That case did not exist before this story: the comparison was
// always complete, because the caller had waited for it.
//
// The proof is a pinned executor pass. If DispatchOperation returns while the
// second call is parked inside the stub, it cannot have waited for it, and the
// bytes it returned are the legacy ones.
func TestSW245_ShadowReturnsWithoutWaitingForTheComparison(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)

	stub := newGatedCanaryStub()
	defer stub.release()

	ctx := gatedContext(t)
	done := make(chan struct{})
	var got []byte
	var err error
	go func() {
		got, err = DispatchOperation(ctx, stub, &DeadCodeArgs{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("DispatchOperation did not return while the comparison was in flight — the " +
			"dual run is still on the caller's critical path")
	}
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if want := []byte(`{"legacy":true}`); !bytes.Equal(got, want) {
		t.Fatalf("caller received %s, want the legacy result %s", got, want)
	}

	// And the comparison really was still running: the executor pass is parked
	// in the stub right now.
	select {
	case <-stub.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the deferred comparison never started; the second path is not running at all")
	}
	if n := stub.callCount(); n != 2 {
		t.Fatalf("the client saw %d call(s) while the comparison was in flight, want 2", n)
	}

	stub.release()
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("two identical answers recorded %d mismatch(es): %s", count, last)
	}
}

// TestSW245_ShadowBytesAreIdenticalToLegacyBytes is AC-2's byte-for-byte
// clause, asserted across the positions rather than within one.
//
// The same call, same arguments, same client, in `legacy` and in `shadow`: the
// caller's bytes must be identical. It is a cheap test and it is the one that
// would catch the plausible mistake — a deferral that returns a copy, a
// re-serialisation, or a truncation of what it kept for the comparison.
func TestSW245_ShadowBytesAreIdenticalToLegacyBytes(t *testing.T) {
	withCleanCanaryRecorder(t)
	direct, _ := executorFixture(t)

	var perMode = map[CanaryMode][]byte{}
	for _, mode := range []CanaryMode{CanaryModeLegacy, CanaryModeShadow} {
		func() {
			withCanaryMode(t, mode)
			out, err := DispatchOperation(context.Background(), direct, &DeadCodeArgs{MaxItems: 5})
			if err != nil {
				t.Fatalf("%q: DispatchOperation: %v", mode, err)
			}
			perMode[mode] = out
		}()
	}
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}
	if !bytes.Equal(perMode[CanaryModeLegacy], perMode[CanaryModeShadow]) {
		t.Fatalf("shadow returned different bytes from legacy:\n  legacy: %s\n  shadow: %s",
			perMode[CanaryModeLegacy], perMode[CanaryModeShadow])
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("the fixture's two paths diverged: %d — %s", count, last)
	}
}

// TestSW245_MutatingTheCallersBytesDoesNotCorruptTheComparison is the reason
// canary_shadow.go copies the legacy result.
//
// A caller owns the slice DispatchOperation hands back. If the deferred
// comparison held the SAME backing array, a caller that appended to or wrote
// through its own return value would be racing the comparison — and, worse,
// could turn an agreeing pair into a recorded "bytes" divergence that never
// happened. The test writes over every byte the caller received, then requires
// the comparison to still find the two paths identical.
func TestSW245_MutatingTheCallersBytesDoesNotCorruptTheComparison(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)

	stub := newGatedCanaryStub()
	defer stub.release()

	got, err := DispatchOperation(gatedContext(t), stub, &DeadCodeArgs{})
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	for i := range got {
		got[i] = 'X'
	}
	stub.release()
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("scribbling on the caller's own return value produced %d mismatch(es): %s\n"+
			"the deferred comparison is sharing the caller's buffer", count, last)
	}
}

// TestSW245_DeferredMismatchIsRecordedWithFullFidelity is AC-3 at this level:
// a divergence found off the critical path is recorded exactly as one found on
// it — same kind, same renderings, same in-process counter, same hand-off to
// the durable recorder (which flushes a mismatch immediately; internal/
// divergence owns that half and tests it).
func TestSW245_DeferredMismatchIsRecordedWithFullFidelity(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	stub := &canaryStub{answer: func(call int, _ DeadCodeParams) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"call":%d}`, call)), nil
	}}
	if _, err := DispatchOperation(context.Background(), stub, &DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}

	observations := recorder.observed(t)
	if len(observations) != 1 {
		t.Fatalf("recorded %d observation(s), want 1", len(observations))
	}
	obs := observations[0]
	if !obs.mismatch || obs.kind != "bytes" {
		t.Fatalf("observation = %+v, want a `bytes` mismatch", obs)
	}
	if obs.legacy == "" || obs.executor == "" {
		t.Fatalf("the deferred comparison lost a rendering: %+v", obs)
	}
	if count, last := CanaryMismatches(); count != 1 || last.Kind != "bytes" {
		t.Fatalf("in-process recorder (%d, %q) disagrees with the deferred observation", count, last.Kind)
	}
	if skipped, reasons := CanarySkipped(); skipped != 0 {
		t.Fatalf("a comparison that ran was also counted as skipped: %d %v", skipped, reasons)
	}
}

// TestSW245_ExecutorUnavailableIsStillObservedFromTheWorker: building the
// Executor moved off the caller's thread with the comparison, so the
// "executor-unavailable" divergence — the seam could not even construct the
// path it was asked to compare — now has to be raised by the worker. If it were
// dropped there, an operator would read a seam that looks untouched rather than
// one that failed to run.
func TestSW245_ExecutorUnavailableIsStillObservedFromTheWorker(t *testing.T) {
	withCleanCanaryRecorder(t)
	recorder := withDivergenceRecorder(t)

	// A nil Client is the one Executor construction failure reachable from a
	// fixture (NewExecutor's own guard), and it is now reachable through the
	// real deferred path because the legacy call no longer needs the client to
	// have survived it.
	runShadowComparison(shadowJob{
		ctx:         context.Background(),
		client:      nil,
		args:        &DeadCodeArgs{},
		operation:   CanaryOperation,
		legacyBytes: []byte(`{"ok":true}`),
	})

	observations := recorder.observed(t)
	if len(observations) != 1 {
		t.Fatalf("recorded %d observation(s), want 1", len(observations))
	}
	if obs := observations[0]; !obs.mismatch || obs.kind != "executor-unavailable" {
		t.Fatalf("observation = %+v, want an executor-unavailable mismatch", obs)
	}
	if count, _ := CanaryMismatches(); count != 1 {
		t.Fatalf("in-process mismatches = %d, want 1", count)
	}
}

// TestSW245_QueueFullIsDisclosedNotSwallowed is AC-4's load half.
//
// The queue is bounded, so a caller that outruns the worker DOES lose
// comparisons — the story permits that and forbids hiding it. This test pins
// the worker open, pushes past the bound, and requires every lost comparison to
// be counted in process AND reported to the durable recorder with a reason.
//
// It also requires the CALLER to be unharmed: a full queue is a diagnostic
// problem, never an outage.
func TestSW245_QueueFullIsDisclosedNotSwallowed(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	stub := newGatedCanaryStub()
	defer stub.release()

	// One dispatch to occupy the worker, then the queue's capacity to fill it,
	// then a surplus that cannot fit.
	const surplus = 5
	ctx := gatedContext(t)
	total := 1 + shadowQueueCapacity + surplus
	for i := 0; i < total; i++ {
		got, err := DispatchOperation(ctx, stub, &DeadCodeArgs{})
		if err != nil {
			t.Fatalf("dispatch %d failed because the comparison queue was full: %v", i, err)
		}
		if want := []byte(`{"legacy":true}`); !bytes.Equal(got, want) {
			t.Fatalf("dispatch %d returned %s, want the legacy answer %s", i, got, want)
		}
	}

	skipped, reasons := CanarySkipped()
	if skipped < surplus {
		t.Fatalf("%d dispatch(es) past a %d-deep queue with the worker pinned produced only %d "+
			"skip(s); comparisons are being lost without being counted",
			total, shadowQueueCapacity, skipped)
	}
	if reasons[shadowSkipQueueFull] != skipped {
		t.Fatalf("skip reasons = %v, want all %d under %q", reasons, skipped, shadowSkipQueueFull)
	}

	// The durable record must have been told the same thing: a coverage gap
	// only the process knows about is not a disclosure.
	stub.release()
	var recorded int
	for _, s := range recorder.skipped(t) {
		if s.operation != CanaryOperation {
			t.Errorf("a skip was recorded against %q, not the dispatched operation", s.operation)
		}
		if s.reason != shadowSkipQueueFull {
			t.Errorf("skip reason = %q, want %q", s.reason, shadowSkipQueueFull)
		}
		recorded += s.count
	}
	if recorded != skipped {
		t.Fatalf("the recorder was told about %d skip(s), the process counted %d", recorded, skipped)
	}
}

// TestSW245_DrainAbandonmentIsDisclosed is AC-3's failure branch and AC-4's
// shutdown half.
//
// A drain that runs out of budget is a real possibility, and the wrong answer
// to it is silence. The queued comparisons it gives up on are counted as
// skipped, for the same reason a queue-full drop is, and the drain says how
// many rather than returning nil.
func TestSW245_DrainAbandonmentIsDisclosed(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)

	stub := newGatedCanaryStub()
	defer stub.release()

	const queued = 4
	gated := gatedContext(t)
	for i := 0; i < 1+queued; i++ {
		if _, err := DispatchOperation(gated, stub, &DeadCodeArgs{}); err != nil {
			t.Fatalf("DispatchOperation %d: %v", i, err)
		}
	}
	// Wait until the worker is definitely holding one, so the rest are queued.
	select {
	case <-stub.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never picked a job up")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := DrainCanaryShadow(ctx)
	if err == nil {
		t.Fatal("a drain that abandoned queued comparisons reported success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("drain error %v does not carry the cause", err)
	}

	skipped, reasons := CanarySkipped()
	if skipped != queued {
		t.Fatalf("abandoned %d queued comparison(s) but counted %d skip(s) %v", queued, skipped, reasons)
	}
	if reasons[shadowSkipDrainAbandoned] != queued {
		t.Fatalf("skip reasons = %v, want %d under %q", reasons, queued, shadowSkipDrainAbandoned)
	}

	stub.release()
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow after release: %v", err)
	}
}

// TestSW245_ACompletedDrainLosesNothing is the other side of the test above,
// and the one Runtime.Close depends on: with budget, the drain waits for every
// accepted comparison and abandons none.
func TestSW245_ACompletedDrainLosesNothing(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	direct, _ := executorFixture(t)
	const calls = 12
	for i := 0; i < calls; i++ {
		if _, err := DispatchOperation(context.Background(), direct, &DeadCodeArgs{}); err != nil {
			t.Fatalf("DispatchOperation %d: %v", i, err)
		}
	}
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}
	if got := len(recorder.observed(t)); got != calls {
		t.Fatalf("%d dispatch(es) produced %d observation(s) after a completed drain", calls, got)
	}
	if skipped, reasons := CanarySkipped(); skipped != 0 {
		t.Fatalf("a completed drain still lost %d comparison(s) %v", skipped, reasons)
	}
}

// TestSW245_ConcurrentDispatchIsRaceCleanAndComplete runs the seam the way a
// server does — many callers at once — because that is the shape the deferral
// introduced and the one `-race` has to be pointed at.
//
// It asserts completeness as well as safety: every dispatch is either observed
// or counted as skipped, never neither. That equation is what makes the record's
// coverage figure meaningful, so it is checked rather than assumed.
func TestSW245_ConcurrentDispatchIsRaceCleanAndComplete(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	direct, _ := executorFixture(t)
	const callers, each = 8, 10

	var wg sync.WaitGroup
	for c := 0; c < callers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := DispatchOperation(context.Background(), direct, &DeadCodeArgs{}); err != nil {
					t.Errorf("DispatchOperation: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := DrainCanaryShadow(context.Background()); err != nil {
		t.Fatalf("DrainCanaryShadow: %v", err)
	}

	observed := len(recorder.observed(t))
	skipped, reasons := CanarySkipped()
	if observed+skipped != callers*each {
		t.Fatalf("%d dispatch(es) accounted for %d observation(s) + %d skip(s) = %d; a dispatch "+
			"that is neither compared nor disclosed is exactly the hole AC-4 forbids",
			callers*each, observed, skipped, observed+skipped)
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("concurrent dispatch produced %d mismatch(es): %s", count, last)
	}
	if skipped > 0 {
		t.Logf("SW-245: %d of %d comparisons were skipped under %d concurrent callers (%v) — "+
			"disclosed, not lost", skipped, callers*each, callers, reasons)
	}
}

// TestSW245_LegacyAndActivePositionsDeferNothing keeps the change confined to
// the position it is about. `legacy` must remain the untouched pre-AX-06 call,
// and `active` returns the executor's own result so it has nothing it COULD
// defer — a deferral there would be a kill switch answering from a goroutine
// the caller never hears from.
func TestSW245_LegacyAndActivePositionsDeferNothing(t *testing.T) {
	withCleanCanaryRecorder(t)
	direct, _ := executorFixture(t)

	for _, mode := range []CanaryMode{CanaryModeLegacy, CanaryModeActive} {
		t.Run(string(mode), func(t *testing.T) {
			withCanaryMode(t, mode)
			if _, err := DispatchOperation(context.Background(), direct, &DeadCodeArgs{}); err != nil {
				t.Fatalf("DispatchOperation: %v", err)
			}
			if n := canaryShadow.pending(); n != 0 {
				t.Fatalf("%q queued %d deferred comparison(s); only shadow defers", mode, n)
			}
		})
	}
}

// TestSW245_CancellingTheCallersContextDoesNotCancelTheComparison is the
// property that makes the deferral usable on a real surface at all.
//
// An HTTP handler's context is cancelled the moment the handler returns, and an
// MCP call's the moment the call completes — i.e. at almost exactly the instant
// the worker picks the job up. Handing the caller's context through would turn
// `shadow` into a machine for manufacturing false "error-presence" divergences
// out of its own success. So the job carries context.WithoutCancel: every value
// the caller's context held, none of its cancellation.
//
// The test cancels BEFORE the comparison runs — the worker is pinned — and then
// requires the comparison to complete and agree.
func TestSW245_CancellingTheCallersContextDoesNotCancelTheComparison(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	stub := newGatedCanaryStub()
	defer stub.release()

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := DispatchOperation(ctx, stub, &DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	select {
	case <-stub.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the deferred comparison never started")
	}
	// The caller is gone. Everything it owned is cancelled.
	cancel()

	stub.release()
	observations := recorder.observed(t)
	if len(observations) != 1 {
		t.Fatalf("recorded %d observation(s) after the caller's context was cancelled, want 1 — "+
			"a cancelled caller must not cancel the comparison it already paid for", len(observations))
	}
	if obs := observations[0]; obs.mismatch {
		t.Fatalf("the cancelled caller's context produced a divergence out of nothing: %+v", obs)
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("in-process mismatches = %d after cancellation: %s", count, last)
	}
}

// cancelledCallerStub is a legacy client that behaves the way every migrated
// operation actually behaves under a cancelled caller: it honours the context.
//
// The store reaches SQLite through QueryContext and the analysis engines return
// ctx.Err() from their own loops, so a caller that disconnects DURING the legacy
// call gets a context error back. The stub reproduces exactly that, and answers
// the deferred pass normally — because that pass runs on the detached context
// and therefore cannot be cancelled.
type cancelledCallerStub struct {
	Client
	mu    sync.Mutex
	calls int
	// cancel, when set, is invoked on the caller's pass: the client hangs up
	// while the legacy method is still running.
	cancel context.CancelFunc
	// legacyErr overrides ctx.Err() on the caller's pass, so a test can cover
	// the shape where the error is a WRAPPED context error the caller's own
	// context does not show (a per-call deadline inside the legacy path).
	legacyErr error
}

func (s *cancelledCallerStub) DeadCode(ctx context.Context, _ DeadCodeParams) ([]byte, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call > 1 {
		// The deferred executor pass. It succeeds, which is the whole hazard:
		// compared against the caller's `context canceled` it reads as an
		// error-presence divergence.
		return []byte(`{"legacy":true}`), nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.legacyErr != nil {
		return nil, s.legacyErr
	}
	return nil, ctx.Err()
}

func (s *cancelledCallerStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestSW245_ACancelledCallerIsNotComparedAndIsDisclosed is the regression the
// review found: the deferral must not manufacture a divergence out of a caller
// that walked away.
//
// Detaching the context (TestSW245_CancellingTheCallersContextDoesNotCancelThe
// Comparison) is right for the WORKER and wrong for the COMPARISON, because the
// two halves would then run under different cancellation semantics. When the
// caller's context dies during the legacy call the caller receives
// `context canceled`; the executor pass, on a context that cannot be cancelled,
// succeeds; compareCanaryOutcomes reads that pair as `error-presence`. That is
// a mismatch: flushed immediately, never coalesced away, permanent in the
// segment, and enough on its own to flip `graphi doctor -divergence` to
// DIVERGED — on evidence of nothing. Before SW-245 both passes shared the one
// cancelled context and agreed, so it would be a regression in what the record
// says.
//
// The fix is not to compare, and not to fall silent about it either: the
// dispatch is counted as a skip under `caller-cancelled`, which AC-4 renders as
// a coverage gap rather than as agreement.
//
// The sibling test above covers the OTHER ordering (legacy succeeded, then the
// caller cancelled) and cannot see this one.
func TestSW245_ACancelledCallerIsNotComparedAndIsDisclosed(t *testing.T) {
	cases := []struct {
		name string
		// build returns the dispatch context and the stub for one shape.
		build func(t *testing.T) (context.Context, *cancelledCallerStub)
		// wantErr is what DispatchOperation must hand back to the caller,
		// unchanged by any of this.
		wantErr error
	}{
		{
			// The common shape: an HTTP client disconnects, or an MCP session
			// tears down, while the legacy method is still working.
			name: "cancelled during the legacy call",
			build: func(t *testing.T) (context.Context, *cancelledCallerStub) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				return ctx, &cancelledCallerStub{cancel: cancel}
			},
			wantErr: context.Canceled,
		},
		{
			// The caller was already gone when the dispatch started.
			name: "cancelled before the dispatch",
			build: func(t *testing.T) (context.Context, *cancelledCallerStub) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, &cancelledCallerStub{}
			},
			wantErr: context.Canceled,
		},
		{
			// A deadline that fired inside the legacy path, wrapped on the way
			// out. The caller's own context is still live, so only errors.Is
			// can see it.
			name: "a wrapped deadline the caller's context does not show",
			build: func(t *testing.T) (context.Context, *cancelledCallerStub) {
				return context.Background(), &cancelledCallerStub{
					legacyErr: fmt.Errorf("dead_code: %w", context.DeadlineExceeded),
				}
			},
			wantErr: context.DeadlineExceeded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCleanCanaryRecorder(t)
			withCanaryMode(t, CanaryModeShadow)
			recorder := withDivergenceRecorder(t)

			ctx, stub := tc.build(t)
			got, err := DispatchOperation(ctx, stub, &DeadCodeArgs{})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("DispatchOperation err = %v, want %v — the caller's own outcome is untouched by any of this", err, tc.wantErr)
			}
			if got != nil {
				t.Fatalf("DispatchOperation returned %q alongside the error", got)
			}

			if obs := recorder.observed(t); len(obs) != 0 {
				t.Fatalf("a cancelled caller produced %d observation(s): %+v — "+
					"the legacy outcome is the caller giving up, not a result to judge the executor against", len(obs), obs)
			}
			if count, last := CanaryMismatches(); count != 0 {
				t.Fatalf("a cancelled caller manufactured %d in-process mismatch(es): %s", count, last)
			}
			if calls := stub.callCount(); calls != 1 {
				t.Fatalf("legacy client was called %d time(s), want 1 — no executor pass may run for an uncomparable outcome", calls)
			}

			// AC-4: not compared is not agreement. It is a disclosed gap.
			total, reasons := CanarySkipped()
			if total != 1 || reasons[shadowSkipCallerCancelled] != 1 {
				t.Fatalf("in-process skips = %d %v, want exactly 1 under %q", total, reasons, shadowSkipCallerCancelled)
			}
			skips := recorder.skipped(t)
			if len(skips) != 1 {
				t.Fatalf("recorder was told about %d skip(s), want 1: %+v", len(skips), skips)
			}
			if s := skips[0]; s.operation != CanaryOperation || s.count != 1 || s.reason != shadowSkipCallerCancelled {
				t.Fatalf("skip = %+v, want {%s 1 %s}", s, CanaryOperation, shadowSkipCallerCancelled)
			}
		})
	}
}

// TestSW245_ALiveCallerIsStillCompared is the non-vacuity half of the test
// above: the guard must key on the caller being gone, not on there being an
// error at all. A legacy error that is NOT a context error is still a
// comparable outcome and must still be compared — otherwise the fix would
// quietly stop comparing every failing call.
func TestSW245_ALiveCallerIsStillCompared(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	recorder := withDivergenceRecorder(t)

	stub := &canaryStub{answer: steady(nil, errors.New("dead_code: store is busy"))}
	if _, err := DispatchOperation(context.Background(), stub, &DeadCodeArgs{}); err == nil {
		t.Fatal("DispatchOperation err = nil, want the stub's error")
	}
	if obs := recorder.observed(t); len(obs) != 1 {
		t.Fatalf("recorded %d observation(s), want 1 — an ordinary legacy failure is still comparable", len(obs))
	}
	if total, reasons := CanarySkipped(); total != 0 {
		t.Fatalf("an ordinary legacy failure was skipped: %d %v", total, reasons)
	}
}
