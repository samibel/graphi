package client

// SW-245 — the dual run, off the caller's critical path.
//
// # The problem this file exists to solve
//
// Until SW-245 the `shadow` position ran the legacy method and then the
// executor path SYNCHRONOUSLY, on the goroutine that served the request, and
// only then returned the legacy bytes. Measured under SW-242's recalibrated
// method (docs/rc/ax06-canary-latency.md §6.2): p50 382.979 µs → 783.542 µs,
// 2.05× legacy, with a residue of +9.5 µs and exactly zero allocations once
// `legacy + executor` was subtracted. The cost was not seam overhead. It was
// simply that both paths ran before the caller heard anything.
//
// SW-244 made `shadow` the shipped default, so that 2.05× is what every MCP and
// HTTP call to a migrated operation pays, for every user, for as long as
// evidence is being gathered. This file removes the reason to hesitate: the
// caller returns on the legacy result the moment the legacy method answers, and
// the second path runs on a worker goroutine afterwards.
//
// # What is NOT traded away
//
// The story's "out of scope" section rejects three cheaper answers outright:
// comparing less, comparing more cheaply than byte-exactly, and dropping the
// comparison under load without disclosing it. None of them is taken here.
//
//   - Every shadow dispatch is queued for a FULL comparison. There is no
//     sampling rate, and there is no cheaper comparison — the worker calls the
//     same compareCanaryOutcomes, on the same canonical bytes, with the same
//     error-class check, that the synchronous path called.
//   - The queue is bounded, so a caller that outruns the worker DOES lose
//     comparisons. That is disclosed rather than hidden: every skipped
//     comparison is counted, per operation and per reason, reaches the durable
//     record through DivergenceRecorder.RecordSkipped, and is printed by
//     `graphi doctor -divergence` next to the observation count it qualifies.
//     An observation count can therefore never be read as full coverage when it
//     is not (AC-4).
//
// # Why a bounded queue and not an unbounded one
//
// An unbounded queue converts a caller that outruns the comparison into
// unbounded memory growth, and each queued job retains a COPY of the legacy
// result bytes (see below). A server under sustained load would grow until it
// died, which is a worse failure than a disclosed coverage gap. The bound is
// small on purpose (shadowQueueCapacity), and the cost of reaching it is a
// counted, printed skip.
//
// # Why the legacy bytes are copied
//
// The slice DispatchOperation returns belongs to the CALLER once it returns.
// A caller is entitled to append to it or reuse its backing array; the
// comparison reads it later, from another goroutine. Sharing it would make the
// caller's own use of its own return value a data race — precisely the kind of
// coupling AC-2 forbids ("not affected by whether the comparison has
// completed"). One clone per shadow dispatch is the price, it is on the
// critical path, and it is stated in the AC-6 accounting rather than omitted
// because it no longer shows up as a second full pass.
//
// # Why the context is detached
//
// The caller's context is cancelled the instant the HTTP handler returns or the
// MCP call completes. Handing it to the worker would cancel the comparison at
// almost exactly the moment the worker started it, turning shadow into a
// machine for manufacturing false "error-presence" divergences.
// context.WithoutCancel keeps every VALUE the caller's context carried — which
// is what the client adapters below may read — and drops only the cancellation
// and the deadline.
//
// # Why a cancelled caller is not compared
//
// Detaching is right for the worker and wrong for the comparison, because the
// two halves of a comparison would then run under different cancellation
// semantics. The legacy path honours the caller's context all the way down —
// the store's QueryContext, and the analysis engines' own ctx.Err() checks — so
// a caller that disconnects or times out DURING the legacy call receives
// `context canceled`. The executor pass, running on the detached context,
// cannot be cancelled and succeeds. compareCanaryOutcomes reads that pair as an
// `error-presence` divergence: legacy "context canceled" vs executor "ok".
//
// That would be a divergence manufactured by this file, not observed by it. It
// is a mismatch, so it is flushed immediately and never coalesced away; it is
// permanent in the segment; it flips `graphi doctor -divergence` to DIVERGED and
// blocks the migration precondition that reads it. Before SW-245 both passes
// shared the one cancelled context and agreed.
//
// So a dispatch whose caller was cancelled is NOT queued. The alternative —
// letting the worker inherit the cancellation — trades a false divergence for a
// different false divergence (both passes cancelled at unrelated moments) and
// loses the comparison anyway. Nor is it silently dropped: it is counted as a
// skip under `caller-cancelled`, which is a coverage gap under AC-4 and is
// reported as one. Absence of a comparison never reads as agreement.
//
// # Why exit drains rather than abandons (AC-3)
//
// A queued comparison that has not run yet holds a finding nobody has seen. The
// process must not walk away from it. DrainCanaryShadow blocks until the queue
// is empty and nothing is in flight; Runtime.Close calls it before it closes
// the store the comparison reads from, and then flushes the divergence store.
// A drain that runs out of budget does not fall silent either: the jobs it
// abandons are counted and recorded as skipped, for the same reason a queue-full
// drop is.

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// shadowQueueCapacity bounds how many comparisons may be waiting at once.
//
// The number is a memory bound, not a throughput target: every queued job
// retains a copy of one legacy result, so the queue's worst case is
// shadowQueueCapacity × the largest result a migrated operation produces. 64 is
// enough that a single caller in a tight loop never reaches it (the worker's
// per-job cost is about one legacy call, so it keeps pace with one caller) and
// small enough that the worst case stays in single-digit megabytes.
const shadowQueueCapacity = 64

