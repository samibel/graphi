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
const AggregateMethod = "Every per-query metric in report.reproducible is recomputed from raw/hits-<baseline>.json and dataset.json " +
	"through the same Evaluate the run used, every aggregate through the same Aggregate, and every latency percentile " +
	"from raw/latency-<baseline>.json through the same nearest-rank PercentileInt64; each is compared EXACTLY. The hit " +
	"lists in the report are compared to the raw hit lists as well, so a report cannot be scored against samples it did " +
	"not publish. The environment block is checked for presence only. A metric whose raw samples are absent reads UNKNOWN " +
	"and is never counted as reproduced."

// Reproduce recomputes every published statistic in a run directory.
func Reproduce(run *RunDir) AggregateReport {
	rep := run.Report.Reproducible
	out := AggregateReport{
		FormatVersion:  FormatVersion,
		HarnessVersion: HarnessVersion,
		ScorerVersion:  ScorerVersion,
		Repo:           rep.Repo.Name,
		RunnerClass:    rep.RunnerClass,
		Environment:    run.Report.Environment,
		MissingRaw:     append([]string(nil), run.MissingRaw...),
		Method:         AggregateMethod,
	}
	out.MissingEnvironment = run.Report.Environment.Missing()
	out.EnvironmentComplete = len(out.MissingEnvironment) == 0

	byID := map[string]Query{}
	for _, q := range run.Dataset.Dataset.Queries {
		byID[q.ID] = q
	}
	minGrade := run.Dataset.Dataset.MinGrade()
	budgets := rep.TokenBudgets

	add := func(c MetricCheck) {
		out.Metrics = append(out.Metrics, c)
		out.Checked++
		switch c.Status {
		case CheckReproduced:
			out.Reproduced++
		case CheckDiscrepant:
			out.Discrepant++
			out.Discrepancies = append(out.Discrepancies, fmt.Sprintf("%s %s: published %s, recomputed %s", c.Baseline, c.Metric, c.Published, c.Recomputed))
		default:
			out.Unknown++
		}
	}
	exact := func(b Baseline, metric string, published, recomputed any) {
		c := MetricCheck{Baseline: b, Metric: metric, Published: render(published), Recomputed: render(recomputed), Status: CheckReproduced}
		if !reflect.DeepEqual(published, recomputed) {
			c.Status = CheckDiscrepant
		}
		add(c)
	}
	unknown := func(b Baseline, metric string, published any, reason string) {
		add(MetricCheck{Baseline: b, Metric: metric, Published: render(published), Status: CheckUnknown, Reason: reason})
	}

	perf := map[Baseline]BaselinePerformance{}
	for _, p := range run.Report.Performance {
		perf[p.Baseline] = p
	}

	for _, b := range rep.Baselines {
		if b.Status != BaselineStatusOK {
			// An unavailable baseline publishes no numbers; the one claim it
			// makes is that nothing was measured, which the raw side must agree
			// with.
			if hits, ok := run.Hits[b.Name]; ok && hits.Samples > 0 {
				exact(b.Name, "status", b.Status, "unavailable baseline with raw hits")
			} else {
				exact(b.Name, "status", b.Status, b.Status)
			}
			continue
		}
		hits, haveHits := run.Hits[b.Name]
		rawByID := map[string][]Hit{}
		if haveHits {
			for _, q := range hits.Queries {
				rawByID[q.ID] = q.Hits
			}
		}
		recomputed := make([]QueryResult, 0, len(b.Queries))
		for _, q := range b.Queries {
			if !haveHits {
				unknown(b.Name, "query."+q.ID+".hits", len(q.Hits), "no raw hit file for this baseline")
				continue
			}
			raw, ok := rawByID[q.ID]
			if !ok {
				unknown(b.Name, "query."+q.ID+".hits", len(q.Hits), "query absent from the raw hit file")
				continue
			}
			exact(b.Name, "query."+q.ID+".hits", normalizeHits(q.Hits), normalizeHits(raw))
			dq, ok := byID[q.ID]
			if !ok {
				unknown(b.Name, "query."+q.ID+".metrics", q.Metrics, "query absent from dataset.json")
				continue
			}
			rm := Evaluate(raw, dq, minGrade, budgets)
			exact(b.Name, "query."+q.ID+".metrics", normalizeMetrics(q.Metrics), normalizeMetrics(rm))
			recomputed = append(recomputed, QueryResult{ID: q.ID, Stratum: dq.Stratum, Split: dq.Split, Hits: raw, Metrics: rm})
		}
		if haveHits && len(recomputed) == len(b.Queries) {
			overall, strata, splits := AggregateAll(recomputed, budgets)
			exact(b.Name, "overall", normalizeAgg(b.Overall), normalizeAgg(overall))
			for _, s := range Strata {
				exact(b.Name, "strata."+s, normalizeAgg(b.Strata[s]), normalizeAgg(strata[s]))
			}
			for _, s := range []string{SplitDev, SplitHoldout} {
				exact(b.Name, "splits."+s, normalizeAgg(b.Splits[s]), normalizeAgg(splits[s]))
			}
		} else {
			unknown(b.Name, "overall", b.Overall.Status, "aggregates cannot be recomputed without every query's raw hits")
		}

		p, havePerf := perf[b.Name]
		lat, haveLat := run.Latency[b.Name]
		if !havePerf {
			unknown(b.Name, "performance", "absent", "no performance block for this baseline")
			continue
		}
		var samples []int64
		if haveLat {
			for _, q := range lat.Queries {
				samples = append(samples, q.SamplesUS...)
			}
		}
		checkMeasure := func(metric string, m Measure, raw *float64, haveRaw bool, reason string) {
			switch {
			case m.Status != StatusMeasured:
				// Nothing published; the raw side must have nothing either.
				if haveRaw && raw != nil {
					exact(b.Name, metric, m.Status, "raw sample present for a "+m.Status+" measure")
				} else {
					exact(b.Name, metric, m.Status, m.Status)
				}
			case !haveRaw || raw == nil:
				unknown(b.Name, metric, *m.Value, reason)
			default:
				exact(b.Name, metric, *m.Value, *raw)
			}
		}
		var p50, p95 *float64
		if len(samples) > 0 {
			v50 := float64(PercentileInt64(samples, 50))
			v95 := float64(PercentileInt64(samples, 95))
			p50, p95 = &v50, &v95
		}
		checkMeasure("query_p50_us", p.QueryP50US, p50, haveLat, "no raw latency file for this baseline")
		checkMeasure("query_p95_us", p.QueryP95US, p95, haveLat, "no raw latency file for this baseline")
		exact(b.Name, "latency_samples", p.LatencySamples, len(samples))
		checkMeasure("index_ms", p.IndexMS, lat.IndexMS, haveLat, "no raw latency file for this baseline")
		checkMeasure("peak_rss_mb", p.PeakRSSMB, lat.PeakRSSMB, haveLat, "no raw latency file for this baseline")
	}

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
	case string:
		return x
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
