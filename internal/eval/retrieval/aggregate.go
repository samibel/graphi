package retrieval

// `-aggregate <dir>`: reproduce every published number from the raw data and
// say so with an exit code nobody has to interpret. The discipline is
// cmd/eval's (SW-128): a published number that does not follow from its
// samples is a DISCREPANCY, a number nobody can check is UNKNOWN, and only a
// run with neither, whose environment is documented, is publishable.
//
//	0  every published metric reproduced, environment captured — publishable
//	1  DISCREPANCY: a published number does not follow from its raw samples
//	2  usage, I/O, or an artifact this build cannot read
//	3  INCOMPLETE: nothing contradicted, but something is unmeasured or
//	   undocumented, so the aggregate must not be published

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Aggregator exit codes.
const (
	ExitReproduced  = 0
	ExitDiscrepancy = 1
	ExitUsage       = 2
	ExitIncomplete  = 3
)

// MetricCheck is one published statistic checked against the raw data.
type MetricCheck struct {
	Baseline Baseline `json:"baseline"`
	Metric   string   `json:"metric"`
	// Published and Recomputed are rendered as strings so a per-query hit
	// list and a float are checked with the same shape; the comparison itself
	// is exact on the typed values.
	Published  string `json:"published"`
	Recomputed string `json:"recomputed,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// Check statuses.
const (
	CheckReproduced = "reproduced"
	CheckDiscrepant = "DISCREPANCY"
	CheckUnknown    = "UNKNOWN"
)

// AggregateReport is the reproduction: what was checked, what agreed, what
// could not be checked, and whether the run may be published.
type AggregateReport struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	ScorerVersion  string `json:"scorer_version"`

	Repo        string `json:"repo"`
	RunnerClass string `json:"runner_class"`

	Environment         Environment `json:"environment"`
	EnvironmentComplete bool        `json:"environment_complete"`
	MissingEnvironment  []string    `json:"missing_environment,omitempty"`

	Metrics []MetricCheck `json:"metrics"`

	Checked    int `json:"metrics_checked"`
	Reproduced int `json:"metrics_reproduced"`
	Discrepant int `json:"metrics_discrepant"`
	Unknown    int `json:"metrics_unknown"`

	Discrepancies []string `json:"discrepancies,omitempty"`
	MissingRaw    []string `json:"missing_raw,omitempty"`

	Complete    bool   `json:"complete"`
	Publishable bool   `json:"publishable"`
	Status      string `json:"status"`
	Method      string `json:"method"`
}

// AggregateMethod states the arithmetic inline.
const AggregateMethod = "Closed world first: report.reproducible.dataset (id, sha256, evidence class, query counts, sorted query ids) is compared " +
	"EXACTLY with the same citation rebuilt from dataset.json, whose sha256 is recomputed from its bytes (never copied from run.json); " +
	"the baseline universe is the harness constant (lexical, hybrid_v1, semantic_name_only, oracle_upper_bound) and the report's " +
	"baseline list, its performance blocks, and the raw hits and raw latency series the run index lists must each equal it exactly. " +
	"So a query removed coherently from dataset.json, the report and every raw series is caught by the dataset citation the report " +
	"still carries; a tamperer who also rewrites that citation has produced a different report, and the sha256 that " +
	"docs/eval/retrieval-targets.json and -budgets.json record in derived_from no longer matches it - that provenance layer, not " +
	"this aggregate, is what binds a checked-in artifact to the report it came from. " +
	"For every published baseline the query-id set is compared for EXACT equality with dataset.json and, per raw series, " +
	"with raw/hits-<baseline>.json and raw/latency-<baseline>.json: an omitted or extra query on any side is a discrepancy. Every " +
	"per-query metric in report.reproducible is recomputed from the raw hits and dataset.json through the same Evaluate the run used; " +
	"every aggregate through the same Aggregate over the RAW hit set in dataset order (not over the report's query list); every " +
	"performance block through the same PerformanceFromRaw (nearest-rank PercentileInt64 over the timed executions; index_ms, " +
	"peak_rss_mb and vector_sidecar_bytes from the record's own measures), each compared EXACTLY, status and reason included. The " +
	"hit lists in the report are compared to the raw hit lists as well, so a report cannot be scored against samples it did not " +
	"publish. A baseline may read `unavailable` only when BOTH its raw records say collected: false; then the report's reason, the " +
	"hit record's reason and the latency record's reason must agree, both records must carry zero samples and an empty query set, " +
	"and the report's empty query list, UNKNOWN aggregates and UNKNOWN measures are all checked against that; a collected record " +
	"makes any other status a discrepancy. The environment block is checked for presence only. A metric whose raw samples are " +
	"absent reads UNKNOWN and is never counted as reproduced."

// Reproduce recomputes every published statistic in a run directory.
func Reproduce(run *RunDir) AggregateReport {
	rep := run.Report.Reproducible
	r := &reproducer{
		run: run,
		out: AggregateReport{
			FormatVersion:  FormatVersion,
			HarnessVersion: HarnessVersion,
			ScorerVersion:  ScorerVersion,
			Repo:           rep.Repo.Name,
			RunnerClass:    rep.RunnerClass,
			Environment:    run.Report.Environment,
			MissingRaw:     append([]string(nil), run.MissingRaw...),
			Method:         AggregateMethod,
		},
		byID:     map[string]Query{},
		minGrade: run.Dataset.Dataset.MinGrade(),
		budgets:  rep.TokenBudgets,
		perf:     map[Baseline]BaselinePerformance{},
	}
	r.out.MissingEnvironment = run.Report.Environment.Missing()
	r.out.EnvironmentComplete = len(r.out.MissingEnvironment) == 0
	for _, q := range run.Dataset.Dataset.Queries {
		r.byID[q.ID] = q
		r.datasetOrder = append(r.datasetOrder, q.ID)
	}
	r.datasetIDs = sortedIDs(r.datasetOrder)
	for _, p := range run.Report.Performance {
		r.perf[p.Baseline] = p
	}

	r.checkRun()
	for _, b := range rep.Baselines {
		r.checkBaseline(b)
	}

	out := r.out
	out.Complete = out.Unknown == 0 && len(out.MissingRaw) == 0
	out.Publishable = out.Complete && out.Discrepant == 0 && out.EnvironmentComplete
	switch {
	case out.Discrepant > 0:
		out.Status = CheckDiscrepant
	case !out.Publishable:
		out.Status = "INCOMPLETE"
	default:
		out.Status = "PASS"
	}
	sort.Strings(out.MissingRaw)
	return out
}

// ExitCode maps an aggregate to the four-verdict exit code.
func (a AggregateReport) ExitCode() int {
	switch {
	case a.Discrepant > 0:
		return ExitDiscrepancy
	case !a.Publishable:
		return ExitIncomplete
	}
	return ExitReproduced
}

// reproducer is one reproduction in progress: the run directory, the dataset
// index and the checks recorded so far.
type reproducer struct {
	run *RunDir
	out AggregateReport

	byID map[string]Query
	// datasetOrder is the dataset's query order, which the aggregate sums
	// follow so the floating-point result is reproduced bit for bit;
	// datasetIDs is the same set sorted, for set equality.
	datasetOrder []string
	datasetIDs   []string
	minGrade     int
	budgets      []int
	perf         map[Baseline]BaselinePerformance
}

func (r *reproducer) add(c MetricCheck) {
	r.out.Metrics = append(r.out.Metrics, c)
	r.out.Checked++
	switch c.Status {
	case CheckReproduced:
		r.out.Reproduced++
	case CheckDiscrepant:
		r.out.Discrepant++
		r.out.Discrepancies = append(r.out.Discrepancies, fmt.Sprintf("%s %s: published %s, recomputed %s", c.Baseline, c.Metric, c.Published, c.Recomputed))
	default:
		r.out.Unknown++
	}
}

// exact records a check that passes only when published and recomputed are
// deeply equal — no tolerance, no coercion.
func (r *reproducer) exact(b Baseline, metric string, published, recomputed any) {
	c := MetricCheck{Baseline: b, Metric: metric, Published: render(published), Recomputed: render(recomputed), Status: CheckReproduced}
	if !reflect.DeepEqual(published, recomputed) {
		c.Status = CheckDiscrepant
	}
	r.add(c)
}

func (r *reproducer) unknown(b Baseline, metric string, published any, reason string) {
	r.add(MetricCheck{Baseline: b, Metric: metric, Published: render(published), Status: CheckUnknown, Reason: reason})
}

// runLevel labels the checks that belong to the run as a whole rather than
// to one baseline.
const runLevel Baseline = "run"

// checkRun is the closed-world check: the report's dataset citation must be
// the run directory's dataset, and the baseline universe on every side must
// be the harness constant — not whatever the report happens to list.
func (r *reproducer) checkRun() {
	published := r.run.Report.Reproducible.Dataset
	// Rebuilt from the dataset copy; its SHA256 was recomputed from the bytes
	// by LoadDataset. File is carried from the report, not compared.
	r.exact(runLevel, "dataset", normalizeDatasetRef(published), normalizeDatasetRef(DatasetRefOf(r.run.Dataset, published.File)))

	universe := baselineSet(AllBaselines)
	var results, blocks []Baseline
	for _, b := range r.run.Report.Reproducible.Baselines {
		results = append(results, b.Name)
	}
	for _, p := range r.run.Report.Performance {
		blocks = append(blocks, p.Baseline)
	}
	r.exact(runLevel, "baseline_set", baselineSet(results), universe)
	r.exact(runLevel, "performance_set", baselineSet(blocks), universe)
	// The raw side is what the run index LISTS: a listed file that is absent
	// is INCOMPLETE (MissingRaw), a series the index never had is a
	// discrepancy.
	for _, series := range []string{RawSeriesHits, RawSeriesLatency} {
		var listed []Baseline
		for _, ref := range r.run.Index.Raw {
			if ref.Series == series {
				listed = append(listed, ref.Baseline)
			}
		}
		r.exact(runLevel, "raw."+series+".series_set", baselineSet(listed), universe)
	}
}

// checkBaseline checks one published baseline. The raw records decide what
// status it may carry: collected records make it ok, uncollected ones
// unavailable, and a report that says otherwise is a discrepancy. The rest
// of the shape follows from the status the raw side allows.
func (r *reproducer) checkBaseline(b BaselineResult) {
	hits, haveHits := r.run.Hits[b.Name]
	lat, haveLat := r.run.Latency[b.Name]
	switch {
	case haveHits && haveLat:
		r.exact(b.Name, "status", b.Status, rawStatus(hits.Collected, lat.Collected))
	case haveHits:
		r.exact(b.Name, "status", b.Status, rawStatus(hits.Collected, hits.Collected))
	case haveLat:
		r.exact(b.Name, "status", b.Status, rawStatus(lat.Collected, lat.Collected))
	default:
		r.unknown(b.Name, "status", b.Status, "no raw file for this baseline")
	}
	if b.Status == BaselineStatusOK {
		r.checkOK(b, hits, haveHits, lat, haveLat)
	} else {
		r.checkUnavailable(b, hits, haveHits, lat, haveLat)
	}
	r.checkPerformance(b.Name, lat, haveLat)
}

// rawStatus is the only status the raw records permit.
func rawStatus(hitsCollected, latencyCollected bool) string {
	switch {
	case hitsCollected && latencyCollected:
		return BaselineStatusOK
	case !hitsCollected && !latencyCollected:
		return BaselineStatusUnavailable
	}
	return "inconsistent raw records (hits collected: " + strconv.FormatBool(hitsCollected) +
		", latency collected: " + strconv.FormatBool(latencyCollected) + ")"
}

// checkOK checks a baseline that ran: query-set equality on every side, each
// published hit list and score against the raw hits, and the aggregates
// recomputed from the RAW hit set over the dataset's queries — not from the
// report's query list — so a report that omits a query and re-averages the
// rest still disagrees.
func (r *reproducer) checkOK(b BaselineResult, hits RawHitSet, haveHits bool, lat RawLatencySet, haveLat bool) {
	r.exact(b.Name, "query_set", queryIDs(b.Queries), r.datasetIDs)
	if haveHits {
		r.exact(b.Name, "raw.hits.query_set", rawHitIDs(hits.Queries), r.datasetIDs)
	}
	if haveLat {
		r.exact(b.Name, "raw.latency.query_set", rawLatencyIDs(lat.Queries), r.datasetIDs)
	}

	rawByID := map[string][]Hit{}
	if haveHits {
		for _, q := range hits.Queries {
			rawByID[q.ID] = q.Hits
		}
	}
	for _, q := range b.Queries {
		if !haveHits {
			r.unknown(b.Name, "query."+q.ID+".hits", len(q.Hits), "no raw hit file for this baseline")
			continue
		}
		raw, ok := rawByID[q.ID]
		if !ok {
			r.unknown(b.Name, "query."+q.ID+".hits", len(q.Hits), "query absent from the raw hit file")
			continue
		}
		r.exact(b.Name, "query."+q.ID+".hits", normalizeHits(q.Hits), normalizeHits(raw))
		dq, ok := r.byID[q.ID]
		if !ok {
			r.unknown(b.Name, "query."+q.ID+".metrics", q.Metrics, "query absent from dataset.json")
			continue
		}
		r.exact(b.Name, "query."+q.ID+".metrics", normalizeMetrics(q.Metrics), normalizeMetrics(Evaluate(raw, dq, r.minGrade, r.budgets)))
	}

	if !haveHits {
		r.unknown(b.Name, "overall", b.Overall.Status, "no raw hit file for this baseline")
		return
	}
	recomputed := make([]QueryResult, 0, len(r.datasetOrder))
	for _, id := range r.datasetOrder {
		raw, ok := rawByID[id]
		if !ok {
			r.unknown(b.Name, "overall", b.Overall.Status, "aggregates cannot be recomputed without every dataset query's raw hits ("+id+" is absent)")
			return
		}
		dq := r.byID[id]
		recomputed = append(recomputed, QueryResult{ID: id, Stratum: dq.Stratum, Split: dq.Split, Hits: raw, Metrics: Evaluate(raw, dq, r.minGrade, r.budgets)})
	}
	overall, strata, splits := AggregateAll(recomputed, r.budgets)
	r.checkAggregates(b, overall, strata, splits)
}

// checkUnavailable checks the complete shape of an unavailable baseline
// (AC-6) against BOTH its raw records: the typed reason (report == hit
// record == latency record), no queries and zero samples on every side, and
// UNKNOWN aggregates. Only records that say collected: false can justify the
// status (checkBaseline); everything the report says beyond that must follow
// from the records' reason alone.
func (r *reproducer) checkUnavailable(b BaselineResult, hits RawHitSet, haveHits bool, lat RawLatencySet, haveLat bool) {
	if haveLat {
		r.exact(b.Name, "raw.latency.reason", b.Reason, rawReason(lat.Reason, "latency"))
		r.exact(b.Name, "raw.latency.query_set", rawLatencyIDs(lat.Queries), []string{})
		r.exact(b.Name, "raw.latency.samples", lat.Samples, 0)
	}
	if !haveHits {
		r.unknown(b.Name, "reason", b.Reason, "no raw hit file for this baseline")
		r.unknown(b.Name, "overall", b.Overall.Status, "no raw hit file for this baseline")
		return
	}
	want := unavailableBaseline(b.Name, b.Method, rawReason(hits.Reason, "hit"))
	r.exact(b.Name, "reason", b.Reason, want.Reason)
	r.exact(b.Name, "query_set", queryIDs(b.Queries), []string{})
	r.exact(b.Name, "raw.hits.query_set", rawHitIDs(hits.Queries), []string{})
	r.exact(b.Name, "raw.hits.samples", hits.Samples, 0)
	r.checkAggregates(b, want.Overall, want.Strata, want.Splits)
}

// rawReason is a raw record's reason, or a sentinel no report can match when
// the record carries none.
func rawReason(reason, record string) string {
	if reason == "" {
		return "(the raw " + record + " record carries no reason)"
	}
	return reason
}

func (r *reproducer) checkAggregates(b BaselineResult, overall AggregateMetrics, strata, splits map[string]AggregateMetrics) {
	r.exact(b.Name, "overall", normalizeAgg(b.Overall), normalizeAgg(overall))
	for _, s := range Strata {
		r.exact(b.Name, "strata."+s, normalizeAgg(b.Strata[s]), normalizeAgg(strata[s]))
	}
	for _, s := range []string{SplitDev, SplitHoldout} {
		r.exact(b.Name, "splits."+s, normalizeAgg(b.Splits[s]), normalizeAgg(splits[s]))
	}
}

// performanceMeasures names AC-4's per-baseline measures in report order;
// every one of them is checked, an untaken one against the raw record's
// status and reason rather than against itself.
var performanceMeasures = []struct {
	name string
	get  func(BaselinePerformance) Measure
}{
	{"index_ms", func(p BaselinePerformance) Measure { return p.IndexMS }},
	{"query_p50_us", func(p BaselinePerformance) Measure { return p.QueryP50US }},
	{"query_p95_us", func(p BaselinePerformance) Measure { return p.QueryP95US }},
	{"peak_rss_mb", func(p BaselinePerformance) Measure { return p.PeakRSSMB }},
	{"vector_sidecar_bytes", func(p BaselinePerformance) Measure { return p.VectorSidecarBytes }},
}

// checkPerformance recomputes the performance block through the same
// PerformanceFromRaw the runner published it with and compares every
// measure exactly — status, value, unit and reason.
func (r *reproducer) checkPerformance(name Baseline, lat RawLatencySet, haveLat bool) {
	p, ok := r.perf[name]
	if !ok {
		r.unknown(name, "performance", "absent", "no performance block for this baseline")
		return
	}
	if !haveLat {
		for _, m := range performanceMeasures {
			r.unknown(name, m.name, m.get(p), "no raw latency file for this baseline")
		}
		r.unknown(name, "latency_samples", p.LatencySamples, "no raw latency file for this baseline")
		return
	}
	want := PerformanceFromRaw(name, lat)
	for _, m := range performanceMeasures {
		r.exact(name, m.name, m.get(p), m.get(want))
	}
	r.exact(name, "latency_samples", p.LatencySamples, want.LatencySamples)
}

// baselineSet renders a baseline list as a sorted, non-nil string slice for
// exact set comparison; a duplicate entry stays duplicated and so differs.
func baselineSet(names []Baseline) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, string(n))
	}
	sort.Strings(out)
	return out
}

// normalizeDatasetRef makes a JSON round-tripped citation comparable with a
// rebuilt one: a missing query_ids list reads as empty, never as nil.
func normalizeDatasetRef(d DatasetRef) DatasetRef {
	d.QueryIDs = sortedIDs(d.QueryIDs)
	return d
}

// Query-id sets. Every helper returns a sorted, non-nil slice so two empty
// sets compare equal and order never counts as a difference.
func sortedIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	out = append(out, ids...)
	sort.Strings(out)
	return out
}

func queryIDs(qs []QueryResult) []string {
	ids := make([]string, 0, len(qs))
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	return sortedIDs(ids)
}

func rawHitIDs(qs []RawQueryHits) []string {
	ids := make([]string, 0, len(qs))
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	return sortedIDs(ids)
}

func rawLatencyIDs(qs []RawQueryLatency) []string {
	ids := make([]string, 0, len(qs))
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	return sortedIDs(ids)
}

// normalizeHits strips nothing but makes nil and empty compare equal.
func normalizeHits(h []Hit) []Hit {
	if h == nil {
		return []Hit{}
	}
	return h
}

// normalizeMetrics makes a JSON round-tripped QueryMetrics comparable with a
// freshly computed one: nil vs empty maps/slices, and pointer targets.
func normalizeMetrics(m QueryMetrics) QueryMetrics {
	if m.RecallAtTokens == nil {
		m.RecallAtTokens = map[string]float64{}
	}
	if m.HitGrades == nil {
		m.HitGrades = []int{}
	}
	if m.FirstRelevantRank != nil {
		v := *m.FirstRelevantRank
		m.FirstRelevantRank = &v
	}
	if m.NegativeHitAt5 != nil {
		v := *m.NegativeHitAt5
		m.NegativeHitAt5 = &v
	}
	return m
}

func normalizeAgg(a AggregateMetrics) AggregateMetrics {
	if a.Metrics == nil {
		a.Metrics = map[string]float64{}
	}
	return a
}

// render prints a checked value for the artifact.
func render(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	case []string:
		if len(x) == 0 {
			return "(no queries)"
		}
		return strings.Join(x, ",")
	case Measure:
		if x.Status == StatusMeasured && x.Value != nil {
			return strconv.FormatFloat(*x.Value, 'g', -1, 64) + x.Unit
		}
		if x.Reason != "" {
			return x.Status + " (" + x.Reason + ")"
		}
		return x.Status
	case DatasetRef:
		return fmt.Sprintf("%s sha256=%s evidence=%q queries=%d dev=%d holdout=%d ids=%s",
			x.ID, x.SHA256, x.EvidenceClass, x.Queries, x.Dev, x.Holdout, render(x.QueryIDs))
	case []Hit:
		return fmt.Sprintf("%d hit(s)", len(x))
	case QueryMetrics:
		return fmt.Sprintf("top1=%g r5=%g r10=%g mrr=%g ndcg=%g", x.Top1, x.Recall5, x.Recall10, x.MRR10, x.NDCG10)
	case AggregateMetrics:
		keys := make([]string, 0, len(x.Metrics))
		for k := range x.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		s := fmt.Sprintf("%s queries=%d scored=%d", x.Status, x.Queries, x.Scored)
		for _, k := range keys {
			s += fmt.Sprintf(" %s=%g", k, x.Metrics[k])
		}
		return s
	}
	return fmt.Sprint(v)
}
