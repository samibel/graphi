package client

import (
	"context"
	"fmt"
	"math/rand"
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
//
// What the rotation deliberately does NOT do, since SW-245, is drain the
// deferred comparisons between samples. After a `shadow` sample the worker runs
// a whole executor pass concurrently with the arms that follow, so EVERY arm —
// both legacy controls included — is timed on a machine carrying that load, and
// the same-run A/A control cannot see it because both control arms carry it
// equally. That is intentional: the load is what the shipped default really
// does to a host, so leaving it in makes the ratio a caller-perceived one under
// real conditions, and draining would flatter it by removing cost that shadow
// genuinely imposes. The price is that this instrument cannot attribute a
// between-RUN shift in the pooled baseline to the worker rather than to machine
// state, which docs/rc/ax06-canary-latency.md §7.2 states rather than papers
// over. Do not add a drain here to "clean up" the numbers without moving that
// paragraph with it.
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

// canaryLatencyBudget is §2's budget arithmetic, extracted so every judgement
// made anywhere in this file is made against the SAME bar.
//
// SW-244 extracted it. That story flips the shipped kill-switch default to
// `shadow` and therefore has to judge a cost it introduces itself, which is
// exactly the situation in which a second, more forgiving copy of this
// arithmetic would appear. There is no second copy: the shadow accounting in
// TestSW244_ShadowDefaultCostIsAccounted calls this function, so the 10 %/250 µs
// fixed term, the 3x same-run noise term and the 4x ceiling it is held to are
// the ones SW-242 fixed, byte for byte, and widening them for shadow would mean
// widening the AX-06 gate itself — a change no story gets to make quietly.
//
// The clamp is unconditional, so "budget <= ceiling" is an invariant of this
// function rather than a property of its inputs.
func canaryLatencyBudget(baseline, refDelta time.Duration) (noiseTerm, fixedBar, ceiling, budget time.Duration) {
	noiseTerm = time.Duration(float64(refDelta) * canaryLatencyNoiseFactor)

	fixedBar = time.Duration(float64(baseline) * canaryLatencyRelative)
	if fixedBar < canaryLatencyAbsolute {
		fixedBar = canaryLatencyAbsolute
	}
	ceiling = time.Duration(float64(fixedBar) * canaryLatencyDegradedMultiple)

	budget = fixedBar
	if noiseTerm > budget {
		budget = noiseTerm
	}
	if budget > ceiling {
		budget = ceiling
	}
	return noiseTerm, fixedBar, ceiling, budget
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
	s.NoiseTerm, s.FixedBar, s.Ceiling, s.Budget = canaryLatencyBudget(s.Baseline, s.RefDelta)

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
	// The median-only shape is annotated wherever it appears — on a round, and
	// on the run-level verdict a run of such rounds collapses to (which is
	// reported as UNKNOWN, which is why the predicate is written on the two
	// statistics rather than on r.Verdict).
	if canaryLatencyMedianOnly(r) {
		s += " | tail not judgeable this run (median-only verdict)"
	}
	return s
}

// canaryLatencyStatDecisive reports whether ONE statistic's FAIL is DECISIVE:
// an overhead so far past every scale the measurement demonstrated that it is
// signal at whatever resolution was achieved, however poor that resolution is.
//
// # This is not a new idea — it is round 2's idea, applied consistently
//
// evaluateCanaryStat already tests FAIL BEFORE the degraded branch, on the
// reasoning that an overhead past both the clamp and the run's own
// signal-to-noise requirement is signal at any resolution. That reasoning was
// accepted and verified. It was simply not applied in the two places where one
// statistic's answer meets another's: the composition of a round, and the
// arbitration of a run. SW-242's PR #169 failed in exactly that gap — a tail
// FAIL at 42x its budget was discarded because the median of the same round was
// unjudgeable, and two earlier rounds that had caught the same regression at 79
// and 92 ms over budget were then erased by that one UNKNOWN round.
//
// # Why the threshold is 3x the widest scale, not 1x
//
// The literal form of the round-2 rule — overhead past the clamp AND past 3x
// THIS statistic's own A/A control — was implemented first and measured, on the
// same contended noise model round 3 used. It reds 2.365 % of clean runs
// against the 0.100 % the shipped rule reds: a 24x regression on the very
// runner SW-242 exists to stop crying wolf on (§5.9). The reason is structural
// rather than incidental. RefDelta is |a - b| from a SINGLE pair of A/A arms; it
// is a folded-normal draw that is sometimes near zero, and 3x a lucky-narrow
// control is not a bar at all. So the rule uses the widest scale the
// measurement actually demonstrated:
//
//	scale    = max(Ceiling, resolution)
//	decisive = Overhead > canaryLatencyNoiseFactor x scale
//
// where resolution is the WORST A/A control observed at that percentile — the
// round's own when only the round is in evidence, the worst any round of the run
// produced when the whole run is (canaryLatencyRunResolution). No new constant
// is introduced: the multiple is the gate's existing signal-to-noise factor,
// applied to the largest scale in evidence instead of the smallest.
//
// Measured on the round-2/round-3 contended model this leaves the false-FAIL
// rate at 0.100 %, bit-for-bit the shipped rule's, versus 9.99 % for the
// rejected "any FAIL always stands" variant (§5.9).
func canaryLatencyStatDecisive(s canaryLatencyStat, resolution time.Duration) bool {
	if s.Verdict != canaryLatencyFail {
		return false
	}
	scale := s.Ceiling
	if resolution > scale {
		scale = resolution
	}
	return float64(s.Overhead) > canaryLatencyNoiseFactor*float64(scale)
}

// canaryLatencyTailFailNote explains a tail FAIL in the terms of whatever the
// median did, so the verdict line never claims "the median is clean" about a
// round whose median was never judged.
func canaryLatencyTailFailNote(median canaryLatencyStat) string {
	if median.Verdict == canaryLatencyUnknown {
		return ". The MEDIAN of this round could not be judged, and that does NOT withdraw this " +
			"measurement: the tail overhead is past 3x every scale the round demonstrated — its " +
			"own ceiling clamp and its own same-run A/A control — so it is signal at whatever " +
			"resolution the round achieved. An unjudgeable median is an absence of evidence, " +
			"and absence of evidence does not erase a measurement that was taken"
	}
	return ". The median is clean, so this is a regression confined to a minority of calls " +
		"— the shape the median cannot see and the tail exists to catch"
}

// canaryLatencyCompose is §2's composition: two judged statistics in, one round
// verdict out.
//
// It is split out of evaluateCanaryLatency so the arbitration property tests can
// drive the SHIPPED composition with synthesised statistics rather than a copy
// of it that could drift.
//
// Order, and what each step is for:
//
//  1. A median FAIL fails the round, whatever the tail did. Unchanged.
//  2. A DECISIVE tail FAIL fails the round, whatever the median did — including
//     when the median could not be judged at all. This is the round-4 fix: a
//     definitive measurement by one statistic is not suppressed by the other
//     statistic being unjudgeable. A MARGINAL tail FAIL is deliberately NOT
//     given that power (step 4 still requires a judged median), which is the
//     boundary that keeps the false-FAIL rate where it was.
//  3. A median that could not be judged, with no decisive tail FAIL to stand on,
//     takes the round to UNKNOWN. Unchanged.
//  4. A tail FAIL against a clean median fails the round. Unchanged.
func canaryLatencyCompose(median, tail canaryLatencyStat) canaryLatencyResult {
	r := canaryLatencyResult{Median: median, Tail: tail}
	switch {
	case r.Median.Verdict == canaryLatencyFail:
		r.Verdict = canaryLatencyFail
		r.Reason = r.Median.Reason
	case canaryLatencyStatDecisive(r.Tail, r.Tail.RefDelta):
		r.Verdict = canaryLatencyFail
		r.Reason = r.Tail.Reason + canaryLatencyTailFailNote(r.Median)
	case r.Median.Verdict == canaryLatencyUnknown:
		r.Verdict = canaryLatencyUnknown
		r.Reason = r.Median.Reason
	case r.Tail.Verdict == canaryLatencyFail:
		r.Verdict = canaryLatencyFail
		r.Reason = r.Tail.Reason + canaryLatencyTailFailNote(r.Median)
	default:
		r.Verdict = canaryLatencyPass
	}
	return r
}

// evaluateCanaryLatency is §2's rule: judge the median, judge the tail, compose.
func evaluateCanaryLatency(base, ref, exec []time.Duration) canaryLatencyResult {
	return canaryLatencyCompose(
		evaluateCanaryStat("p50", 0.50, base, ref, exec),
		evaluateCanaryStat("p95", 0.95, base, ref, exec),
	)
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
		// Only a FULL pass ends the loop. A median-only pass has not measured
		// the tail at all, so stopping on it would spend the remaining rounds'
		// only purpose — giving a regression another chance to be measured on a
		// calmer round — to buy nothing. Under the contention modelled in doc
		// §5.6 the tail was unjudgeable in 4 runs of 6, so this was not a corner
		// case: it was the common case exactly when the rounds matter most.
		if canaryLatencyFullPass(res) {
			break
		}
	}
	return results, first
}

// canaryLatencyFullPass reports whether a round is a FULL pass: both gated
// statistics were judged, and both passed.
//
// The distinction is load-bearing. A round whose Verdict is PASS has not
// necessarily measured the seam. When the tail's own A/A control is wider than
// its ceiling the tail reads UNKNOWN and the round passes on the MEDIAN ALONE
// (§2's composition table) — a defensible answer for that round, since the
// statistic that survives contention still spoke, but one that carries no
// information whatever about the minority-incidence shape the tail exists to
// catch. Only a full pass is a round in which the whole gate ran.
func canaryLatencyFullPass(r canaryLatencyResult) bool {
	return r.Verdict == canaryLatencyPass && r.Tail.Verdict == canaryLatencyPass
}

// canaryLatencyMedianOnly reports whether a result was decided on the median
// with the tail unjudgeable. It is written on the two statistics rather than on
// the composed verdict so it holds for a median-only ROUND (whose verdict is
// PASS) and for the run-level verdict such rounds collapse to (whose verdict is
// UNKNOWN) alike.
func canaryLatencyMedianOnly(r canaryLatencyResult) bool {
	return r.Median.Verdict == canaryLatencyPass && r.Tail.Verdict == canaryLatencyUnknown
}

// canaryLatencyMedianOnlyPass reports whether a ROUND passed on the median
// alone because its tail was not judgeable. It is the complement of
// canaryLatencyFullPass over PASS rounds: evaluateCanaryLatency never returns
// PASS with a FAILing tail, so a PASS round's tail is either PASS or UNKNOWN.
func canaryLatencyMedianOnlyPass(r canaryLatencyResult) bool {
	return r.Verdict == canaryLatencyPass && canaryLatencyMedianOnly(r)
}

// canaryLatencyOverall collapses the rounds into the run's verdict, in this
// order:
//
//  1. A FULL PASS wins outright — both gated statistics judged, both clean.
//     This is §1's anti-flake provision, unchanged in intent and now restricted
//     to the rounds that earn it.
//  2. Failing that, a round whose MEDIAN could not be judged makes the run
//     UNKNOWN. Unchanged: if the machine could not tell two byte-identical
//     paths apart at the centre of the distribution, it resolved nothing.
//  3. Failing that, a round that passed on the MEDIAN ALONE makes the run
//     UNKNOWN — see canaryLatencyMedianOnlyOverall. This is the SW-242 round-3
//     change, and it is where a FAIL recorded by another round is HELD rather
//     than laundered into a pass.
//  4. Failing that, every round FAILed, and the run FAILs.
//
// # What changed in SW-242 round 3, and why
//
// Round 2 introduced a SECOND, weaker way to reach canaryLatencyPass: the
// median-only verdict, reached whenever the tail's own A/A control is too wide
// to judge. This function did not distinguish it from a full pass, and neither
// did the round loop's early exit. Two consequences, both demonstrated in
// review:
//
//   - A median-only round OUTRANKED a round that had positively demonstrated a
//     regression through the tail. Contention arriving mid-run widens the p95
//     control; the tail goes UNKNOWN, the median stays clean, and the resulting
//     "PASS" discarded the earlier FAIL. Absence of a measurement read as
//     evidence of good latency — precisely what AC-2 forbids — only across
//     rounds rather than within one.
//   - The loop stopped at the first median-only round, so on a runner contended
//     enough for the tail to be unjudgeable (4 runs in 6 under §5.6's load) the
//     gate ended after round 1 and the calmer rounds never ran.
//
// A median-only round is therefore ranked EXACTLY where a fully UNKNOWN round
// is ranked: above nothing, below a full pass, and unable to produce a PASS.
// The alternative considered — letting a FAIL outrank a median-only round, so
// that a tail-caught regression survives to red — was implemented, measured and
// rejected: on a noise model calibrated to the runner §5.6 actually measured it
// raises the run-level FALSE-fail rate on a clean tree from 0.13 % to 10.4 %
// (§5.8). A gate that reds ~1 PR in 10 for being scheduled on a busy runner is
// the disease SW-242 exists to cure, and AC-3 does not ask for detection at
// that price. Ranking median-only with UNKNOWN keeps the whole benefit that
// matters — the regression stops reading as PASS — at no false-FAIL cost at
// all, which is not a judgement call but a theorem:
//
//	This function returns FAIL only when EVERY round FAILed, which is exactly
//	when the round-1 rule returned FAIL, and it returns PASS only when some
//	round was a full pass, which is a round the round-1 rule would also have
//	taken as a PASS. So round 3 differs from round 1 ONLY by turning some PASS
//	verdicts into UNKNOWN. It cannot fail anything round 1 passed.
//
// TestAX06_LatencyArbitrationIsMonotone proves that exhaustively over every
// three-round sequence of reachable round shapes, and
// TestAX06_LatencyArbitrationOnPureNoise measures what it costs on noise.
func canaryLatencyOverall(results []canaryLatencyResult) canaryLatencyResult {
	if len(results) == 0 {
		return canaryLatencyResult{Verdict: canaryLatencyUnknown, Reason: "no rounds ran"}
	}
	for _, r := range results {
		if canaryLatencyFullPass(r) {
			return r
		}
	}
	// SW-242 round 4. A DECISIVE FAIL — an overhead past 3x the widest scale
	// this RUN demonstrated at that percentile — is not erased by another round
	// being unjudgeable. Ranked below a full pass, because a full pass is a
	// positive measurement that contradicts the FAIL, and above UNKNOWN and
	// median-only, because those are the ABSENCE of a measurement and AC-2's
	// rule is that absence of a measurement is not evidence — in either
	// direction.
	medRes, tailRes := canaryLatencyRunResolution(results)
	for _, r := range results {
		if canaryLatencyDecisiveRound(r, medRes, tailRes) {
			return canaryLatencyDecisiveOverall(results, r)
		}
	}
	for _, r := range results {
		if r.Verdict == canaryLatencyUnknown {
			return r
		}
	}
	for _, r := range results {
		if canaryLatencyMedianOnlyPass(r) {
			return canaryLatencyMedianOnlyOverall(results)
		}
	}
	for _, r := range results {
		if r.Verdict == canaryLatencyFail {
			return r
		}
	}
	return results[len(results)-1]
}

