package main

// SW-127 (P0-C4): the progress-stall observer.
//
// PRD §12.2 gates "progress stall p95 <= 2 s" and FR-8 asks for the longest
// stall. The source has been in place the whole time — `engine/ingest` emits
// `ingest.ProgressEvent` and `Ingester.WithProgress` delivers it — so this story
// adds NO engine hook (AC-7): the existing event interface is sufficient, and
// the only thing missing was something watching it.
//
// THREE THINGS THIS FILE OWNS.
//
//  1. THE HOT PATH. Progress events are delivered synchronously from the
//     ingesting goroutine, which means an expensive handler inserts itself into
//     the very silence it is measuring. `observe` therefore reads the clock
//     once, appends a fixed-size struct, and returns. No formatting, no sorting,
//     no locking, no I/O. All derivation happens in `series`, after the index
//     has finished, and a timing test pins the per-event cost.
//
//  2. THE BOUNDARIES (AC-4). The interval before the first event is NOT a stall
//     — counting it would report one enormous stall on every run — and neither
//     is the interval after the last one. Both are measured and published under
//     their own names, with a warning when either is longer than the longest
//     recorded stall, because a silence hidden outside the distribution is worse
//     than one inside it.
//
//  3. THE DEGENERATE CASE (AC-5), which is the point of the whole story. A run
//     that emitted no progress produces an empty distribution, and an empty
//     distribution renders exactly like a clean one. So `observable` is an
//     explicit boolean, an unobservable series FAILS, and no code path can turn
//     "nothing was watching" into "zero stalls, passed".

import (
	"fmt"
	"os"
	"time"

	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/evalreport"
)

// progressStallStory is the contract's `measured_by` value for the gate this
// harness answers. A constant so the gate selection cannot drift from the story
// that owns it.
const progressStallStory = "SW-127"

// stallIntervalCapacity pre-sizes the retention slice. A cold index of the
// reference scenario emits one event per parsed file plus the phase
// transitions, so the slice would otherwise grow through several reallocations
// DURING the measured window. Pre-sizing keeps the amortized cost of the hot
// path flat for a repository of any realistic size; growth beyond it still
// works, it just costs an occasional copy.
const stallIntervalCapacity = 8192

// stallObserver turns a stream of progress events into inter-arrival intervals.
//
// The clock is a field so tests can pin exact microseconds instead of racing a
// real one — a stall test that flakes is a defect here, consistent with the
// project's stance on conformance flakes.
type stallObserver struct {
	now func() time.Time

	started time.Time
	stopped time.Time
	last    time.Time

	events    int
	leadInUS  int64
	intervals []evalreport.StallInterval

	begun  bool
	ended  bool
	tailUS int64
}

func newStallObserver() *stallObserver {
	return &stallObserver{
		now:       time.Now,
		intervals: make([]evalreport.StallInterval, 0, stallIntervalCapacity),
	}
}

// begin opens the measurement window. It takes the start instant rather than
// reading the clock so the stall window and the index wallclock are the SAME
// window — two clocks started microseconds apart would make lead-in, intervals
// and tail fail to reconcile against the published wallclock.
func (o *stallObserver) begin(at time.Time) {
	o.started = at
	o.last = at
	o.begun = true
}

// observe is the hot path. It runs inside the ingesting goroutine, between two
// units of real work, and everything it does is O(1) with no allocation beyond
// the amortized append.
func (o *stallObserver) observe(ev ingest.ProgressEvent) {
	now := o.now()
	o.events++
	if o.events == 1 {
		// The first event closes the LEAD-IN, not a stall: there is no preceding
		// event for it to be a gap from.
		o.leadInUS = microsBetween(o.started, now)
		o.last = now
		return
	}
	o.intervals = append(o.intervals, evalreport.StallInterval{
		Seq:   len(o.intervals) + 1,
		Phase: string(ev.Phase),
		US:    microsBetween(o.last, now),
	})
	o.last = now
}

// end closes the measurement window at the same instant the index wallclock
// stops.
func (o *stallObserver) end(at time.Time) {
	o.stopped = at
	o.ended = true
	if o.events > 0 {
		o.tailUS = microsBetween(o.last, at)
	}
}

