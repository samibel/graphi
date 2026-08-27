package client

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// SW-226 (AX-06) AC-5: the executor seam's latency cost, measured against the
// threshold that was written down FIRST.
//
// The bar and the method are docs/rc/ax06-canary-latency.md §1–§3. This test is
// the instrument that bar judges; it does not get to define it.
//
// ---------------------------------------------------------------------------
//
// # SW-242 — what changed here, and why the ORIGINAL method had to go
//
// The AX-06 method was: sample 200 legacy calls, then 200 executor calls, then
// 200 shadow calls, in three consecutive BLOCKS, and compare the two p95s
// against max(10 % of legacy p95, 250 µs). On an idle laptop that reads clean.
// On a shared CI runner it failed on essentially every PR — five recorded
// occurrences in the backlog (:1043), each failing ALL THREE anti-flake rounds,
// each green on a plain re-run of the identical commit, and in each case on a
// PR that provably touched nothing under surfaces/client or the canary path.
//
// The old method had three independent defects, and the fix addresses each one
// rather than widening the number:
//
//  1. BLOCK ORDER. legacy and executor were sampled in DIFFERENT time windows.
//     Any drift in machine load between the two windows — a co-tenant job
//     starting, a throttling step, another test binary landing on the same core
//     — is charged in full to the executor seam, which is always the later
//     block. The recorded failures are consistent with exactly this: legacy p95
//     inflated 6–15× over the idle figure, and the executor block, sampled
//     second, higher again by 1.2–1.6×.
//     FIX: the arms are INTERLEAVED call-by-call, and the arm order ROTATES
//     each iteration so every arm occupies every position in the rotation the
//     same number of times. A load excursion of any duration is then shared by
//     the arms in proportion, and no arm can be systematically later than
//     another. This is a property of the sampling schedule, not of the runner,
//     so it holds without needing a contended machine to prove it.
//
//  2. THE STATISTIC. p95 is a TAIL statistic, and contention is a tail
//     phenomenon: preemption and co-tenant interference add a heavy right tail
//     while barely moving the centre. A real seam regression is the opposite
//     shape — the seam runs on every call, so it is a LOCATION SHIFT that moves
//     the whole distribution, median included.
//     FIX: the gate judges the MEDIAN, which is where a seam regression lives
//     and where contention noise is weakest. p95 is still measured and still
//     logged, because it is the number that describes what a caller feels; it
//     is simply not the number the gate reads.
//
//  3. THE BAR WAS ABSOLUTE. 250 µs was calibrated on an idle Apple M2 Max. It
//     encodes a machine, not a design constraint, and on a runner an order of
//     magnitude slower it is below the noise floor — so the gate was asking a
//     question the apparatus could not answer, and answering it anyway.
//     FIX: a SAME-RUN REFERENCE. A second legacy arm runs in the same rotation
//     under the same conditions. Both legacy arms execute byte-identical code,
//     so the difference between their medians is, by construction, pure
//     measurement noise: it is this run's demonstrated inability to tell two
//     identical paths apart. The gate requires the executor's overhead to
//     exceed a multiple of that demonstrated resolution before it calls it
//     signal (§2 of the doc).
//
// The reference arm is also what makes the honest third answer possible. When
// the run's own null control is so wide that even a gross regression would sit
// inside it, the gate reports UNKNOWN rather than passing — absence of a usable
// measurement is not evidence of good latency. The budget is CLAMPED so it can
// never widen past a fixed multiple of the bar; past that point the only
// outcomes are FAIL and UNKNOWN, never PASS. That clamp is what stops "adapt to
// the runner" from degenerating into "a gate that cannot fail".
const (
	// canaryLatencySamples is N from §1: the per-arm sample count. The sampler
	// rounds it up to a multiple of the arm count so the rotation is exactly
	// balanced.
	canaryLatencySamples = 200
	// canaryLatencyWarmup is the per-arm warm-up count from §1.
	canaryLatencyWarmup = 20
	// canaryLatencyRelative is the 10 % relative term from §2.
	canaryLatencyRelative = 0.10
	// canaryLatencyAbsolute is the 250 µs floor from §2.
	canaryLatencyAbsolute = 250 * time.Microsecond
	// canaryLatencyRounds is the best-of-three provision from §1.
	canaryLatencyRounds = 3
	// canaryLatencyNoiseFactor is §2's signal-to-noise requirement: the
	// executor's overhead must exceed this multiple of the same-run A/A control
	// before it is read as the seam's cost rather than the machine's noise.
	canaryLatencyNoiseFactor = 3.0
	// canaryLatencyDegradedMultiple is §2's clamp: the noise term may widen the
	// fixed bar by at most this factor. Beyond it the run is unjudgeable and the
	// verdict is UNKNOWN — it is never PASS.
	canaryLatencyDegradedMultiple = 4.0
)

