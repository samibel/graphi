package evalreport

// SW-124 (P0-C1): the percentile maths and the recomputability contract.
//
// These are the cases that break naive percentile implementations — one sample,
// even counts, odd counts, p100 — plus the property AC-6 turns into law: every
// published aggregate must be reproducible from the stored per-run samples, and
// a run that aborted must never enter a distribution.

import (
	"encoding/json"
	"math"
	"testing"
)

// AC-2 / test notes: nearest rank, on the cases that expose an off-by-one.
func TestPercentile_NearestRankEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		samples []int64
		p       int
		want    int64
	}{
		// One sample: every percentile is that sample. A naive
		// `sorted[p*n/100]` indexes 0 for p50 and 0 for p95 by luck here, but
		// panics for p100 without a clamp.
		{"single sample p50", []int64{7}, 50, 7},
		{"single sample p95", []int64{7}, 95, 7},
		{"single sample p100", []int64{7}, 100, 7},

		// Odd count: rank = ceil(0.5*5) = 3 → the true middle.
		{"odd count p50", []int64{5, 1, 3, 2, 4}, 50, 3},
		// Even count: rank = ceil(0.5*4) = 2 → the LOWER middle, an observed
		// sample. Interpolating would publish 2.5, a value never measured.
		{"even count p50", []int64{1, 2, 3, 4}, 50, 2},
		{"even count p95", []int64{1, 2, 3, 4}, 95, 4},

		// The FR-8 shape: ten runs. p95 of n=10 is rank ceil(9.5)=10, i.e. the
		// maximum — a property of the sample size, recorded rather than hidden.
		{"ten runs p50", []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, 50, 50},
		{"ten runs p95", []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}, 95, 100},

		// Nine runs (one of ten aborted): p95 = rank ceil(8.55) = 9 = max.
		{"nine runs p50", []int64{10, 20, 30, 40, 50, 60, 70, 80, 90}, 50, 50},
		{"nine runs p95", []int64{10, 20, 30, 40, 50, 60, 70, 80, 90}, 95, 90},

		// Empty: 0, and callers publish N beside it so the zero is readable.
		{"empty", nil, 50, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PercentileInt64(tc.samples, tc.p); got != tc.want {
				t.Errorf("PercentileInt64(%v, %d) = %d, want %d", tc.samples, tc.p, got, tc.want)
			}
			floats := make([]float64, len(tc.samples))
			for i, s := range tc.samples {
				floats[i] = float64(s)
			}
			if got := PercentileFloat64(floats, tc.p); got != float64(tc.want) {
				t.Errorf("PercentileFloat64(%v, %d) = %v, want %v", floats, tc.p, got, float64(tc.want))
			}
		})
	}
}

// The input slice must survive: the harness reuses its sample slices for
// several percentiles, and an in-place sort would be an invisible reordering.
func TestPercentile_DoesNotMutateInput(t *testing.T) {
	xs := []int64{3, 1, 2}
	_ = PercentileInt64(xs, 95)
	if xs[0] != 3 || xs[1] != 1 || xs[2] != 2 {
		t.Fatalf("input reordered: %v", xs)
	}
}

// p50 and p95 must come from the same definition — the whole reason there is
// one implementation. For a monotone sample p50 <= p95 always.
func TestPercentile_P50NeverExceedsP95(t *testing.T) {
	samples := []int64{93_000, 41_000, 88_000, 44_000, 91_000, 43_000, 90_000, 42_000, 89_000, 45_000}
	p50, p95 := PercentileInt64(samples, 50), PercentileInt64(samples, 95)
	if p50 > p95 {
		t.Fatalf("p50 %d > p95 %d", p50, p95)
	}
	if p95 != 93_000 {
		t.Fatalf("p95 = %d, want the maximum 93000 for n=10", p95)
	}
}