// canaryLatencyRunResolution returns the widest same-run A/A control the RUN
// demonstrated, per gated percentile: the worst disagreement between two
// byte-identical legacy paths that any round of this run produced.
//
// This — not the narrowest control, and not the control of the round that
// happens to carry the FAIL — is what the run has DEMONSTRATED about its own
// resolution. A single round's RefDelta can be near zero by luck, and a bar set
// at 3x a lucky-narrow control is not a bar; §5.9 measures what using it costs.
//
// The two percentiles are kept separate on purpose. A round that could not
// resolve the TAIL says nothing about the run's resolution at the MEDIAN, and
// vice versa. That separation is what makes the PR #169 sequence come out right:
// its round 3 had a collapsed MEDIAN control (443 µs), which raises the bar for
// median FAILs and leaves the bar for the tail FAILs — measured against narrow
// tail controls in all three rounds — exactly where it was.
func canaryLatencyRunResolution(results []canaryLatencyResult) (median, tail time.Duration) {
	for _, r := range results {
		if r.Median.RefDelta > median {
			median = r.Median.RefDelta
		}
		if r.Tail.RefDelta > tail {
			tail = r.Tail.RefDelta
		}
	}
	return median, tail
}

// canaryLatencyDecisiveRound reports whether a round recorded a DECISIVE FAIL in
// either gated statistic, judged against the resolution the whole run
// demonstrated rather than against the luckiest single control in it.
func canaryLatencyDecisiveRound(r canaryLatencyResult, medRes, tailRes time.Duration) bool {
	return canaryLatencyStatDecisive(r.Median, medRes) ||
		canaryLatencyStatDecisive(r.Tail, tailRes)
}

// canaryLatencyDecisiveOverall is the run-level verdict when a round recorded a
// decisive FAIL: that round's FAIL, with the rounds that could not be judged
// counted in the reason so the reader can see they were considered and did not
// withdraw it.
func canaryLatencyDecisiveOverall(results []canaryLatencyResult, failed canaryLatencyResult) canaryLatencyResult {
	out := failed
	var unjudged int
	for _, r := range results {
		if r.Verdict == canaryLatencyUnknown || canaryLatencyMedianOnlyPass(r) {
			unjudged++
		}
	}
	if unjudged > 0 {
		out.Reason += fmt.Sprintf(
			". NOTE: %d of %d round(s) of this run could not be judged, and that does NOT "+
				"withdraw this FAIL: the overhead is past 3x every scale the run demonstrated "+
				"— the ceiling clamp and the widest same-run A/A control any round of this run "+
				"produced at that percentile — so it is signal at whatever resolution this "+
				"runner achieved. A round that measured nothing is not evidence of good "+
				"latency (AC-2)",
			unjudged, len(results))
	}
	return out
}

// canaryLatencyMedianOnlyOverall is the run-level verdict when no round was a
// full pass and at least one round passed on the median alone: the median was
// judged, the tail was not, and the run therefore holds no evidence either way
// about a regression confined to a minority of calls (§3).
//
// It is deliberately not PASS. Calling it PASS would state a conclusion the run
// did not reach — the same "absence of a measurement reading as evidence of
// good latency" that AC-2 rules out for the median, and the reason UNKNOWN
// exists at all. It is equally not FAIL: nothing was resolved to fail on, and
// failing a run because the runner was busy is the cry-wolf behaviour SW-242
// exists to remove.
//
// When another round DID record a FAIL, that FAIL is quoted here in full. This
// is the case review found being silently discarded, so it is the one the
// verdict line has to be loudest about: the run is UNKNOWN because the tail
// could not be re-measured, NOT because the FAIL was disbelieved, and the
// reader is told exactly what was seen and in which round.
func canaryLatencyMedianOnlyOverall(results []canaryLatencyResult) canaryLatencyResult {
	var (
		out       canaryLatencyResult
		count     int
		failed    *canaryLatencyResult
		failRound int
	)
	for i := range results {
		if canaryLatencyMedianOnlyPass(results[i]) {
			count++
			out = results[i]
		}
		if results[i].Verdict == canaryLatencyFail && failed == nil {
			failed = &results[i]
			failRound = i + 1
		}
	}
	out.Verdict = canaryLatencyUnknown
	out.Reason = fmt.Sprintf(
		"tail not judgeable: %d of %d round(s) passed on the MEDIAN ALONE — the median was "+
			"judged and clean (last such round: overhead %v against a %v budget), but the "+
			"tail's own same-run A/A control was wider than its %v ceiling (3x%v = %v), so "+
			"this run carries no evidence about a regression confined to a minority of calls. "+
			"Reported as UNKNOWN rather than PASS because half the gate did not run",
		count, len(results), out.Median.Overhead, out.Median.Budget,
		out.Tail.Ceiling, out.Tail.RefDelta, out.Tail.NoiseTerm)
	if failed != nil {
		out.Reason += fmt.Sprintf(
			". NOTE: round %d of this same run recorded a FAIL, which is NOT withdrawn and NOT "+
				"disbelieved — it simply could not be re-measured on a round that could resolve "+
				"the tail, and one unrepeatable measurement is not the standard this gate fails "+
				"on. Re-run on a quieter machine before concluding anything. The FAIL was: %s",
			failRound, failed.Reason)
	}
	return out
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
		// AC-2. Not a pass: this run did not measure what the gate claims to
		// judge, and says so. The message is emitted by t.Skipf, which
		// `go test -json` (and therefore cmd/testgate) always records, so an
		// UNKNOWN run is visible in CI output rather than hidden behind a green
		// package line.
		//
		// There are two shapes of UNKNOWN and they are not the same report, so
		// they do not get the same words. Either the median control collapsed
		// and nothing was resolved at all, or every round resolved a clean
		// median and never resolved the tail.
		detail := "This is NOT evidence that the executor seam is fast. It is a report that this\n" +
			"  runner could not distinguish two byte-identical legacy paths well enough for any\n" +
			"  comparison to mean anything. Re-run on a quieter machine to obtain a verdict."
		if canaryLatencyMedianOnly(overall) {
			detail = "Half the gate ran. The MEDIAN was judged and clean, so there is no systemic seam\n" +
				"  regression above the 250 µs floor. The TAIL was not judgeable, so this run says\n" +
				"  NOTHING about a regression confined to a minority of calls\n" +
				"  (docs/rc/ax06-canary-latency.md §3) — which is why it is reported as UNKNOWN and\n" +
				"  not as a pass. Re-run on a quieter machine to obtain a full verdict."
		}
		t.Skipf("AX-06-LATENCY-VERDICT: UNKNOWN after %d round(s) — %s\n"+
			"  %s\n"+
			"  %s\n"+
			"  Round 1 samples:%s",
			len(results), overall.Reason, overall, detail, canaryLatencyReport(first))
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

// canaryLatencyMedianUnjudgeable is the same guard for the MEDIAN. PR #169's
// round 3 is why it exists: a runner can lose the median while keeping the tail,
// not only the other way round, and a run that could not resolve two
// byte-identical legacy paths at the centre of the distribution has not measured
// the injection either.
func canaryLatencyMedianUnjudgeable(results []canaryLatencyResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Median.Verdict != canaryLatencyUnknown {
			return false
		}
	}
	return true
}

