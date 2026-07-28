package main

// SW-126 (P0-C3): the driver's arithmetic and its failure classification.
//
// These run against a scripted target and a CONTROLLED CLOCK. Freshness over a
// wall clock would flake, and the story's test notes are explicit that a flaky
// freshness test is a real defect here rather than something to retry — so the
// clock is a parameter and every microsecond asserted below is exact.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
)

// fakeClock advances by a fixed tick on every read, so a sequence of clock
// reads has exactly known differences.
type fakeClock struct {
	at   time.Time
	tick time.Duration
	// reads counts clock reads, so a test can assert the driver reads it the
	// number of times the measurement definition requires.
	reads int
}

func newFakeClock(tick time.Duration) *fakeClock {
	return &fakeClock{at: time.Unix(1700000000, 0).UTC(), tick: tick}
}

func (c *fakeClock) now() time.Time {
	c.reads++
	now := c.at
	c.at = c.at.Add(c.tick)
	return now
}

// advance is what a scripted target calls to consume time between clock reads,
// so a slow update or a slow convergence is expressible without sleeping.
func (c *fakeClock) advance(d time.Duration) { c.at = c.at.Add(d) }

// scriptedTarget is a target whose every stage is programmable per step.
type scriptedTarget struct {
	applyErr  func(s changeStep) error
	updateErr func(s changeStep) error
	// answerAfter is the probe number at which a step converges; 0 means it
	// never does.
	answerAfter func(s changeStep) int
	answerErr   func(s changeStep) error
	onUpdate    func(s changeStep)
	onProbe     func(s changeStep, probe int)

	probes map[int]int
}

func (t *scriptedTarget) apply(ctx context.Context, s changeStep) error {
	if t.applyErr != nil {
		return t.applyErr(s)
	}
	return nil
}

func (t *scriptedTarget) update(ctx context.Context, s changeStep) error {
	if t.onUpdate != nil {
		t.onUpdate(s)
	}
	if t.updateErr != nil {
		return t.updateErr(s)
	}
	return nil
}

func (t *scriptedTarget) answers(ctx context.Context, s changeStep) (bool, error) {
	if t.probes == nil {
		t.probes = map[int]int{}
	}
	t.probes[s.index]++
	probe := t.probes[s.index]
	if t.onProbe != nil {
		t.onProbe(s, probe)
	}
	if t.answerErr != nil {
		if err := t.answerErr(s); err != nil {
			return false, err
		}
	}
	want := 1
	if t.answerAfter != nil {
		want = t.answerAfter(s)
	}
	if want <= 0 {
		return false, nil
	}
	return probe >= want, nil
}

func testDriver(target incrementalTarget, clock *fakeClock) incrementalDriver {
	return incrementalDriver{
		target:     target,
		now:        clock.now,
		sleep:      func(time.Duration) {}, // the clock is advanced explicitly
		maxProbes:  5,
		probeDelay: time.Millisecond,
	}
}

// AC-3/AC-4 at the sample level, with exact numbers: the update clock covers
// the ingest call and the freshness clock covers change-to-answer, so freshness
// contains the update.
func TestIncrementalDriver_MeasuresUpdateAndFreshnessExactly(t *testing.T) {
	clock := newFakeClock(0) // no implicit drift; the target advances time
	target := &scriptedTarget{
		onUpdate:    func(changeStep) { clock.advance(30 * time.Millisecond) },
		answerAfter: func(changeStep) int { return 3 },
		onProbe:     func(_ changeStep, _ int) { clock.advance(10 * time.Millisecond) },
	}
	steps := buildChangeSequence(testSequenceInput(1))
	samples := testDriver(target, clock).run(context.Background(), steps)

	if len(samples) != 1 {
		t.Fatalf("got %d samples for 1 step", len(samples))
	}
	got := samples[0]
	if got.Status != evalreport.ChangeCompleted {
		t.Fatalf("status = %s (%s: %s)", got.Status, got.FailedStage, got.Error)
	}
	if got.UpdateUS != 30_000 {
		t.Errorf("update = %d µs, want exactly 30000 (the ingest call and nothing else)", got.UpdateUS)
	}
	// change → update (30ms) → three probes (10ms each) = 60ms.
	if got.FreshnessUS != 60_000 {
		t.Errorf("freshness = %d µs, want exactly 60000 (change to the answering probe)", got.FreshnessUS)
	}
	if got.FreshnessUS < got.UpdateUS {
		t.Error("freshness must contain the update")
	}
	if got.Probes != 3 {
		t.Errorf("probes = %d, want 3", got.Probes)
	}
	if !got.UpdateMeasured || !got.FreshnessMeasured {
		t.Errorf("measured flags = %v/%v, want both true", got.UpdateMeasured, got.FreshnessMeasured)
	}
}