func completedSample(run int, wallclockMS, indexRSS, stableRSS, db int64, nodes, edges int) ColdRunSample {
	s := ColdRunSample{
		Run:    run,
		Status: ColdRunCompleted,
		Index: IndexMetrics{
			WallclockMS: wallclockMS,
			PeakRSSMB:   indexRSS,
			DBSizeBytes: db,
			Nodes:       nodes,
			Edges:       edges,
			Files:       nodes,
		},
		StablePeakRSSMB: stableRSS,
	}
	s.BytesPerEdge = BytesPerEdge(s.Index)
	return s
}

// AC-6: every published aggregate is recomputable from the stored samples.
func TestRecomputeColdAggregates_ReproducesEveryMetric(t *testing.T) {
	runs := []ColdRunSample{
		completedSample(1, 100, 300, 400, 2000, 10, 20),
		completedSample(2, 300, 310, 420, 4000, 12, 40),
		completedSample(3, 200, 320, 410, 3000, 11, 30),
	}
	agg := RecomputeColdAggregates(runs)

	for _, metric := range []string{
		MetricIndexWallclockMS, MetricIndexPeakRSSMB, MetricStablePeakRSSMB,
		MetricDBSizeBytes, MetricNodes, MetricEdges, MetricBytesPerEdge,
	} {
		a, ok := agg[metric]
		if !ok {
			t.Fatalf("metric %q absent from the aggregates", metric)
		}
		if a.N != 3 {
			t.Errorf("metric %q: n = %d, want 3", metric, a.N)
		}
		if a.Unit == "" {
			t.Errorf("metric %q has no unit", metric)
		}
		if a.Min > a.P50 || a.P50 > a.P95 || a.P95 > a.Max {
			t.Errorf("metric %q: min/p50/p95/max not ordered: %+v", metric, a)
		}
	}
	if got := agg[MetricIndexWallclockMS]; got.Min != 100 || got.P50 != 200 || got.P95 != 300 || got.Max != 300 {
		t.Errorf("wallclock aggregate = %+v, want min 100 p50 200 p95 300 max 300", got)
	}
	// bytes_per_edge is derived PER RUN and then aggregated, never
	// db_size_p50 / edges_p50 (which would mix two distributions).
	if got := agg[MetricBytesPerEdge]; math.Abs(got.Min-100) > 1e-9 || math.Abs(got.Max-100) > 1e-9 {
		t.Errorf("bytes_per_edge = %+v, want every sample 100 (2000/20, 4000/40, 3000/30)", got)
	}

	// Recomputing from the same samples is idempotent — the property SW-128's
	// aggregator relies on.
	again := RecomputeColdAggregates(runs)
	for metric, a := range agg {
		if again[metric] != a {
			t.Errorf("metric %q not reproducible: %+v vs %+v", metric, a, again[metric])
		}
	}
}

// AC-5: an aborted run is visible in the report and absent from every
// distribution. Both halves matter — dropping it silently is the failure, and
// so is letting its zero metrics drag a percentile down.
func TestRecomputeColdAggregates_AbortedRunsNeverEnterTheDistribution(t *testing.T) {
	runs := []ColdRunSample{
		completedSample(1, 100, 300, 400, 2000, 10, 20),
		{Run: 2, Status: ColdRunAborted, Error: "index: context deadline exceeded"},
		completedSample(3, 300, 320, 420, 4000, 12, 40),
	}
	agg := RecomputeColdAggregates(runs)
	wall, ok := agg[MetricIndexWallclockMS]
	if !ok {
		t.Fatal("wallclock aggregate missing")
	}
	if wall.N != 2 {
		t.Fatalf("n = %d, want 2 — the aborted run must not be a sample", wall.N)
	}
	if wall.Min != 100 {
		t.Errorf("min = %v, want 100; an aborted run's zero must not become the minimum", wall.Min)
	}
	if wall.P50 != 100 || wall.P95 != 300 {
		t.Errorf("p50/p95 = %v/%v, want 100/300 over the two completed runs", wall.P50, wall.P95)
	}
}