// canaryLatencyRunUnresolved reports whether a run failed to resolve a gated
// statistic in EVERY round — either statistic. It is the guard on the
// demonstrations below, and it is deliberately narrow: it asks that a whole
// statistic was unavailable for the whole run, not that any single round was
// noisy.
func canaryLatencyRunUnresolved(results []canaryLatencyResult) bool {
	return canaryLatencyTailUnjudgeable(results) || canaryLatencyMedianUnjudgeable(results)
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
				if canaryLatencyRunUnresolved(results) {
					which := "tail was unjudgeable in all %d round(s) at p95"
					if canaryLatencyMedianUnjudgeable(results) {
						which = "median was unjudgeable in all %d round(s) at p50"
					}
					t.Skipf("minority-incidence demonstration inconclusive: the "+which+
						" (this runner could not tell two byte-identical legacy paths apart "+
						"there), so the injection could not be measured. Not evidence that the "+
						"gate missed it: %s", len(results), overall)
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

// TestAX06_LatencyRoundArbitration pins how the rounds collapse into the run's
// verdict, which is where SW-242 round 2's median-only composition met the
// pre-existing "any PASS wins" rule and produced a hole.
//
// Every round in this table is built by the SHIPPED evaluateCanaryLatency from
// real sample sets, not by hand-filling a result struct, so the cases are
// reachable states of the gate rather than shapes invented for the assertion.
// The `medianOnly` round is the exact scenario review reproduced: the same
// minority-incidence regression the tail caught in one round, measured again in
// a round whose p95 control has widened the way contention widens it, so the
// tail can no longer judge it and the round passes on the median alone.
// canaryLatencyPR169Rounds rebuilds the three rounds of the CI failure on
// SW-242's own PR #169 as sample data, so the sequence that broke the gate is a
// pinned test rather than a paragraph in a story file.
//
// The job failed in test-gate, `go test -race` and release-gate, all on
// TestAX06_LatencyGateFailsOnMinorityIncidenceRegression/one_call_in_eight, and
// printed:
//
//	round 1/3: FAIL — p50 PASS overhead=143.42µs budget=400.893µs
//	                | p95 FAIL overhead=79.556457ms budget=805.63µs
//	round 2/3: FAIL — p50 PASS overhead=142.363µs budget=402.244µs
//	                | p95 FAIL overhead=92.47372ms budget=2.151255ms
//	round 3/3: UNKNOWN — p50 UNKNOWN overhead=569.458µs budget=1.297072ms
//	                     (fixed=324.268µs noise=3x442.871µs=1.328613ms ceiling=1.297072ms)
//	                   | p95 FAIL overhead=74.651076ms budget=1.773147ms
//	final: "must turn the gate red, got UNKNOWN after 3 round(s)"
//
// # What is real and what is reconstructed
//
// Every number the log printed is reproduced exactly and asserted below. Round
// 3's p50 line was printed in full — fixed, noise, control and ceiling — and it
// pins that arm completely: a 3.24268 ms p50 baseline (the 324.268 µs fixed bar
// is 10 % of it) and a 442.871 µs A/A control whose noise term, 1.328613 ms,
// exceeded the 1.297072 ms ceiling. That is what took the median to UNKNOWN.
//
// The p95 fixed/noise/ceiling triple was NOT printed. The reconstruction takes
// each round's p95 budget as its FIXED bar (a narrow p95 A/A control), which is
// the most conservative of the possibilities available: it gives the WIDEST
// ceiling — 4x the budget rather than 1x it, had the budget been the clamp — and
// therefore makes "decisive" hardest to reach. The observed overheads clear even
// that by 8x, 11x and 11x.
func canaryLatencyPR169Rounds() []canaryLatencyResult {
	ns := func(d int) time.Duration { return time.Duration(d) }
	// arm builds one arm from its p50 and p95, which is all the two gated
	// statistics read.
	arm := func(p50, p95 time.Duration) []time.Duration {
		return canaryLatencySamplesAt(p50, p95-p50)
	}
	round := func(aP50, aP95, bP50, bP95, xP50, xP95 time.Duration) canaryLatencyResult {
		return evaluateCanaryLatency(arm(aP50, aP95), arm(bP50, bP95), arm(xP50, xP95))
	}
	return []canaryLatencyResult{
		// Round 1: p50 baseline 4.00893 ms (fixed bar 400.893 µs), p95 baseline
		// 8.0563 ms (fixed bar 805.63 µs), both legacy arms agreeing.
		round(ns(4008930), ns(8056300), ns(4008930), ns(8056300),
			ns(4008930+143420), ns(8056300+79556457)),
		// Round 2: p50 baseline 4.02244 ms, p95 baseline 21.51255 ms.
		round(ns(4022440), ns(21512550), ns(4022440), ns(21512550),
			ns(4022440+142363), ns(21512550+92473720)),
		// Round 3: the median control collapses — the two byte-identical legacy
		// arms disagree by 442.871 µs at p50 around a 3.24268 ms baseline — while
		// the tail control stays narrow and the tail overhead is 74.651076 ms.
		round(ns(3464116), ns(17731470), ns(3021245), ns(17731470),
			ns(3242680+569458), ns(17731470+74651076)),
	}
}

// TestAX06_LatencyGateFailsThePR169CISequence is the regression test for the
// defect this round fixes, driven by the numbers CI actually printed.
//
// Two stacked ordering bugs produced one wrong answer:
//
//  1. WITHIN round 3, a p95 FAIL at 42x its budget was discarded because the p50
//     of the same round was unjudgeable — a definitive measurement suppressed by
//     the absence of another one.
//  2. ACROSS rounds, that single UNKNOWN round then erased two FAIL rounds that
//     had each caught the same regression at 79 ms and 92 ms over budget.
//
// The run reported UNKNOWN. Because cmd/testgate does not summarise skips, an
// UNKNOWN renders GREEN in the CI rollup, so the net effect was a run in which
// the tail definitively caught a ~75–90 ms regression reporting no failure at
// all — strictly worse than the gate SW-242 replaces.
//
// The test asserts the "before" as well as the "after", against the superseded
// rules kept in this file, so the sequence cannot silently stop being a
// regression test if a future edit makes the two rules agree.
func TestAX06_LatencyGateFailsThePR169CISequence(t *testing.T) {
	rounds := canaryLatencyPR169Rounds()
	if len(rounds) != canaryLatencyRounds {
		t.Fatalf("the CI sequence is %d rounds, got %d", canaryLatencyRounds, len(rounds))
	}

	// 1. The reconstruction reproduces the numbers the job printed. If it stops
	//    doing so, everything below is testing a different failure.
	us := time.Microsecond
	ms := time.Millisecond
	for _, w := range []struct {
		round            int
		stat             string
		overhead, budget time.Duration
	}{
		{0, "p50", 143420 * time.Nanosecond, 400893 * time.Nanosecond},
		{0, "p95", 79556457 * time.Nanosecond, 805630 * time.Nanosecond},
		{1, "p50", 142363 * time.Nanosecond, 402244 * time.Nanosecond},
		{1, "p95", 92473720 * time.Nanosecond, 2151255 * time.Nanosecond},
		{2, "p50", 569458 * time.Nanosecond, 1297072 * time.Nanosecond},
		{2, "p95", 74651076 * time.Nanosecond, 1773147 * time.Nanosecond},
	} {
		s := rounds[w.round].Median
		if w.stat == "p95" {
			s = rounds[w.round].Tail
		}
		if s.Overhead != w.overhead || s.Budget != w.budget {
			t.Fatalf("round %d %s: reconstruction drifted from the PR #169 log: want "+
				"overhead=%v budget=%v, got overhead=%v budget=%v",
				w.round+1, w.stat, w.overhead, w.budget, s.Overhead, s.Budget)
		}
	}
	// Round 3's p50 line was printed in full, so it is pinned in full.
	r3 := rounds[2].Median
	if r3.FixedBar != 324268*time.Nanosecond || r3.RefDelta != 442871*time.Nanosecond ||
		r3.NoiseTerm != 1328613*time.Nanosecond || r3.Ceiling != 1297072*time.Nanosecond {
		t.Fatalf("round 3 p50: want fixed=324.268µs noise=3x442.871µs=1.328613ms "+
			"ceiling=1.297072ms, got fixed=%v noise=3x%v=%v ceiling=%v",
			r3.FixedBar, r3.RefDelta, r3.NoiseTerm, r3.Ceiling)
	}

	// 2. BEFORE — the shipped round-3 rules, round by round and overall.
	wantBefore := []canaryLatencyVerdict{canaryLatencyFail, canaryLatencyFail, canaryLatencyUnknown}
	for i, r := range rounds {
		got := canaryLatencyComposeRound3Rule(r.Median, r.Tail).Verdict
		if got != wantBefore[i] {
			t.Fatalf("before: round %d composed to %s under the superseded rule, want %s "+
				"(that is what the CI log printed)", i+1, got, wantBefore[i])
		}
	}
	if before := canaryLatencyOverallRound3Rule(rounds); before.Verdict != canaryLatencyUnknown {
		t.Fatalf("before: the superseded arbitration answered %s on the PR #169 sequence, "+
			"want UNKNOWN — this test is no longer reproducing the defect", before.Verdict)
	}

	// 3. AFTER, part one — the WITHIN-round fix, isolated. Round 3 on its own:
	//    an unjudgeable median must not suppress a tail FAIL that is past 3x
	//    every scale the round demonstrated.
	if got := rounds[2].Verdict; got != canaryLatencyFail {
		t.Fatalf("within a round: round 3 composed to %s, want FAIL — its p95 overhead of %v "+
			"is %.0fx its %v budget and past 3x its %v ceiling, and an unjudgeable median "+
			"must not discard it: %s",
			got, rounds[2].Tail.Overhead,
			float64(rounds[2].Tail.Overhead)/float64(rounds[2].Tail.Budget),
			rounds[2].Tail.Budget, rounds[2].Tail.Ceiling, rounds[2])
	}
	if !strings.Contains(rounds[2].Reason, "MEDIAN of this round could not be judged") {
		t.Fatalf("the round must say WHY it failed with an unjudged median, got %q", rounds[2].Reason)
	}
	if single := canaryLatencyOverall(rounds[2:]); single.Verdict != canaryLatencyFail {
		t.Fatalf("within a round: round 3 alone answered %s, want FAIL", single.Verdict)
	}

	// 4. AFTER, part two — the ACROSS-rounds fix, isolated. Round 1's FAIL must
	//    survive a round that could not be judged. Round 3 is replaced by a
	//    round that resolved nothing at all, so only the arbitration can produce
	//    the FAIL.
	blind := evaluateCanaryLatency(
		canaryLatencySamplesAt(1*ms, 1*ms), canaryLatencySamplesAt(3*ms, 1*ms),
		canaryLatencySamplesAt(1*ms, 1*ms))
	if blind.Verdict != canaryLatencyUnknown {
		t.Fatalf("premise: the blind round must be UNKNOWN, got %s", blind)
	}
	across := canaryLatencyOverall([]canaryLatencyResult{rounds[0], blind, blind})
	if across.Verdict != canaryLatencyFail {
		t.Fatalf("across rounds: a decisive FAIL followed by two unjudgeable rounds answered "+
			"%s, want FAIL — an UNKNOWN round must not erase a FAIL that is outside the "+
			"degraded regime: %s", across.Verdict, across)
	}
	if !strings.Contains(across.Reason, "could not be judged, and that does NOT withdraw this FAIL") {
		t.Fatalf("the held FAIL must say that the unjudgeable rounds did not withdraw it, got %q",
			across.Reason)
	}

	// 5. AFTER — the whole sequence, which is what PR #169 ran.
	after := canaryLatencyOverall(rounds)
	if after.Verdict != canaryLatencyFail {
		t.Fatalf("the PR #169 sequence (FAIL, FAIL, UNKNOWN-with-a-tail-FAIL) answered %s, "+
			"want FAIL: %s", after.Verdict, after)
	}
	if after.Tail.Verdict != canaryLatencyFail {
		t.Fatalf("the tail is the statistic that caught it; the run must report the tail's "+
			"FAIL, got tail=%s", after.Tail.Verdict)
	}

	// 6. The boundary that keeps this safe: the same sequence with a MARGINAL
	//    tail FAIL — over budget, inside 3x the clamp — is still UNKNOWN, not
	//    FAIL. "Any FAIL always stands" was measured and rejected; this is the
	//    line that was drawn instead.
	marginal := evaluateCanaryLatency(
		canaryLatencySamplesAt(400*us, 100*us), canaryLatencySamplesAt(400*us, 100*us),
		canaryLatencySamplesIncident(400*us, 600*us, 3))
	if marginal.Verdict != canaryLatencyFail || marginal.Tail.Verdict != canaryLatencyFail {
		t.Fatalf("premise: the marginal round must be a tail FAIL, got %s", marginal)
	}
	if marginal.Tail.Overhead > 3*marginal.Tail.Ceiling {
		t.Fatalf("premise: the marginal round must NOT be decisive, overhead %v vs 3x%v",
			marginal.Tail.Overhead, marginal.Tail.Ceiling)
	}
	if got := canaryLatencyOverall([]canaryLatencyResult{marginal, blind, blind}); got.Verdict != canaryLatencyUnknown {
		t.Fatalf("boundary: a MARGINAL tail FAIL beside unjudgeable rounds must stay UNKNOWN, "+
			"got %s — round 4 has overcorrected into \"any FAIL always stands\": %s",
			got.Verdict, got)
	}

	t.Logf("PR #169 sequence: superseded rule -> UNKNOWN (green in the rollup), round 4 -> %s\n  %s\n  %s",
		after.Verdict, after, after.Reason)
}

func TestAX06_LatencyRoundArbitration(t *testing.T) {
	ms := time.Millisecond
	us := time.Microsecond
	at := canaryLatencySamplesAt

	// The regression the tail can see, against a narrow control: tail FAIL.
	tailFail := evaluateCanaryLatency(
		at(400*us, 100*us), at(400*us, 100*us), canaryLatencySamplesIncident(400*us, 2*ms, 3))
	// The SAME regression, against a control widened at p95 only: the tail can
	// no longer judge it, the median is still clean, so the round passes on the
	// median alone. Nothing about the seam changed between the two rounds — only
	// the machine did.
	medianOnly := evaluateCanaryLatency(
		at(400*us, 1*ms), at(400*us, 5*ms), canaryLatencySamplesIncident(400*us, 2*ms, 3))
	// Both statistics judged, both clean.
	fullPass := evaluateCanaryLatency(at(400*us, 100*us), at(400*us, 100*us), at(400*us, 100*us))
	// The median's own control past the ceiling: nothing was resolved at all.
	unknown := evaluateCanaryLatency(at(1*ms, 1*ms), at(3*ms, 1*ms), at(1*ms, 1*ms))
	// A systemic regression the median itself catches. MARGINAL: 251 µs is one
	// microsecond past a 250 µs budget and well inside 3x the 1 ms clamp.
	medianFail := evaluateCanaryLatency(
		at(400*us, 20*ms), at(400*us, 20*ms), at(400*us+251*us, 20*ms))
	// The same tail-caught shape as tailFail but DECISIVE: 29.9 ms of tail
	// overhead against a 1 ms ceiling, past 3x every scale any round below
	// demonstrates. This is the PR #169 magnitude in miniature.
	tailFailDecisive := evaluateCanaryLatency(
		at(400*us, 100*us), at(400*us, 100*us), canaryLatencySamplesIncident(400*us, 30*ms, 3))
	// The PR #169 round-3 shape: the MEDIAN control collapsed while the tail
	// control stayed narrow and the tail overhead was enormous. The round must
	// FAIL — an unjudgeable median does not suppress a definitive tail.
	medianUnknownTailDecisive := evaluateCanaryLatency(
		at(1*ms, 5*ms), at(3*ms, 3*ms), at(2*ms, 14*ms))

	// The premises the table rests on. If any of these stops holding, the cases
	// below would still pass while testing something else, so they are checked
	// rather than assumed.
	for _, p := range []struct {
		name         string
		r            canaryLatencyResult
		run          canaryLatencyVerdict
		med, tailVer canaryLatencyVerdict
	}{
		{"tailFail", tailFail, canaryLatencyFail, canaryLatencyPass, canaryLatencyFail},
		{"medianOnly", medianOnly, canaryLatencyPass, canaryLatencyPass, canaryLatencyUnknown},
		{"fullPass", fullPass, canaryLatencyPass, canaryLatencyPass, canaryLatencyPass},
		{"unknown", unknown, canaryLatencyUnknown, canaryLatencyUnknown, canaryLatencyUnknown},
		{"medianFail", medianFail, canaryLatencyFail, canaryLatencyFail, canaryLatencyPass},
		{"tailFailDecisive", tailFailDecisive, canaryLatencyFail, canaryLatencyPass, canaryLatencyFail},
		{"medianUnknownTailDecisive", medianUnknownTailDecisive,
			canaryLatencyFail, canaryLatencyUnknown, canaryLatencyFail},
	} {
		if p.r.Verdict != p.run || p.r.Median.Verdict != p.med || p.r.Tail.Verdict != p.tailVer {
			t.Fatalf("premise %s: want run=%s median=%s tail=%s, got run=%s median=%s tail=%s: %s",
				p.name, p.run, p.med, p.tailVer, p.r.Verdict, p.r.Median.Verdict, p.r.Tail.Verdict, p.r)
		}
	}

	// The MARGINAL/DECISIVE split the round-4 arbitration turns on, asserted so
	// the cases below cannot quietly stop exercising both sides of it.
	for _, d := range []struct {
		name     string
		stat     canaryLatencyStat
		decisive bool
	}{
		{"tailFail (marginal)", tailFail.Tail, false},
		{"medianFail (marginal)", medianFail.Median, false},
		{"tailFailDecisive", tailFailDecisive.Tail, true},
		{"medianUnknownTailDecisive", medianUnknownTailDecisive.Tail, true},
	} {
		if got := canaryLatencyStatDecisive(d.stat, d.stat.RefDelta); got != d.decisive {
			t.Fatalf("premise %s: decisive=%v, want %v (overhead %v, ceiling %v, control %v)",
				d.name, got, d.decisive, d.stat.Overhead, d.stat.Ceiling, d.stat.RefDelta)
		}
	}

	for _, tc := range []struct {
		name    string
		rounds  []canaryLatencyResult
		want    canaryLatencyVerdict
		checkFn func(t *testing.T, got canaryLatencyResult)
	}{
		{
			// The review finding, in its original direction: a genuine
			// tail-caught FAIL followed by a degraded-tail round. The later
			// round measured NOTHING at the tail, so it cannot turn the run
			// green. It is not entitled to turn it red either — one
			// unrepeatable measurement is not this gate's standard for failing
			// — so the run is UNKNOWN and the FAIL is quoted in the reason.
			name:   "a_later_median_only_pass_does_not_turn_a_tail_fail_into_a_pass",
			rounds: []canaryLatencyResult{tailFail, medianOnly},
			want:   canaryLatencyUnknown,
			checkFn: func(t *testing.T, got canaryLatencyResult) {
				if !strings.Contains(got.Reason, "recorded a FAIL") {
					t.Fatalf("the held FAIL must be reported, not dropped, got %q", got.Reason)
				}
				if !strings.Contains(got.Reason, "minority of calls") {
					t.Fatalf("the reason must quote the FAIL it is holding, got %q", got.Reason)
				}
			},
		},
		{
			// The same in the other order, which is also the shape that used to
			// end the round loop at round 1.
			name:   "an_earlier_median_only_pass_does_not_turn_a_tail_fail_into_a_pass",
			rounds: []canaryLatencyResult{medianOnly, tailFail, medianOnly},
			want:   canaryLatencyUnknown,
		},
		{
			name:   "a_median_only_pass_does_not_turn_a_median_fail_into_a_pass",
			rounds: []canaryLatencyResult{medianFail, medianOnly},
			want:   canaryLatencyUnknown,
		},
		{
			// The anti-flake provision itself is untouched for the rounds that
			// earn it: a round in which the WHOLE gate ran and passed still
			// wins outright, which is what keeps a genuinely noisy FAIL from
			// taxing the merge.
			name:   "a_full_pass_still_wins_over_a_fail",
			rounds: []canaryLatencyResult{tailFail, fullPass},
			want:   canaryLatencyPass,
			checkFn: func(t *testing.T, got canaryLatencyResult) {
				if got.Tail.Verdict != canaryLatencyPass {
					t.Fatalf("only a full pass may win: got a pass whose tail is %s", got.Tail.Verdict)
				}
			},
		},
		{
			// AC-3 across rounds: a regression that keeps being measured keeps
			// being FAILed. This is the case the arbitration must not soften,
			// and the one the minority-incidence demonstration relies on.
			name:   "a_tail_fail_in_every_round_is_a_fail",
			rounds: []canaryLatencyResult{tailFail, tailFail, tailFail},
			want:   canaryLatencyFail,
			checkFn: func(t *testing.T, got canaryLatencyResult) {
				if got.Tail.Verdict != canaryLatencyFail {
					t.Fatalf("the tail must be the statistic that failed, got %s", got.Tail.Verdict)
				}
			},
		},
		{
			name:   "a_median_fail_in_every_round_is_a_fail",
			rounds: []canaryLatencyResult{medianFail, medianFail, medianFail},
			want:   canaryLatencyFail,
		},
		{
			name:   "fails_of_either_statistic_across_rounds_still_fail",
			rounds: []canaryLatencyResult{tailFail, medianFail, tailFail},
			want:   canaryLatencyFail,
		},
		{
			// Pre-existing and deliberately unchanged: a round whose MEDIAN
			// control collapsed impeaches the apparatus for the whole
			// invocation, so it outranks a FAIL.
			name:   "an_unknown_round_still_beats_a_fail",
			rounds: []canaryLatencyResult{medianFail, unknown},
			want:   canaryLatencyUnknown,
		},
		{
			// The boundary. This is the SAME shape as
			// a_decisive_tail_fail_survives_an_unknown_round below, at a
			// magnitude inside 3x the clamp. A marginal FAIL is exactly what
			// "any FAIL always stands" would have red-flagged here, at ~10 % of
			// clean contended runs (§5.9), and it is what round 4 deliberately
			// does not do.
			name:   "an_unknown_round_still_beats_a_MARGINAL_tail_fail",
			rounds: []canaryLatencyResult{tailFail, unknown},
			want:   canaryLatencyUnknown,
		},
		{
			// The all-median-only run. Half the gate never ran, so the run is
			// reported as such: not PASS, which would claim a conclusion it
			// never reached, and not FAIL, which would fail a run for being
			// measured on a busy machine.
			name:   "every_round_median_only_is_reported_as_unknown_not_pass",
			rounds: []canaryLatencyResult{medianOnly, medianOnly, medianOnly},
			want:   canaryLatencyUnknown,
			checkFn: func(t *testing.T, got canaryLatencyResult) {
				if !canaryLatencyMedianOnly(got) {
					t.Fatalf("the aggregate must carry the median-only numbers, got %s", got)
				}
				if !strings.Contains(got.Reason, "MEDIAN ALONE") {
					t.Fatalf("the reason must state which half of the gate ran, got %q", got.Reason)
				}
				if strings.Contains(got.Reason, "recorded a FAIL") {
					t.Fatalf("no round failed here, so nothing may be reported as held: %q", got.Reason)
				}
				if !strings.Contains(got.String(), "median-only verdict") {
					t.Fatalf("the verdict line must be annotated median-only, got %s", got)
				}
			},
		},
		{
			name:   "a_single_median_only_round_is_also_not_a_pass",
			rounds: []canaryLatencyResult{medianOnly},
			want:   canaryLatencyUnknown,
		},
		{
			name:   "every_round_full_pass_is_a_pass",
			rounds: []canaryLatencyResult{fullPass, fullPass},
			want:   canaryLatencyPass,
		},
		{
			// SW-242 round 4, within a round: the PR #169 round-3 shape. The
			// median could not be judged; the tail measured a regression past
			// 3x every scale the round demonstrated. The round FAILs.
			name:   "a_decisive_tail_fail_is_not_suppressed_by_an_unjudgeable_median",
			rounds: []canaryLatencyResult{medianUnknownTailDecisive},
			want:   canaryLatencyFail,
			checkFn: func(t *testing.T, got canaryLatencyResult) {
				if got.Median.Verdict != canaryLatencyUnknown {
					t.Fatalf("the premise of this case is an unjudged median, got %s",
						got.Median.Verdict)
				}
				if !strings.Contains(got.Reason, "MEDIAN of this round could not be judged") {
					t.Fatalf("the reason must state that the median was not judged, got %q",
						got.Reason)
				}
			},
		},
		{
			// SW-242 round 4, across rounds: this is the case PR #169 failed
			// on. A round that resolved NOTHING does not withdraw a FAIL that
			// is outside the degraded regime.
			name:   "a_decisive_tail_fail_survives_an_unknown_round",
			rounds: []canaryLatencyResult{tailFailDecisive, unknown},
			want:   canaryLatencyFail,
			checkFn: func(t *testing.T, got canaryLatencyResult) {
				if !strings.Contains(got.Reason, "does NOT withdraw this FAIL") {
					t.Fatalf("the run must say the unjudgeable round did not withdraw the "+
						"FAIL, got %q", got.Reason)
				}
			},
		},
		{
			name:   "a_decisive_fail_survives_an_unknown_round_in_either_order",
			rounds: []canaryLatencyResult{unknown, tailFailDecisive, unknown},
			want:   canaryLatencyFail,
		},
		{
			// The same across a median-only round, which is the shape §5.6
			// measured as the COMMON one on a contended runner.
			name:   "a_decisive_tail_fail_survives_a_median_only_round",
			rounds: []canaryLatencyResult{tailFailDecisive, medianOnly, medianOnly},
			want:   canaryLatencyFail,
		},
		{
			// The anti-flake provision outranks even a decisive FAIL: a full
			// pass is a POSITIVE measurement that contradicts it, where an
			// UNKNOWN round is the absence of one. That asymmetry is the whole
			// principle, and it cuts both ways.
			name:   "a_full_pass_still_wins_over_a_decisive_fail",
			rounds: []canaryLatencyResult{tailFailDecisive, fullPass},
			want:   canaryLatencyPass,
		},
		{
			name:   "no_rounds_is_unknown",
			rounds: nil,
			want:   canaryLatencyUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := canaryLatencyOverall(tc.rounds)
			if got.Verdict != tc.want {
				t.Fatalf("want %s, got %s: %s", tc.want, got.Verdict, got)
			}
			// The rule that makes all of the above one rule: PASS is reserved
			// for runs in which BOTH gated statistics were judged and passed.
			if got.Verdict == canaryLatencyPass && got.Tail.Verdict != canaryLatencyPass {
				t.Fatalf("a run may only report PASS on a full pass, got tail=%s: %s",
					got.Tail.Verdict, got)
			}
			if got.Verdict != canaryLatencyPass && got.Reason == "" {
				t.Fatalf("%s must carry a reason", got.Verdict)
			}
			if tc.checkFn != nil {
				tc.checkFn(t, got)
			}
		})
	}
}

// canaryLatencyExtraLegacyPassesEvery widens the same-run A/A control at the
// TAIL ONLY: the extra legacy work is charged to one call in `every`, so the
// reference arm's p95 carries the full injected cost while its median does not
// move at all.
//
// That is the one part of a contended runner that can be reproduced on demand
// in the shape that matters here — §5.6 measured the p95 control widening from
// 6–28 µs idle to 297 µs–1.12 ms under load while the median control stayed
// usable — and it is what produces a median-only round deterministically.
func canaryLatencyExtraLegacyPassesEvery(n, every int) func(testing.TB, *Direct) {
	inner := canaryLatencyExtraLegacyPasses(n)
	calls := 0
	return func(t testing.TB, direct *Direct) {
		calls++
		if every > 0 && calls%every == 0 {
			inner(t, direct)
		}
	}
}

// TestAX06_LatencyGateStopsOnlyOnAFullPass drives the SHIPPED round loop on a
// runner whose tail control has been widened the way contention widens it, and
// requires the loop to keep going.
//
// The rounds exist so a regression gets more than one chance to be measured on
// a calm interval. A median-only round has not measured the tail at all, so
// stopping on it spends that provision to buy nothing — and it is not a rare
// state: under the load in §5.6 the tail was unjudgeable in 4 runs out of 6, so
// before this change the gate would routinely have ended after round 1 on
// exactly the runners where the extra rounds are the entire point.
func TestAX06_LatencyGateStopsOnlyOnAFullPass(t *testing.T) {
	if testing.Short() {
		t.Skip("the round loop drives the real sampler")
	}
	direct := canaryLatencyFixture(t)
	// One call in ten carries 20 extra legacy passes: p95 of legacy-b moves by
	// the full injected cost, p50 does not move at all. The executor arm is
	// untouched — there is no regression here, only a machine that can no
	// longer resolve its own tail.
	arms := canaryLatencyArmsWith(canaryArmReference, canaryLatencyExtraLegacyPassesEvery(20, 10))
	results, _ := runCanaryLatencyGate(t, direct, arms)
	if len(results) == 0 {
		t.Fatal("the gate must run at least one round")
	}

	medianOnly := 0
	for i, r := range results {
		t.Logf("round %d/%d: %s", i+1, len(results), r)
		if r.Verdict == canaryLatencyPass && r.Tail.Verdict == canaryLatencyPass {
			// The injection did not manage to make the tail unjudgeable on this
			// machine, so the loop was entitled to stop. Nothing is proved
			// either way; say so with the numbers rather than passing quietly.
			t.Skipf("inconclusive: round %d resolved a FULL pass despite the injected tail "+
				"noise, so the early exit was legitimate and the property under test was not "+
				"exercised: %s", i+1, r)
		}
		if r.Verdict == canaryLatencyPass && r.Tail.Verdict == canaryLatencyUnknown {
			medianOnly++
		}
	}
	// The property: with no full pass anywhere, the loop must have used every
	// round it has. Before SW-242 round 3 it stopped at the first median-only
	// round — typically round 1.
	if len(results) != canaryLatencyRounds {
		t.Fatalf("no round was a full pass, so the gate must use all %d rounds, it used %d. "+
			"A median-only pass must not end the round loop: %s",
			canaryLatencyRounds, len(results), results[len(results)-1])
	}
	if medianOnly == 0 {
		t.Skipf("inconclusive: the injected tail noise produced no median-only round in %d "+
			"round(s), so the property under test was not exercised: %s",
			len(results), results[len(results)-1])
	}

	// ...and the run it produces is not a pass. There is no regression here, so
	// the honest answer is UNKNOWN — half the gate ran — and NOT the PASS this
	// same measurement produced before round 3.
	overall := canaryLatencyOverall(results)
	if overall.Verdict == canaryLatencyPass {
		t.Fatalf("a run containing no full-pass round must not report PASS, got %s", overall)
	}
	allMedianOnly := true
	for _, r := range results {
		if r.Verdict != canaryLatencyPass || r.Tail.Verdict != canaryLatencyUnknown {
			allMedianOnly = false
		}
	}
	if allMedianOnly {
		if overall.Verdict != canaryLatencyUnknown {
			t.Fatalf("a run of nothing but median-only rounds must report UNKNOWN, got %s", overall)
		}
		if !strings.Contains(overall.Reason, "MEDIAN ALONE") {
			t.Fatalf("the reason must state which half of the gate ran, got %q", overall.Reason)
		}
	}
	t.Logf("round-loop demonstration: %d/%d rounds ran, %d of them median-only -> %s\n  %s\n  %s",
		len(results), canaryLatencyRounds, medianOnly, overall.Verdict, overall, overall.Reason)
}

// canaryLatencyComposeRound3Rule and canaryLatencyOverallRound3Rule are the
// SUPERSEDED SW-242 round-3 rules — the ones that shipped at e03bd97 and that
// PR #169 proved wrong. They are kept ONLY as the comparison arm of the two
// property tests below, so "the round-4 change can only ever turn an UNKNOWN
// into a FAIL" is a checked statement about two functions rather than a claim in
// prose. Neither is ever called by the gate.
//
// The round-3 composition collapsed a round to UNKNOWN whenever the median was
// unjudgeable, even when the tail had definitively FAILed; the round-3
// arbitration then let a single such round outrank every FAIL in the run.
func canaryLatencyComposeRound3Rule(median, tail canaryLatencyStat) canaryLatencyResult {
	r := canaryLatencyResult{Median: median, Tail: tail}
	switch {
	case r.Median.Verdict == canaryLatencyUnknown:
		r.Verdict = canaryLatencyUnknown
		r.Reason = r.Median.Reason
	case r.Median.Verdict == canaryLatencyFail:
		r.Verdict = canaryLatencyFail
		r.Reason = r.Median.Reason
	case r.Tail.Verdict == canaryLatencyFail:
		r.Verdict = canaryLatencyFail
		r.Reason = r.Tail.Reason + canaryLatencyTailFailNote(r.Median)
	default:
		r.Verdict = canaryLatencyPass
	}
	return r
}

func canaryLatencyOverallRound3Rule(results []canaryLatencyResult) canaryLatencyResult {
	if len(results) == 0 {
		return canaryLatencyResult{Verdict: canaryLatencyUnknown, Reason: "no rounds ran"}
	}
	rounds := make([]canaryLatencyResult, 0, len(results))
	for _, r := range results {
		rounds = append(rounds, canaryLatencyComposeRound3Rule(r.Median, r.Tail))
	}
	for _, r := range rounds {
		if canaryLatencyFullPass(r) {
			return r
		}
	}
	for _, r := range rounds {
		if r.Verdict == canaryLatencyUnknown {
			return r
		}
	}
	for _, r := range rounds {
		if canaryLatencyMedianOnlyPass(r) {
			return canaryLatencyMedianOnlyOverall(rounds)
		}
	}
	for _, r := range rounds {
		if r.Verdict == canaryLatencyFail {
			return r
		}
	}
	return rounds[len(rounds)-1]
}

// canaryLatencyOverallAnyFailStands is the REJECTED variant: "any FAIL always
// stands", with no decisiveness test at all. Round 3 measured it at 10.4 % false
// FAIL on a contended runner and rejected it; §5.9 re-measures it beside the
// shipped rule so the boundary the round-4 change draws is justified by a
// number rather than by an assurance. Never called by the gate.
func canaryLatencyOverallAnyFailStands(results []canaryLatencyResult) canaryLatencyResult {
	if len(results) == 0 {
		return canaryLatencyResult{Verdict: canaryLatencyUnknown, Reason: "no rounds ran"}
	}
	rounds := make([]canaryLatencyResult, 0, len(results))
	for _, r := range results {
		rounds = append(rounds, canaryLatencyComposeRound3Rule(r.Median, r.Tail))
	}
	for _, r := range rounds {
		if canaryLatencyFullPass(r) {
			return r
		}
	}
	for _, r := range rounds {
		if r.Verdict == canaryLatencyFail {
			return r
		}
	}
	for _, r := range rounds {
		if r.Verdict == canaryLatencyUnknown {
			return r
		}
	}
	return canaryLatencyMedianOnlyOverall(rounds)
}

// canaryLatencyStopAt models a round loop's early exit over a pre-computed
// slate of rounds: the rounds the gate would actually have looked at, given a
// stop condition. The two rules stop on different things, so comparing them
// fairly means comparing the runs each would have performed, not just the
// verdicts each would have returned on a common slate.
func canaryLatencyStopAt(rounds []canaryLatencyResult, stop func(canaryLatencyResult) bool) []canaryLatencyResult {
	for i, r := range rounds {
		if stop(r) {
			return rounds[:i+1]
		}
	}
	return rounds
}

// canaryLatencyShapeStat builds ONE synthesised statistic for the arbitration
// property test: a verdict plus the arithmetic the decisiveness test reads.
//
// The numbers are chosen so the shapes stay meaningful when they are mixed in
// one slate. A DECISIVE FAIL is 50 ms against a 1 ms ceiling, which is past 3x
// the widest control any shape here produces (an UNKNOWN statistic's 2 ms), so a
// decisive FAIL stays decisive even in a run whose other rounds could not
// resolve anything — which is exactly the situation the round-4 change is about.
// A MARGINAL FAIL is 500 µs: past its budget, well inside 3x the clamp.
func canaryLatencyShapeStat(name string, v canaryLatencyVerdict, decisive bool) canaryLatencyStat {
	s := canaryLatencyStat{
		Name:     name,
		Verdict:  v,
		FixedBar: 250 * time.Microsecond,
		Budget:   250 * time.Microsecond,
		Ceiling:  time.Millisecond,
		RefDelta: 10 * time.Microsecond,
	}
	switch v {
	case canaryLatencyFail:
		s.Overhead = 500 * time.Microsecond
		if decisive {
			s.Overhead = 50 * time.Millisecond
		}
		s.Reason = "synthetic FAIL"
	case canaryLatencyUnknown:
		// An unjudgeable statistic is one whose own A/A control blew past the
		// ceiling; carrying that width is what makes it contribute honestly to
		// the run's demonstrated resolution.
		s.RefDelta = 2 * time.Millisecond
		s.NoiseTerm = 6 * time.Millisecond
		s.Budget = s.Ceiling
		s.Reason = "synthetic UNKNOWN"
	}
	if v == canaryLatencyFail {
		s.NoiseTerm = 30 * time.Microsecond
	}
	return s
}

// canaryLatencyRoundShapes returns one result per reachable (median, tail)
// combination, composed through the SHIPPED composition rather than asserted.
//
// FAIL appears twice for each statistic — marginal and decisive — because that
// distinction is the whole boundary the round-4 change draws, and a shape table
// that could not express it would let the property tests pass while testing
// nothing. TestAX06_LatencyRoundArbitration pins the shapes that matter against
// real evaluated sample data.
func canaryLatencyRoundShapes() []canaryLatencyResult {
	p := canaryLatencyShapeStat
	type spec struct {
		medV, tailV     canaryLatencyVerdict
		medDec, tailDec bool
	}
	pass, fail, unk := canaryLatencyPass, canaryLatencyFail, canaryLatencyUnknown
	specs := []spec{
		{pass, pass, false, false}, // full pass
		{pass, unk, false, false},  // median-only pass
		{pass, fail, false, false}, // tail caught it, marginally
		{pass, fail, false, true},  // tail caught it, decisively
		{fail, pass, false, false}, // median caught it, marginally
		{fail, pass, true, false},  // median caught it, decisively
		{fail, unk, false, false},  // median caught it, tail unjudgeable
		{fail, unk, true, false},   // ditto, decisively
		{fail, fail, false, false}, // both caught it
		{fail, fail, true, true},   // both, decisively
		{unk, pass, false, false},  // median unjudgeable
		{unk, fail, false, false},  // median unjudgeable, marginal tail FAIL
		{unk, fail, false, true},   // median unjudgeable, DECISIVE tail FAIL
		{unk, unk, false, false},   // nothing resolved
	}
	out := make([]canaryLatencyResult, 0, len(specs))
	for _, sp := range specs {
		out = append(out, canaryLatencyCompose(
			p("p50", sp.medV, sp.medDec), p("p95", sp.tailV, sp.tailDec)))
	}
	return out
}

// TestAX06_LatencyArbitrationIsMonotone is the safety half of the round-4
// change, and it is a proof rather than a sample.
//
// Round 4 makes the gate STRICTER in two places — a decisive FAIL now survives
// an unjudgeable median within a round, and an unjudgeable round across rounds —
// and the whole reason SW-242 exists is that a gate which cries wolf is worse
// than no gate. So the question is not whether the new rule is stricter (it is)
// but whether the extra strictness can land on a run the SHIPPED rule passed. It
// cannot, and the reason is structural rather than statistical:
//
//   - The round-4 composition FAILs a superset of the rounds the round-3
//     composition FAILed (it adds "median UNKNOWN + decisive tail FAIL" and
//     changes nothing else), and it PASSes exactly the same rounds.
//   - Both arbitrations scan for a full pass FIRST, so a run containing a full
//     pass reports PASS under both.
//   - Round 4's extra FAIL branch is reachable only when no round was a full
//     pass, so it can only ever convert UNKNOWN into FAIL.
//
// So the two rules differ ONLY by turning some UNKNOWN verdicts into FAIL. This
// test walks every sequence of one, two and three rounds over every reachable
// round shape and checks the implication on each, plus that the difference is
// real, so the property cannot be satisfied vacuously by the change not being
// wired in.
func TestAX06_LatencyArbitrationIsMonotone(t *testing.T) {
	shapes := canaryLatencyRoundShapes()
	fullStop := func(r canaryLatencyResult) bool { return canaryLatencyFullPass(r) }

	var checked, disagreed int
	var walk func(prefix []canaryLatencyResult)
	walk = func(prefix []canaryLatencyResult) {
		if len(prefix) > 0 {
			slate := append([]canaryLatencyResult(nil), prefix...)
			stopped := canaryLatencyStopAt(slate, fullStop)
			now := canaryLatencyOverall(stopped)
			was := canaryLatencyOverallRound3Rule(stopped)
			checked++
			if now.Verdict == canaryLatencyPass && was.Verdict != canaryLatencyPass {
				t.Fatalf("round 4 PASSED a run the shipped rule answered %s: %v",
					was.Verdict, canaryLatencyShapeNames(stopped))
			}
			if was.Verdict == canaryLatencyPass && now.Verdict != canaryLatencyPass {
				t.Fatalf("round 4 answered %s on a run the shipped rule PASSED — the "+
					"arbitration is manufacturing failures: %v",
					now.Verdict, canaryLatencyShapeNames(stopped))
			}
			if now.Verdict == canaryLatencyPass && now.Tail.Verdict != canaryLatencyPass {
				t.Fatalf("round 4 reported PASS with an unjudged tail: %v",
					canaryLatencyShapeNames(stopped))
			}
			if now.Verdict != canaryLatencyPass && now.Reason == "" {
				t.Fatalf("round 4 reported %s with no reason: %v",
					now.Verdict, canaryLatencyShapeNames(stopped))
			}
			if now.Verdict != was.Verdict {
				disagreed++
				// The only permitted difference.
				if was.Verdict != canaryLatencyUnknown || now.Verdict != canaryLatencyFail {
					t.Fatalf("the shipped rule said %s and round 4 said %s; the only difference "+
						"the change is allowed to make is UNKNOWN -> FAIL: %v",
						was.Verdict, now.Verdict, canaryLatencyShapeNames(stopped))
				}
				// And it may only make it where a FAIL was actually decisive.
				medRes, tailRes := canaryLatencyRunResolution(stopped)
				var decisive bool
				for _, r := range stopped {
					if canaryLatencyDecisiveRound(r, medRes, tailRes) {
						decisive = true
					}
				}
				if !decisive {
					t.Fatalf("round 4 turned UNKNOWN into FAIL with no decisive FAIL in the "+
						"run: %v", canaryLatencyShapeNames(stopped))
				}
			}
		}
		if len(prefix) == canaryLatencyRounds {
			return
		}
		for _, sh := range shapes {
			walk(append(prefix, sh))
		}
	}
	walk(nil)

	n := len(shapes)
	if checked != n+n*n+n*n*n {
		t.Fatalf("walked %d sequences, expected every sequence of 1..%d rounds over %d shapes",
			checked, canaryLatencyRounds, n)
	}
	if disagreed == 0 {
		t.Fatal("the two rules never disagreed: the monotonicity property is vacuous, " +
			"which means the round-4 change is not wired into canaryLatencyOverall")
	}
	t.Logf("arbitration monotonicity: %d round sequences over %d shapes checked, %d differ "+
		"from the shipped rule, every difference UNKNOWN -> FAIL and every one carrying a "+
		"decisive FAIL; no sequence exists on which round 4 fails a run the shipped rule passed",
		checked, n, disagreed)
}

// canaryLatencyShapeNames renders a slate as its verdict triples, so a failure
// in the walk above names the sequence that broke rather than a struct dump.
func canaryLatencyShapeNames(rounds []canaryLatencyResult) []string {
	out := make([]string, 0, len(rounds))
	for _, r := range rounds {
		out = append(out, fmt.Sprintf("%s(p50=%s,p95=%s)", r.Verdict, r.Median.Verdict, r.Tail.Verdict))
	}
	return out
}

// canaryLatencyNoisyArm draws one arm's samples from a heavy-right-tailed noise
// model. The spike term is what makes the model worth anything: contention is a
// tail phenomenon, and it is precisely the p95 disagreement between two
// byte-identical arms that produces the median-only rounds where the round-1
// and round-3 rules differ. A purely Gaussian model resolves every tail and the
// two rules are then trivially identical.
//
// The levels used below are calibrated against this gate's OWN measured
// observables rather than invented: doc §5.1 for the idle machine (p50 ~385 µs,
// p50 A/A control 0.25–3.4 µs, p95 A/A control 6–28 µs) and §5.6 for the
// contended one (p95 A/A control 297 µs–1.12 ms with the median budget still at
// its 250 µs floor). TestAX06_LatencyNoiseModelIsCalibrated checks that the
// model still reproduces them.
func canaryLatencyNoisyArm(rng *rand.Rand, mean time.Duration, sdFrac, spikeRate, spikeMult float64) []time.Duration {
	const n = 201
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		v := float64(mean) * (1 + sdFrac*rng.NormFloat64())
		if rng.Float64() < spikeRate {
			v *= 1 + spikeMult*rng.ExpFloat64()
		}
		if v < float64(time.Microsecond) {
			v = float64(time.Microsecond)
		}
		out = append(out, time.Duration(v))
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// canaryLatencyNoiseLevel is one calibrated runner condition for the Monte
// Carlo below.
type canaryLatencyNoiseLevel struct {
	name                        string
	mean                        time.Duration
	sdFrac, spikeRate, spikeMul float64
}

// canaryLatencyNoiseLevels are the conditions the measurements in §5.8 are
// taken under: an idle machine, a lightly loaded one, the runner §5.6 actually
// measured under 24 spinning processes, and one worse than any yet observed.
func canaryLatencyNoiseLevels() []canaryLatencyNoiseLevel {
	return []canaryLatencyNoiseLevel{
		{"idle", 385 * time.Microsecond, 0.05, 0.05, 0.4},
		{"lightly_loaded", 800 * time.Microsecond, 0.20, 0.08, 0.8},
		{"contended_as_measured", 900 * time.Microsecond, 0.30, 0.12, 1.5},
		{"worse_than_measured", 2 * time.Millisecond, 0.35, 0.15, 2.0},
		// The runner PR #169 actually ran on, calibrated to the numbers in that
		// job's own log rather than to a laptop: a p50 baseline of 3.24 ms
		// (round 3 printed fixed=324.268 µs, which is 10 % of it) and a p50 A/A
		// control wide enough to reach the 442.871 µs that collapsed round 3's
		// median. It is the condition the defect lives on, so the rates below
		// are meaningless without it.
		{"ci_pr169_runner", 3240 * time.Microsecond, 0.40, 0.18, 2.5},
	}
}

// TestAX06_LatencyNoiseModelIsCalibrated keeps the Monte Carlo below honest.
// A noise model tuned to make a rule look good is worth nothing, so the model's
// observables are pinned against the numbers this gate measured on real
// machines: §5.1's idle figures and §5.6's contended ones. If a future edit
// moves the model, this fails before the rates below quietly change meaning.
func TestAX06_LatencyNoiseModelIsCalibrated(t *testing.T) {
	if testing.Short() {
		t.Skip("the noise model check is not a -short gate")
	}
	quantile := func(x []float64, p float64) time.Duration {
		sort.Float64s(x)
		return time.Duration(x[int(float64(len(x)-1)*p)])
	}
	for _, lvl := range canaryLatencyNoiseLevels() {
		rng := rand.New(rand.NewSource(20260828))
		var p50s, d50s, d95s []float64
		for i := 0; i < 400; i++ {
			a := canaryLatencyNoisyArm(rng, lvl.mean, lvl.sdFrac, lvl.spikeRate, lvl.spikeMul)
			b := canaryLatencyNoisyArm(rng, lvl.mean, lvl.sdFrac, lvl.spikeRate, lvl.spikeMul)
			p50s = append(p50s, float64(percentile(a, 0.50)))
			abs := func(d time.Duration) float64 {
				if d < 0 {
					return float64(-d)
				}
				return float64(d)
			}
			d50s = append(d50s, abs(percentile(a, 0.50)-percentile(b, 0.50)))
			d95s = append(d95s, abs(percentile(a, 0.95)-percentile(b, 0.95)))
		}
		t.Logf("%-21s p50=%v | A/A control p50: med=%v p90=%v | A/A control p95: med=%v p90=%v",
			lvl.name, quantile(p50s, 0.50), quantile(d50s, 0.50), quantile(d50s, 0.90),
			quantile(d95s, 0.50), quantile(d95s, 0.90))

		switch lvl.name {
		case "idle":
			// §5.1: p50 ~385 µs, p50 control 0.25–3.4 µs, p95 control 6–28 µs.
			if got := quantile(p50s, 0.50); got < 350*time.Microsecond || got > 425*time.Microsecond {
				t.Fatalf("idle p50 %v is not the ~385 µs §5.1 measured", got)
			}
			if got := quantile(d50s, 0.90); got > 10*time.Microsecond {
				t.Fatalf("idle p50 A/A control %v is far above the 0.25–3.4 µs §5.1 measured", got)
			}
			if got := quantile(d95s, 0.90); got > 60*time.Microsecond {
				t.Fatalf("idle p95 A/A control %v is far above the 6–28 µs §5.1 measured", got)
			}
		case "ci_pr169_runner":
			// PR #169's own log: round 3 printed a p50 fixed bar of 324.268 µs
			// (so a 3.24268 ms p50 baseline) and a p50 A/A control of
			// 442.871 µs, which is what took that round's median to UNKNOWN.
			// The model has to be able to produce both.
			if got := quantile(p50s, 0.50); got < 2900*time.Microsecond || got > 3600*time.Microsecond {
				t.Fatalf("PR #169 p50 %v is not the ~3.24 ms that job's round 3 printed", got)
			}
			if got := quantile(d50s, 0.90); got < 150*time.Microsecond || got > 600*time.Microsecond {
				t.Fatalf("PR #169 p50 A/A control %v does not bracket the 442.871 µs that "+
					"collapsed round 3's median", got)
			}
		case "contended_as_measured":
			// §5.6: p95 control 297 µs–1.12 ms, median budget still at its floor
			// (which needs 3 x the p50 control to stay under 250 µs).
			if got := quantile(d95s, 0.50); got < 250*time.Microsecond || got > 1200*time.Microsecond {
				t.Fatalf("contended p95 A/A control %v is outside the 297 µs–1.12 ms §5.6 measured", got)
			}
			if got := quantile(d50s, 0.90); got > 83*time.Microsecond {
				t.Fatalf("contended p50 A/A control %v would move the median budget off its "+
					"250 µs floor, which §5.6 measured it never leaving", got)
			}
		}
	}
}

// TestAX06_LatencyArbitrationOnPureNoise is the measurement half of the round-4
// change: what the new rule costs on runs with NO regression injected anywhere —
// every arm in every round drawn from one distribution, so any FAIL is a false
// FAIL by construction — and what it buys on runs that DO carry a
// minority-incidence regression.
//
// Three rules are evaluated on the SAME trials, so the comparison is paired
// rather than across separate samples:
//
//   - shipped: the round-3 rule PR #169 failed under;
//   - round 4: the rule this file implements;
//   - "any FAIL stands": the variant round 3 measured at ~10.4 % false FAIL and
//     rejected, re-measured here so the boundary round 4 draws is justified by a
//     number rather than by an assurance.
//
// The assertion is the one the story turns on: on the contended condition the
// round-4 false-FAIL rate must not exceed the shipped rule's by more than a
// rounding of the sample, and must stay far below the rejected variant's.
func TestAX06_LatencyArbitrationOnPureNoise(t *testing.T) {
	if testing.Short() {
		t.Skip("the Monte Carlo is not a -short gate")
	}
	const trials = 2000
	fullStop := func(r canaryLatencyResult) bool { return canaryLatencyFullPass(r) }

	for _, lvl := range canaryLatencyNoiseLevels() {
		lvl := lvl
		t.Run(lvl.name, func(t *testing.T) {
			// inject is the per-incident cost added to one executor call in
			// eight; 0 is the no-regression arm.
			for _, inject := range []time.Duration{0, 2 * time.Millisecond, 20 * time.Millisecond} {
				rng := rand.New(rand.NewSource(20260828))
				var shipPass, shipFail, nowPass, nowFail, nowUnknown, anyFail int
				var medianOnlyRounds, decisiveRounds, roundsSeen int
				for i := 0; i < trials; i++ {
					rounds := make([]canaryLatencyResult, 0, canaryLatencyRounds)
					for r := 0; r < canaryLatencyRounds; r++ {
						exec := canaryLatencyNoisyArm(rng, lvl.mean, lvl.sdFrac, lvl.spikeRate, lvl.spikeMul)
						if inject > 0 {
							for k := range exec {
								if k%8 == 0 {
									exec[k] += inject
								}
							}
							sort.Slice(exec, func(a, b int) bool { return exec[a] < exec[b] })
						}
						rounds = append(rounds, evaluateCanaryLatency(
							canaryLatencyNoisyArm(rng, lvl.mean, lvl.sdFrac, lvl.spikeRate, lvl.spikeMul),
							canaryLatencyNoisyArm(rng, lvl.mean, lvl.sdFrac, lvl.spikeRate, lvl.spikeMul),
							exec))
					}
					// Both rules stop the loop on the same thing — a full pass —
					// so one slate serves both.
					slate := canaryLatencyStopAt(rounds, fullStop)
					now := canaryLatencyOverall(slate)
					was := canaryLatencyOverallRound3Rule(slate)
					strict := canaryLatencyOverallAnyFailStands(slate)
					roundsSeen += len(slate)
					medRes, tailRes := canaryLatencyRunResolution(slate)
					for _, r := range slate {
						if canaryLatencyMedianOnlyPass(r) {
							medianOnlyRounds++
						}
						if canaryLatencyDecisiveRound(r, medRes, tailRes) {
							decisiveRounds++
						}
					}
					// The monotonicity implication, on evaluated data rather
					// than on synthesised verdict triples.
					if was.Verdict == canaryLatencyPass && now.Verdict != canaryLatencyPass {
						t.Fatalf("trial %d: round 4 answered %s on a run the shipped rule passed: %s",
							i, now.Verdict, now)
					}
					if now.Verdict == canaryLatencyPass && was.Verdict != canaryLatencyPass {
						t.Fatalf("trial %d: round 4 passed a run the shipped rule answered %s: %s",
							i, was.Verdict, now)
					}
					if now.Verdict == canaryLatencyPass && now.Tail.Verdict != canaryLatencyPass {
						t.Fatalf("trial %d: round 4 reported PASS on an unjudged tail: %s", i, now)
					}
					switch was.Verdict {
					case canaryLatencyPass:
						shipPass++
					case canaryLatencyFail:
						shipFail++
					}
					if strict.Verdict == canaryLatencyFail {
						anyFail++
					}
					switch now.Verdict {
					case canaryLatencyPass:
						nowPass++
					case canaryLatencyFail:
						nowFail++
					case canaryLatencyUnknown:
						nowUnknown++
					}
				}
				pct := func(n int) float64 { return 100 * float64(n) / float64(trials) }
				label := "no regression (every PASS honest, every FAIL false)"
				if inject > 0 {
					label = fmt.Sprintf("%v on 1 call in 8 (every PASS a miss)", inject)
				}
				t.Logf("%s | %s\n    shipped (round 3): PASS=%.2f%% FAIL=%.2f%%\n"+
					"    round 4:           PASS=%.2f%% FAIL=%.2f%% UNKNOWN=%.2f%%\n"+
					"    rejected any-FAIL: FAIL=%.2f%%\n"+
					"    of %d rounds evaluated: %.1f%% median-only, %.1f%% decisive",
					lvl.name, label, pct(shipPass), pct(shipFail),
					pct(nowPass), pct(nowFail), pct(nowUnknown), pct(anyFail), roundsSeen,
					100*float64(medianOnlyRounds)/float64(roundsSeen),
					100*float64(decisiveRounds)/float64(roundsSeen))

				if inject != 0 {
					continue
				}
				// AC-5 / the round-2 finding, re-checked: the round-4 boundary
				// must not have bought its detection with false FAILs. On a
				// clean tree the two rules must red the same runs to within one
				// trial in 200, and the rejected variant must be visibly worse
				// wherever it costs anything at all.
				if nowFail > shipFail+trials/200 {
					t.Fatalf("%s: round 4 reds %.2f%% of clean runs against the shipped rule's "+
						"%.2f%% — the decisiveness boundary is not holding the false-FAIL rate",
						lvl.name, pct(nowFail), pct(shipFail))
				}
				if anyFail > nowFail+trials/200 && nowFail > shipFail {
					t.Logf("%s: the rejected any-FAIL variant would red %.2f%% here against "+
						"round 4's %.2f%%", lvl.name, pct(anyFail), pct(nowFail))
				}
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
//
// # What SW-245 changed about how to read it
//
// ns/op is now the CALLER's cost and only the caller's cost: in `shadow` the
// second path runs on the worker, so the timer no longer covers it. B/op and
// allocs/op are NOT caller-only — Go's allocation counters are process-wide
// deltas, so the worker's allocations land in them wherever they were made.
// That asymmetry is exactly what AC-6 asks for: the latency is gone from the
// caller, the CPU and allocation are not gone from the machine, and this
// instrument reports each where it actually is instead of letting the second
// disappear with the first.
//
// Two provisions keep the shadow row honest:
//
//   - The loop drains before the counters are read (timer stopped, so the
//     backlog is not charged to ns/op but its allocations are still counted).
//     Without it, whatever the worker had not finished would be work the
//     benchmark did and did not measure.
//   - Skipped comparisons are reported as a metric. A tight benchmark loop can
//     outrun a single worker; an arm that dropped comparisons did less work than
//     a full-coverage arm, and a per-op figure taken over dropped work would
//     understate the cost. Reporting it lets the reader see whether it happened.
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
			ResetCanaryMismatches()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DispatchOperation(ctx, direct, &DeadCodeArgs{}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if err := DrainCanaryShadow(context.Background()); err != nil {
				b.Fatalf("DrainCanaryShadow: %v", err)
			}
			skipped, _ := CanarySkipped()
			b.ReportMetric(float64(skipped)/float64(b.N), "skipped/op")
		})
	}
}

// ---------------------------------------------------------------------------
// SW-244 — the dual-run cost of the shipped default, measured and accounted.
//
// The AX-06 gate above judges the EXECUTOR arm, and §3 of the document
// deliberately does not gate shadow: shadow runs both paths by construction, so
// ~2x legacy is its correct behaviour and gating its total would be either
// vacuous or a standing invitation to weaken the comparison it exists to
// perform. That exemption was written while `shadow` was an opt-in position an
// operator turned on while investigating.
//
// SW-244 makes `shadow` the DEFAULT path, so "not gated" stops being an
// acceptable answer and "gate the total" is still the wrong one. What this
// section adds is the question in between, which is the one a reviewer actually
// needs answered: how much of shadow's cost is NOT explained by "both paths
// ran"? That residue is the comparison, the recorder hand-off and whatever else
// the position costs beyond running two things that are each already measured —
// and unlike shadow's total, it has no reason to be large.
//
// It is judged against canaryLatencyBudget, which is SW-242's bar unmodified.
// AC-3 of this story forbids widening a budget to admit a cost the story
// introduces, and the way that prohibition is honoured here is structural: there
// is no second budget to widen.
//
// # Amendment (SW-245): what this section still measures, and what it no longer
//
// SW-245 took the second path off the caller's thread, so `shadow` no longer
// contains an executor pass in its timed window. `unaccounted = shadow -
// (baseline + executor)` therefore reads about MINUS one executor pass on a
// healthy run — it went from +9.5 µs to roughly -437 µs — and the p50 residue
// stopped being a number worth reading as "the comparison's own cost".
//
// The section is kept, unchanged, for two reasons rather than out of
// sentimentality. It is still the load-bearing check for cost added INSIDE the
// shadow window (TestSW244_ShadowAccountingCatchesUnexplainedCost injects
// exactly that and still requires FAIL), and it is the standing detector for the
// deferral being undone: if a future change puts the comparison back on the
// caller's thread, this residue climbs back to zero from below long before
// anything else notices. What replaced it as the primary reading of shadow's
// cost is SW-245's ratio bar in §7 of the document, which judges shadow's TOTAL
// against legacy rather than its residue.

// canaryLatencyAccounting is the shadow residue at one percentile.
//
// The arithmetic:
//
//	baseline    = (legacy-a + legacy-b) / 2      the pooled legacy centre
//	accounted   = baseline + executor            what "both paths ran" costs
//	unaccounted = shadow - accounted             what is left to explain
//
// and unaccounted is then held to canaryLatencyBudget(baseline, |a-b|) — the
// same fixed bar, the same 3x same-run noise term, the same 4x ceiling and the
// same three-valued verdict as the gate.
//
// # What this measures well, and where it is lenient
//
// At the MEDIAN the arithmetic is sound in the way that matters: the median of
// a sum of two independent costs is close to the sum of their medians, so a
// residue at p50 is a real per-call cost and is reported as one.
//
// At the TAIL it is deliberately LENIENT and the leniency is stated rather than
// hidden. p95(legacy) + p95(executor) is an OVERestimate of p95(legacy +
// executor), because the two arms' slow calls do not have to coincide — a slow
// legacy pass and a slow executor pass land in the same shadow call only
// sometimes. So `accounted` at p95 is generous, `unaccounted` is correspondingly
// understated, and a healthy run reads NEGATIVE there (it did on the recorded
// measurement: -327µs). The consequence is asymmetric and worth being explicit
// about: a FAIL at p95 is strong evidence, a PASS at p95 is weak. The p50
// judgement is what carries this check, exactly as it carries the gate.
type canaryLatencyAccounting struct {
	Name        string
	Pct         float64
	Baseline    time.Duration
	ExecVal     time.Duration
	ShadowVal   time.Duration
	Accounted   time.Duration
	Unaccounted time.Duration
	RefDelta    time.Duration
	NoiseTerm   time.Duration
	FixedBar    time.Duration
	Ceiling     time.Duration
	Budget      time.Duration
	Verdict     canaryLatencyVerdict
	Reason      string
}

func (a canaryLatencyAccounting) String() string {
	ratio := 0.0
	if a.Baseline > 0 {
		ratio = float64(a.ShadowVal) / float64(a.Baseline)
	}
	return fmt.Sprintf(
		"%s %s shadow=%v (%.2fx legacy) accounted=%v unaccounted=%v budget=%v "+
			"(fixed=%v noise=3x%v=%v ceiling=%v) baseline=%v executor=%v",
		a.Name, a.Verdict, a.ShadowVal, ratio, a.Accounted, a.Unaccounted, a.Budget,
		a.FixedBar, a.RefDelta, a.NoiseTerm, a.Ceiling, a.Baseline, a.ExecVal)
}

// evaluateCanaryAccounting applies the rule above at one percentile. It is a
// pure function of four sorted sample sets so the decision is testable without
// owning a quiet machine.
func evaluateCanaryAccounting(name string, pct float64, base, ref, exec, shadow []time.Duration) canaryLatencyAccounting {
	a := canaryLatencyAccounting{Name: name, Pct: pct}
	if len(base) == 0 || len(ref) == 0 || len(exec) == 0 || len(shadow) == 0 {
		a.Verdict = canaryLatencyUnknown
		a.Reason = fmt.Sprintf("%s: no usable measurement: an arm produced no samples", name)
		return a
	}
	baseVal, refVal := percentile(base, pct), percentile(ref, pct)
	a.ExecVal = percentile(exec, pct)
	a.ShadowVal = percentile(shadow, pct)
	a.Baseline = (baseVal + refVal) / 2
	a.RefDelta = baseVal - refVal
	if a.RefDelta < 0 {
		a.RefDelta = -a.RefDelta
	}
	a.Accounted = a.Baseline + a.ExecVal
	a.Unaccounted = a.ShadowVal - a.Accounted
	a.NoiseTerm, a.FixedBar, a.Ceiling, a.Budget = canaryLatencyBudget(a.Baseline, a.RefDelta)

	switch {
	case a.Baseline <= 0:
		a.Verdict = canaryLatencyUnknown
		a.Reason = fmt.Sprintf("%s: zero legacy baseline", name)
	case a.Unaccounted > a.Budget && a.Unaccounted > a.NoiseTerm:
		// Same ordering as the gate, for the same reason: an excess past both
		// the clamped budget AND three times this run's own demonstrated
		// resolution is signal at whatever resolution the run achieved, and a
		// degraded runner is not a licence to launder it into a pass.
		a.Verdict = canaryLatencyFail
		a.Reason = fmt.Sprintf(
			"%s: shadow %v - (legacy %v + executor %v) = %v is cost the dual run does NOT "+
				"explain; it exceeds the %v budget AND 3x the same-run A/A control (%v)",
			name, a.ShadowVal, a.Baseline, a.ExecVal, a.Unaccounted, a.Budget, a.RefDelta)
	case a.NoiseTerm > a.Ceiling:
		a.Verdict = canaryLatencyUnknown
		a.Reason = fmt.Sprintf(
			"%s: runner degraded beyond comparison: the same-run A/A control differs by %v, "+
				"so 3x noise = %v exceeds the %v ceiling", name, a.RefDelta, a.NoiseTerm, a.Ceiling)
	case a.Unaccounted <= a.Budget:
		a.Verdict = canaryLatencyPass
	default:
		a.Verdict = canaryLatencyFail
		a.Reason = fmt.Sprintf(
			"%s: shadow %v - (legacy %v + executor %v) = %v exceeds the %v budget, and the "+
				"same-run A/A control (%v) is small enough that the difference is signal",
			name, a.ShadowVal, a.Baseline, a.ExecVal, a.Unaccounted, a.Budget, a.RefDelta)
	}
	return a
}

// TestSW244_ShadowDefaultCostIsAccounted is AC-2's measurement and AC-3's stop
// condition, in the shipped decision path rather than in a spreadsheet.
//
// It records the dual-run cost of the position this story makes the default —
// shadow's p50 and p95 against the legacy baseline measured in the SAME run,
// same rotation, same machine, same moment — and it FAILS if the part of that
// cost which "both paths ran" does not explain exceeds SW-242's bar.
//
// The anti-flake provision is the gate's: up to canaryLatencyRounds rounds,
// pass on the first round where both percentiles pass, and report the FIRST
// round's numbers regardless of which round passed so the record is not
// cherry-picked. UNKNOWN is a skip with the numbers attached, never a silent
// pass — a run that could not measure says so.
func TestSW244_ShadowDefaultCostIsAccounted(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)

	var (
		first  []canaryLatencyAccounting
		last   []canaryLatencyAccounting
		passed bool
	)
	for round := 1; round <= canaryLatencyRounds && !passed; round++ {
		samples := canaryLatencySample(t, direct, canaryLatencyGateArms())
		stats := []canaryLatencyAccounting{
			evaluateCanaryAccounting("p50", 0.50,
				samples[canaryArmBaseline], samples[canaryArmReference],
				samples[canaryArmExecutor], samples[canaryArmShadow]),
			evaluateCanaryAccounting("p95", 0.95,
				samples[canaryArmBaseline], samples[canaryArmReference],
				samples[canaryArmExecutor], samples[canaryArmShadow]),
		}
		if round == 1 {
			first = stats
		}
		last = stats
		passed = stats[0].Verdict == canaryLatencyPass && stats[1].Verdict == canaryLatencyPass
		t.Logf("SW-244-SHADOW-COST round %d/%d: %s | %s%s",
			round, canaryLatencyRounds, stats[0], stats[1], canaryLatencyReport(samples))
	}

	// The record is round 1's, whichever round passed.
	for _, a := range first {
		t.Logf("SW-244-SHADOW-COST-RECORD %s", a)
	}
	if passed {
		t.Logf("SW-244-SHADOW-COST-VERDICT: PASS — the dual-run cost of the shipped default "+
			"is explained by running both paths (p50 residue %v against a %v budget)",
			first[0].Unaccounted, first[0].Budget)
		return
	}
	for _, a := range last {
		switch a.Verdict {
		case canaryLatencyFail:
			// AC-3. The remedy is NOT to widen canaryLatencyBudget — that is the
			// AX-06 gate's own bar, and widening it to admit a cost this story
			// introduced is what AC-3 exists to forbid. The remedy is to find
			// the unexplained cost, or to leave the shipped default at `legacy`.
			t.Errorf("SW-244-SHADOW-COST-VERDICT: FAIL — %s\n  %s\n  The shipped default may "+
				"not be moved to `shadow` on this evidence. Do NOT widen the budget: it is "+
				"SW-242's AX-06 bar, shared with the gate.", a.Reason, a)
		case canaryLatencyUnknown:
			t.Logf("SW-244-SHADOW-COST-VERDICT: UNKNOWN at %s — %s\n  %s", a.Name, a.Reason, a)
		}
	}
	if !t.Failed() {
		t.Skipf("SW-244-SHADOW-COST-VERDICT: UNKNOWN after %d round(s) — this runner could "+
			"not resolve the residue. NOT evidence that the dual run is cheap; re-run on a "+
			"quieter machine.", canaryLatencyRounds)
	}
}

// TestSW244_ShadowAccountingCatchesUnexplainedCost is the load-bearing test of
// the check above: an accounting rule that cannot go red is not an accounting
// rule.
//
// The injected cost is real and is added to the SHADOW arm only — the seam's own
// code, run extra times inside shadow's timed window. That is a cost the dual
// run does not explain by construction, since neither the legacy arm nor the
// executor arm paid it, so the residue must move by it and the verdict must be
// FAIL. UNKNOWN would not do: a check that answers "I could not tell" to a
// tripled shadow arm has the practical value of one that answers PASS.
func TestSW244_ShadowAccountingCatchesUnexplainedCost(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)
	// Two extra executor passes inside the shadow window: ~2x the executor
	// arm's cost, several hundred microseconds on the recorded machine, well
	// past the 250 µs floor and well past 3x any plausible A/A control.
	arms := canaryLatencyArmsWith(canaryArmShadow, canaryLatencyExtraSeamPasses(2))

	var got canaryLatencyVerdict
	for round := 1; round <= canaryLatencyRounds; round++ {
		samples := canaryLatencySample(t, direct, arms)
		a := evaluateCanaryAccounting("p50", 0.50,
			samples[canaryArmBaseline], samples[canaryArmReference],
			samples[canaryArmExecutor], samples[canaryArmShadow])
		t.Logf("injected round %d/%d: %s", round, canaryLatencyRounds, a)
		got = a.Verdict
		if got == canaryLatencyFail {
			return
		}
	}
	t.Errorf("the shadow accounting answered %q to a shadow arm carrying two whole extra "+
		"executor passes. A residue that large is cost the dual run does not explain, and a "+
		"check that cannot see it cannot justify the shipped default", got)
}

// TestSW244_ShadowAccountingDecisionRule pins the rule's boundaries without
// owning a machine, the way TestAX06_LatencyDecisionRule pins the gate's.
//
// The cases that matter are: a clean dual run passes (including the NEGATIVE
// residue a healthy tail produces), an unexplained cost past the bar fails, an
// empty arm is UNKNOWN and not a pass, and the budget invariants SW-242 made
// code hold here too — because they are the same function.
func TestSW244_ShadowAccountingDecisionRule(t *testing.T) {
	rep := func(d time.Duration) []time.Duration {
		out := make([]time.Duration, 20)
		for i := range out {
			out[i] = d
		}
		return out
	}
	ms := time.Millisecond

	for _, tc := range []struct {
		name                       string
		base, ref, exec, shadow    []time.Duration
		want                       canaryLatencyVerdict
		wantUnaccountedNonPositive bool
	}{
		{
			name: "clean_dual_run_passes",
			base: rep(1 * ms), ref: rep(1 * ms), exec: rep(1 * ms),
			// Exactly legacy + executor, nothing else.
			shadow: rep(2 * ms),
			want:   canaryLatencyPass,
		},
		{
			name: "negative_residue_passes",
			base: rep(1 * ms), ref: rep(1 * ms), exec: rep(1 * ms),
			// The tail shape: the two arms' slow calls did not coincide.
			shadow:                     rep(1500 * time.Microsecond),
			want:                       canaryLatencyPass,
			wantUnaccountedNonPositive: true,
		},
		{
			name: "residue_inside_the_fixed_bar_passes",
			base: rep(1 * ms), ref: rep(1 * ms), exec: rep(1 * ms),
			// +200 µs, inside the 250 µs floor. 10 % of a 1 ms baseline is
			// 100 µs, so the floor is what is doing the work here.
			shadow: rep(2*ms + 200*time.Microsecond),
			want:   canaryLatencyPass,
		},
		{
			name: "residue_past_the_bar_fails",
			base: rep(1 * ms), ref: rep(1 * ms), exec: rep(1 * ms),
			// +600 µs of cost neither arm paid.
			shadow: rep(2*ms + 600*time.Microsecond),
			want:   canaryLatencyFail,
		},
		{
			name: "degraded_control_is_unknown_not_pass",
			// A/A control 2 ms wide against a 2 ms baseline: 3x noise = 6 ms,
			// past the 4x800 µs ceiling. The residue is inside it, so nothing
			// can be concluded.
			base: rep(1 * ms), ref: rep(3 * ms), exec: rep(2 * ms),
			shadow: rep(4*ms + 500*time.Microsecond),
			want:   canaryLatencyUnknown,
		},
		{
			name: "degraded_control_still_fails_a_gross_residue",
			// Same degraded control, but a residue past BOTH the ceiling and
			// 3x the control. A noisy runner may not launder that into a pass.
			base: rep(1 * ms), ref: rep(3 * ms), exec: rep(2 * ms),
			shadow: rep(11 * ms),
			want:   canaryLatencyFail,
		},
		{
			name: "empty_shadow_arm_is_unknown",
			base: rep(1 * ms), ref: rep(1 * ms), exec: rep(1 * ms), shadow: nil,
			want: canaryLatencyUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := evaluateCanaryAccounting("p50", 0.50, tc.base, tc.ref, tc.exec, tc.shadow)
			if a.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q — %s", a.Verdict, tc.want, a)
			}
			if a.Verdict != canaryLatencyPass && a.Reason == "" {
				t.Errorf("a non-PASS verdict carries no reason: %s", a)
			}
			if tc.wantUnaccountedNonPositive && a.Unaccounted > 0 {
				t.Errorf("unaccounted = %v, want <= 0: %s", a.Unaccounted, a)
			}
			if len(tc.shadow) == 0 {
				return
			}
			// SW-242's invariants, which this rule inherits by construction
			// because it calls the same function.
			if a.Budget > a.Ceiling {
				t.Errorf("invariant broken: budget %v > ceiling %v", a.Budget, a.Ceiling)
			}
			if a.Budget < a.FixedBar && a.FixedBar <= a.Ceiling {
				t.Errorf("invariant broken: budget %v < fixed bar %v", a.Budget, a.FixedBar)
			}
			if a.Ceiling != time.Duration(float64(a.FixedBar)*canaryLatencyDegradedMultiple) {
				t.Errorf("ceiling %v is not %vx the fixed bar %v",
					a.Ceiling, canaryLatencyDegradedMultiple, a.FixedBar)
			}
		})
	}
}

