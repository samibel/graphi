package evalreport

// SW-128 (P0-C5): the aggregator that reproduces every published number.
//
// The central test is AC-2, and it is deliberately the first one below: a fixed
// raw dataset in, the published aggregates out, and a byte-for-byte agreement
// between them. Everything after it is a way of being wrong — a number that
// drifted, a metric with no raw data behind it, an environment that was never
// captured — and in every one of those cases the aggregate must refuse to read
// as reproduced.

import (
	"strings"
	"testing"
)

// fixedColdSeries is the fixed raw dataset AC-2 asks for: ten completed cold
// runs with hand-chosen wallclocks, so the expected p50 and p95 can be stated
// here rather than taken from the code under test.
func fixedColdSeries() ([]ColdRunSample, *ColdRunSeries) {
	// Ten runs, wallclock 1000..1900 ms in 100 ms steps. Nearest rank puts the
	// p50 at rank ceil(0.5*10) = 5 (1400 ms) and the p95 at rank
	// ceil(0.95*10) = 10 (1900 ms).
	var runs []ColdRunSample
	for i := 0; i < 10; i++ {
		runs = append(runs, ColdRunSample{
			Run:    i + 1,
			Status: ColdRunCompleted,
			Index: IndexMetrics{
				WallclockMS: int64(1000 + i*100),
				PeakRSSMB:   int64(500 + i),
				DBSizeBytes: int64(200_000_000 + i),
				Nodes:       1000,
				Edges:       4000,
			},
			StablePeakRSSMB: int64(600 + i),
			BytesPerEdge:    float64(200_000_000+i) / 4000,
		})
	}
	series := &ColdRunSeries{
		Repo:          "grpc-go",
		RunsRequested: 10,
		RunsCompleted: 10,
		MinimumRuns:   ColdRunMinimum,
		Sufficient:    true,
		Runs:          runs,
		Aggregates:    RecomputeColdAggregates(runs),
	}
	return runs, series
}

// AC-2, the central case: every published number follows from the raw data.
func TestAggregate_ReproducesEveryPublishedColdAggregate(t *testing.T) {
	runs, series := fixedColdSeries()
	report := FullRunReport{ColdSeries: series}
	sets := map[string]RawSampleSet{
		RawSeriesCold: NewRawColdSet("grpc-go", completeEnvironment(), runs),
	}

	got := Reproduce(report, sets, completeEnvironment())

	if got.Status != StatusPass {
		t.Fatalf("status = %s (%v), want PASS", got.Status, got.Discrepancies)
	}
	if !got.Publishable {
		t.Fatal("a fully reproduced, fully captured run is not publishable")
	}
	if got.Checked == 0 {
		t.Fatal("no metric was checked at all")
	}
	if got.Discrepant != 0 || got.Unknown != 0 {
		t.Fatalf("discrepant = %d, unknown = %d, want 0/0", got.Discrepant, got.Unknown)
	}

	// The expected values are stated here, not read from the code, so a change
	// in the percentile definition breaks this test rather than sliding through
	// both sides of the comparison.
	want := map[string]float64{
		"cold_index.index_wallclock_ms.p50": 1400,
		"cold_index.index_wallclock_ms.p95": 1900,
		"cold_index.index_wallclock_ms.min": 1000,
		"cold_index.index_wallclock_ms.max": 1900,
		"cold_index.index_wallclock_ms.n":   10,
	}
	byMetric := map[string]MetricCheck{}
	for _, m := range got.Metrics {
		byMetric[m.Metric] = m
	}
	for metric, value := range want {
		check, ok := byMetric[metric]
		if !ok {
			t.Errorf("metric %s was never checked", metric)
			continue
		}
		if check.Recomputed != value {
			t.Errorf("%s recomputed = %v, want %v", metric, check.Recomputed, value)
		}
		if check.Published != value {
			t.Errorf("%s published = %v, want %v", metric, check.Published, value)
		}
		if check.Status != StatusPass {
			t.Errorf("%s status = %s, want PASS", metric, check.Status)
		}
	}
}

