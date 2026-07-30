package main

// SW-127 (P0-C4): the observer's arithmetic and its degenerate cases, under a
// CONTROLLED CLOCK. A stall test that races a real clock would flake, and the
// project treats a flaky timing test as a real defect rather than as noise — so
// every interval below is pinned to the microsecond.
//
// The end-to-end properties — that a real cold index really emits events, that
// the hook really changes nothing in ingest — live in stallharness_test.go.

import (
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/evalreport"
)

// scriptedClock returns the given instants in order, then repeats the last one.
// Deterministic to the microsecond, which is what pins the intervals below.
type scriptedClock struct {
	base  time.Time
	steps []time.Duration
	i     int
}

func newScriptedClock(offsets ...time.Duration) *scriptedClock {
	return &scriptedClock{base: time.Unix(1_700_000_000, 0).UTC(), steps: offsets}
}

func (c *scriptedClock) start() time.Time { return c.base }

func (c *scriptedClock) at(d time.Duration) time.Time { return c.base.Add(d) }

func (c *scriptedClock) now() time.Time {
	if c.i >= len(c.steps) {
		if len(c.steps) == 0 {
			return c.base
		}
		return c.base.Add(c.steps[len(c.steps)-1])
	}
	t := c.base.Add(c.steps[c.i])
	c.i++
	return t
}

func event(phase ingest.Phase) ingest.ProgressEvent { return ingest.ProgressEvent{Phase: phase} }

// observeAt drives the observer over a scripted schedule and returns the series.
func observeAt(t *testing.T, endAt time.Duration, phases []ingest.Phase, offsets ...time.Duration) *evalreport.StallSeries {
	t.Helper()
	if len(phases) != len(offsets) {
		t.Fatalf("test bug: %d phases for %d offsets", len(phases), len(offsets))
	}
	clock := newScriptedClock(offsets...)
	obs := newStallObserver()
	obs.now = clock.now
	obs.begin(clock.start())
	for _, p := range phases {
		obs.observe(event(p))
	}
	obs.end(clock.at(endAt))
	return obs.series("fixture", string(ingest.HeartbeatNonTTY))
}

func phases(n int, p ingest.Phase) []ingest.Phase {
	out := make([]ingest.Phase, n)
	for i := range out {
		out[i] = p
	}
	return out
}

// AC-1: the intervals recorded are the gaps between CONSECUTIVE events — n
// events yield n-1 gaps, each equal to the difference between its two events.
func TestStallObserver_RecordsTheIntervalsBetweenConsecutiveEvents(t *testing.T) {
	s := observeAt(t, 7*time.Second,
		[]ingest.Phase{ingest.PhaseWalk, ingest.PhaseParse, ingest.PhaseParse, ingest.PhaseDone},
		1*time.Second, 2*time.Second, 5*time.Second, 6*time.Second)

	if s.Events != 4 {
		t.Fatalf("events = %d, want 4", s.Events)
	}
	if len(s.Intervals) != 3 {
		t.Fatalf("%d interval(s) from 4 events, want 3", len(s.Intervals))
	}
	want := []int64{1_000_000, 3_000_000, 1_000_000}
	for i, w := range want {
		if s.Intervals[i].US != w {
			t.Errorf("interval %d = %d µs, want %d", i+1, s.Intervals[i].US, w)
		}
		if s.Intervals[i].Seq != i+1 {
			t.Errorf("interval %d carries seq %d", i+1, s.Intervals[i].Seq)
		}
	}
	// Each gap is attributed to the phase of the event that ENDED it.
	if s.Intervals[2].Phase != string(ingest.PhaseDone) {
		t.Errorf("the last gap is attributed to %q, want the phase of the event that ended it (%q)", s.Intervals[2].Phase, ingest.PhaseDone)
	}
}

