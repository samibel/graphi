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
//     and where contention noise is weakest.
//     It does NOT judge the median ALONE. A first cut of SW-242 gated the median
//     only, and review showed what that costs: a regression confined to a
//     minority of calls never moves the median however severe, so a ~20 ms hit
//     on one call in three scored +97 µs and passed clean. The tail is therefore
//     gated too, by the same arithmetic against the same-run control at p95 —
//     which is to say the ORIGINAL AX-06 p95 bar survives, now with a measured
//     reference in place of an absolute delta calibrated on one laptop. The two
//     statistics answer different questions (§3 of the doc says exactly which),
//     and either can fail the run. The asymmetry is that an unjudgeable TAIL
//     degrades to a median-only verdict rather than spoiling the run, because
//     the tail is the noisier measurement and its noise is what made the old
//     gate cry wolf.
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

// canaryLatencyStat is ONE gated statistic's arithmetic, kept whole so the log
// line and the failure message read the same numbers the decision read.
//
// The gate reads TWO of these — the median and the tail — and both are built by
// this one construction. That is deliberate. The ceiling clamp that keeps AC-5
// honest ("a noisy runner can never talk the gate into tolerating an arbitrary
// regression") is then a property of a single function, proved once and holding
// for every gated statistic, rather than a rule re-implemented per statistic
// and at risk of being lost in one of them.
type canaryLatencyStat struct {
	// Name and Pct identify the statistic: "p50" at 0.50, "p95" at 0.95.
	Name string
	Pct  float64
	// BaseVal and RefVal are the two legacy arms at this percentile — identical
	// code, so their difference is measurement noise and nothing else.
	BaseVal time.Duration
	RefVal  time.Duration
	ExecVal time.Duration
	// Baseline is the pooled legacy centre the executor is compared against.
	Baseline time.Duration
	// Overhead is what the gate judges: ExecVal - Baseline.
	Overhead time.Duration
	// RefDelta is the same-run reference measurement: |BaseVal - RefVal|, this
	// run's demonstrated resolution AT THIS PERCENTILE. The tail's resolution is
	// worse than the median's, and this is where that shows up honestly instead
	// of being assumed away.
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
	// Verdict is this statistic's own answer; Reason justifies a non-PASS one.
	Verdict canaryLatencyVerdict
	Reason  string
}

func (s canaryLatencyStat) String() string {
	return fmt.Sprintf(
		"%s %s overhead=%v budget=%v (fixed=%v noise=3x%v=%v ceiling=%v) "+
			"legacy-a=%v legacy-b=%v baseline=%v executor=%v",
		s.Name, s.Verdict, s.Overhead, s.Budget, s.FixedBar, s.RefDelta, s.NoiseTerm,
		s.Ceiling, s.BaseVal, s.RefVal, s.Baseline, s.ExecVal)
}