// TestSW244_ShadowAccountingSharesTheGateBudget is AC-3 made structural.
//
// The prohibition on widening a budget to admit this story's cost is only worth
// anything if there is one budget. This asserts that the accounting rule and the
// AX-06 gate read the SAME arithmetic for the same inputs, so a future edit that
// softened the accounting would have to soften the gate — which is a change no
// reviewer would miss.
func TestSW244_ShadowAccountingSharesTheGateBudget(t *testing.T) {
	for _, baseline := range []time.Duration{
		100 * time.Microsecond, 400 * time.Microsecond, time.Millisecond, 50 * time.Millisecond,
	} {
		for _, refDelta := range []time.Duration{0, time.Microsecond, 200 * time.Microsecond, time.Second} {
			base := []time.Duration{baseline + refDelta/2}
			ref := []time.Duration{baseline - refDelta/2}
			exec := []time.Duration{baseline}
			shadow := []time.Duration{2 * baseline}

			gate := evaluateCanaryStat("p50", 0.50, base, ref, exec)
			acct := evaluateCanaryAccounting("p50", 0.50, base, ref, exec, shadow)

			if gate.Budget != acct.Budget || gate.FixedBar != acct.FixedBar ||
				gate.Ceiling != acct.Ceiling || gate.NoiseTerm != acct.NoiseTerm {
				t.Fatalf("baseline=%v refDelta=%v: the shadow accounting judges against a "+
					"DIFFERENT bar than the AX-06 gate\n  gate: %s\n  acct: %s",
					baseline, refDelta, gate, acct)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// SW-245 — the shipped default's latency, once the dual run is off the caller's
// critical path.

// canaryShadowRatioBar is AC-1: shadow's p50 may cost at most this multiple of
// the legacy baseline.
//
// SW-244 measured 2.05×, and §3 of the AX-06 document exempted shadow's TOTAL
// from the gate for a reason that was correct while shadow ran both paths
// synchronously — gating it would have been either vacuous or an invitation to
// weaken the comparison. SW-245 changes the fact that exemption rested on: the
// caller no longer runs the second path, so shadow's total is no longer "two
// paths by construction" and there IS a number to hold it to.
//
// This is a NEW bar on a statistic §3 did not gate. It does not touch
// canaryLatencyBudget, the noise term, the floor or the 4×fixedBar clamp — the
// AX-06 gate below judges exactly what it judged before, and AC-5 requires that
// to stay true.
const canaryShadowRatioBar = 1.15

// canaryShadowRatio is one round's judgement of AC-1.
type canaryShadowRatio struct {
	Name     string
	Pct      float64
	Baseline time.Duration
	ShadowV  time.Duration
	RefDelta time.Duration
	Ratio    float64
	RefRatio float64
	Verdict  canaryLatencyVerdict
	Reason   string
}

func (r canaryShadowRatio) String() string {
	return fmt.Sprintf("%s %s shadow=%v legacy=%v ratio=%.3fx bar=%.2fx "+
		"(same-run A/A control %v = %.3fx)",
		r.Name, r.Verdict, r.ShadowV, r.Baseline, r.Ratio, canaryShadowRatioBar, r.RefDelta, r.RefRatio)
}

// evaluateCanaryShadowRatio applies AC-1's rule at one percentile.
//
// The three-valued verdict and the resolution discipline are SW-242's, reused
// rather than reinvented: a run whose own A/A control cannot resolve a 15 %
// difference cannot judge a 15 % bar either, and the honest answer there is
// UNKNOWN. Answering PASS on a runner that could not tell is the failure mode
// SW-242 was written to remove, and it would be no less wrong here.
func evaluateCanaryShadowRatio(name string, pct float64, base, ref, shadow []time.Duration) canaryShadowRatio {
	r := canaryShadowRatio{Name: name, Pct: pct}
	if len(base) == 0 || len(ref) == 0 || len(shadow) == 0 {
		r.Verdict = canaryLatencyUnknown
		r.Reason = fmt.Sprintf("%s: no usable measurement: an arm produced no samples", name)
		return r
	}
	baseVal, refVal := percentile(base, pct), percentile(ref, pct)
	r.Baseline = (baseVal + refVal) / 2
	r.ShadowV = percentile(shadow, pct)
	r.RefDelta = baseVal - refVal
	if r.RefDelta < 0 {
		r.RefDelta = -r.RefDelta
	}
	if r.Baseline <= 0 {
		r.Verdict = canaryLatencyUnknown
		r.Reason = fmt.Sprintf("%s: zero legacy baseline", name)
		return r
	}
	r.Ratio = float64(r.ShadowV) / float64(r.Baseline)
	r.RefRatio = 1 + float64(r.RefDelta)/float64(r.Baseline)

	switch {
	case r.Ratio <= canaryShadowRatioBar:
		// A pass needs no resolution argument: the measurement came in UNDER
		// the bar, and a noisy runner can only have pushed it up.
		r.Verdict = canaryLatencyPass
	case r.RefRatio >= canaryShadowRatioBar:
		// The two byte-identical legacy arms differ by as much as the bar
		// itself. Nothing this run says about a 1.15x threshold is information.
		r.Verdict = canaryLatencyUnknown
		r.Reason = fmt.Sprintf(
			"%s: runner cannot resolve the bar: the same-run A/A control differs by %v (%.3fx), "+
				"which is at or past the %.2fx being tested",
			name, r.RefDelta, r.RefRatio, canaryShadowRatioBar)
	default:
		r.Verdict = canaryLatencyFail
		r.Reason = fmt.Sprintf(
			"%s: shadow %v is %.3fx the legacy baseline %v, past the %.2fx bar, and this run's "+
				"own A/A control (%v, %.3fx) is small enough that the difference is signal",
			name, r.ShadowV, r.Ratio, r.Baseline, canaryShadowRatioBar, r.RefDelta, r.RefRatio)
	}
	return r
}

// TestSW245_ShadowIsOffTheCallersCriticalPath is AC-1.
//
// Same harness, same four arms, same rotation, same N and warm-up as SW-242 and
// SW-244 — the story requires the number to be taken under the recalibrated
// method, and the way to guarantee that is to call the recalibrated method's own
// sampler rather than to write a second one.
//
// Reporting is round 1's regardless of which round passed, for the reason
// SW-244 gave: a record that quotes whichever round looked best is a
// cherry-picked record. Both percentiles are reported; AC-1 is judged at p50,
// and p95 is reported beside it because the story asks for both.
func TestSW245_ShadowIsOffTheCallersCriticalPath(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)

	var (
		first  []canaryShadowRatio
		last   []canaryShadowRatio
		passed bool
	)
	for round := 1; round <= canaryLatencyRounds && !passed; round++ {
		samples := canaryLatencySample(t, direct, canaryLatencyGateArms())
		stats := []canaryShadowRatio{
			evaluateCanaryShadowRatio("p50", 0.50,
				samples[canaryArmBaseline], samples[canaryArmReference], samples[canaryArmShadow]),
			evaluateCanaryShadowRatio("p95", 0.95,
				samples[canaryArmBaseline], samples[canaryArmReference], samples[canaryArmShadow]),
		}
		if round == 1 {
			first = stats
		}
		last = stats
		// AC-1 names p50. p95 is measured and reported, and is not the bar.
		passed = stats[0].Verdict == canaryLatencyPass
		t.Logf("SW-245-SHADOW-RATIO round %d/%d: %s | %s%s",
			round, canaryLatencyRounds, stats[0], stats[1], canaryLatencyReport(samples))
	}
	for _, r := range first {
		t.Logf("SW-245-SHADOW-RATIO-RECORD %s", r)
	}
	if skipped, reasons := CanarySkipped(); skipped > 0 {
		// AC-4 again, in the one place the number could quietly flatter the
		// measurement: a shadow arm that dropped comparisons is cheaper than one
		// that made them, and a ratio taken over dropped work is not the ratio
		// the story asked for.
		t.Logf("SW-245-SHADOW-RATIO-COVERAGE: %d comparison(s) were skipped during the "+
			"measurement (%v) — the arm did less work than a full-coverage arm would", skipped, reasons)
	}
	if passed {
		t.Logf("SW-245-SHADOW-RATIO-VERDICT: PASS — the shipped default's p50 is %.3fx legacy, "+
			"within the %.2fx bar", first[0].Ratio, canaryShadowRatioBar)
		return
	}
	switch last[0].Verdict {
	case canaryLatencyFail:
		t.Errorf("SW-245-SHADOW-RATIO-VERDICT: FAIL — %s\n  %s\n  AC-1 is not met on this "+
			"evidence.", last[0].Reason, last[0])
	default:
		t.Skipf("SW-245-SHADOW-RATIO-VERDICT: UNKNOWN after %d round(s) — %s. NOT evidence "+
			"that the deferral works; re-run on a quieter machine.", canaryLatencyRounds, last[0].Reason)
	}
}

// TestSW245_ShadowRatioCatchesASynchronousDualRun is the non-vacuity proof, and
// it is the sharpest one available: the regression it injects is exactly the
// behaviour this story removed.
//
// canaryLatencyExtraSeamPasses(1) runs one whole extra executor pass inside the
// shadow arm's timed window — which is, to within the enqueue, what `shadow`
// cost before SW-245. If the bar cannot turn red on that, it cannot be evidence
// that SW-245 achieved anything, and a future change that quietly put the
// comparison back on the caller's thread would pass it.
func TestSW245_ShadowRatioCatchesASynchronousDualRun(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement is not a -short gate")
	}
	direct := canaryLatencyFixture(t)
	arms := canaryLatencyArmsWith(canaryArmShadow, canaryLatencyExtraSeamPasses(1))

	var got canaryLatencyVerdict
	for round := 1; round <= canaryLatencyRounds; round++ {
		samples := canaryLatencySample(t, direct, arms)
		r := evaluateCanaryShadowRatio("p50", 0.50,
			samples[canaryArmBaseline], samples[canaryArmReference], samples[canaryArmShadow])
		t.Logf("injected synchronous dual run, round %d/%d: %s", round, canaryLatencyRounds, r)
		got = r.Verdict
		if got == canaryLatencyFail {
			return
		}
	}
	t.Errorf("the AC-1 bar answered %q to a shadow arm carrying a whole extra executor pass in "+
		"its timed window — that is the pre-SW-245 cost, and a bar that cannot see it is not "+
		"evidence that the cost was removed", got)
}

// TestSW245_ShadowRatioDecisionRule pins AC-1's rule without owning a quiet
// machine, the way TestAX06_LatencyDecisionRule and
// TestSW244_ShadowAccountingDecisionRule pin theirs.
func TestSW245_ShadowRatioDecisionRule(t *testing.T) {
	rep := func(d time.Duration) []time.Duration {
		out := make([]time.Duration, 20)
		for i := range out {
			out[i] = d
		}
		return out
	}
	for _, tc := range []struct {
		name              string
		base, ref, shadow time.Duration
		want              canaryLatencyVerdict
	}{
		{
			name: "deferred dual run is within the bar",
			base: 400 * time.Microsecond, ref: 400 * time.Microsecond, shadow: 420 * time.Microsecond,
			want: canaryLatencyPass,
		},
		{
			name: "exactly at the bar passes",
			base: 400 * time.Microsecond, ref: 400 * time.Microsecond, shadow: 460 * time.Microsecond,
			want: canaryLatencyPass,
		},
		{
			name: "a synchronous dual run fails",
			base: 400 * time.Microsecond, ref: 400 * time.Microsecond, shadow: 800 * time.Microsecond,
			want: canaryLatencyFail,
		},
		{
			name: "faster than legacy passes",
			base: 400 * time.Microsecond, ref: 400 * time.Microsecond, shadow: 390 * time.Microsecond,
			want: canaryLatencyPass,
		},
		{
			name: "a control as wide as the bar cannot judge it",
			base: 460 * time.Microsecond, ref: 340 * time.Microsecond, shadow: 700 * time.Microsecond,
			want: canaryLatencyUnknown,
		},
		{
			name: "a wide control still passes a measurement under the bar",
			base: 460 * time.Microsecond, ref: 340 * time.Microsecond, shadow: 405 * time.Microsecond,
			want: canaryLatencyPass,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateCanaryShadowRatio("p50", 0.50, rep(tc.base), rep(tc.ref), rep(tc.shadow))
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q: %s", got.Verdict, tc.want, got)
			}
		})
	}
	// An empty arm is UNKNOWN, never a pass — the same fail-closed shape the
	// gate and the accounting both have.
	if got := evaluateCanaryShadowRatio("p50", 0.50, nil, rep(time.Millisecond), rep(time.Millisecond)); got.Verdict != canaryLatencyUnknown {
		t.Fatalf("an empty baseline arm read %q, want UNKNOWN", got.Verdict)
	}
}