// shadowSkipQueueFull is the reason recorded when a dispatch could not be
// queued because the bound above was reached.
const shadowSkipQueueFull = "queue-full"

// shadowSkipDrainAbandoned is the reason recorded when a drain ran out of
// budget and gave up on jobs that were still waiting.
const shadowSkipDrainAbandoned = "drain-abandoned"

// shadowSkipCallerCancelled is the reason recorded when the caller's context
// was cancelled or its deadline fired while the LEGACY method was still
// running, so the legacy outcome the comparison would be judged against is not
// a result at all — it is the caller walking away.
//
// See "Why a cancelled caller is not compared" in the file comment.
const shadowSkipCallerCancelled = "caller-cancelled"

// shadowJob is one deferred comparison: everything the worker needs to run the
// executor path and compare it against what the caller already received.
type shadowJob struct {
	// ctx is the caller's context with its cancellation and deadline removed.
	ctx context.Context
	// client is the legacy client the executor path runs through. Every
	// surface that reaches DispatchOperation already serves concurrent
	// requests through this same value, so the worker is not a new concurrency
	// requirement on it.
	client Client
	// args is the caller's typed argument value. Surfaces construct a fresh one
	// per call and never retain or mutate it after dispatch (surfaces/mcp
	// toolcalls.go, surfaces/http handlers.go), which is what makes it safe to
	// read here.
	args      Arguments
	operation string
	// legacyBytes is a COPY of what the caller received. See the file comment.
	legacyBytes []byte
	legacyErr   error
}

// canaryShadow is the process-wide deferred-comparison queue.
//
// It is a mutex-guarded slice rather than a channel for one reason that matters
// to AC-3 and AC-4: a drain that runs out of budget has to be able to SEE what
// it is abandoning, per operation, so it can record those as skipped. A channel
// cannot be inspected, so an abandoned channel's contents would be silent loss —
// the exact thing this story is forbidden from introducing.
var canaryShadow = &shadowRunner{}

type shadowRunner struct {
	mu       sync.Mutex
	cond     *sync.Cond
	jobs     []shadowJob
	inFlight int
	started  bool
	// skips counts comparisons this process did not perform, keyed by
	// operation and reason, so the in-process view matches what the durable
	// record was told.
	skips map[shadowSkipKey]int
}

type shadowSkipKey struct {
	operation string
	reason    string
}

// ensureStartedLocked starts the single worker goroutine on first use. It is
// lazy so a process that never dispatches in `shadow` — a rolled-back install,
// or any embedding of surfaces/client — starts no goroutine at all.
func (r *shadowRunner) ensureStartedLocked() {
	if r.started {
		return
	}
	if r.cond == nil {
		r.cond = sync.NewCond(&r.mu)
	}
	r.started = true
	go r.work()
}

// enqueue offers one job. It reports whether the job was accepted; a refusal is
// recorded as a skipped comparison by the caller.
func (r *shadowRunner) enqueue(job shadowJob) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStartedLocked()
	if len(r.jobs) >= shadowQueueCapacity {
		return false
	}
	r.jobs = append(r.jobs, job)
	r.cond.Broadcast()
	return true
}