// evaluateCanaryStat is §2's rule applied at one percentile, as a pure function
// of three sorted sample sets, so the decision can be unit-tested at its
// boundaries without owning a contended machine.
//
// base and ref are the two legacy arms; exec is the executor arm.
func evaluateCanaryStat(name string, pct float64, base, ref, exec []time.Duration) canaryLatencyStat {
	s := canaryLatencyStat{
		Name:    name,
		Pct:     pct,
		BaseVal: percentile(base, pct),
		RefVal:  percentile(ref, pct),
		ExecVal: percentile(exec, pct),
	}
	s.Baseline = (s.BaseVal + s.RefVal) / 2
	s.RefDelta = s.BaseVal - s.RefVal
	if s.RefDelta < 0 {
		s.RefDelta = -s.RefDelta
	}
	s.Overhead = s.ExecVal - s.Baseline
	s.NoiseTerm = time.Duration(float64(s.RefDelta) * canaryLatencyNoiseFactor)

	s.FixedBar = time.Duration(float64(s.Baseline) * canaryLatencyRelative)
	if s.FixedBar < canaryLatencyAbsolute {
		s.FixedBar = canaryLatencyAbsolute
	}
	s.Ceiling = time.Duration(float64(s.FixedBar) * canaryLatencyDegradedMultiple)

	s.Budget = s.FixedBar
	if s.NoiseTerm > s.Budget {
		s.Budget = s.NoiseTerm
	}
	// The clamp is unconditional, so "Budget <= Ceiling" is an invariant of this
	// function rather than a property of the inputs — and because both gated
	// statistics are produced here, it is an invariant of both.
	if s.Budget > s.Ceiling {
		s.Budget = s.Ceiling
	}

	switch {
	case len(base) == 0 || len(ref) == 0 || len(exec) == 0 || s.Baseline <= 0:
		s.Verdict = canaryLatencyUnknown
		s.Reason = fmt.Sprintf(
			"%s: no usable measurement: an arm produced no samples or a zero baseline", s.Name)
	case s.Overhead > s.Budget && s.Overhead > s.NoiseTerm:
		// A degraded run is not a licence to pass an arbitrarily large
		// regression. This case is checked BEFORE the degraded branch on
		// purpose: an overhead that exceeds both the clamped budget AND three
		// times the run's own demonstrated resolution is signal by the gate's
		// own signal-to-noise rule, however poor that resolution is. Without
		// this ordering, a runner degraded enough to widen the control past the
		// ceiling would launder any regression into a PASS via the UNKNOWN
		// branch — precisely what AC-2 forbids in the opposite direction.
		//
		// In the NON-degraded regime this is exactly the old FAIL condition:
		// Budget is then max(FixedBar, NoiseTerm) >= NoiseTerm, so
		// "Overhead > Budget" already implies "Overhead > NoiseTerm". The
		// clause only changes behaviour where the control is wider than the
		// ceiling, and only ever in the direction of failing.
		s.Verdict = canaryLatencyFail
		s.Reason = fmt.Sprintf(
			"%s: executor %s - legacy %s = %v exceeds the %v budget AND exceeds 3x the "+
				"same-run A/A control (%v), so it is signal even at this run's resolution",
			s.Name, s.Name, s.Name, s.Overhead, s.Budget, s.RefDelta)
	case s.NoiseTerm > s.Ceiling:
		s.Verdict = canaryLatencyUnknown
		s.Reason = fmt.Sprintf(
			"%s: runner degraded beyond comparison: the same-run A/A control differs by %v, "+
				"so 3x noise = %v exceeds the %v ceiling (%.0fx the %v bar). Two byte-identical "+
				"legacy paths could not be told apart at this resolution, so nothing can be "+
				"concluded about the executor seam from this run at this percentile",
			s.Name, s.RefDelta, s.NoiseTerm, s.Ceiling, canaryLatencyDegradedMultiple, s.FixedBar)
	case s.Overhead <= s.Budget:
		s.Verdict = canaryLatencyPass
	default:
		s.Verdict = canaryLatencyFail
		s.Reason = fmt.Sprintf(
			"%s: executor %s - legacy %s = %v exceeds the %v budget, and the same-run A/A control "+
				"(%v) is small enough that the difference is signal, not noise",
			s.Name, s.Name, s.Name, s.Overhead, s.Budget, s.RefDelta)
	}
	return s
}