// The four sampled arms. legacy-a and legacy-b are the SAME code path; the pair
// exists so every run carries its own null control.
const (
	canaryArmBaseline  = "legacy-a"
	canaryArmReference = "legacy-b"
	canaryArmExecutor  = "executor"
	canaryArmShadow    = "shadow"
)

// canaryLatencyFixture seeds a graph large enough that dead_code does real work
// (the analysis is a whole-graph node + edge pass) while staying hermetic.
func canaryLatencyFixture(t testing.TB) *Direct {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	const symbols = 120

	nodes := make([]model.Node, 0, symbols)
	for i := 0; i < symbols; i++ {
		n, err := model.NewNode("function", fmt.Sprintf("p.F%03d", i), fmt.Sprintf("p/f%03d.go", i), 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	// A sparse call chain: every third symbol is called, so the analysis has
	// both live and dead symbols to score and exclude.
	for i := 3; i < symbols; i += 3 {
		e, err := model.NewEdge(nodes[i-3].ID(), nodes[i].ID(), query.EdgeKindCalls,
			model.TierConfirmed, 1, "chain", []string{"e"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
}

// canaryLatencyArm is one sampled path in the rotation.
type canaryLatencyArm struct {
	name string
	mode CanaryMode
	// extra runs INSIDE this arm's timed window, once per timed call, and is
	// the ONLY way a cost is added to one arm and not the others. The gate never
	// sets it; it exists so AC-3 can prove the gate still turns red on a real
	// slowdown, and so the UNKNOWN branch can be driven end to end by widening
	// the null control the way a contended runner widens it.
	extra func(t testing.TB, direct *Direct)
}

// canaryLatencyGateArms is the arm set the gate itself measures: the A/A legacy
// pair that supplies the same-run reference, the executor path under test, and
// shadow, which is measured for the record and never gated (§3).
func canaryLatencyGateArms() []canaryLatencyArm {
	return []canaryLatencyArm{
		{name: canaryArmBaseline, mode: CanaryModeLegacy},
		{name: canaryArmReference, mode: CanaryModeLegacy},
		{name: canaryArmExecutor, mode: CanaryModeActive},
		{name: canaryArmShadow, mode: CanaryModeShadow},
	}
}

// canaryLatencySample runs the interleaved, rotating schedule and returns the
// sorted per-call durations for each arm.
//
// The schedule is the whole point, so it is worth being explicit about it. For
// iteration i and rotation slot j, the arm sampled is arms[(i+j) % len(arms)].
// Over a number of iterations that is a multiple of len(arms) — which the
// sample count is rounded up to be — every arm therefore appears in every slot
// exactly the same number of times. Two consequences follow WITHOUT any
// assumption about the machine:
//
//   - No arm is systematically sampled later in the run than another, so a
//     monotonic drift in machine load cannot be charged to one arm.
//   - No arm is systematically sampled immediately after the same neighbour, so
//     cache/GC after-effects of an expensive neighbour (shadow runs both paths)
//     are shared evenly.
//
// The kill-switch write sits OUTSIDE the timed window; it is one atomic store
// and is not part of what is being measured.
func canaryLatencySample(t testing.TB, direct *Direct, arms []canaryLatencyArm) map[string][]time.Duration {
	t.Helper()
	if len(arms) == 0 {
		t.Fatal("canaryLatencySample: no arms")
	}
	previous := CanaryModeDefault()
	t.Cleanup(func() {
		if err := SetCanaryModeDefault(previous); err != nil {
			t.Errorf("restore canary mode: %v", err)
		}
	})

	ctx := context.Background()
	one := func(arm canaryLatencyArm) time.Duration {
		if err := SetCanaryModeDefault(arm.mode); err != nil {
			t.Fatalf("SetCanaryModeDefault(%q): %v", arm.mode, err)
		}
		start := time.Now()
		if _, err := DispatchOperation(ctx, direct, &DeadCodeArgs{}); err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		if arm.extra != nil {
			arm.extra(t, direct)
		}
		return time.Since(start)
	}

	for _, arm := range arms {
		for i := 0; i < canaryLatencyWarmup; i++ {
			one(arm)
		}
	}

	iterations := canaryLatencySamples
	if r := iterations % len(arms); r != 0 {
		iterations += len(arms) - r
	}
	out := make(map[string][]time.Duration, len(arms))
	for _, arm := range arms {
		out[arm.name] = make([]time.Duration, 0, iterations)
	}
	for i := 0; i < iterations; i++ {
		for j := range arms {
			arm := arms[(i+j)%len(arms)]
			out[arm.name] = append(out[arm.name], one(arm))
		}
	}
	for _, samples := range out {
		sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
	}
	return out
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// canaryLatencyVerdict is the three-valued answer §2 now allows. UNKNOWN is a
// first-class outcome, not a silent pass: a run that cannot measure says so.
type canaryLatencyVerdict string

const (
	canaryLatencyPass    canaryLatencyVerdict = "PASS"
	canaryLatencyFail    canaryLatencyVerdict = "FAIL"
	canaryLatencyUnknown canaryLatencyVerdict = "UNKNOWN"
)

// canaryLatencyResult is one round's arithmetic, kept whole so the log line and
// the failure message read the same numbers the decision read.
type canaryLatencyResult struct {
	Verdict canaryLatencyVerdict
	// BaseP50 and RefP50 are the two legacy arms — identical code, so their
	// difference is measurement noise and nothing else.
	BaseP50 time.Duration
	RefP50  time.Duration
	// Baseline is the pooled legacy centre the executor is compared against.
	Baseline time.Duration
	ExecP50  time.Duration
	// Overhead is what the gate judges: ExecP50 - Baseline.
	Overhead time.Duration
	// RefDelta is the same-run reference measurement: |BaseP50 - RefP50|, this
	// run's demonstrated resolution.
	RefDelta time.Duration
	// NoiseTerm is RefDelta scaled by the signal-to-noise requirement.
	NoiseTerm time.Duration
	// FixedBar is the machine-independent part of the bar: max(10 %, 250 µs).
	FixedBar time.Duration
	// Ceiling is the hard clamp on Budget. The budget can never exceed it, so
	// the gate can never be widened into uselessness by a noisy runner.
	Ceiling time.Duration
	// Budget is what Overhead is actually compared against.
	Budget time.Duration
	// Reason is the one-line justification for a non-PASS verdict.
	Reason string
}

func (r canaryLatencyResult) String() string {
	return fmt.Sprintf(
		"%s — overhead=%v budget=%v (fixed=%v noise=3x%v=%v ceiling=%v) "+
			"legacy-a p50=%v legacy-b p50=%v baseline=%v executor p50=%v",
		r.Verdict, r.Overhead, r.Budget, r.FixedBar, r.RefDelta, r.NoiseTerm, r.Ceiling,
		r.BaseP50, r.RefP50, r.Baseline, r.ExecP50)
}

// evaluateCanaryLatency is §2's rule as a pure function of three sorted sample
// sets, so the decision can be unit-tested at its boundaries without owning a
// contended machine.
//
// base and ref are the two legacy arms; exec is the executor arm.
func evaluateCanaryLatency(base, ref, exec []time.Duration) canaryLatencyResult {
	r := canaryLatencyResult{
		BaseP50: percentile(base, 0.50),
		RefP50:  percentile(ref, 0.50),
		ExecP50: percentile(exec, 0.50),
	}
	r.Baseline = (r.BaseP50 + r.RefP50) / 2
	r.RefDelta = r.BaseP50 - r.RefP50
	if r.RefDelta < 0 {
		r.RefDelta = -r.RefDelta
	}
	r.Overhead = r.ExecP50 - r.Baseline
	r.NoiseTerm = time.Duration(float64(r.RefDelta) * canaryLatencyNoiseFactor)

	r.FixedBar = time.Duration(float64(r.Baseline) * canaryLatencyRelative)
	if r.FixedBar < canaryLatencyAbsolute {
		r.FixedBar = canaryLatencyAbsolute
	}
	r.Ceiling = time.Duration(float64(r.FixedBar) * canaryLatencyDegradedMultiple)

	r.Budget = r.FixedBar
	if r.NoiseTerm > r.Budget {
		r.Budget = r.NoiseTerm
	}
	// The clamp is unconditional, so "Budget <= Ceiling" is an invariant of this
	// function rather than a property of the inputs.
	if r.Budget > r.Ceiling {
		r.Budget = r.Ceiling
	}

	switch {
	case len(base) == 0 || len(ref) == 0 || len(exec) == 0 || r.Baseline <= 0:
		r.Verdict = canaryLatencyUnknown
		r.Reason = "no usable measurement: an arm produced no samples or a zero baseline"
	case r.NoiseTerm > r.Ceiling:
		r.Verdict = canaryLatencyUnknown
		r.Reason = fmt.Sprintf(
			"runner degraded beyond comparison: the same-run A/A control differs by %v, "+
				"so 3x noise = %v exceeds the %v ceiling (%.0fx the %v bar). Two byte-identical "+
				"legacy paths could not be told apart at this resolution, so nothing can be "+
				"concluded about the executor seam from this run",
			r.RefDelta, r.NoiseTerm, r.Ceiling, canaryLatencyDegradedMultiple, r.FixedBar)
	case r.Overhead <= r.Budget:
		r.Verdict = canaryLatencyPass
	default:
		r.Verdict = canaryLatencyFail
		r.Reason = fmt.Sprintf(
			"executor p50 - legacy p50 = %v exceeds the %v budget, and the same-run A/A control "+
				"(%v) is small enough that the difference is signal, not noise",
			r.Overhead, r.Budget, r.RefDelta)
	}
	return r
}

// canaryLatencyReport renders one round's four arms for the record.
func canaryLatencyReport(samples map[string][]time.Duration) string {
	var b strings.Builder
	for _, name := range []string{canaryArmBaseline, canaryArmReference, canaryArmExecutor, canaryArmShadow} {
		s, ok := samples[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n  %-9s n=%d p50=%v p95=%v", name, len(s),
			percentile(s, 0.50), percentile(s, 0.95))
	}
	return b.String()
}

// runCanaryLatencyGate executes the round loop and returns every round's result
// plus the first round's raw samples. It is shared by the gate and by the AC-3
// demonstration so the demonstration proves the SHIPPED decision path and not a
// parallel copy of it.
func runCanaryLatencyGate(t testing.TB, direct *Direct, arms []canaryLatencyArm) ([]canaryLatencyResult, map[string][]time.Duration) {
	t.Helper()
	var (
		results []canaryLatencyResult
		first   map[string][]time.Duration
	)
	for round := 1; round <= canaryLatencyRounds; round++ {
		samples := canaryLatencySample(t, direct, arms)
		if round == 1 {
			first = samples
		}
		res := evaluateCanaryLatency(samples[canaryArmBaseline], samples[canaryArmReference], samples[canaryArmExecutor])
		results = append(results, res)
		t.Logf("AX-06-LATENCY round %d/%d: %s%s", round, canaryLatencyRounds, res, canaryLatencyReport(samples))
		if res.Verdict == canaryLatencyPass {
			break
		}
	}
	return results, first
}

// canaryLatencyOverall collapses the rounds into the run's verdict.
//
// Any PASS wins — that is the anti-flake provision from §1, unchanged. Failing
// that, an UNKNOWN round beats a FAIL round: if the machine was at any point too
// degraded to tell two identical paths apart, the honest answer to "did the seam
// regress" is that this run does not know, not that it did.
func canaryLatencyOverall(results []canaryLatencyResult) canaryLatencyResult {
	if len(results) == 0 {
		return canaryLatencyResult{Verdict: canaryLatencyUnknown, Reason: "no rounds ran"}
	}
	for _, r := range results {
		if r.Verdict == canaryLatencyPass {
			return r
		}
	}
	for _, r := range results {
		if r.Verdict == canaryLatencyUnknown {
			return r
		}
	}
	return results[len(results)-1]
}

// TestAX06_ExecutorSeamLatencyWithinThreshold measures the canary's positions
// and judges the executor path against the recalibrated bar in
// docs/rc/ax06-canary-latency.md §2.
//
// Run it with -v to read the numbers; they are what §4 of that document records.
func TestAX06_ExecutorSeamLatencyWithinThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)
	results, first := runCanaryLatencyGate(t, direct, canaryLatencyGateArms())
	overall := canaryLatencyOverall(results)

	switch overall.Verdict {
	case canaryLatencyPass:
		t.Logf("AX-06-LATENCY-VERDICT: PASS after %d round(s): %s", len(results), overall)
	case canaryLatencyUnknown:
		// AC-2. Not a pass: this run measured nothing usable and says so. The
		// message is emitted by t.Skipf, which `go test -json` (and therefore
		// cmd/testgate) always records, so an UNKNOWN run is visible in CI
		// output rather than hidden behind a green package line.
		t.Skipf("AX-06-LATENCY-VERDICT: UNKNOWN after %d round(s) — %s\n"+
			"  %s\n"+
			"  This is NOT evidence that the executor seam is fast. It is a report that this\n"+
			"  runner could not distinguish two byte-identical legacy paths well enough for any\n"+
			"  comparison to mean anything. Re-run on a quieter machine to obtain a verdict.\n"+
			"  Round 1 samples:%s",
			len(results), overall.Reason, overall, canaryLatencyReport(first))
	default:
		t.Errorf("AX-06-LATENCY-VERDICT: FAIL in all %d round(s) "+
			"(docs/rc/ax06-canary-latency.md §2) — %s\n  %s\n  Round 1 samples:%s",
			len(results), overall.Reason, overall, canaryLatencyReport(first))
	}
}

// canaryLatencyExtraSeamPasses returns an arm hook that runs the executor
// seam's OWN code n extra times inside the timed window: catalog lookup, request
// construction with its JSON argument round trip, typed decode and adapter call.
//
// This is a real slowdown at the real seam, not a sleep standing in for one. It
// is what "the seam became n+1 times as expensive" costs, measured by the same
// clock, on the same arm, under the same rotation.
func canaryLatencyExtraSeamPasses(n int) func(testing.TB, *Direct) {
	return func(t testing.TB, direct *Direct) {
		ctx := context.Background()
		for i := 0; i < n; i++ {
			executor, err := NewExecutor(direct)
			if err != nil {
				t.Fatalf("injected seam pass: NewExecutor: %v", err)
			}
			if _, err := executeCanary(ctx, executor, &DeadCodeArgs{}); err != nil {
				t.Fatalf("injected seam pass: execute: %v", err)
			}
		}
	}
}

// canaryLatencyExtraLegacyPasses is the same idea applied to a LEGACY arm. It
// does not model a seam regression — both legacy arms are the baseline — it
// models the symptom of a degraded runner: two byte-identical paths that measure
// differently. Widening the A/A control is precisely what sustained contention
// does to this gate, and it is the only part of a contended runner that can be
// reproduced deterministically.
func canaryLatencyExtraLegacyPasses(n int) func(testing.TB, *Direct) {
	return func(t testing.TB, direct *Direct) {
		ctx := context.Background()
		for i := 0; i < n; i++ {
			if _, err := (&DeadCodeArgs{}).invoke(ctx, direct); err != nil {
				t.Fatalf("injected legacy pass: %v", err)
			}
		}
	}
}

// canaryLatencyArmsWith returns the gate's arm set with one arm given a hook.
func canaryLatencyArmsWith(arm string, extra func(testing.TB, *Direct)) []canaryLatencyArm {
	arms := canaryLatencyGateArms()
	for i := range arms {
		if arms[i].name == arm {
			arms[i].extra = extra
		}
	}
	return arms
}

// TestAX06_LatencyGateFailsOnInjectedSeamRegression is SW-242 AC-3, and it is
// the load-bearing test of this story.
//
// A recalibrated gate that adapts to the runner is only worth having if it can
// still go red. This test injects a REAL slowdown at the executor seam — the
// seam's own code, run extra times, inside the timed window — and requires the
// shipped decision path (the same runCanaryLatencyGate the gate calls, the same
// evaluateCanaryLatency, the same three rounds) to return FAIL.
//
// The verdict is required to be FAIL specifically. UNKNOWN would not do: a gate
// that answers "I could not tell" to a doubled seam has the same practical value
// as one that answers PASS, and this assertion is what stops the calibration
// from drifting there.
func TestAX06_LatencyGateFailsOnInjectedSeamRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	for _, tc := range []struct {
		name  string
		extra int
	}{
		{name: "seam_cost_doubled", extra: 1},
		{name: "seam_cost_quadrupled", extra: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := canaryLatencyFixture(t)
			arms := canaryLatencyArmsWith(canaryArmExecutor, canaryLatencyExtraSeamPasses(tc.extra))
			results, _ := runCanaryLatencyGate(t, direct, arms)
			overall := canaryLatencyOverall(results)
			if overall.Verdict != canaryLatencyFail {
				t.Fatalf("AC-3: %d extra executor-seam pass(es) per call must turn the gate red, "+
					"got %s after %d round(s): %s",
					tc.extra, overall.Verdict, len(results), overall)
			}
			t.Logf("AC-3 demonstration: %d extra seam pass(es) per call -> %s after %d round(s)\n  %s\n  %s",
				tc.extra, overall.Verdict, len(results), overall, overall.Reason)
		})
	}
}

// TestAX06_LatencyGateReportsUnknownOnDegradedReference is SW-242 AC-2 driven
// end to end.
//
// Sustained CI contention cannot be reproduced on demand, but its SIGNATURE can:
// the run's own A/A control stops reading zero, because two byte-identical
// legacy paths no longer measure the same. Loading one legacy arm produces that
// signature deterministically. The gate must then report UNKNOWN — not PASS
// (which would launder an unusable measurement into evidence of good latency)
// and not FAIL (which is the cry-wolf behaviour this story exists to remove).
func TestAX06_LatencyGateReportsUnknownOnDegradedReference(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)
	arms := canaryLatencyArmsWith(canaryArmReference, canaryLatencyExtraLegacyPasses(3))
	results, _ := runCanaryLatencyGate(t, direct, arms)
	overall := canaryLatencyOverall(results)
	if overall.Verdict != canaryLatencyUnknown {
		t.Fatalf("AC-2: a widened same-run A/A control must report UNKNOWN, got %s after %d round(s): %s",
			overall.Verdict, len(results), overall)
	}
	if overall.Reason == "" {
		t.Fatal("AC-2: an UNKNOWN verdict must say why")
	}
	t.Logf("AC-2 demonstration: %s\n  %s\n  %s", overall.Verdict, overall, overall.Reason)
}

// canaryLatencySamplesAt builds a sorted sample set whose median is d, for the
// decision-rule table below. The spread is deliberately asymmetric and heavy on
// the right so that a p95-reading gate and a p50-reading gate disagree — which
// is the point of the statistic change.
func canaryLatencySamplesAt(d, tail time.Duration) []time.Duration {
	const n = 201
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		v := d
		if i > n*90/100 {
			v = d + tail
		}
		out = append(out, v)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// TestAX06_LatencyDecisionRule pins §2's arithmetic at its boundaries. The
// end-to-end tests above prove the rule is wired to a real seam; this one proves
// the rule itself is the one the document describes, including the two
// properties the story turns on: the budget can never exceed the ceiling, and a
// control wider than the ceiling yields UNKNOWN rather than PASS.
func TestAX06_LatencyDecisionRule(t *testing.T) {
	ms := time.Millisecond
	us := time.Microsecond
	for _, tc := range []struct {
		name    string
		base    time.Duration
		ref     time.Duration
		exec    time.Duration
		want    canaryLatencyVerdict
		checkFn func(t *testing.T, r canaryLatencyResult)
	}{
		{
			name: "quiet_runner_no_overhead_passes",
			base: 400 * us, ref: 400 * us, exec: 400 * us,
			want: canaryLatencyPass,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Budget != canaryLatencyAbsolute {
					t.Fatalf("a silent control must leave the 250µs floor as the budget, got %v", r.Budget)
				}
			},
		},
		{
			name: "quiet_runner_just_under_the_floor_passes",
			base: 400 * us, ref: 400 * us, exec: 400*us + 249*us,
			want: canaryLatencyPass,
		},
		{
			name: "quiet_runner_just_over_the_floor_fails",
			base: 400 * us, ref: 400 * us, exec: 400*us + 251*us,
			want: canaryLatencyFail,
		},
		{
			// The old gate's failure mode: everything uniformly slow. The
			// executor is 12 % over the baseline in absolute terms but the
			// control shows the machine cannot resolve better than that, so it
			// is not signal.
			name: "uniformly_slow_runner_is_not_a_regression",
			base: 8 * ms, ref: 8*ms + 300*us, exec: 8*ms + 400*us,
			want: canaryLatencyPass,
		},
		{
			name: "wide_control_widens_the_budget_but_not_past_the_ceiling",
			base: 1 * ms, ref: 1*ms + 300*us, exec: 1*ms + 500*us,
			want: canaryLatencyPass,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Budget <= r.FixedBar {
					t.Fatalf("a wide control must widen the budget: budget=%v fixed=%v", r.Budget, r.FixedBar)
				}
				if r.Budget > r.Ceiling {
					t.Fatalf("budget %v must never exceed ceiling %v", r.Budget, r.Ceiling)
				}
			},
		},
		{
			name: "control_wider_than_the_ceiling_is_unknown_not_pass",
			base: 1 * ms, ref: 3 * ms, exec: 2 * ms,
			want: canaryLatencyUnknown,
		},
		{
			// The dangerous case: a degraded runner that ALSO happens to look
			// fast. UNKNOWN, never PASS — absence of a measurement is not
			// evidence of good latency.
			name: "degraded_runner_with_a_flattering_number_is_still_unknown",
			base: 1 * ms, ref: 3 * ms, exec: 1 * ms,
			want: canaryLatencyUnknown,
		},
		{
			// A regression big enough to clear the ceiling still fails, however
			// wide the control, as long as the control is inside the ceiling.
			name: "gross_regression_fails_through_a_noisy_control",
			base: 1 * ms, ref: 1*ms + 300*us, exec: 10 * ms,
			want: canaryLatencyFail,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := evaluateCanaryLatency(
				canaryLatencySamplesAt(tc.base, 20*ms),
				canaryLatencySamplesAt(tc.ref, 20*ms),
				canaryLatencySamplesAt(tc.exec, 20*ms),
			)
			if r.Verdict != tc.want {
				t.Fatalf("want %s, got %s: %s", tc.want, r.Verdict, r)
			}
			if r.Budget > r.Ceiling {
				t.Fatalf("invariant broken: budget %v > ceiling %v", r.Budget, r.Ceiling)
			}
			if r.Verdict != canaryLatencyPass && r.Reason == "" {
				t.Fatalf("%s must carry a reason", r.Verdict)
			}
			if tc.checkFn != nil {
				tc.checkFn(t, r)
			}
		})
	}
}