// AC-2's other half: a report whose published number does NOT follow from its
// samples is an ERROR. Not a warning, not a rounding note — the whole aggregate
// fails, and the offending metric is named.
func TestAggregate_ADriftedPublishedNumberIsAnError(t *testing.T) {
	runs, series := fixedColdSeries()
	// One aggregate is edited to a plausible neighbouring value — the kind of
	// drift a hand-maintained report or a half-finished refactor produces.
	edited := series.Aggregates[MetricIndexWallclockMS]
	edited.P95 = 1850
	series.Aggregates[MetricIndexWallclockMS] = edited

	report := FullRunReport{ColdSeries: series}
	sets := map[string]RawSampleSet{RawSeriesCold: NewRawColdSet("grpc-go", completeEnvironment(), runs)}

	got := Reproduce(report, sets, completeEnvironment())

	if got.Status != StatusFail {
		t.Fatalf("status = %s, want FAIL", got.Status)
	}
	if got.Publishable {
		t.Fatal("an aggregate that contradicts its raw data is publishable")
	}
	if got.Discrepant != 1 {
		t.Fatalf("discrepant = %d, want exactly 1", got.Discrepant)
	}
	if len(got.Discrepancies) != 1 || !strings.Contains(got.Discrepancies[0], "index_wallclock_ms.p95") {
		t.Fatalf("discrepancies = %v, want the drifted metric named", got.Discrepancies)
	}
	if !strings.Contains(got.Discrepancies[0], "1850") || !strings.Contains(got.Discrepancies[0], "1900") {
		t.Errorf("discrepancy = %q, want both the published and the recomputed value", got.Discrepancies[0])
	}
}

// AC-5: raw data missing for a metric makes that metric UNKNOWN — never a
// number, and never a pass by omission.
func TestAggregate_MissingRawDataReadsUNKNOWN(t *testing.T) {
	_, series := fixedColdSeries()
	report := FullRunReport{ColdSeries: series}

	// The published cold aggregates are there; the raw samples are not.
	got := Reproduce(report, map[string]RawSampleSet{}, completeEnvironment())

	if got.Status != StatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN", got.Status)
	}
	if got.Publishable {
		t.Fatal("AC-5: an aggregate without raw data must not be publishable")
	}
	if got.Unknown == 0 {
		t.Fatal("no metric read UNKNOWN despite there being no raw data at all")
	}
	for _, m := range got.Metrics {
		if m.Status != StatusUnknown {
			t.Fatalf("metric %s = %s, want UNKNOWN when no raw data exists", m.Metric, m.Status)
		}
		if m.HasRaw {
			t.Fatalf("metric %s claims raw data that was never supplied", m.Metric)
		}
	}
	if !strings.Contains(strings.Join(got.MissingSeries, ","), RawSeriesCold) {
		t.Errorf("missing series = %v, want cold_index named", got.MissingSeries)
	}
}

// UNKNOWN is not FAIL, and the distinction has to survive into the summary:
// a run with no raw data is unproven, not contradicted. Conflating them would
// make "we did not measure it" and "the number is wrong" the same signal.
func TestAggregate_UnknownIsNotFail(t *testing.T) {
	_, series := fixedColdSeries()
	got := Reproduce(FullRunReport{ColdSeries: series}, map[string]RawSampleSet{}, completeEnvironment())
	if got.Discrepant != 0 {
		t.Fatalf("discrepant = %d, want 0 — nothing was contradicted, only unmeasured", got.Discrepant)
	}
	if len(got.Discrepancies) != 0 {
		t.Fatalf("discrepancies = %v, want none", got.Discrepancies)
	}
}

// AC-3 feeding AC-5: an environment that was not fully captured blocks
// publication even when every metric reproduces. A number whose machine is
// undocumented is not a baseline.
func TestAggregate_AnIncompleteEnvironmentBlocksPublication(t *testing.T) {
	runs, series := fixedColdSeries()
	env := completeEnvironment()
	env.Kernel = ""

	got := Reproduce(FullRunReport{ColdSeries: series},
		map[string]RawSampleSet{RawSeriesCold: NewRawColdSet("grpc-go", env, runs)}, env)

	if got.Discrepant != 0 {
		t.Fatalf("discrepant = %d, want 0 — the arithmetic is fine, the environment is not", got.Discrepant)
	}
	if got.Publishable {
		t.Fatal("a run with an undocumented kernel is publishable")
	}
	if got.Status != StatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN", got.Status)
	}
	if len(got.MissingEnvironment) != 1 || got.MissingEnvironment[0] != EnvKernel {
		t.Fatalf("missing environment = %v, want [kernel]", got.MissingEnvironment)
	}
}

// A report with nothing published at all must not read as a clean reproduction.
// Zero checks passing zero comparisons is the most seductive false green there
// is.
func TestAggregate_AnEmptyReportIsNotAPass(t *testing.T) {
	got := Reproduce(FullRunReport{}, map[string]RawSampleSet{}, completeEnvironment())
	if got.Status == StatusPass {
		t.Fatal("a report with no published metrics read as PASS")
	}
	if got.Publishable {
		t.Fatal("a report with no published metrics is publishable")
	}
	if got.Checked != 0 {
		t.Fatalf("checked = %d, want 0", got.Checked)
	}
}