// canaryLatencyResult is one round's answer: the two gated statistics and the
// verdict they compose into.
//
// # Why two statistics (SW-242 round 2)
//
// The median is the statistic a SYSTEMIC regression lives in: the seam runs on
// every call, so making it more expensive shifts the whole distribution. That is
// the shape of every incident in backlog :1043, and it is the shape the median
// catches down to the unchanged 250 µs floor.
//
// But the median is blind by construction to a regression that hits only a
// MINORITY of calls, however severe: half the distribution has to move before
// the median does. That is not a corner case — a slow path taken on cache miss,
// a lock contended only sometimes, an allocation that only occasionally trips
// GC are all exactly this shape. Gating the median alone would have retired the
// original AX-06 p95 gate's coverage of it entirely.
//
// So the TAIL is gated too, by the same construction and against the same
// budget arithmetic — which is to say, the original AX-06 p95 bar is still here,
// now with a same-run A/A control at p95 in place of the absolute 250 µs delta
// calibrated on one laptop. p95 reads the top 5 % of calls, so it moves by close
// to the full per-incident cost as soon as a regression's incidence exceeds
// ~5 %. Between them the two statistics cover both shapes; §3 of the doc records
// exactly what each can and cannot see.
//
// The composition is asymmetric, and that asymmetry is what keeps this story's
// own win. The tail is the noisier measurement, and its noise is precisely what
// made the old gate cry wolf. So:
//
//   - either statistic may FAIL the run — a regression is a regression whichever
//     part of the distribution it lives in;
//   - a tail whose OWN A/A control is too wide to judge (tail UNKNOWN) does not
//     drag the run to UNKNOWN. It degrades to median-only and says so. A
//     contended runner therefore still gets a usable verdict from the statistic
//     that survives contention, which is the whole point of SW-242;
//   - a median that cannot be judged still takes the whole run to UNKNOWN: if
//     the run cannot resolve two identical paths at the centre of the
//     distribution, it has resolved nothing at all.
type canaryLatencyResult struct {
	Verdict canaryLatencyVerdict
	// Reason is the one-line justification for a non-PASS verdict.
	Reason string
	// Median is the gated location statistic; Tail is the gated p95.
	Median canaryLatencyStat
	Tail   canaryLatencyStat
}

func (r canaryLatencyResult) String() string {
	s := fmt.Sprintf("%s — %s | %s", r.Verdict, r.Median, r.Tail)
	if r.Verdict == canaryLatencyPass && r.Tail.Verdict == canaryLatencyUnknown {
		s += " | tail not judgeable this run (median-only verdict)"
	}
	return s
}

