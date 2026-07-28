package evalreport

// SW-127 (P0-C4): the progress-stall payload.
//
// PRD §12.2 gates "progress stall p95 <= 2 s" and FR-8 additionally asks for
// the LONGEST stall. Neither was measured anywhere, and the gap is not merely a
// missing number: `context/standards.md` states that a slow index must stay
// OBSERVABLE — long work emits `ingest.ProgressEvent` rather than going silent —
// and nothing in the tree turned red when it stopped doing so.
//
// So this schema is a regression guard before it is a statistic, and the rule it
// enforces structurally is the degenerate case:
//
//	A RUN THAT EMITTED NO PROGRESS IS NEVER A ZERO-STALL PASS.
//
// `Observable` is an explicit boolean rather than something a reader infers from
// an empty distribution, because an empty distribution is exactly what a silent
// index produces — and "no stalls were recorded" and "nothing was watching"
// would otherwise render identically. A series that is not observable FAILS,
// and its gate can never read PASS.
//
// The other two rules are the ones the cold, query-latency and freshness series
// already enforce:
//
//   - EVERY INDIVIDUAL INTERVAL IS RETAINED (AC-6). `Intervals` holds one sample
//     per gap between consecutive events; every published percentile is derived
//     from those samples by RecomputeStalls, which is what SW-128's aggregator
//     calls to reproduce them. A published number that disagrees with its
//     samples is a test failure, not a discrepancy nobody can see.
//   - THE BOUNDARIES ARE NAMED, NOT SILENTLY FOLDED IN (AC-4). The interval
//     before the first event and the interval after the last one are measured
//     and published as `lead_in_us` and `tail_us`, and they are NOT stalls.
//     Counting the lead-in as a gap would report one enormous stall on every
//     run; dropping it without saying so would hide a real silence.
//
// Percentiles come from LatencyStatsFrom / PercentileInt64 in coldrun.go — the
// one nearest-rank implementation in the tree, so a stall p95 and a freshness
// p95 cannot disagree about what a rank means.

import "sort"

// StallEventMinimum is the number of progress events a run must emit before any
// interval between consecutive events exists at all. Two is not a statistical
// floor, it is an arithmetic one: with fewer than two events there is no gap to
// measure, and a distribution with no samples must never read as "no stalls".
//
// A successful `IngestAll` emits far more than two — the walk, the parse phase,
// per-file progress, the write, link, FTS, resolve and checkpoint phases and a
// terminal `done`. Falling below this is therefore not a small sample; it is the
// observability invariant having broken.
const StallEventMinimum = 2

// StallInterval is ONE gap between two consecutive progress events.
//
// Phase is the phase of the event that ENDED the gap — the phase whose progress
// broke the silence. Attributing the gap to its ending event rather than to its
// starting one is what makes a phase transition legible: a long silence that
// ends with the first `resolve` event is a silence the resolve phase's start
// paid for, and that is what a profile (SW-129) needs to know.
type StallInterval struct {
	// Seq is the 1-based position of this gap in the run, so a reader can
	// locate the longest stall in time without a timestamp.
	Seq   int    `json:"seq"`
	Phase string `json:"phase"`
	US    int64  `json:"us"`
}

// PhaseStall is one phase's stall distribution, so a missed gate is
// attributable to a phase instead of only to the pooled run.
type PhaseStall struct {
	Phase string       `json:"phase"`
	Stats LatencyStats `json:"stats"`
}