// SW-125's statistics: per-operation, per-class and per-gate-pool figures are
// all recomputed, and the pool membership comes from the raw file rather than
// being inferred from operation names.
func TestAggregate_ReproducesQueryLatencyAcrossOperationsClassesAndPools(t *testing.T) {
	callers := []int64{10, 20, 30, 40}
	impact := []int64{100, 200, 300, 400}

	rawOps := []RawQueryOperation{
		{Operation: "callers", Class: QueryClassStructural, SamplesUS: callers},
		{Operation: "impact", Class: QueryClassStructural, SamplesUS: impact},
	}
	rawPools := []RawQueryPool{
		{ID: QueryClassStructural, Kind: RawPoolClass, Operations: []string{"callers", "impact"}},
		{ID: "caller_callee_impact_p95", Kind: RawPoolGate, Operations: []string{"callers", "impact"}},
	}

	series := &QueryLatencySeries{
		Repo: "grpc-go",
		Operations: []QueryOpLatency{
			{Operation: "callers", Class: QueryClassStructural, Measured: true, Latency: statsPtr(LatencyStatsFrom(callers)), SamplesUS: callers},
			{Operation: "impact", Class: QueryClassStructural, Measured: true, Latency: statsPtr(LatencyStatsFrom(impact)), SamplesUS: impact},
			// The lifecycle operation carries no distribution at all; it must
			// not become a checked metric, and it must not become an UNKNOWN
			// either — it is a recorded decision, not a gap.
			{Operation: "index", Class: QueryClassLifecycle, Measured: false, Note: LifecycleOperationNote},
		},
		Classes: []QueryClassLatency{{
			Class:        QueryClassStructural,
			Operations:   []string{"callers", "impact"},
			Executions:   8,
			LatencyStats: LatencyStatsFrom(append(append([]int64{}, callers...), impact...)),
		}},
		Pools: []QueryPoolLatency{{
			GateID:       "caller_callee_impact_p95",
			Class:        QueryClassStructural,
			Operations:   []string{"callers", "impact"},
			Executions:   8,
			LatencyStats: LatencyStatsFrom(append(append([]int64{}, callers...), impact...)),
		}},
	}

	got := Reproduce(FullRunReport{Repo: FullRepoRun{QueryLatency: series}},
		map[string]RawSampleSet{RawSeriesQuery: NewRawQuerySet("grpc-go", completeEnvironment(), rawOps, rawPools)},
		completeEnvironment())

	if got.Status != StatusPass {
		t.Fatalf("status = %s (%v), want PASS", got.Status, got.Discrepancies)
	}
	for _, metric := range []string{
		"query_latency.operation.callers.p95_us",
		"query_latency.operation.impact.p50_us",
		"query_latency.class.structural.p95_us",
		"query_latency.pool.caller_callee_impact_p95.p95_us",
	} {
		if !hasMetric(got, metric) {
			t.Errorf("metric %s was never checked", metric)
		}
	}
	for _, m := range got.Metrics {
		if strings.Contains(m.Metric, ".index.") {
			t.Errorf("the lifecycle operation produced a metric: %s", m.Metric)
		}
	}
}