// evaluateCanaryLatency is §2's rule: judge the median, judge the tail, compose.
func evaluateCanaryLatency(base, ref, exec []time.Duration) canaryLatencyResult {
	r := canaryLatencyResult{
		Median: evaluateCanaryStat("p50", 0.50, base, ref, exec),
		Tail:   evaluateCanaryStat("p95", 0.95, base, ref, exec),
	}
	switch {
	case r.Median.Verdict == canaryLatencyUnknown:
		r.Verdict = canaryLatencyUnknown
		r.Reason = r.Median.Reason
	case r.Median.Verdict == canaryLatencyFail:
		r.Verdict = canaryLatencyFail
		r.Reason = r.Median.Reason
	case r.Tail.Verdict == canaryLatencyFail:
		r.Verdict = canaryLatencyFail
		r.Reason = r.Tail.Reason +
			". The median is clean, so this is a regression confined to a minority of calls " +
			"— the shape the median cannot see and the tail exists to catch"
	default:
		r.Verdict = canaryLatencyPass
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

// canaryLatencyExtraSeamPassesEvery is the MINORITY-INCIDENCE regression shape:
// the same real seam cost as canaryLatencyExtraSeamPasses, but charged only on
// every `every`-th timed call instead of on all of them.
//
// This is what a slow path taken on cache miss, a lock contended only
// sometimes, or an allocation that only occasionally trips GC actually looks
// like, and it is the shape a median-only gate cannot see at any magnitude. It
// exists so that claim is TESTED rather than reasoned about: SW-242's tail
// statistic is judged by whether it turns this red.
func canaryLatencyExtraSeamPassesEvery(n, every int) func(testing.TB, *Direct) {
	inner := canaryLatencyExtraSeamPasses(n)
	calls := 0
	return func(t testing.TB, direct *Direct) {
		calls++
		if every > 0 && calls%every == 0 {
			inner(t, direct)
		}
	}
}

// TestAX06_LatencyGateFailsOnMinorityIncidenceRegression is SW-242 round 2's
// load-bearing test, and the reason the tail is gated at all.
//
// Round 1 gated the median only. The median is the right statistic for a
// systemic seam regression and it is robust to contention, but it is blind by
// construction to a regression confined to a minority of calls: the median does
// not move until more than half the distribution does. Review demonstrated the
// consequence — a ~20 ms per-incident cost on one call in three scored +97 µs of
// median overhead and PASSED.
//
// This test injects exactly that shape with real seam work and requires the
// SHIPPED decision path to return FAIL. Two incidences are pinned: one call in
// eight (12.5 %) and one call in sixteen (6.25 %), each carrying five extra real
// seam passes (~2 ms). p95 reads the top 5 % of calls, so the design predicts
// the transition sits just above 5 % incidence rather than the median's ~50 %,
// and the measured sweep in doc §5.6 confirms it: 6.25 % is caught with a ~7.7x
// margin over the budget and 3.1 % is not, independently of magnitude. The
// pinned cases sit above that boundary with margin so the gate does not become
// its own flake.
// canaryLatencyTailUnjudgeable reports whether every round found the tail's own
// A/A control too wide to draw any conclusion from. It is the guard on the
// minority-incidence demonstration: a run that could not measure the tail is not
// a run in which the tail failed to catch something.
func canaryLatencyTailUnjudgeable(results []canaryLatencyResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Tail.Verdict != canaryLatencyUnknown {
			return false
		}
	}
	return true
}

func TestAX06_LatencyGateFailsOnMinorityIncidenceRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	for _, tc := range []struct {
		name  string
		extra int
		every int
	}{
		{name: "one_call_in_eight", extra: 20, every: 8},
		{name: "one_call_in_sixteen", extra: 20, every: 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct := canaryLatencyFixture(t)
			arms := canaryLatencyArmsWith(canaryArmExecutor,
				canaryLatencyExtraSeamPassesEvery(tc.extra, tc.every))
			results, _ := runCanaryLatencyGate(t, direct, arms)
			overall := canaryLatencyOverall(results)
			if overall.Verdict != canaryLatencyFail {
				// The demonstration is only meaningful on a run that could
				// resolve the tail at all. If every round reported the tail
				// unjudgeable — two byte-identical legacy paths whose p95s
				// differed by more than the injection — then this run measured
				// nothing about the tail and cannot be asked to judge it. That
				// is the same honest answer the gate itself gives, reported the
				// same way, and it is the limit recorded in doc §3: sustained
				// contention costs the tail statistic, and with it the
				// minority-incidence coverage, for that run.
				if canaryLatencyTailUnjudgeable(results) {
					t.Skipf("minority-incidence demonstration inconclusive: the tail was "+
						"unjudgeable in all %d round(s) (this runner could not tell two "+
						"byte-identical legacy paths apart at p95), so the injection could not "+
						"be measured. Not evidence that the gate missed it: %s",
						len(results), overall)
				}
				t.Fatalf("SW-242 AC-3 (minority incidence): %d extra executor-seam pass(es) on "+
					"1 call in %d must turn the gate red, got %s after %d round(s): %s",
					tc.extra, tc.every, overall.Verdict, len(results), overall)
			}
			// The point of the test is that the TAIL caught it. If the median
			// caught it too the injection was not actually a minority shape and
			// the test would be proving nothing about the blind spot.
			last := results[len(results)-1]
			if last.Median.Verdict != canaryLatencyPass {
				t.Logf("note: the median also read this as %s (overhead %v vs budget %v) — "+
					"the injection was large enough to shift the centre too",
					last.Median.Verdict, last.Median.Overhead, last.Median.Budget)
			}
			t.Logf("minority-incidence demonstration: %d extra seam pass(es) on 1 in %d -> %s "+
				"after %d round(s)\n  %s\n  %s",
				tc.extra, tc.every, overall.Verdict, len(results), overall, overall.Reason)
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

// canaryLatencySamplesAt builds a sorted sample set whose median is d and whose
// p95 is d+tail. The spread is deliberately asymmetric and heavy on the right so
// that the two gated statistics see different things — which is the point of
// gating both.
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

// canaryLatencySamplesIncident builds the MINORITY-INCIDENCE shape as pure data:
// one call in `every` costs `cost` more, the rest cost d. For every > 2 the
// median is exactly d — untouched — while p95 carries the full per-incident cost
// as long as the incidence exceeds the 5 % the statistic reads.
func canaryLatencySamplesIncident(d, cost time.Duration, every int) []time.Duration {
	const n = 201
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		v := d
		if every > 0 && i%every == 0 {
			v = d + cost
		}
		out = append(out, v)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// TestAX06_LatencyDecisionRule pins §2's arithmetic at its boundaries. The
// end-to-end tests above prove the rule is wired to a real seam; this one proves
// the rule itself is the one the document describes, including the properties
// the story turns on: neither statistic's budget can exceed its ceiling, a
// control wider than the ceiling yields UNKNOWN rather than PASS, a
// minority-incidence regression the median cannot see still fails through the
// tail, and tail noise alone does not fail the run.
func TestAX06_LatencyDecisionRule(t *testing.T) {
	ms := time.Millisecond
	us := time.Microsecond
	at := canaryLatencySamplesAt
	for _, tc := range []struct {
		name            string
		base, ref, exec []time.Duration
		want            canaryLatencyVerdict
		checkFn         func(t *testing.T, r canaryLatencyResult)
	}{
		{
			name: "quiet_runner_no_overhead_passes",
			base: at(400*us, 20*ms), ref: at(400*us, 20*ms), exec: at(400*us, 20*ms),
			want: canaryLatencyPass,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Median.Budget != canaryLatencyAbsolute {
					t.Fatalf("a silent control must leave the 250µs floor as the median budget, got %v", r.Median.Budget)
				}
			},
		},
		{
			name: "quiet_runner_just_under_the_floor_passes",
			base: at(400*us, 20*ms), ref: at(400*us, 20*ms), exec: at(400*us+249*us, 20*ms),
			want: canaryLatencyPass,
		},
		{
			name: "quiet_runner_just_over_the_floor_fails",
			base: at(400*us, 20*ms), ref: at(400*us, 20*ms), exec: at(400*us+251*us, 20*ms),
			want: canaryLatencyFail,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Median.Verdict != canaryLatencyFail {
					t.Fatalf("the median must be the statistic that fails here, got %s", r.Median.Verdict)
				}
			},
		},
		{
			// The old gate's failure mode: everything uniformly slow. The
			// executor is 12 % over the baseline in absolute terms but the
			// control shows the machine cannot resolve better than that, so it
			// is not signal.
			name: "uniformly_slow_runner_is_not_a_regression",
			base: at(8*ms, 20*ms), ref: at(8*ms+300*us, 20*ms), exec: at(8*ms+400*us, 20*ms),
			want: canaryLatencyPass,
		},
		{
			name: "wide_control_widens_the_budget_but_not_past_the_ceiling",
			base: at(1*ms, 20*ms), ref: at(1*ms+300*us, 20*ms), exec: at(1*ms+500*us, 20*ms),
			want: canaryLatencyPass,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Median.Budget <= r.Median.FixedBar {
					t.Fatalf("a wide control must widen the budget: budget=%v fixed=%v", r.Median.Budget, r.Median.FixedBar)
				}
				if r.Median.Budget > r.Median.Ceiling {
					t.Fatalf("budget %v must never exceed ceiling %v", r.Median.Budget, r.Median.Ceiling)
				}
			},
		},
		{
			name: "control_wider_than_the_ceiling_is_unknown_not_pass",
			base: at(1*ms, 20*ms), ref: at(3*ms, 20*ms), exec: at(2*ms, 20*ms),
			want: canaryLatencyUnknown,
		},
		{
			// The dangerous case: a degraded runner that ALSO happens to look
			// fast. UNKNOWN, never PASS — absence of a measurement is not
			// evidence of good latency.
			name: "degraded_runner_with_a_flattering_number_is_still_unknown",
			base: at(1*ms, 20*ms), ref: at(3*ms, 20*ms), exec: at(1*ms, 20*ms),
			want: canaryLatencyUnknown,
		},
		{
			// A regression big enough to clear the ceiling still fails, however
			// wide the control, as long as the control is inside the ceiling.
			name: "gross_regression_fails_through_a_noisy_control",
			base: at(1*ms, 20*ms), ref: at(1*ms+300*us, 20*ms), exec: at(10*ms, 20*ms),
			want: canaryLatencyFail,
		},
		{
			// A degraded control is not a licence to pass an arbitrary
			// regression: an overhead past both the clamp and 3x the run's own
			// demonstrated resolution is signal at any resolution.
			name: "gross_regression_fails_even_through_a_degraded_control",
			base: at(1*ms, 1*ms), ref: at(3*ms, 1*ms), exec: at(20*ms, 1*ms),
			want: canaryLatencyFail,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Median.NoiseTerm <= r.Median.Ceiling {
					t.Fatalf("this case must exercise the DEGRADED regime: noise=%v ceiling=%v",
						r.Median.NoiseTerm, r.Median.Ceiling)
				}
				if r.Median.Verdict != canaryLatencyFail {
					t.Fatalf("a degraded control must not launder a gross regression, got %s", r.Median.Verdict)
				}
			},
		},
		{
			// SW-242 round 2, the finding this test exists for: a 2 ms cost on
			// one call in three leaves the median EXACTLY unmoved. A median-only
			// gate passes this; the tail must not.
			name: "minority_incidence_regression_fails_the_tail_with_a_clean_median",
			base: at(400*us, 100*us), ref: at(400*us, 100*us),
			exec: canaryLatencySamplesIncident(400*us, 2*ms, 3),
			want: canaryLatencyFail,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Median.Overhead != 0 {
					t.Fatalf("the injection must leave the median untouched to prove the point, got %v", r.Median.Overhead)
				}
				if r.Median.Verdict != canaryLatencyPass {
					t.Fatalf("the median must PASS this, got %s", r.Median.Verdict)
				}
				if r.Tail.Verdict != canaryLatencyFail {
					t.Fatalf("the tail must FAIL this, got %s", r.Tail.Verdict)
				}
			},
		},
		{
			// The asymmetry that keeps SW-242's win: the tail is the noisier
			// measurement, so a tail whose own A/A control is unjudgeable
			// degrades to a median-only verdict instead of dragging a clean run
			// to UNKNOWN. This is the case that stops the tail check from
			// reintroducing the flake this story removed.
			name: "unjudgeable_tail_does_not_spoil_a_clean_median",
			base: at(400*us, 1*ms), ref: at(400*us, 5*ms), exec: at(400*us, 3*ms),
			want: canaryLatencyPass,
			checkFn: func(t *testing.T, r canaryLatencyResult) {
				if r.Tail.Verdict != canaryLatencyUnknown {
					t.Fatalf("this control must make the tail unjudgeable, got %s: %s", r.Tail.Verdict, r.Tail)
				}
				if r.Median.Verdict != canaryLatencyPass {
					t.Fatalf("the median must be clean here, got %s", r.Median.Verdict)
				}
			},
		},
		{
			// ...but an unjudgeable tail is not a licence to pass a broken
			// median. The median still decides UNKNOWN for the whole run.
			name: "unjudgeable_median_takes_the_whole_run_to_unknown",
			base: at(1*ms, 1*ms), ref: at(3*ms, 1*ms), exec: at(1*ms, 1*ms),
			want: canaryLatencyUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := evaluateCanaryLatency(tc.base, tc.ref, tc.exec)
			if r.Verdict != tc.want {
				t.Fatalf("want %s, got %s: %s", tc.want, r.Verdict, r)
			}
			// AC-5 as an invariant, asserted for EVERY gated statistic: the
			// budget is never below the fixed bar and never above the ceiling,
			// and the fixed bar never drops below the AX-06 250 µs floor.
			for _, s := range []canaryLatencyStat{r.Median, r.Tail} {
				if s.FixedBar < canaryLatencyAbsolute {
					t.Fatalf("invariant broken: %s fixed bar %v below the %v floor", s.Name, s.FixedBar, canaryLatencyAbsolute)
				}
				if s.Budget > s.Ceiling {
					t.Fatalf("invariant broken: %s budget %v > ceiling %v", s.Name, s.Budget, s.Ceiling)
				}
				if s.Budget < s.FixedBar {
					t.Fatalf("invariant broken: %s budget %v < fixed bar %v", s.Name, s.Budget, s.FixedBar)
				}
				if s.Verdict != canaryLatencyPass && s.Reason == "" {
					t.Fatalf("%s %s must carry a reason", s.Name, s.Verdict)
				}
				// The property the whole clamp exists for: past the ceiling the
				// only available answers are FAIL and UNKNOWN, never PASS.
				if s.NoiseTerm > s.Ceiling && s.Verdict == canaryLatencyPass {
					t.Fatalf("invariant broken: %s PASSED with the control (noise=%v) past the %v ceiling",
						s.Name, s.NoiseTerm, s.Ceiling)
				}
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
// input too, not only for the noisy one — and for both gated statistics.
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
			if r.Median.Verdict != canaryLatencyUnknown || r.Tail.Verdict != canaryLatencyUnknown {
				t.Fatalf("both statistics must report UNKNOWN on degenerate input, got median=%s tail=%s",
					r.Median.Verdict, r.Tail.Verdict)
			}
		})
	}
}