// AC-2: p95 and the maximum are both published, and a delayed emitter shows up
// in both. Ten 3-second silences in a hundred gaps is the case that must not be
// invisible.
func TestStallObserver_ADelayedEmitterShowsUpInTheP95AndTheMaximum(t *testing.T) {
	// 101 events, so 100 gaps: ninety at 2 ms and ten at 3 s.
	gaps := make([]time.Duration, 0, 100)
	for i := 0; i < 90; i++ {
		gaps = append(gaps, 2*time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		gaps = append(gaps, 3*time.Second)
	}
	at := time.Second
	offsets := []time.Duration{at}
	for _, g := range gaps {
		at += g
		offsets = append(offsets, at)
	}
	s := observeAt(t, at+time.Millisecond, phases(len(offsets), ingest.PhaseParse), offsets...)

	if s.Stalls.N != 100 {
		t.Fatalf("n = %d, want 100 gaps from 101 events", s.Stalls.N)
	}
	if s.Stalls.MaxUS != 3_000_000 {
		t.Errorf("max = %d µs, want the 3 s silence", s.Stalls.MaxUS)
	}
	if s.Stalls.P95US != 3_000_000 {
		t.Errorf("p95 = %d µs; ten 3 s silences in a hundred gaps must move the p95", s.Stalls.P95US)
	}
	if s.Stalls.P50US != 2_000 {
		t.Errorf("p50 = %d µs, want the 2 ms body of the distribution", s.Stalls.P50US)
	}
}

// AC-4, the first boundary. The interval before the first event is measured and
// named — and it is NOT a stall. Without this rule a run whose first event
// arrives 30 s in would publish one enormous stall and nothing else.
func TestStallObserver_TheLeadInIsNotCountedAsAStall(t *testing.T) {
	s := observeAt(t, 32*time.Second,
		[]ingest.Phase{ingest.PhaseWalk, ingest.PhaseDone},
		30*time.Second, 30*time.Second+50*time.Millisecond)

	if s.LeadInUS != 30_000_000 || !s.LeadInMeasured {
		t.Fatalf("lead-in = %d µs (measured=%v), want 30 s measured", s.LeadInUS, s.LeadInMeasured)
	}
	if s.Stalls.N != 1 || s.Stalls.MaxUS != 50_000 {
		t.Fatalf("stalls = %+v; the 30 s lead-in must not appear in the distribution", s.Stalls)
	}
	if !containsSubstring(s.Warnings, "lead-in") {
		t.Errorf("a lead-in longer than the longest stall must be warned about, got warnings %v", s.Warnings)
	}
	if !strings.Contains(s.BoundaryHandling, "lead_in_us") {
		t.Errorf("the artifact does not document its boundary handling")
	}
}

// AC-4, the second boundary. The tail is measured separately too, and a tail
// longer than the longest stall is warned about — a pass that ends with a
// terminal `done` should have a near-zero one.
func TestStallObserver_TheTailIsMeasuredSeparatelyAndWarnedAbout(t *testing.T) {
	s := observeAt(t, 20*time.Second,
		[]ingest.Phase{ingest.PhaseWalk, ingest.PhaseParse},
		1*time.Second, 2*time.Second)

	if s.TailUS != 18_000_000 || !s.TailMeasured {
		t.Fatalf("tail = %d µs (measured=%v), want 18 s measured", s.TailUS, s.TailMeasured)
	}
	if s.Stalls.N != 1 || s.Stalls.MaxUS != 1_000_000 {
		t.Fatalf("stalls = %+v; the 18 s tail must not appear in the distribution", s.Stalls)
	}
	if !containsSubstring(s.Warnings, "tail") {
		t.Errorf("an 18 s tail must be warned about, got warnings %v", s.Warnings)
	}
	// The parts reconcile against the whole: lead-in + gaps + tail == wallclock.
	total := s.LeadInUS + s.TailUS
	for _, in := range s.Intervals {
		total += in.US
	}
	if total != s.IndexWallclockUS {
		t.Errorf("lead-in + intervals + tail = %d µs but the window was %d µs", total, s.IndexWallclockUS)
	}
}

// AC-5, THE test. A run that emitted no progress at all must not come out as
// "zero stalls, passed" — not in the distribution, not in the flag a reader
// checks, and not in the status.
func TestStallObserver_ARunWithNoProgressEventsIsNotAZeroStallPass(t *testing.T) {
	clock := newScriptedClock()
	obs := newStallObserver()
	obs.now = clock.now
	obs.begin(clock.start())
	// The emitter is suppressed: the index ran for a minute and said nothing.
	obs.end(clock.at(60 * time.Second))
	s := obs.series("fixture", string(ingest.HeartbeatNonTTY))

	if s == nil {
		t.Fatal("a run that was watched must still produce a series; nil would be indistinguishable from a run that never indexed")
	}
	if s.Events != 0 {
		t.Fatalf("events = %d, want 0", s.Events)
	}
	if s.Observable {
		t.Fatal("a run with no progress events reported itself as observable")
	}
	if s.Stalls.N != 0 {
		t.Fatalf("stalls.n = %d over no events", s.Stalls.N)
	}
	if s.SilenceReason == "" {
		t.Error("an unobservable series must say why in the artifact")
	}
	if got := stallStatus(s); got != evalreport.StatusFail {
		t.Fatalf("status = %s; a silent index must FAIL, never render as zero stalls passed", got)
	}
	if s.LeadInMeasured || s.TailMeasured {
		t.Error("no event means neither boundary was observed; publishing one would invent a measurement")
	}
}

// The generalisation of AC-5: one event is one event, not one gap. A single
// progress event over a long index is the same silence defect, and the
// arithmetic must not present it as a clean empty distribution either.
func TestStallObserver_ASingleEventIsNotAMeasurement(t *testing.T) {
	s := observeAt(t, 60*time.Second, []ingest.Phase{ingest.PhaseWalk}, 1*time.Second)

	if s.Events != 1 {
		t.Fatalf("events = %d, want 1", s.Events)
	}
	if s.Observable {
		t.Fatal("one event yields no interval between consecutive events; that is not an observable run")
	}
	if got := stallStatus(s); got != evalreport.StatusFail {
		t.Fatalf("status = %s, want FAIL", got)
	}
	if s.Minimum != evalreport.StallEventMinimum {
		t.Errorf("the artifact does not carry the minimum it was judged against")
	}
}

// AC-6: every interval is retained, and the published statistics are
// reproducible from them alone.
func TestStallObserver_RetainsEveryIntervalAndItsStatisticsRecompute(t *testing.T) {
	const n = 500
	var offsets []time.Duration
	at := 5 * time.Millisecond
	for i := 0; i < n; i++ {
		at += time.Duration(1+i%37) * time.Millisecond
		offsets = append(offsets, at)
	}
	s := observeAt(t, at+time.Millisecond, phases(n, ingest.PhaseParse), offsets...)

	if len(s.Intervals) != n-1 {
		t.Fatalf("retained %d interval(s) from %d events, want %d", len(s.Intervals), n, n-1)
	}
	if got := evalreport.RecomputeStalls(s.Intervals).Stalls; got != s.Stalls {
		t.Errorf("recomputed %+v from the retained intervals, but the series published %+v", got, s.Stalls)
	}
	if len(s.PerPhase) != 1 || s.PerPhase[0].Stats.N != n-1 {
		t.Errorf("per-phase table = %+v, want one `parse` row with every interval", s.PerPhase)
	}
}

// The hook must stay out of the hot path (test note). Events are delivered
// synchronously from the ingesting goroutine, so a handler that did real work
// would insert itself into the silence it is measuring.
//
// NOISE BUDGET (SW-154). The 5 µs bound is an absolute constant compared against
// a real wall-clock interval, so it needs a stated budget rather than the word
// "loose". The natural cost it discriminates against, MEASURED over 10 rounds of
// 200 000 events through this same loop (2026-07-30, darwin/arm64, go1.26.5):
// 67 ns median / 175 ns max without -race, and 226 ns median / 272 ns max UNDER
// -race, which is the condition the 2e1e186 flake needed. 5 µs is ~18x the
// measured worst case, so the bound is kept exactly where it is.
//
// What keeps it there is not luck but averaging: perEvent is a MEAN over 200 000
// events, so lifting a 226 ns average to 5 µs takes 4.774 µs x 200 000 = ~0.955 s
// of extra time INSIDE the loop, which no single scheduler hiccup or GC pause
// supplies. That is why this test never flaked while the stall harness's
// single-interval assertion did.
//
// And SW-154's mutation check found that the same averaging bounds what this
// assertion can claim, which the previous comment overstated. Adding one
// fmt.Sprintf per event to `observe` moves the mean to 232 ns plain / 2.276 µs
// under -race — real work in the hot path, and this bound does NOT catch it.
// What the 5 µs bound catches is per-event work above the ~4.8 µs of headroom:
// sorting the retained intervals, a real write syscall, a contended lock. The
// cheap allocating cases are caught by its sibling below,
// TestStallObserver_ObserveDoesNotAllocatePerEvent, which failed at 3.00
// allocations per event under that same Sprintf mutation. The pair is the guard;
// neither half is it alone.
func TestStallObserver_ObserveStaysOutOfTheHotPath(t *testing.T) {
	const events = 200_000
	obs := newStallObserver()
	obs.begin(time.Now())

	ev := event(ingest.PhaseParse)
	start := time.Now()
	for i := 0; i < events; i++ {
		obs.observe(ev)
	}
	elapsed := time.Since(start)

	perEvent := elapsed / events
	if perEvent > 5*time.Microsecond {
		t.Fatalf("observe costs %v per event over %d events (%v total); the measurement must not be the thing that causes the stall",
			perEvent, events, elapsed)
	}
	if obs.events != events || len(obs.intervals) != events-1 {
		t.Fatalf("observer lost samples under load: %d events, %d intervals", obs.events, len(obs.intervals))
	}
}

// The other half of "out of the hot path": the handler must not allocate per
// event beyond the retention slice's amortized growth. A handler that allocated
// a string or a map per event would add GC pressure to the ingesting goroutine.
func TestStallObserver_ObserveDoesNotAllocatePerEvent(t *testing.T) {
	obs := newStallObserver()
	obs.begin(time.Now())
	ev := event(ingest.PhaseParse)
	// Fill past the pre-sized capacity so the steady state — not the first
	// growth — is what is measured.
	for i := 0; i < stallIntervalCapacity*2; i++ {
		obs.observe(ev)
	}

	allocs := testing.AllocsPerRun(2000, func() { obs.observe(ev) })
	if allocs > 1 {
		t.Fatalf("observe allocates %.2f object(s) per event; the hot path must only append a fixed-size sample", allocs)
	}
}

// Silence must also reach the RUN's own verdict, so a cold-series child that
// indexed without ever emitting progress is not filed as a clean completed run.
func TestStallRunFailure_SilenceIsARunFailureNotOnlyAGateVerdict(t *testing.T) {
	silent := &evalreport.StallSeries{Observable: false, SilenceReason: "0 progress event(s) observed over 90.000 s"}
	failure, isSilent := stallRunFailure(silent)
	if !isSilent {
		t.Fatal("a silent series produced no run failure; the cold series would file the child as clean")
	}
	if !strings.Contains(failure, silent.SilenceReason) {
		t.Errorf("the run failure %q does not carry the reason", failure)
	}

	healthy := seriesWith(1_000, 2_000)
	if _, isSilent := stallRunFailure(healthy); isSilent {
		t.Error("an observable run must not be marked failed")
	}
	// A run whose index never completed has no series at all — that is an index
	// failure, already recorded, and must not be double-reported as silence.
	if _, isSilent := stallRunFailure(nil); isSilent {
		t.Error("a nil series is an index that never finished, not a silent one")
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