// SW-126 and SW-127: the freshness and stall statistics are reproduced from
// their raw samples too, so the aggregator covers all four harnesses rather
// than the one it was easiest to wire.
func TestAggregate_ReproducesFreshnessAndStalls(t *testing.T) {
	changes := []ChangeSample{
		{Step: 1, Class: "add", UpdateUS: 100, UpdateMeasured: true, FreshnessUS: 150, FreshnessMeasured: true},
		{Step: 2, Class: "modify", UpdateUS: 200, UpdateMeasured: true, FreshnessUS: 250, FreshnessMeasured: true},
		// A change whose update completed but which never converged: it has an
		// update measurement and no freshness one, and the two distributions
		// must differ by exactly this row.
		{Step: 3, Class: "delete", UpdateUS: 300, UpdateMeasured: true},
	}
	recomputed := RecomputeIncremental(changes)
	incremental := &IncrementalSeries{
		Repo:      "grpc-go",
		Completed: 3,
		Changes:   changes,
		Update:    recomputed.Update,
		Freshness: recomputed.Freshness,
		PerClass: []ChangeClassLatency{
			recomputed.Classes["add"], recomputed.Classes["modify"], recomputed.Classes["delete"],
		},
	}

	in := []StallInterval{{Seq: 1, Phase: "parse", US: 500}, {Seq: 2, Phase: "parse", US: 900}, {Seq: 3, Phase: "link", US: 100}}
	stalls := &StallSeries{
		Repo:       "grpc-go",
		Events:     4,
		Observable: true,
		Intervals:  in,
		Stalls:     RecomputeStalls(in).Stalls,
		PerPhase:   PhaseStallsOf(in),
	}

	got := Reproduce(
		FullRunReport{Repo: FullRepoRun{Incremental: incremental, Stalls: stalls}},
		map[string]RawSampleSet{
			RawSeriesIncremental: NewRawIncrementalSet("grpc-go", completeEnvironment(), changes),
			RawSeriesStalls:      NewRawStallSet("grpc-go", completeEnvironment(), in),
		},
		completeEnvironment())

	if got.Status != StatusPass {
		t.Fatalf("status = %s (%v), want PASS", got.Status, got.Discrepancies)
	}
	for _, metric := range []string{
		"incremental.update.p95_us",
		"incremental.freshness.p50_us",
		"incremental.class.delete.update.n",
		"progress_stalls.pooled.max_us",
		"progress_stalls.phase.link.n",
	} {
		if !hasMetric(got, metric) {
			t.Errorf("metric %s was never checked", metric)
		}
	}
	// The two pooled counts differ by the unconverged change — proof the
	// aggregator read the measured flags rather than the row count.
	update := metricOf(got, "incremental.update.n")
	fresh := metricOf(got, "incremental.freshness.n")
	if update.Recomputed != 3 || fresh.Recomputed != 2 {
		t.Fatalf("update n = %v, freshness n = %v, want 3 and 2", update.Recomputed, fresh.Recomputed)
	}
}

// SW-127's silent index is the case the Collected flag exists for: the series
// RAN and produced no intervals. That must reproduce cleanly as a zero-sample
// distribution — the FAIL belongs to the stall gate, not to the aggregator —
// and it must NOT be reported as missing raw data.
func TestAggregate_ASilentIndexIsReproducedNotReportedMissing(t *testing.T) {
	stalls := &StallSeries{
		Repo:          "grpc-go",
		Events:        0,
		Observable:    false,
		SilenceReason: StallSilenceNote,
		Intervals:     []StallInterval{},
		Stalls:        RecomputeStalls(nil).Stalls,
	}
	got := Reproduce(FullRunReport{Repo: FullRepoRun{Stalls: stalls}},
		map[string]RawSampleSet{RawSeriesStalls: NewRawStallSet("grpc-go", completeEnvironment(), []StallInterval{})},
		completeEnvironment())

	if got.Discrepant != 0 {
		t.Fatalf("discrepant = %d, want 0 — a silent run's empty distribution is reproducible", got.Discrepant)
	}
	for _, series := range got.MissingSeries {
		if series == RawSeriesStalls {
			t.Fatal("a collected-but-empty series was reported as missing raw data")
		}
	}
	check := metricOf(got, "progress_stalls.pooled.n")
	if check.Status != StatusPass || check.Recomputed != 0 {
		t.Fatalf("pooled n = %+v, want a reproduced 0", check)
	}
}

// The aggregator states its own arithmetic and its own versions in the
// artifact, so a reader with only aggregate.json can tell which methodology
// produced it (AC-7).
func TestAggregate_StampsItsVersions(t *testing.T) {
	runs, series := fixedColdSeries()
	got := Reproduce(FullRunReport{ColdSeries: series},
		map[string]RawSampleSet{RawSeriesCold: NewRawColdSet("grpc-go", completeEnvironment(), runs)},
		completeEnvironment())

	if got.FormatVersion != RawFormatVersion {
		t.Errorf("format_version = %d, want %d", got.FormatVersion, RawFormatVersion)
	}
	if got.HarnessVersion != HarnessVersion || got.ScorerVersion != ScorerVersion {
		t.Errorf("versions = %s/%s, want %s/%s", got.HarnessVersion, got.ScorerVersion, HarnessVersion, ScorerVersion)
	}
	if got.Method == "" {
		t.Error("the aggregate does not state its own method")
	}
}

func statsPtr(s LatencyStats) *LatencyStats { return &s }

func hasMetric(r AggregateReport, name string) bool {
	for _, m := range r.Metrics {
		if m.Metric == name {
			return true
		}
	}
	return false
}

func metricOf(r AggregateReport, name string) MetricCheck {
	for _, m := range r.Metrics {
		if m.Metric == name {
			return m
		}
	}
	return MetricCheck{}
}