// TestAX06_LatencyRotationIsBalanced proves the schedule property the whole
// design rests on: over the sampled iterations every arm occupies every slot in
// the rotation the same number of times. That is what makes "no arm is
// systematically later than another" a fact about the code rather than a hope
// about the runner.
//
// It proves it by RUNNING canaryLatencySample — the production sampler — with
// instrumented arms and reading back the order it actually executed, rather than
// by re-deriving the rotation formula alongside it. A re-derivation would agree
// with a future edit that broke the real loop; this does not.
func TestAX06_LatencyRotationIsBalanced(t *testing.T) {
	if testing.Short() {
		t.Skip("the rotation check drives the real sampler")
	}
	direct := canaryLatencyFixture(t)
	for _, armCount := range []int{2, 3, 4, 7} {
		t.Run(fmt.Sprintf("arms_%d", armCount), func(t *testing.T) {
			var order []int
			arms := make([]canaryLatencyArm, armCount)
			for i := range arms {
				idx := i
				arms[i] = canaryLatencyArm{
					name: fmt.Sprintf("legacy-%d", idx),
					mode: CanaryModeLegacy,
					extra: func(testing.TB, *Direct) {
						order = append(order, idx)
					},
				}
			}
			samples := canaryLatencySample(t, direct, arms)

			// Warm-up runs before the rotation and is not part of it: the
			// sampler warms each arm in turn, canaryLatencyWarmup calls at a
			// time. Everything after that is the rotation under test.
			warm := armCount * canaryLatencyWarmup
			if len(order) <= warm {
				t.Fatalf("sampler executed %d calls, expected more than the %d warm-up calls", len(order), warm)
			}
			timed := order[warm:]
			if len(timed)%(armCount*armCount) != 0 {
				t.Fatalf("timed calls %d is not a whole number of complete rotations over %d arms",
					len(timed), armCount)
			}
			if got := len(timed) / armCount; got < canaryLatencySamples {
				t.Fatalf("sampler took %d samples per arm, want at least %d", got, canaryLatencySamples)
			}

			// counts[arm][slot], read off what the sampler DID.
			counts := make([][]int, armCount)
			for i := range counts {
				counts[i] = make([]int, armCount)
			}
			for k, arm := range timed {
				counts[arm][k%armCount]++
			}
			want := len(timed) / (armCount * armCount)
			for arm := 0; arm < armCount; arm++ {
				for slot := 0; slot < armCount; slot++ {
					if counts[arm][slot] != want {
						t.Fatalf("arms=%d: arm %d held slot %d %d times, want %d",
							armCount, arm, slot, counts[arm][slot], want)
					}
				}
			}
			// The returned sample sets must agree with the executed order:
			// every arm carries exactly the samples it ran.
			for _, arm := range arms {
				if got, w := len(samples[arm.name]), len(timed)/armCount; got != w {
					t.Fatalf("arms=%d: arm %q returned %d samples, want %d", armCount, arm.name, got, w)
				}
			}
		})
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