// The synchronous case: the ingest call already applied the change, so the very
// first probe answers and freshness equals the update.
func TestIncrementalDriver_SynchronousChangeConvergesOnTheFirstProbe(t *testing.T) {
	clock := newFakeClock(0)
	target := &scriptedTarget{onUpdate: func(changeStep) { clock.advance(5 * time.Millisecond) }}
	samples := testDriver(target, clock).run(context.Background(), buildChangeSequence(testSequenceInput(4)))

	for _, s := range samples {
		if s.Probes != 1 {
			t.Errorf("step %d probed %d times, want 1", s.Step, s.Probes)
		}
		if s.FreshnessUS != s.UpdateUS {
			t.Errorf("step %d: freshness %d != update %d with no waiting", s.Step, s.FreshnessUS, s.UpdateUS)
		}
	}
}

// AC-6, the sync error: the change is retained, named, staged, and the sequence
// keeps running. And it contributes NO update latency — the sync did not
// perform an update.
func TestIncrementalDriver_SyncErrorIsRetainedAndTheSequenceContinues(t *testing.T) {
	clock := newFakeClock(time.Millisecond)
	target := &scriptedTarget{
		updateErr: func(s changeStep) error {
			if s.index == 2 {
				return errors.New("deliberate sync failure")
			}
			return nil
		},
	}
	steps := buildChangeSequence(testSequenceInput(8))
	samples := testDriver(target, clock).run(context.Background(), steps)

	if len(samples) != len(steps) {
		t.Fatalf("got %d samples for %d steps: a failed change truncated the sequence", len(samples), len(steps))
	}
	failed := samples[1]
	if failed.Status != evalreport.ChangeFailed || failed.FailedStage != evalreport.ChangeStageUpdate {
		t.Fatalf("step 2 = %+v, want a failed update", failed)
	}
	if !strings.Contains(failed.Error, "deliberate sync failure") {
		t.Errorf("step 2 error = %q, want the underlying cause verbatim", failed.Error)
	}
	if failed.UpdateMeasured || failed.FreshnessMeasured {
		t.Error("a failed sync must contribute no latency measurement")
	}
	for i, s := range samples {
		if i != 1 && s.Status != evalreport.ChangeCompleted {
			t.Errorf("step %d = %s, want the rest of the sequence unaffected", s.Step, s.Status)
		}
	}

	// And it survives into the series: counted, warned about, and it stops the
	// series reading green.
	series := buildIncrementalSeries("fixture", 8, testSequenceInput(8), steps, samples)
	if series.Failed != 1 || series.Completed != 7 {
		t.Errorf("series counts = %d completed / %d failed, want 7/1", series.Completed, series.Failed)
	}
	if len(series.Changes) != len(steps) {
		t.Errorf("the series retained %d of %d changes", len(series.Changes), len(steps))
	}
	if series.Update.N != 7 {
		t.Errorf("update n = %d, want 7 — the failed change must not be counted as a zero", series.Update.N)
	}
	if incrementalStatus(series) == evalreport.StatusPass {
		t.Error("a series with a failed change must not read PASS")
	}
	var warned bool
	for _, w := range series.Warnings {
		if strings.Contains(w, "deliberate sync failure") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the failure is not visible in the series warnings: %v", series.Warnings)
	}
}