// A metric nobody measured is absent, not zero: "no distribution" and
// "a measured zero" are different claims and must not render alike.
//
// The distinction runs BOTH ways, which is the point of this test. A completed
// run that indexed a degenerate repository really did measure zero edges, and
// that zero is a sample — dropping it would make `n` silently disagree with
// runs_completed and hide the degenerate repository behind an absence. A
// ratio over those zero edges, by contrast, is undefined and stays absent.
func TestRecomputeColdAggregates_MeasuredZeroIsNotAnAbsentMetric(t *testing.T) {
	runs := []ColdRunSample{completedSample(1, 100, 300, 400, 2000, 10, 0)}
	agg := RecomputeColdAggregates(runs)

	edges, ok := agg[MetricEdges]
	if !ok {
		t.Fatal("edges aggregate absent for a completed run that measured zero edges — a measured zero is a sample")
	}
	if edges.N != 1 || edges.Min != 0 || edges.P50 != 0 || edges.P95 != 0 || edges.Max != 0 {
		t.Errorf("edges aggregate = %+v, want one sample of zero", edges)
	}
	if _, ok := agg[MetricBytesPerEdge]; ok {
		t.Error("bytes_per_edge present without edges — a ratio over zero edges is not a measurement")
	}
	if _, ok := agg[MetricIndexWallclockMS]; !ok {
		t.Error("wallclock aggregate should still be present")
	}

	// An ABORTED run is a different claim again: it measured nothing at all,
	// so it contributes no zero to any distribution.
	aborted := RecomputeColdAggregates([]ColdRunSample{{Run: 1, Status: ColdRunAborted, Error: "clone failed"}})
	if len(aborted) != 0 {
		t.Errorf("an aborted run produced aggregates %+v; it measured nothing", aborted)
	}
}

// A zero-valued OOMResult must read UNKNOWN, so a forgotten check can never
// render as a pass (AC-4).
func TestOOMResult_ZeroValueIsNotAPass(t *testing.T) {
	var r OOMResult
	if r.Status == StatusPass {
		t.Fatal("zero-value OOMResult must not be PASS")
	}
	if r.LimitVerified || r.RunCompleted {
		t.Fatal("zero-value OOMResult claims a verified limit or a completed run")
	}
}

// AC-2 / AC-6: the samples survive serialisation, and the aggregates recompute
// from the DESERIALISED report — the form a later story actually reads.
func TestColdRunSeries_SurvivesSerialisationAndRecomputes(t *testing.T) {
	series := ColdRunSeries{
		Repo:          "grpc-go",
		RunsRequested: 3,
		RunsCompleted: 2,
		RunsAborted:   1,
		MinimumRuns:   ColdRunMinimum,
		Runs: []ColdRunSample{
			completedSample(1, 100, 300, 400, 2000, 10, 20),
			{Run: 2, Status: ColdRunAborted, Error: "clone: network unreachable"},
			completedSample(3, 300, 320, 420, 4000, 12, 40),
		},
		AggregateMethod: AggregateMethodNote,
		Status:          StatusUnknown,
	}
	series.Aggregates = RecomputeColdAggregates(series.Runs)

	raw, err := json.Marshal(series)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ColdRunSeries
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Runs) != 3 {
		t.Fatalf("runs after round-trip = %d, want 3 (including the aborted one)", len(back.Runs))
	}
	if back.Runs[1].Status != ColdRunAborted || back.Runs[1].Error == "" {
		t.Errorf("aborted run lost its status or reason: %+v", back.Runs[1])
	}
	recomputed := RecomputeColdAggregates(back.Runs)
	for metric, published := range back.Aggregates {
		if recomputed[metric] != published {
			t.Errorf("metric %q published as %+v but recomputes to %+v", metric, published, recomputed[metric])
		}
	}
	if len(recomputed) == 0 {
		t.Fatal("no aggregates recomputed from the deserialised samples")
	}
}
