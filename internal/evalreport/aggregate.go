package evalreport

// SW-128 (P0-C5): the aggregator that reproduces every published number.
//
// PRD §8.1 is "measured, not asserted". This file is that principle expressed
// as arithmetic: it takes the PUBLISHED report and the RAW samples, recomputes
// every statistic the report claims, and compares. A published number that does
// not follow from its samples is an ERROR (AC-2) — not a warning, not a rounding
// note. A published number with no samples behind it is UNKNOWN and blocks
// publication (AC-5).
//
// Three rules make the check real rather than decorative:
//
//   - The comparison is EXACT. Every percentile in this tree is a nearest-rank
//     observed sample, never an interpolation, so two correct derivations over
//     the same samples agree bit for bit. A tolerance would be a place for drift
//     to live.
//   - The recomputation goes through the SAME exported functions the harnesses
//     used to produce the numbers (RecomputeColdAggregates, RecomputeQueryLatency,
//     RecomputeIncremental, RecomputeStalls). A second implementation would
//     eventually disagree with the first about something that is not a defect.
//     What this check catches is a report that stopped matching its samples —
//     a hand-edited artifact, a partial refactor, two runs' files in one
//     directory — which is exactly the failure FR-9 is about.
//   - UNKNOWN is not FAIL and neither is a PASS. "The number is wrong" and "we
//     never measured it" are different facts and stay different, and only the
//     conjunction of neither being present makes a run publishable.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MetricCheck is ONE published statistic checked against the raw data.
type MetricCheck struct {
	Series string `json:"series"`
	// Metric is the fully-qualified name, e.g.
	// `cold_index.index_wallclock_ms.p50` or `query_latency.class.search.p95_us`.
	// It is stable across runs so two aggregates diff line by line.
	Metric string `json:"metric"`
	Unit   string `json:"unit,omitempty"`

	Published    float64 `json:"published"`
	Recomputed   float64 `json:"recomputed"`
	HasPublished bool    `json:"has_published"`
	// HasRaw is whether raw samples for this metric existed at all. It is what
	// separates a reproduced zero from a number nobody can check.
	HasRaw bool   `json:"has_raw"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// SeriesCoverage is one harness's contribution to the run: was it published,
// was its raw data present, and how did its metrics read.
type SeriesCoverage struct {
	Series string `json:"series"`
	// Published is whether the report carries this series at all. A run that
	// measured no freshness is not a run with wrong freshness numbers.
	Published bool `json:"published"`
	// RawPresent is whether a raw file for it was supplied; RawCollected is
	// whether that file says the harness actually ran (a collected file with
	// zero samples is a measurement, an absent one is not).
	RawPresent   bool   `json:"raw_present"`
	RawCollected bool   `json:"raw_collected"`
	RawSamples   int    `json:"raw_samples"`
	Metrics      int    `json:"metrics"`
	Reproduced   int    `json:"reproduced"`
	Discrepant   int    `json:"discrepant"`
	Unknown      int    `json:"unknown"`
	Status       string `json:"status"`
}

// AggregateReport is the whole reproduction: what was checked, what agreed,
// what could not be checked, and whether the run may be published at all.
type AggregateReport struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	ScorerVersion  string `json:"scorer_version"`

	Repo        string `json:"repo,omitempty"`
	RunnerClass string `json:"runner_class,omitempty"`

	Environment RunEnvironment `json:"environment"`
	// EnvironmentComplete and MissingEnvironment are AC-3 read as a gate: a
	// number whose machine is undocumented is not a baseline, so an incomplete
	// environment blocks publication even when every metric reproduces.
	EnvironmentComplete bool     `json:"environment_complete"`
	MissingEnvironment  []string `json:"missing_environment,omitempty"`

	Series  []SeriesCoverage `json:"series"`
	Metrics []MetricCheck    `json:"metrics"`

	Checked    int `json:"metrics_checked"`
	Reproduced int `json:"metrics_reproduced"`
	Discrepant int `json:"metrics_discrepant"`
	Unknown    int `json:"metrics_unknown"`

	// Discrepancies names every metric that contradicted its samples, in
	// report order, so the summary and the exit message need no second pass.
	Discrepancies []string `json:"discrepancies,omitempty"`
	// MissingSeries names the published series that had no raw data (AC-5).
	MissingSeries []string `json:"missing_raw_series,omitempty"`

	// Complete is "every published metric had raw data behind it". Publishable
	// additionally requires a complete environment and zero discrepancies — it
	// is the one boolean that answers "may this run be published".
	Complete    bool   `json:"complete"`
	Publishable bool   `json:"publishable"`
	Status      string `json:"status"`
	Method      string `json:"method"`
	Notes       string `json:"notes,omitempty"`
}

// AggregateMethod states the arithmetic inline, so the artifact explains its
// own check.
const AggregateMethod = "Every statistic in `report` is recomputed from `raw/` through the same exported derivations the harnesses used " +
	"to produce it (RecomputeColdAggregates, RecomputeQueryLatency, RecomputeIncremental, RecomputeStalls) and compared " +
	"EXACTLY. Exactly, not within a tolerance: every percentile in this tree is a nearest-rank OBSERVED sample rather than " +
	"an interpolation, so two correct derivations over the same samples agree bit for bit and a tolerance would only be " +
	"somewhere for drift to hide. A metric whose raw samples are absent reads UNKNOWN and is never counted as reproduced."

// AggregateNotes explains the artifact to a reader who has only the JSON.
const AggregateNotes = "SW-128 aggregate reproduction: the published report checked against the raw measurements it claims to be derived " +
	"from. `publishable` is the conjunction a P0 baseline needs — every published metric reproduced from raw data AND the " +
	"run environment fully captured. A DISCREPANCY (a published number that does not follow from its samples) is a " +
	"failure; UNKNOWN (a number nobody can check, or a machine nobody documented) is not a failure and is not a pass " +
	"either, per PRD §8.2. The two are counted separately and never merged."

// Reproduce recomputes every metric the report publishes from the raw sample
// sets and reports the comparison.
//
// env is the run's environment record; it is a parameter rather than read off
// the raw sets because the run directory's environment.json is the authority
// for the run as a whole, while each raw file carries a copy for the case where
// it is read alone.
func Reproduce(report FullRunReport, sets map[string]RawSampleSet, env RunEnvironment) AggregateReport {
	out := AggregateReport{
		FormatVersion:  RawFormatVersion,
		HarnessVersion: HarnessVersion,
		ScorerVersion:  ScorerVersion,
		RunnerClass:    report.RunnerClass,
		Repo:           report.Repo.Name,
		Environment:    env,
		Method:         AggregateMethod,
		Notes:          AggregateNotes,
	}
	out.MissingEnvironment = env.Missing()
	out.EnvironmentComplete = len(out.MissingEnvironment) == 0

	a := &aggregator{sets: sets}
	a.coldIndex(report.ColdSeries)
	a.queryLatency(report.Repo.QueryLatency)
	a.incremental(report.Repo.Incremental)
	a.stalls(report.Repo.Stalls)

	out.Metrics = a.metrics
	out.Series = a.coverage()
	for _, m := range a.metrics {
		out.Checked++
		switch m.Status {
		case StatusPass:
			out.Reproduced++
		case StatusFail:
			out.Discrepant++
			out.Discrepancies = append(out.Discrepancies, fmt.Sprintf(
				"%s: the report publishes %s but the raw samples give %s", m.Metric, trimFloat(m.Published), trimFloat(m.Recomputed)))
		default:
			out.Unknown++
		}
	}
	for _, c := range out.Series {
		if c.Published && !c.RawPresent {
			out.MissingSeries = append(out.MissingSeries, c.Series)
		}
	}

	// Complete is about the METRICS: every published number had samples behind
	// it. It is deliberately false for a report that published nothing at all —
	// zero checks passing zero comparisons is the most seductive false green
	// there is.
	out.Complete = out.Checked > 0 && out.Unknown == 0
	out.Publishable = out.Complete && out.Discrepant == 0 && out.EnvironmentComplete
	switch {
	case out.Discrepant > 0:
		out.Status = StatusFail
	case out.Publishable:
		out.Status = StatusPass
	default:
		out.Status = StatusUnknown
	}
	return out
}

// aggregator accumulates the per-metric checks and the per-series coverage.
type aggregator struct {
	sets    map[string]RawSampleSet
	metrics []MetricCheck
	series  []SeriesCoverage
}

// begin opens one series. published says the report carries it; the returned
// set is the raw data, and ok is false when there is none — in which case every
// metric of the series is recorded UNKNOWN rather than skipped, because a
// metric that vanishes from the table is a metric nobody notices is missing.
func (a *aggregator) begin(series string, published bool) (RawSampleSet, bool) {
	set, ok := a.sets[series]
	a.series = append(a.series, SeriesCoverage{
		Series:       series,
		Published:    published,
		RawPresent:   ok,
		RawCollected: ok && set.Collected,
		RawSamples:   set.Samples,
	})
	return set, ok
}

// record adds one metric check. hasRaw false makes it UNKNOWN whatever the
// values are — the recomputed side is meaningless without samples.
func (a *aggregator) record(series, metric, unit string, published, recomputed float64, hasRaw bool) {
	check := MetricCheck{
		Series:       series,
		Metric:       metric,
		Unit:         unit,
		Published:    published,
		Recomputed:   recomputed,
		HasPublished: true,
		HasRaw:       hasRaw,
	}
	switch {
	case !hasRaw:
		check.Status = StatusUnknown
		check.Recomputed = 0
		check.Reason = "no raw samples for this metric: it cannot be reproduced, so it is UNKNOWN rather than accepted"
	case published == recomputed:
		check.Status = StatusPass
	default:
		check.Status = StatusFail
		check.Reason = "the published value does not follow from the raw samples"
	}
	a.metrics = append(a.metrics, check)
}

// recordStats expands one LatencyStats into its five published numbers. They
// are checked individually because they fail individually: a p95 can drift
// while the p50, the min and the max still agree.
func (a *aggregator) recordStats(series, prefix string, published, recomputed LatencyStats, hasRaw bool) {
	a.record(series, prefix+".n", "samples", float64(published.N), float64(recomputed.N), hasRaw)
	a.record(series, prefix+".min_us", "us", float64(published.MinUS), float64(recomputed.MinUS), hasRaw)
	a.record(series, prefix+".p50_us", "us", float64(published.P50US), float64(recomputed.P50US), hasRaw)
	a.record(series, prefix+".p95_us", "us", float64(published.P95US), float64(recomputed.P95US), hasRaw)
	a.record(series, prefix+".max_us", "us", float64(published.MaxUS), float64(recomputed.MaxUS), hasRaw)
}

// coldIndex reproduces SW-124's aggregates and its run accounting.
func (a *aggregator) coldIndex(series *ColdRunSeries) {
	set, ok := a.begin(RawSeriesCold, series != nil)
	if series == nil {
		return
	}
	var recomputed map[string]Aggregate
	if ok {
		recomputed = RecomputeColdAggregates(set.ColdRuns)
	}

	// Published metric names are walked in sorted order so two aggregates of
	// the same run are byte-comparable.
	names := make([]string, 0, len(series.Aggregates))
	for name := range series.Aggregates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		published := series.Aggregates[name]
		got := recomputed[name]
		// A metric the report publishes but the recomputation does not produce
		// has no samples behind it, whatever the rest of the file contains.
		_, produced := recomputed[name]
		has := ok && produced
		prefix := RawSeriesCold + "." + name
		a.record(RawSeriesCold, prefix+".n", "samples", float64(published.N), float64(got.N), has)
		a.record(RawSeriesCold, prefix+".min", published.Unit, published.Min, got.Min, has)
		a.record(RawSeriesCold, prefix+".p50", published.Unit, published.P50, got.P50, has)
		a.record(RawSeriesCold, prefix+".p95", published.Unit, published.P95, got.P95, has)
		a.record(RawSeriesCold, prefix+".max", published.Unit, published.Max, got.Max, has)
	}

	// The run accounting is published too, and it is the number that says
	// whether the distribution above is the one FR-8 asked for.
	var completed, aborted int
	for _, r := range set.ColdRuns {
		if r.Status == ColdRunCompleted {
			completed++
		} else {
			aborted++
		}
	}
	a.record(RawSeriesCold, RawSeriesCold+".runs_completed", "runs", float64(series.RunsCompleted), float64(completed), ok)
	a.record(RawSeriesCold, RawSeriesCold+".runs_aborted", "runs", float64(series.RunsAborted), float64(aborted), ok)
}

// queryLatency reproduces SW-125's per-operation, per-class and per-gate-pool
// statistics.
func (a *aggregator) queryLatency(series *QueryLatencySeries) {
	set, ok := a.begin(RawSeriesQuery, series != nil)
	if series == nil {
		return
	}
	// The recomputation is driven from the RAW file's operations and
	// membership lists, never from the published series — reading membership
	// off the report would let a report with the wrong pool agree with itself.
	var recomputed QueryLatencyRecomputation
	if ok {
		recomputed = RecomputeQueryLatency(rawQuerySeries(set))
	}

	for _, op := range series.Operations {
		// The lifecycle operation publishes no distribution. It is a recorded
		// decision, not a gap, so it produces neither a metric nor an UNKNOWN.
		if op.Latency == nil {
			continue
		}
		got, produced := recomputed.Operations[op.Operation]
		a.recordStats(RawSeriesQuery, RawSeriesQuery+".operation."+op.Operation, *op.Latency, got, ok && produced)
	}
	for _, c := range series.Classes {
		got, produced := recomputed.Classes[c.Class]
		a.recordStats(RawSeriesQuery, RawSeriesQuery+".class."+c.Class, c.LatencyStats, got, ok && produced)
		a.record(RawSeriesQuery, RawSeriesQuery+".class."+c.Class+".executions", "executions",
			float64(c.Executions), float64(got.N), ok && produced)
	}
	for _, p := range series.Pools {
		got, produced := recomputed.Pools[p.GateID]
		a.recordStats(RawSeriesQuery, RawSeriesQuery+".pool."+p.GateID, p.LatencyStats, got, ok && produced)
		a.record(RawSeriesQuery, RawSeriesQuery+".pool."+p.GateID+".executions", "executions",
			float64(p.Executions), float64(got.N), ok && produced)
	}
}

// rawQuerySeries rebuilds the minimal QueryLatencySeries shape
// RecomputeQueryLatency reads — per-operation samples plus class and pool
// membership — from the raw file alone.
func rawQuerySeries(set RawSampleSet) QueryLatencySeries {
	s := QueryLatencySeries{}
	for _, op := range set.QueryOperations {
		s.Operations = append(s.Operations, QueryOpLatency{
			Operation: op.Operation,
			Class:     op.Class,
			SamplesUS: op.SamplesUS,
		})
	}
	for _, pool := range set.QueryPools {
		switch pool.Kind {
		case RawPoolClass:
			s.Classes = append(s.Classes, QueryClassLatency{Class: pool.ID, Operations: pool.Operations})
		case RawPoolGate:
			s.Pools = append(s.Pools, QueryPoolLatency{GateID: pool.ID, Operations: pool.Operations})
		}
	}
	return s
}

// incremental reproduces SW-126's pooled and per-class distributions.
func (a *aggregator) incremental(series *IncrementalSeries) {
	set, ok := a.begin(RawSeriesIncremental, series != nil)
	if series == nil {
		return
	}
	var recomputed IncrementalRecomputation
	if ok {
		recomputed = RecomputeIncremental(set.Changes)
	}
	a.recordStats(RawSeriesIncremental, RawSeriesIncremental+".update", series.Update, recomputed.Update, ok)
	a.recordStats(RawSeriesIncremental, RawSeriesIncremental+".freshness", series.Freshness, recomputed.Freshness, ok)
	for _, c := range series.PerClass {
		got, produced := recomputed.Classes[c.Class]
		has := ok && produced
		prefix := RawSeriesIncremental + ".class." + c.Class
		a.record(RawSeriesIncremental, prefix+".changes", "changes", float64(c.Changes), float64(got.Changes), has)
		a.recordStats(RawSeriesIncremental, prefix+".update", c.Update, got.Update, has)
		a.recordStats(RawSeriesIncremental, prefix+".freshness", c.Freshness, got.Freshness, has)
	}
}

// stalls reproduces SW-127's pooled and per-phase distributions.
//
// A COLLECTED set with no intervals is a real measurement — the index ran and
// stayed silent — so it recomputes to a zero-sample distribution and passes.
// The failure that case deserves belongs to the stall gate, which already
// issues it; the aggregator's job is to confirm the report says what the raw
// data says, and here it does.
func (a *aggregator) stalls(series *StallSeries) {
	set, ok := a.begin(RawSeriesStalls, series != nil)
	if series == nil {
		return
	}
	var recomputed StallRecomputation
	if ok {
		recomputed = RecomputeStalls(set.Intervals)
	}
	a.recordStats(RawSeriesStalls, RawSeriesStalls+".pooled", series.Stalls, recomputed.Stalls, ok)
	for _, phase := range series.PerPhase {
		got, produced := recomputed.Phases[phase.Phase]
		a.recordStats(RawSeriesStalls, RawSeriesStalls+".phase."+phase.Phase, phase.Stats, got, ok && produced)
	}
}

// coverage folds the per-metric outcomes back into the per-series rows.
func (a *aggregator) coverage() []SeriesCoverage {
	byName := map[string]*SeriesCoverage{}
	for i := range a.series {
		byName[a.series[i].Series] = &a.series[i]
	}
	for _, m := range a.metrics {
		c, ok := byName[m.Series]
		if !ok {
			continue
		}
		c.Metrics++
		switch m.Status {
		case StatusPass:
			c.Reproduced++
		case StatusFail:
			c.Discrepant++
		default:
			c.Unknown++
		}
	}
	for _, c := range byName {
		switch {
		case !c.Published:
			c.Status = StatusUnknown
		case c.Discrepant > 0:
			c.Status = StatusFail
		case c.Unknown > 0 || c.Metrics == 0:
			c.Status = StatusUnknown
		default:
			c.Status = StatusPass
		}
	}
	return a.series
}

// trimFloat renders a value the way a reader wants to see it: integral figures
// without a decimal tail, real ratios with one.
func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return strings.TrimSuffix(s, ".0")
}

// WriteAggregateJSON writes the reproduction as stable, indented JSON. It goes
// through the same writer the raw files use, so the aggregate is byte-comparable
// between two runs of the same directory.
func WriteAggregateJSON(r AggregateReport, path string) error {
	_, err := writeJSONFile(path, r)
	return err
}
