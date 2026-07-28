package evalreport

import (
	"encoding/json"
	"reflect"
	"testing"
)

// SW-125 AC-3/AC-7: the published p50 and p95 are DERIVED, and the derivation
// is the same nearest-rank one the cold series uses. These are the sample
// shapes that break naive implementations.
func TestLatencyStatsFrom_PercentileEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		samples []int64
		want    LatencyStats
	}{
		{"empty is not a measurement", nil, LatencyStats{}},
		{"single sample is every percentile", []int64{7}, LatencyStats{N: 1, MinUS: 7, P50US: 7, P95US: 7, MaxUS: 7}},
		{
			// Even n: nearest rank takes the LOWER middle sample, never the
			// mean of the two — a value nobody observed must not be published.
			"even count takes an observed sample",
			[]int64{10, 20, 30, 40},
			LatencyStats{N: 4, MinUS: 10, P50US: 20, P95US: 40, MaxUS: 40},
		},
		{"odd count", []int64{30, 10, 20}, LatencyStats{N: 3, MinUS: 10, P50US: 20, P95US: 30, MaxUS: 30}},
		{
			"unsorted input is sorted, not trusted",
			[]int64{5, 1, 9, 3, 7, 2, 8, 4, 6, 10},
			LatencyStats{N: 10, MinUS: 1, P50US: 5, P95US: 10, MaxUS: 10},
		},
		{"a measured zero is a measurement", []int64{0, 0, 0}, LatencyStats{N: 3, MinUS: 0, P50US: 0, P95US: 0, MaxUS: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LatencyStatsFrom(tc.samples)
			if got != tc.want {
				t.Fatalf("LatencyStatsFrom(%v) = %+v, want %+v", tc.samples, got, tc.want)
			}
			if got.N > 0 && got.P50US > got.P95US {
				t.Errorf("p50 %d exceeds p95 %d", got.P50US, got.P95US)
			}
		})
	}
}

// LatencyStatsFrom must not reorder its caller's slice: the retained samples
// are published in EXECUTION order, and sorting them in place would silently
// destroy that.
func TestLatencyStatsFrom_DoesNotMutateItsInput(t *testing.T) {
	samples := []int64{9, 1, 5}
	original := append([]int64(nil), samples...)
	LatencyStatsFrom(samples)
	if !reflect.DeepEqual(samples, original) {
		t.Fatalf("input was reordered: %v, want %v", samples, original)
	}
}

func fixtureSeries() QueryLatencySeries {
	return QueryLatencySeries{
		Repo:    "fixture",
		Minimum: QueryExecutionMinimum,
		ClassOf: map[string]string{
			"callers": QueryClassStructural,
			"callees": QueryClassStructural,
			"impact":  QueryClassStructural,
			"search":  QueryClassSearch,
			"index":   QueryClassLifecycle,
		},
		Operations: []QueryOpLatency{
			{Operation: "callees", Class: QueryClassStructural, Measured: true, SamplesUS: []int64{4, 8, 12}},
			{Operation: "callers", Class: QueryClassStructural, Measured: true, SamplesUS: []int64{1, 3, 5}},
			{Operation: "impact", Class: QueryClassStructural, Measured: true, SamplesUS: []int64{100, 200}},
			{Operation: "index", Class: QueryClassLifecycle, Measured: false, Note: LifecycleOperationNote},
			{Operation: "search", Class: QueryClassSearch, Measured: true, SamplesUS: []int64{50, 60, 70, 80}},
		},
		Classes: []QueryClassLatency{
			{Class: QueryClassSearch, Operations: []string{"search"}, Minimum: QueryExecutionMinimum},
			{Class: QueryClassStructural, Operations: []string{"callees", "callers", "impact"}, Minimum: QueryExecutionMinimum},
		},
		Pools: []QueryPoolLatency{
			{GateID: "caller_callee_impact_p95", Operations: []string{"callees", "callers", "impact"}, Minimum: QueryExecutionMinimum},
			{GateID: "warm_search_p95", Operations: []string{"search"}, Minimum: QueryExecutionMinimum},
		},
	}
}

// AC-7: every published statistic is reproducible from the retained samples —
// which is what makes "1000 executions" a checkable claim rather than a label.
func TestRecomputeQueryLatency_DerivesEveryStatisticFromTheSamples(t *testing.T) {
	got := RecomputeQueryLatency(fixtureSeries())

	if want := (LatencyStats{N: 3, MinUS: 1, P50US: 3, P95US: 5, MaxUS: 5}); got.Operations["callers"] != want {
		t.Errorf("callers = %+v, want %+v", got.Operations["callers"], want)
	}
	// The class pools its three operations: 1,3,5,4,8,12,100,200 → n=8.
	if want := (LatencyStats{N: 8, MinUS: 1, P50US: 5, P95US: 200, MaxUS: 200}); got.Classes[QueryClassStructural] != want {
		t.Errorf("structural class = %+v, want %+v", got.Classes[QueryClassStructural], want)
	}
	// The gate pool happens to be the same three operations here; the point is
	// that it is derived from its OWN membership list, not from the class.
	if got.Pools["caller_callee_impact_p95"] != got.Classes[QueryClassStructural] {
		t.Errorf("gate pool = %+v, class = %+v", got.Pools["caller_callee_impact_p95"], got.Classes[QueryClassStructural])
	}
	if want := (LatencyStats{N: 4, MinUS: 50, P50US: 60, P95US: 80, MaxUS: 80}); got.Pools["warm_search_p95"] != want {
		t.Errorf("search pool = %+v, want %+v", got.Pools["warm_search_p95"], want)
	}
	// The lifecycle operation contributes to nothing and is not silently
	// present with a zero distribution (AC-4).
	if _, ok := got.Operations["index"]; ok {
		t.Error("the lifecycle operation must not carry a latency distribution")
	}
}

