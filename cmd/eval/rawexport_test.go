package main

// SW-128 (P0-C5): what the export lifts out of a finished report.
//
// The one property that matters here is the same one the raw format is built
// around: an absent series and an empty one are different claims, and the
// export is the place they could most easily be collapsed — a `nil` slice and a
// `nil` pointer look alike from three lines away.

import (
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

// A run that measured nothing but the cold index exports exactly one raw
// series. The other three are ABSENT, not empty — the aggregator will read
// their metrics as UNKNOWN, which is correct, because they were never
// published either.
func TestRawSetsFrom_OnlyExportsWhatWasMeasured(t *testing.T) {
	runs := []evalreport.ColdRunSample{{Run: 1, Status: evalreport.ColdRunCompleted,
		Index: evalreport.IndexMetrics{WallclockMS: 1000, PeakRSSMB: 100, DBSizeBytes: 500, Edges: 10}}}
	report := evalreport.FullRunReport{ColdSeries: &evalreport.ColdRunSeries{Repo: "grpc-go", Runs: runs}}

	sets := rawSetsFrom(report, evalreport.RunEnvironment{})
	if len(sets) != 1 {
		t.Fatalf("sets = %d, want exactly the cold series", len(sets))
	}
	cold, ok := sets[evalreport.RawSeriesCold]
	if !ok {
		t.Fatal("the cold series was not exported")
	}
	if !cold.Collected || cold.Samples != 1 {
		t.Fatalf("cold set = collected %v, samples %d, want true/1", cold.Collected, cold.Samples)
	}
	if cold.Repo != "grpc-go" {
		t.Errorf("repo = %q, want grpc-go — a raw file read alone must say what it is about", cold.Repo)
	}
}

// SW-127's silent index: the stall series RAN and produced no intervals. That
// must export as a COLLECTED set with zero samples, because the alternative —
// no file — would make the aggregator report it as unmeasured and the silence
// this gate exists to catch would render as missing data.
func TestRawSetsFrom_ASilentStallSeriesIsCollectedNotAbsent(t *testing.T) {
	report := evalreport.FullRunReport{Repo: evalreport.FullRepoRun{
		Stalls: &evalreport.StallSeries{Repo: "grpc-go", Events: 0, Observable: false, Intervals: nil},
	}}

	set, ok := rawSetsFrom(report, evalreport.RunEnvironment{})[evalreport.RawSeriesStalls]
	if !ok {
		t.Fatal("a measured-but-silent stall series was not exported at all")
	}
	if !set.Collected {
		t.Fatal("a silent stall series exported as uncollected — the silence would read as missing data")
	}
	if set.Samples != 0 {
		t.Fatalf("samples = %d, want 0", set.Samples)
	}
}

// The lifecycle operation publishes no distribution, so it contributes no raw
// record. Exporting it with an empty sample list would create a metric the
// aggregator then has to special-case back out.
func TestRawQueryFrom_ExcludesTheLifecycleOperation(t *testing.T) {
	series := &evalreport.QueryLatencySeries{
		Operations: []evalreport.QueryOpLatency{
			{Operation: "callers", Class: evalreport.QueryClassStructural, Measured: true,
				Latency: latencyPtr(evalreport.LatencyStatsFrom([]int64{10, 20})), SamplesUS: []int64{10, 20}},
			{Operation: "index", Class: evalreport.QueryClassLifecycle, Measured: false},
		},
		Classes: []evalreport.QueryClassLatency{{Class: evalreport.QueryClassStructural, Operations: []string{"callers"}}},
		Pools: []evalreport.QueryPoolLatency{{GateID: "caller_callee_impact_p95",
			Class: evalreport.QueryClassStructural, Operations: []string{"callers"}}},
	}

	ops, pools := rawQueryFrom(series)
	if len(ops) != 1 || ops[0].Operation != "callers" {
		t.Fatalf("operations = %+v, want only callers", ops)
	}
	// Membership travels for both kinds: a class statistic and a gate-pool
	// statistic are pooled over different operation sets, and the aggregator
	// must reproduce each over the set the report used.
	kinds := map[string]string{}
	for _, p := range pools {
		kinds[p.ID] = p.Kind
	}
	if kinds[evalreport.QueryClassStructural] != evalreport.RawPoolClass {
		t.Errorf("class pool kind = %q, want %q", kinds[evalreport.QueryClassStructural], evalreport.RawPoolClass)
	}
	if kinds["caller_callee_impact_p95"] != evalreport.RawPoolGate {
		t.Errorf("gate pool kind = %q, want %q", kinds["caller_callee_impact_p95"], evalreport.RawPoolGate)
	}
}

// The cache state in the environment is the state the run REACHED, and the
// series' per-run observation wins over the single-run field — ten verified
// runs are better evidence than one.
func TestObservedCacheState_PrefersTheVerifiedSeriesObservation(t *testing.T) {
	report := evalreport.FullRunReport{
		ColdSeries: &evalreport.ColdRunSeries{Runs: []evalreport.ColdRunSample{
			{Run: 1, Status: evalreport.ColdRunAborted, Cold: evalreport.ColdState{PageCache: evalreport.PageCacheDropFailed}},
			{Run: 2, Status: evalreport.ColdRunCompleted, Cold: evalreport.ColdState{PageCache: evalreport.PageCacheDropped}},
		}},
		Repo: evalreport.FullRepoRun{Cold: evalreport.ColdState{PageCache: evalreport.PageCacheNotDropped}},
	}
	// The ABORTED run's observation must not win: it did not produce a
	// measurement, so it does not describe the measurement's cache state.
	if got := observedCacheState(report); got != evalreport.PageCacheDropped {
		t.Fatalf("cache state = %q, want %q from the completed run", got, evalreport.PageCacheDropped)
	}

	single := evalreport.FullRunReport{Repo: evalreport.FullRepoRun{
		Cold: evalreport.ColdState{PageCache: evalreport.PageCacheUnsupported}}}
	if got := observedCacheState(single); got != evalreport.PageCacheUnsupported {
		t.Fatalf("single-run cache state = %q, want %q", got, evalreport.PageCacheUnsupported)
	}
}