// series assembles the artifact. Every statistic it publishes comes from
// evalreport.RecomputeStalls over the retained intervals, which is the same
// function a consumer uses to reproduce them (AC-6).
func (o *stallObserver) series(repo, heartbeatMode string) *evalreport.StallSeries {
	if !o.begun {
		return nil
	}
	s := &evalreport.StallSeries{
		Repo:             repo,
		Events:           o.events,
		Minimum:          evalreport.StallEventMinimum,
		Observable:       o.events >= evalreport.StallEventMinimum,
		IndexWallclockUS: microsBetween(o.started, o.stopped),
		LeadInUS:         o.leadInUS,
		LeadInMeasured:   o.events > 0,
		TailUS:           o.tailUS,
		TailMeasured:     o.ended && o.events > 0,
		HeartbeatMode:    heartbeatMode,
		Intervals:        o.intervals,
		StallDefinition:  evalreport.StallDefinitionNote,
		BoundaryHandling: evalreport.StallBoundaryNote,
		TimingMethod:     evalreport.StallTimingMethodNote,
		AggregateMethod:  evalreport.StallAggregateMethodNote,
		ScopeLimitation:  evalreport.StallScopeLimitation,
		Notes:            evalreport.StallNotes,
	}
	if s.Intervals == nil {
		s.Intervals = []evalreport.StallInterval{}
	}
	s.Stalls = evalreport.RecomputeStalls(s.Intervals).Stalls
	s.PerPhase = evalreport.PhaseStallsOf(s.Intervals)

	if !s.Observable {
		// AC-5. This is the whole story: the empty distribution below is NOT a
		// clean run, and the artifact says so in the field a reader reaches for
		// first.
		s.SilenceReason = fmt.Sprintf("%d progress event(s) observed over %.3f s, below the minimum of %d: %s",
			s.Events, float64(s.IndexWallclockUS)/1e6, s.Minimum, evalreport.StallSilenceNote)
		s.Warnings = append(s.Warnings, s.SilenceReason)
		return s
	}

	// A boundary longer than the longest recorded stall means the biggest
	// silence of the run sat OUTSIDE the distribution. That is exactly the
	// reading the exclusion could otherwise hide, so it is warned about rather
	// than left for someone to notice.
	if s.LeadInMeasured && s.LeadInUS > s.Stalls.MaxUS {
		s.Warnings = append(s.Warnings, fmt.Sprintf(
			"the lead-in (index start to the first progress event, %.3f s) is longer than the longest recorded stall (%.3f s); "+
				"it is excluded from the distribution by definition, so the run's largest silence is not in the p95",
			float64(s.LeadInUS)/1e6, float64(s.Stalls.MaxUS)/1e6))
	}
	if s.TailMeasured && s.TailUS > s.Stalls.MaxUS {
		s.Warnings = append(s.Warnings, fmt.Sprintf(
			"the tail (last progress event to the end of the index, %.3f s) is longer than the longest recorded stall (%.3f s); "+
				"a pass that ends with a terminal `done` event should have a near-zero tail",
			float64(s.TailUS)/1e6, float64(s.Stalls.MaxUS)/1e6))
	}
	return s
}

// stallRunFailure is the silence recorded as a RUN failure, not only as a gate
// verdict.
//
// It exists because the gate is not the only consumer. `-cold-runs N` reads each
// child run's report and keeps `run_pass` / `run_failures` per sample; a child
// whose index went silent exits non-zero but still produces a perfectly good
// cold-index wallclock, so without this it would be filed as a COMPLETED run
// with `run_pass: true` and the silence would survive only in a child report the
// series does not embed. Recording it here puts the defect where every consumer
// of a full-run report already looks.
func stallRunFailure(s *evalreport.StallSeries) (string, bool) {
	if s == nil || s.Observable {
		return "", false
	}
	reason := s.SilenceReason
	if reason == "" {
		reason = evalreport.StallSilenceNote
	}
	return "progress stalls: " + reason, true
}

// stallStatus applies PRD §8.2 to the series: FAIL beats UNKNOWN beats PASS.
//
// The one place it departs from its sibling harnesses is the first clause, and
// deliberately. An unobservable run FAILS regardless of provenance, because
// "the index went silent" is not a threshold claim scoped to the reference
// scenario — it is the observability invariant in `context/standards.md`
// breaking, and that is machine-independent. Everything else follows the usual
// rule: anything unmeasured stops the series reading green.
func stallStatus(s *evalreport.StallSeries) string {
	if s == nil {
		return evalreport.StatusUnknown
	}
	if !s.Observable {
		return evalreport.StatusFail
	}
	for _, g := range s.Gates {
		if g.Status == evalreport.StatusFail {
			return evalreport.StatusFail
		}
	}
	if len(s.Gates) == 0 {
		return evalreport.StatusUnknown
	}
	for _, g := range s.Gates {
		if g.Status != evalreport.StatusPass {
			return evalreport.StatusUnknown
		}
	}
	return evalreport.StatusPass
}

// printStallSummary makes the measurement readable in the job log.
func printStallSummary(w *os.File, s *evalreport.StallSeries) {
	if s == nil {
		return
	}
	fmt.Fprintf(w, "eval: progress stalls over %s — %d event(s), %d interval(s) (observable=%v)\n",
		s.Repo, s.Events, s.Stalls.N, s.Observable)
	if s.Stalls.N > 0 {
		fmt.Fprintf(w, "eval:   stall     p50 %.3f s  p95 %.3f s  max %.3f s\n",
			float64(s.Stalls.P50US)/1e6, float64(s.Stalls.P95US)/1e6, float64(s.Stalls.MaxUS)/1e6)
	}
	fmt.Fprintf(w, "eval:   boundaries lead-in %.3f s  tail %.3f s (neither is a stall)\n",
		float64(s.LeadInUS)/1e6, float64(s.TailUS)/1e6)
	for _, p := range s.PerPhase {
		fmt.Fprintf(w, "eval:   phase %-12s n=%-6d p95 %.3f s  max %.3f s\n",
			p.Phase, p.Stats.N, float64(p.Stats.P95US)/1e6, float64(p.Stats.MaxUS)/1e6)
	}
	for _, g := range s.Gates {
		fmt.Fprintf(w, "eval:   gate %-20s %-8s %s\n", g.ID, g.Status, g.Reason)
	}
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "eval:   WARNING %s\n", warning)
	}
}