// A gate pool that is a STRICT SUBSET of its class must not inherit the class's
// numbers. This is the reason pools exist at all: `caller_callee_impact_p95`
// names three of six structural operations, and a class that cleared the floor
// says nothing about whether the gated subset did.
func TestRecomputeQueryLatency_GatePoolIsNotItsClass(t *testing.T) {
	s := fixtureSeries()
	s.Pools = []QueryPoolLatency{{
		GateID: "caller_callee_impact_p95", Operations: []string{"callers"}, Minimum: QueryExecutionMinimum,
	}}
	got := RecomputeQueryLatency(s)
	if got.Pools["caller_callee_impact_p95"].N != 3 {
		t.Fatalf("pool n = %d, want 3 (only its own operation's samples)", got.Pools["caller_callee_impact_p95"].N)
	}
	if got.Pools["caller_callee_impact_p95"] == got.Classes[QueryClassStructural] {
		t.Error("a subset pool must not equal its class")
	}
}

// AC-7 over the wire: the retained samples survive serialisation, and the
// aggregates recompute from the DESERIALISED report — which is the form
// SW-128's aggregator will read.
func TestQueryLatencySeries_SurvivesSerialisationAndRecomputes(t *testing.T) {
	s := fixtureSeries()
	produced := RecomputeQueryLatency(s)
	for i := range s.Operations {
		if stats, measured := produced.Operations[s.Operations[i].Operation]; measured {
			s.Operations[i].Latency = &stats
		}
	}
	for i := range s.Classes {
		s.Classes[i].LatencyStats = produced.Classes[s.Classes[i].Class]
		s.Classes[i].Executions = s.Classes[i].N
	}
	for i := range s.Pools {
		s.Pools[i].LatencyStats = produced.Pools[s.Pools[i].GateID]
		s.Pools[i].Executions = s.Pools[i].N
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var round QueryLatencySeries
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	reproduced := RecomputeQueryLatency(round)
	for _, op := range round.Operations {
		if !op.Measured {
			if op.Latency != nil {
				t.Errorf("unmeasured operation %s published a distribution %+v", op.Operation, *op.Latency)
			}
			continue
		}
		if op.Latency == nil {
			t.Fatalf("operation %s lost its distribution across serialisation", op.Operation)
		}
		if *op.Latency != reproduced.Operations[op.Operation] {
			t.Errorf("operation %s published %+v but its samples recompute to %+v", op.Operation, *op.Latency, reproduced.Operations[op.Operation])
		}
	}
	for _, c := range round.Classes {
		if c.LatencyStats != reproduced.Classes[c.Class] {
			t.Errorf("class %s published %+v but recomputes to %+v", c.Class, c.LatencyStats, reproduced.Classes[c.Class])
		}
		if c.Executions != c.N {
			t.Errorf("class %s executions %d != n %d", c.Class, c.Executions, c.N)
		}
	}
	for _, p := range round.Pools {
		if p.LatencyStats != reproduced.Pools[p.GateID] {
			t.Errorf("pool %s published %+v but recomputes to %+v", p.GateID, p.LatencyStats, reproduced.Pools[p.GateID])
		}
	}
}

// AC-5: the digest is an equality check over an ORDERED sample. Two runs that
// sampled the same symbols in a different order did not measure the same
// question, so the digest must say so.
func TestSampleDigest_IsDeterministicAndOrderSensitive(t *testing.T) {
	a := []string{"go:pkg.A", "go:pkg.B", "go:pkg.C"}
	if SampleDigest(a) != SampleDigest([]string{"go:pkg.A", "go:pkg.B", "go:pkg.C"}) {
		t.Fatal("the same ordered sample must produce the same digest")
	}
	if SampleDigest(a) == SampleDigest([]string{"go:pkg.B", "go:pkg.A", "go:pkg.C"}) {
		t.Error("a reordered sample must not read as the same sample")
	}
	if SampleDigest(a) == SampleDigest(a[:2]) {
		t.Error("a shorter sample must not read as the same sample")
	}
	// The separator must not be forgeable by an id that contains it.
	if SampleDigest([]string{"a", "b"}) == SampleDigest([]string{"a\nb"}) {
		t.Error("two ids must not digest identically to one id containing the separator")
	}
	if SampleDigest(nil) != SampleDigest([]string{}) {
		t.Error("nil and empty must agree")
	}
}

// PRD §8.2 in the schema: the FR-8 floor and the class's own count are both in
// the artifact, so a reader can check sufficiency without the PRD — and the
// floor is the constant, not a number the harness chose for itself.
func TestQueryExecutionMinimum_IsFR8sFloor(t *testing.T) {
	if QueryExecutionMinimum != 1000 {
		t.Fatalf("QueryExecutionMinimum = %d, want 1000 (FR-8)", QueryExecutionMinimum)
	}
}