// TestAX06_LatencyDecisionRuleRejectsEmptyInput proves the missing-measurement
// path is UNKNOWN rather than a division-by-zero pass. AC-2's "absence of a
// usable measurement must not read as evidence" has to hold for the degenerate
// input too, not only for the noisy one.
func TestAX06_LatencyDecisionRuleRejectsEmptyInput(t *testing.T) {
	full := canaryLatencySamplesAt(400*time.Microsecond, time.Millisecond)
	for _, tc := range []struct {
		name            string
		base, ref, exec []time.Duration
	}{
		{name: "no_baseline", base: nil, ref: full, exec: full},
		{name: "no_reference", base: full, ref: nil, exec: full},
		{name: "no_executor", base: full, ref: full, exec: nil},
		{name: "zero_baseline", base: canaryLatencySamplesAt(0, 0), ref: canaryLatencySamplesAt(0, 0), exec: full},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := evaluateCanaryLatency(tc.base, tc.ref, tc.exec)
			if r.Verdict != canaryLatencyUnknown {
				t.Fatalf("want UNKNOWN, got %s: %s", r.Verdict, r)
			}
		})
	}
}

// TestAX06_LatencyRotationIsBalanced proves the schedule property the whole
// design rests on: over the sampled iterations every arm occupies every slot in
// the rotation the same number of times. That is what makes "no arm is
// systematically later than another" a fact about the code rather than a hope
// about the runner, and it is checked here because a future edit to the loop
// could silently lose it.
func TestAX06_LatencyRotationIsBalanced(t *testing.T) {
	for _, armCount := range []int{2, 3, 4, 7} {
		iterations := canaryLatencySamples
		if r := iterations % armCount; r != 0 {
			iterations += armCount - r
		}
		// counts[arm][slot]
		counts := make([][]int, armCount)
		for i := range counts {
			counts[i] = make([]int, armCount)
		}
		for i := 0; i < iterations; i++ {
			for j := 0; j < armCount; j++ {
				counts[(i+j)%armCount][j]++
			}
		}
		want := iterations / armCount
		for arm := 0; arm < armCount; arm++ {
			for slot := 0; slot < armCount; slot++ {
				if counts[arm][slot] != want {
					t.Fatalf("arms=%d: arm %d held slot %d %d times, want %d",
						armCount, arm, slot, counts[arm][slot], want)
				}
			}
		}
	}
}

// BenchmarkCanaryDispatch is the reproducible instrument behind the numbers: it
// times the same call in each kill-switch position so a future change can be
// compared against the recorded baseline with `go test -bench`.
func BenchmarkCanaryDispatch(b *testing.B) {
	direct := canaryLatencyFixture(b)
	ctx := context.Background()
	for _, mode := range CanaryModes() {
		b.Run(string(mode), func(b *testing.B) {
			previous := CanaryModeDefault()
			if err := SetCanaryModeDefault(mode); err != nil {
				b.Fatalf("SetCanaryMode: %v", err)
			}
			b.Cleanup(func() { _ = SetCanaryModeDefault(previous) })
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DispatchOperation(ctx, direct, &DeadCodeArgs{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
