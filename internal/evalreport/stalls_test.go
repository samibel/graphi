package evalreport

// SW-127 (P0-C4): the arithmetic of the stall payload.
//
// These tests own the derivation only. What a MEASUREMENT means — a silent run,
// a boundary interval, a gate — lives in cmd/eval, where the observer and the
// gate reader are.

import (
	"encoding/json"
	"testing"
)

func intervals(us ...int64) []StallInterval {
	out := make([]StallInterval, 0, len(us))
	for i, v := range us {
		out = append(out, StallInterval{Seq: i + 1, Phase: "parse", US: v})
	}
	return out
}

// AC-2: p95 AND the maximum are both derived, and both come from the retained
// intervals rather than from anything the harness kept on the side.
func TestRecomputeStalls_DerivesP95AndMaximum(t *testing.T) {
	// 20 samples: nineteen fast gaps and one 3-second stall. Nearest rank puts
	// p95 at rank ceil(0.95*20) = 19, so the p95 is the second-largest sample
	// and the maximum is the outlier — the two must not collapse into one
	// number, which is exactly why FR-8 asks for both.
	var us []int64
	for i := 0; i < 19; i++ {
		us = append(us, int64(1000+i))
	}
	us = append(us, 3_000_000)

	got := RecomputeStalls(intervals(us...)).Stalls
	if got.N != 20 {
		t.Fatalf("n = %d, want 20", got.N)
	}
	if got.MaxUS != 3_000_000 {
		t.Errorf("max = %d µs, want the 3 s outlier", got.MaxUS)
	}
	if got.P95US != 1018 {
		t.Errorf("p95 = %d µs, want 1018 (nearest rank 19 of 20)", got.P95US)
	}
	if got.MinUS != 1000 {
		t.Errorf("min = %d µs, want 1000", got.MinUS)
	}
}

// A delayed emitter — one that goes quiet for seconds at a time — must move the
// p95, not only the maximum. This is the property the gate is read against.
func TestRecomputeStalls_ADelayedEmitterMovesTheP95(t *testing.T) {
	var us []int64
	for i := 0; i < 90; i++ {
		us = append(us, 2_000)
	}
	for i := 0; i < 10; i++ {
		us = append(us, 3_000_000)
	}

	got := RecomputeStalls(intervals(us...)).Stalls
	if got.P95US != 3_000_000 {
		t.Errorf("p95 = %d µs; ten 3 s silences in a hundred gaps must surface in the p95, not only in the maximum", got.P95US)
	}
	if got.MaxUS != 3_000_000 {
		t.Errorf("max = %d µs, want 3 s", got.MaxUS)
	}
}

// AC-6: the per-phase pools attribute a stall, and they are derived from the
// same retained samples as the pooled distribution.
func TestRecomputeStalls_AttributesEachGapToThePhaseThatEndedIt(t *testing.T) {
	in := []StallInterval{
		{Seq: 1, Phase: "walk", US: 1_000},
		{Seq: 2, Phase: "parse", US: 2_000},
		{Seq: 3, Phase: "parse", US: 4_000},
		{Seq: 4, Phase: "resolve", US: 9_000_000},
	}
	got := RecomputeStalls(in)
	if got.Stalls.N != 4 {
		t.Fatalf("pooled n = %d, want 4", got.Stalls.N)
	}
	if got.Phases["parse"].N != 2 || got.Phases["parse"].MaxUS != 4_000 {
		t.Errorf("parse pool = %+v, want n=2 max=4000", got.Phases["parse"])
	}
	if got.Phases["resolve"].MaxUS != 9_000_000 {
		t.Errorf("the 9 s silence was not attributed to `resolve`: %+v", got.Phases["resolve"])
	}

	table := PhaseStallsOf(in)
	if len(table) != 3 {
		t.Fatalf("phase table has %d rows, want 3", len(table))
	}
	// Sorted by name so two runs of the same repository are comparable.
	for i, want := range []string{"parse", "resolve", "walk"} {
		if table[i].Phase != want {
			t.Errorf("phase row %d is %q, want %q (the table must be sorted)", i, table[i].Phase, want)
		}
	}
}

// The degenerate input. An empty interval list yields a distribution whose N is
// 0 — NOT a zero-microsecond stall. Every consumer must read N; this test pins
// that the schema makes that possible rather than publishing a bare 0.
func TestRecomputeStalls_NoIntervalsIsNoMeasurement(t *testing.T) {
	got := RecomputeStalls(nil)
	if got.Stalls.N != 0 {
		t.Fatalf("n = %d over no intervals, want 0", got.Stalls.N)
	}
	if len(got.Phases) != 0 {
		t.Errorf("phases = %v over no intervals, want none", got.Phases)
	}
	if len(PhaseStallsOf(nil)) != 0 {
		t.Errorf("the phase table must be empty when nothing was measured")
	}
}

// The retained intervals are the evidence: whatever the harness published must
// be reproducible from `intervals` alone (AC-6, and SW-128's contract).
func TestStallSeries_PublishedStatisticsRecomputeFromTheRetainedIntervals(t *testing.T) {
	in := intervals(5_000, 1_000, 250_000, 3_000, 900_000)
	series := StallSeries{
		Repo:      "grpc-go",
		Events:    len(in) + 1,
		Intervals: in,
		Stalls:    RecomputeStalls(in).Stalls,
		PerPhase:  PhaseStallsOf(in),
	}

	raw, err := json.Marshal(series)
	if err != nil {
		t.Fatal(err)
	}
	var round StallSeries
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if got := RecomputeStalls(round.Intervals).Stalls; got != round.Stalls {
		t.Errorf("recomputed %+v from the published intervals, but the artifact says %+v", got, round.Stalls)
	}
	if round.Events != len(round.Intervals)+1 {
		t.Errorf("events_observed = %d but %d intervals: n gaps require n+1 events", round.Events, len(round.Intervals))
	}
}