// AC-6, the non-converging change: the update completed and IS measured, the
// change never became answerable, and that is reported as a bounded observation
// rather than a hang.
func TestIncrementalDriver_NonConvergingChangeKeepsItsUpdateAndFailsAtConverge(t *testing.T) {
	clock := newFakeClock(time.Millisecond)
	target := &scriptedTarget{
		answerAfter: func(s changeStep) int {
			if s.index == 1 {
				return 0 // never
			}
			return 1
		},
	}
	steps := buildChangeSequence(testSequenceInput(4))
	samples := testDriver(target, clock).run(context.Background(), steps)

	stuck := samples[0]
	if stuck.Status != evalreport.ChangeFailed || stuck.FailedStage != evalreport.ChangeStageConverge {
		t.Fatalf("step 1 = %+v, want a converge failure", stuck)
	}
	if !stuck.UpdateMeasured {
		t.Error("the update completed and must keep its latency")
	}
	if stuck.FreshnessMeasured {
		t.Error("a change that never became answerable has no freshness")
	}
	if stuck.Probes != 5 {
		t.Errorf("probes = %d, want the full budget of 5", stuck.Probes)
	}
	if !strings.Contains(stuck.Error, "did not become answerable") {
		t.Errorf("error = %q, want it to name the convergence failure", stuck.Error)
	}
	series := buildIncrementalSeries("fixture", 4, testSequenceInput(4), steps, samples)
	if series.Update.N != 4 || series.Freshness.N != 3 {
		t.Errorf("update/freshness n = %d/%d, want 4/3", series.Update.N, series.Freshness.N)
	}
}

// A change that could not even be written fails at the apply stage and has
// neither measurement.
func TestIncrementalDriver_ApplyFailureIsItsOwnStage(t *testing.T) {
	clock := newFakeClock(time.Millisecond)
	target := &scriptedTarget{applyErr: func(changeStep) error { return errors.New("read-only filesystem") }}
	samples := testDriver(target, clock).run(context.Background(), buildChangeSequence(testSequenceInput(2)))

	for _, s := range samples {
		if s.FailedStage != evalreport.ChangeStageApply {
			t.Errorf("step %d failed at %q, want %q", s.Step, s.FailedStage, evalreport.ChangeStageApply)
		}
		if s.UpdateMeasured || s.FreshnessMeasured || s.Probes != 0 {
			t.Errorf("step %d measured something despite never being applied: %+v", s.Step, s)
		}
	}
}

// A broken probe is a converge-stage failure, distinct from "not yet".
func TestIncrementalDriver_ProbeErrorFailsTheChange(t *testing.T) {
	clock := newFakeClock(time.Millisecond)
	target := &scriptedTarget{answerErr: func(changeStep) error { return errors.New("search backend closed") }}
	samples := testDriver(target, clock).run(context.Background(), buildChangeSequence(testSequenceInput(1)))

	got := samples[0]
	if got.FailedStage != evalreport.ChangeStageConverge || !strings.Contains(got.Error, "search backend closed") {
		t.Fatalf("sample = %+v, want a converge failure carrying the probe error", got)
	}
	if got.Probes != 1 {
		t.Errorf("probes = %d, want the driver to stop at the first broken probe", got.Probes)
	}
}

// The series' published statistics must recompute from its own retained
// samples — the producer and the consumer share one function (AC-7).
func TestIncrementalSeries_PublishedStatisticsRecompute(t *testing.T) {
	clock := newFakeClock(0)
	step := 0
	target := &scriptedTarget{onUpdate: func(changeStep) {
		step++
		clock.advance(time.Duration(step) * time.Millisecond)
	}}
	steps := buildChangeSequence(testSequenceInput(40))
	samples := testDriver(target, clock).run(context.Background(), steps)
	series := buildIncrementalSeries("fixture", 40, testSequenceInput(40), steps, samples)

	recomputed := evalreport.RecomputeIncremental(series.Changes)
	if series.Update != recomputed.Update {
		t.Errorf("published update %+v recomputes to %+v", series.Update, recomputed.Update)
	}
	if series.Freshness != recomputed.Freshness {
		t.Errorf("published freshness %+v recomputes to %+v", series.Freshness, recomputed.Freshness)
	}
	for _, c := range series.PerClass {
		if c != recomputed.Classes[c.Class] {
			t.Errorf("class %s published %+v, recomputes to %+v", c.Class, c, recomputed.Classes[c.Class])
		}
	}
	if series.Update.P50US > series.Update.P95US || series.Freshness.P50US > series.Freshness.P95US {
		t.Error("p50 exceeds p95")
	}
	// Every step retains its own measurement — AC-7 is per change, not per class.
	if len(series.Changes) != 40 {
		t.Errorf("retained %d of 40 individual measurements", len(series.Changes))
	}
	if series.Sequence.Digest == "" || series.Sequence.Method == "" {
		t.Error("the sequence is not reproducible from the artifact")
	}
}