// work is the worker loop. One worker is deliberate: the comparison exists to
// be complete, not fast, and a pool would multiply the CPU the position spends
// against a caller it is no longer blocking.
func (r *shadowRunner) work() {
	for {
		r.mu.Lock()
		for len(r.jobs) == 0 {
			r.cond.Wait()
		}
		job := r.jobs[0]
		// Clear the slot so the copied legacy bytes are collectable as soon as
		// the comparison is done with them.
		r.jobs[0] = shadowJob{}
		r.jobs = r.jobs[1:]
		r.inFlight++
		r.mu.Unlock()

		runShadowComparison(job)

		r.mu.Lock()
		r.inFlight--
		r.cond.Broadcast()
		r.mu.Unlock()
	}
}

// pending reports how many comparisons are queued or running.
func (r *shadowRunner) pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs) + r.inFlight
}

// abandonQueuedLocked drops every WAITING job and returns how many were dropped
// per operation. In-flight jobs are not touched: they are already running and
// will record themselves.
func (r *shadowRunner) abandonQueuedLocked() map[string]int {
	if len(r.jobs) == 0 {
		return nil
	}
	dropped := map[string]int{}
	for i := range r.jobs {
		dropped[r.jobs[i].operation]++
		r.jobs[i] = shadowJob{}
	}
	r.jobs = r.jobs[:0]
	return dropped
}

// noteSkip records one or more comparisons this process did not perform.
func (r *shadowRunner) noteSkip(operation, reason string, count int) {
	if count <= 0 || operation == "" {
		return
	}
	r.mu.Lock()
	if r.skips == nil {
		r.skips = map[shadowSkipKey]int{}
	}
	r.skips[shadowSkipKey{operation: operation, reason: reason}] += count
	r.mu.Unlock()
	if recorder := installedDivergenceRecorder(); recorder != nil {
		recorder.RecordSkipped(operation, count, reason)
	}
}

// CanarySkipped returns how many dual-run comparisons this process did NOT
// perform, and the reason counts behind that total.
//
// It is the in-process half of AC-4's disclosure, kept beside CanaryMismatches
// for the same reason that one exists: the fixture suites assert on it, and a
// coverage figure that only lived in a file could drift from what the seam
// actually did.
func CanarySkipped() (int, map[string]int) {
	canaryShadow.mu.Lock()
	defer canaryShadow.mu.Unlock()
	total := 0
	reasons := map[string]int{}
	for key, n := range canaryShadow.skips {
		total += n
		reasons[key.reason] += n
	}
	return total, reasons
}

// resetCanarySkips clears the in-process skip counters. ResetCanaryMismatches
// calls it so a test's zero point covers both halves of the coverage picture.
func resetCanarySkips() {
	canaryShadow.mu.Lock()
	canaryShadow.skips = nil
	canaryShadow.mu.Unlock()
}

// deferCanaryComparison is the shadow position's hand-off. It runs on the
// caller's goroutine and does exactly three things: detach the context, copy the
// legacy bytes, and offer the job. Everything else the position used to do here
// now happens on the worker.
func deferCanaryComparison(ctx context.Context, c Client, args Arguments, operation string, legacyBytes []byte, legacyErr error) {
	if callerWasCancelled(ctx, legacyErr) {
		canaryShadow.noteSkip(operation, shadowSkipCallerCancelled, 1)
		return
	}
	job := shadowJob{
		ctx:         context.WithoutCancel(ctx),
		client:      c,
		args:        args,
		operation:   operation,
		legacyBytes: cloneCanaryBytes(legacyBytes),
		legacyErr:   legacyErr,
	}
	if !canaryShadow.enqueue(job) {
		canaryShadow.noteSkip(operation, shadowSkipQueueFull, 1)
	}
}

// callerWasCancelled reports whether the legacy outcome about to be handed to
// the worker is the caller giving up rather than an answer.
//
// Both halves matter. ctx.Err() catches the cancellation that has already
// landed by the time the legacy method returned — the common shape, since the
// legacy path propagates the same context it was cancelled on. errors.Is
// catches the outcome that carries a context error the caller's own context
// does not (yet) show: a per-call deadline inside the legacy path, or a
// wrapped ctx.Err() returned a moment before the parent was marked done.
//
// It deliberately does NOT look at legacyBytes: a legacy call that returned
// bytes AND a context error is still not a comparable outcome, because the
// executor pass will not see that error.
func callerWasCancelled(ctx context.Context, legacyErr error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(legacyErr, context.Canceled) || errors.Is(legacyErr, context.DeadlineExceeded)
}