// StallSeries is the whole progress-stall measurement for one cold index.
type StallSeries struct {
	Repo string `json:"repo"`

	// Events is how many progress events the observer received. Observable is
	// Events >= StallEventMinimum, and it is the load-bearing field: a series
	// that is not observable is a FAIL, never a zero-stall PASS (AC-5).
	Events     int  `json:"events_observed"`
	Minimum    int  `json:"minimum_events"`
	Observable bool `json:"observable"`
	// SilenceReason states, in the artifact, why an unobservable run is not a
	// clean one — so the JSON explains its own FAIL without the story.
	SilenceReason string `json:"silence_reason,omitempty"`

	// IndexWallclockUS is the same window the stall clock ran over, published so
	// lead-in + every interval + tail can be reconciled against it.
	IndexWallclockUS int64 `json:"index_wallclock_us"`
	// LeadInUS is index start -> first event, and TailUS is last event -> index
	// end. NEITHER IS A STALL (AC-4): see StallBoundaryNote. They are measured
	// and published because excluding them silently would hide a real silence at
	// either end of the pass.
	LeadInUS       int64 `json:"lead_in_us"`
	LeadInMeasured bool  `json:"lead_in_measured"`
	TailUS         int64 `json:"tail_us"`
	TailMeasured   bool  `json:"tail_measured"`

	// HeartbeatMode is the engine's declared heartbeat cadence for this run. It
	// belongs in the artifact because clock-driven heartbeats are part of the
	// event stream by design, and a reader comparing two runs needs to know
	// which cadence produced the events.
	HeartbeatMode string `json:"heartbeat_mode,omitempty"`

	// Stalls is the pooled distribution over every retained interval; PerPhase
	// resolves it to the phase that ended each gap. Both carry p95 AND max,
	// which is FR-8's "and the longest stall" (AC-2).
	Stalls    LatencyStats    `json:"stalls"`
	PerPhase  []PhaseStall    `json:"per_phase,omitempty"`
	Intervals []StallInterval `json:"intervals"`

	Gates    []GateResult `json:"gates,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	Status   string       `json:"status"`

	// The artifact explains its own measurement and its own arithmetic (AC-4).
	StallDefinition  string `json:"stall_definition"`
	BoundaryHandling string `json:"boundary_handling"`
	TimingMethod     string `json:"timing_method"`
	AggregateMethod  string `json:"aggregate_method"`
	ScopeLimitation  string `json:"scope_limitation"`
	Notes            string `json:"notes,omitempty"`
}

// StallDefinitionNote is AC-4's "the report shall state the stall definition",
// stated in the artifact rather than in a story nobody reading the JSON has.
const StallDefinitionNote = "A STALL is silence: the wall-clock interval between two CONSECUTIVE ingest.ProgressEvents delivered to the " +
	"harness's progress handler during the cold full index. One interval per adjacent pair of events, attributed to the " +
	"phase of the event that ENDED it. It is deliberately the interval a consumer of the event stream experiences — the " +
	"thing a user reads as a hang — and not the duration of any underlying work: a phase that takes a minute while " +
	"emitting progress throughout is slow, not stalled, and `context/standards.md` asks for exactly that distinction."

// StallBoundaryNote is the other half of AC-4: the start and end phases are
// handled explicitly, and the reason is stated so the exclusion cannot read as
// a convenient omission.
const StallBoundaryNote = "The two BOUNDARY intervals are measured and published but are NOT stalls, and are excluded from the " +
	"distribution by definition. `lead_in_us` is index start -> first event: there is no preceding event, so counting it " +
	"would report one enormous stall on every run and swamp the p95 with an artefact of where the clock was started. " +
	"`tail_us` is last event -> index end: a successful pass ends with a terminal `done` event, so it is normally near " +
	"zero. Both are published rather than dropped, and a boundary longer than the longest recorded stall raises a warning " +
	"— the largest silence in the run being outside the distribution is something a reader must be told, not spared."

// StallTimingMethodNote states what is inside the clock and what is not.
const StallTimingMethodNote = "One clock, read once per event inside the progress handler, and nothing else in the handler: the handler stores a " +
	"timestamp difference and returns. Events are delivered SYNCHRONOUSLY from the ingesting goroutine (engine/ingest " +
	"documents this), so an expensive handler would insert itself into the very silence it is measuring; the derivation — " +
	"percentiles, per-phase pools, warnings — happens after the index has finished. The window is the same one " +
	"index.wallclock_ms is measured over, so the parts reconcile against the whole."

// StallAggregateMethodNote documents the derivation inline.
const StallAggregateMethodNote = "nearest-rank percentile (rank = ceil(p/100 * n), 1-based) over the retained intervals, ascending — the same " +
	"implementation the cold, query-latency and freshness series use. `n` is the number of gaps, which is one less than " +
	"events_observed. Boundary intervals contribute to neither the pooled nor any per-phase distribution. Every value " +
	"here is reproducible from `intervals` with evalreport.RecomputeStalls. Every interval is TRUNCATED to whole " +
	"microseconds, so `lead_in_us` + the intervals + `tail_us` reconciles with `index_wallclock_us` to within one " +
	"microsecond per interval and is never larger than it — a gap of more than that means a part of the window went " +
	"unaccounted for, not that the arithmetic rounded."

// StallScopeLimitation is the honest boundary of what this measurement covers.
const StallScopeLimitation = "This measures the COLD FULL INDEX only, through the shipped ingest.Ingester.WithProgress hook — the same hook " +
	"the graphi CLI's renderer consumes. It makes no statement about the incremental path (SW-126 measures that), about " +
	"filesystem-watch detection, or about a phase that is slow while still emitting progress. Clock-driven heartbeats are " +
	"part of the shipped event stream and are counted as events, which is correct for this gate: the gate is about the " +
	"index going SILENT, and a heartbeating phase is not silent."

// StallSilenceNote is the reason a silent run FAILS, written into the artifact
// so the verdict is self-explaining.
const StallSilenceNote = "the cold index delivered fewer than the minimum number of progress events, so no interval between consecutive " +
	"events exists. Under context/standards.md a slow index must stay OBSERVABLE — long work emits progress rather than " +
	"going silent — and an index nobody could watch is the defect this gate exists to catch. An empty stall distribution " +
	"is therefore reported as a FAILURE, never as zero stalls"

// StallNotes explains the artifact to a reader who has only the JSON.
const StallNotes = "SW-127 progress-stall measurement: the intervals between consecutive ingest.ProgressEvents during the cold full " +
	"index, reported as p95 AND maximum with every individual interval retained. It is a REGRESSION GUARD as much as a " +
	"number: a run that emits no progress reads FAIL, never `0 stalls, passed`. No PRD §12.2 gate is read unless the run " +
	"is the reference scenario on the reference class and from the frozen candidate; otherwise it is UNKNOWN, which is " +
	"not a PASS (PRD §8.2) — and a silent run is not rescued into a PASS by any of that."

// StallRecomputation is RecomputeStalls' result: the same statistics the series
// publishes, derived from nothing but the retained intervals.
type StallRecomputation struct {
	Stalls LatencyStats
	Phases map[string]LatencyStats
}

// RecomputeStalls derives every published statistic from the retained
// intervals. The harness calls it to PRODUCE the statistics and a consumer
// calls it to REPRODUCE them (AC-6).
//
// An empty input yields the zero LatencyStats, whose N is 0. That is the whole
// point of N being published beside the percentiles: a caller reads N — never a
// zero percentile — to decide whether anything was measured at all.
func RecomputeStalls(intervals []StallInterval) StallRecomputation {
	pooled := make([]int64, 0, len(intervals))
	byPhase := map[string][]int64{}
	for _, in := range intervals {
		pooled = append(pooled, in.US)
		byPhase[in.Phase] = append(byPhase[in.Phase], in.US)
	}
	out := StallRecomputation{
		Stalls: LatencyStatsFrom(pooled),
		Phases: make(map[string]LatencyStats, len(byPhase)),
	}
	for phase, samples := range byPhase {
		out.Phases[phase] = LatencyStatsFrom(samples)
	}
	return out
}

// PhaseStallsOf renders the per-phase distributions as a stable, sorted table.
// Sorted by phase name rather than by first appearance so two runs of the same
// repository produce comparable artifacts even if a phase was skipped in one.
func PhaseStallsOf(intervals []StallInterval) []PhaseStall {
	phases := RecomputeStalls(intervals).Phases
	names := make([]string, 0, len(phases))
	for name := range phases {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]PhaseStall, 0, len(names))
	for _, name := range names {
		out = append(out, PhaseStall{Phase: name, Stats: phases[name]})
	}
	return out
}