// A series below FR-8's floor says so, and every gate over it is UNKNOWN.
func TestIncrementalSeries_BelowTheFloorIsUnknownNotPass(t *testing.T) {
	clock := newFakeClock(time.Millisecond)
	steps := buildChangeSequence(testSequenceInput(8))
	samples := testDriver(&scriptedTarget{}, clock).run(context.Background(), steps)
	series := buildIncrementalSeries("fixture", 8, testSequenceInput(8), steps, samples)

	if series.Minimum != evalreport.IncrementalChangeMinimum {
		t.Errorf("minimum = %d, want FR-8's floor", series.Minimum)
	}
	if series.Sufficient {
		t.Fatal("8 changes cannot be a sufficient FR-8 measurement")
	}
	if !series.ClassesCovered {
		t.Errorf("two full cycles must cover every required class: %+v", series.Coverage)
	}
	if incrementalStatus(series) != evalreport.StatusUnknown {
		t.Errorf("status = %s, want UNKNOWN", incrementalStatus(series))
	}
	if len(series.Warnings) == 0 {
		t.Error("an undersampled series must warn")
	}
}

// The uncovered-class case: no cross-package target means the class is missing,
// and the series must say so rather than reading as a complete sequence.
func TestIncrementalSeries_MissingClassIsNotCovered(t *testing.T) {
	in := testSequenceInput(12)
	in.crossPackage = evalreport.CrossPackageEvidence{Satisfied: false, Reason: "single-package fixture"}
	steps := buildChangeSequence(in)
	samples := testDriver(&scriptedTarget{}, newFakeClock(time.Millisecond)).run(context.Background(), steps)
	series := buildIncrementalSeries("fixture", 12, in, steps, samples)

	if series.ClassesCovered {
		t.Fatal("a sequence with no cross-package change must not read as covering every class")
	}
	if incrementalStatus(series) != evalreport.StatusUnknown {
		t.Errorf("status = %s, want UNKNOWN", incrementalStatus(series))
	}
	var explained bool
	for _, w := range series.Warnings {
		if strings.Contains(w, "single-package fixture") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("the missing class is not explained: %v", series.Warnings)
	}
}

// goPackageClause is what makes an added file join its siblings' package; it
// must survive build tags, comments and a trailing comment on the clause.
func TestGoPackageClause(t *testing.T) {
	cases := map[string]string{
		"package alpha\n": "alpha",
		"//go:build linux\n\npackage beta\n\nimport \"os\"": "beta",
		"// a doc comment\npackage gamma // trailing":       "gamma",
		"\n\n   package delta\n":                            "delta",
		"// no package clause at all\n":                     "",
	}
	for src, want := range cases {
		if got := goPackageClause([]byte(src)); got != want {
			t.Errorf("goPackageClause(%q) = %q, want %q", src, got, want)
		}
	}
}

// The file filter keeps the sequence on files a user edits.
func TestModifiableGoFile(t *testing.T) {
	included := []string{"a/one.go", "main.go", "internal/deep/x.go"}
	excluded := []string{"a/one_test.go", "vendor/x/y.go", "testdata/a.go", "a/readme.md", "third_party/z.go"}
	for _, p := range included {
		if !modifiableGoFile(p) {
			t.Errorf("%s should be modifiable", p)
		}
	}
	for _, p := range excluded {
		if modifiableGoFile(p) {
			t.Errorf("%s should not be modifiable", p)
		}
	}
}