// cloneCanaryBytes copies the caller's result. A nil result stays nil: the
// comparison treats nil and empty alike, and inventing an empty slice would
// make a rendering read `0 bytes:` where the synchronous path rendered the same
// thing — but there is no reason to allocate for it.
func cloneCanaryBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// runShadowComparison is the deferred half of what DispatchOperation's shadow
// arm used to do inline, unchanged in what it computes.
//
// Building the Executor moved here with it. It was on the critical path only
// because the comparison was, and it is not free — it resolves the frozen
// catalog and builds the legacy adapter map. `active`, which returns the
// executor's own result and therefore cannot defer anything, still builds it
// synchronously exactly as before.
func runShadowComparison(job shadowJob) {
	executor, err := NewExecutor(job.client)
	if err != nil {
		observeCanary(job.operation, CanaryMismatch{
			Operation: job.operation,
			Kind:      "executor-unavailable",
			Legacy:    "(ran)",
			Executor:  err.Error(),
		}, true)
		return
	}
	executorBytes, executorErr := executeCanary(job.ctx, executor, job.args)
	mismatch, differs := compareCanaryOutcomes(job.operation, job.legacyBytes, job.legacyErr, executorBytes, executorErr)
	observeCanary(job.operation, mismatch, differs)
}

// CanaryShadowPending reports how many deferred comparisons are waiting or
// running right now.
//
// It exists so a caller on a shutdown path can skip the drain's machinery — a
// timer, a context and a watcher goroutine — in the overwhelmingly common case
// where there is nothing to drain (SW-245 MINOR-5: Runtime.Close drains the
// process-global queue, and short-lived Runtimes close often).
//
// It is a snapshot, so a job enqueued immediately after it returns 0 is not
// covered by a drain that skipped on its answer. That is the same race
// DrainCanaryShadow's own empty fast path has, and it is bounded the same way:
// a shutdown that races an in-flight dispatch is a dispatch whose caller is
// still being served, and the job it queues is counted as skipped by the next
// drain rather than lost silently.
func CanaryShadowPending() int {
	canaryShadow.mu.Lock()
	defer canaryShadow.mu.Unlock()
	return len(canaryShadow.jobs) + canaryShadow.inFlight
}

// DrainCanaryShadow blocks until every deferred comparison this process has
// accepted has been performed and recorded, or until ctx is done.
//
// It is AC-3's mechanism and the tests' synchronisation point. A process that
// exits without calling it discards whatever it had queued — which is why
// Runtime.Close calls it BEFORE it closes the store the comparison reads from,
// and why every in-process assertion about what the seam observed goes through
// it (CanaryMismatches drains for exactly this reason).
//
// If ctx is done while jobs are still WAITING, those jobs are dropped and
// recorded as skipped, and the error names how many. Jobs already running are
// left alone: they are mid-comparison and will record their own outcome.
// Returning an error with the count is the honest answer — pretending a drain
// succeeded would put the coverage claim back where AC-4 says it must not be.
func DrainCanaryShadow(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r := canaryShadow

	r.mu.Lock()
	if r.cond == nil {
		r.cond = sync.NewCond(&r.mu)
	}
	if len(r.jobs) == 0 && r.inFlight == 0 {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// sync.Cond has no deadline, so a watcher turns ctx into a broadcast. The
	// watcher is torn down by `stop` on every return path, including the
	// success one.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-stop:
		}
	}()

	r.mu.Lock()
	for len(r.jobs)+r.inFlight > 0 {
		if ctx.Err() != nil {
			abandoned := r.abandonQueuedLocked()
			running := r.inFlight
			r.mu.Unlock()
			total := 0
			for operation, n := range abandoned {
				total += n
				r.noteSkip(operation, shadowSkipDrainAbandoned, n)
			}
			return fmt.Errorf("client: canary: drain gave up with %d comparison(s) queued and "+
				"%d in flight; the queued ones are recorded as skipped: %w", total, running, ctx.Err())
		}
		r.cond.Wait()
	}
	r.mu.Unlock()
	return nil
}
