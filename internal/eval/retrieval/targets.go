package retrieval

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The two checked-in files used to share one immutability milestone. They no
// longer can: SW-282 rewrites the targets file and must leave the budgets file
// byte-identical, and a single constant cannot express two different next
// stories. Splitting it is the whole point — a shared constant made "which
// file may this story rewrite?" unanswerable.

// TargetsImmutableUntil marks docs/eval/retrieval-targets.json frozen until the
// story that may next rewrite it. SW-266's slices are spent; the next rewrite
// belongs to the retrieval recovery story that fixes the misses this file now
// records (SW-282 AC-5, OQ-1).
const TargetsImmutableUntil = "SW-283"

// BudgetsImmutableUntil marks docs/eval/retrieval-budgets.json frozen. SW-282
// does not recalibrate budgets and leaves that file byte-identical, so its
// milestone does not move.
const BudgetsImmutableUntil = "SW-266"

// TargetsSchemaVersion pins docs/eval/retrieval-targets.json.
const TargetsSchemaVersion = 1

// FusionMinDelta is the absolute improvement on the target metric that fusion
// (SW-263) must show over the best single baseline on the conceptual strata.
const FusionMinDelta = 0.10

// TargetMetric is the metric fusion is judged on.
const TargetMetric = MetricNDCG10

// ConceptualStrata are the strata where a semantic signal is expected to
// matter; the exact strata carry a no-regression floor instead.
var ConceptualStrata = []string{StratumNLBehaviour, StratumArchitectureFlow}

// lowerIsBetter names the metrics where the best value is the minimum.
var lowerIsBetter = map[string]bool{MetricNegativeHitRate5: true, MetricFirstRelevantRankMean: true}

// DerivedFrom cites the report a checked-in file was generated from.
type DerivedFrom struct {
	Report        string `json:"report"`
	SHA256        string `json:"sha256"`
	Repo          string `json:"repo,omitempty"`
	RepoSHA       string `json:"repo_sha,omitempty"`
	Dataset       string `json:"dataset,omitempty"`
	DatasetSHA256 string `json:"dataset_sha256,omitempty"`
	CandidateSHA  string `json:"candidate_sha,omitempty"`
	RunnerClass   string `json:"runner_class,omitempty"`
	Note          string `json:"note,omitempty"`
}

// BestValue is the best single-baseline value of one metric.
type BestValue struct {
	Baseline Baseline `json:"baseline"`
	Value    float64  `json:"value"`
}

// FusionTarget is what fusion must reach on a conceptual stratum.
type FusionTarget struct {
	Metric       string   `json:"metric"`
	BestBaseline Baseline `json:"best_baseline"`
	BestValue    float64  `json:"best_value"`
	MinDelta     float64  `json:"min_delta"`
	// MustReach is best + min_delta, capped at the oracle ceiling.
	MustReach float64 `json:"must_reach"`
}

// NoRegression is the floor fusion may not fall below.
type NoRegression struct {
	Metric   string   `json:"metric"`
	Baseline Baseline `json:"baseline"`
	Floor    float64  `json:"floor"`
}

// StratumTarget is one stratum's row.
type StratumTarget struct {
	DevQueries   int                  `json:"dev_queries"`
	Best         map[string]BestValue `json:"best_single_baseline"`
	Oracle       map[string]float64   `json:"oracle_ceiling"`
	FusionTarget *FusionTarget        `json:"fusion_target,omitempty"`
	NoRegression *NoRegression        `json:"no_regression,omitempty"`
}

// UnavailableBaseline records a baseline that produced no numbers.
//
// Since the comparator set became closed (SW-282 AC-4) nothing can populate
// this list: a comparator that is not `ok` BLOCKS the derivation instead of
// being listed beside a bar computed without it. The type and the field are
// retained so the checked-in file's shape does not move.
type UnavailableBaseline struct {
	Baseline Baseline `json:"baseline"`
	Reason   string   `json:"reason"`
}

// Targets is docs/eval/retrieval-targets.json.
type Targets struct {
	SchemaVersion  int         `json:"schema_version"`
	Date           string      `json:"date"`
	ImmutableUntil string      `json:"immutable_until"`
	Notes          string      `json:"notes"`
	DerivedFrom    DerivedFrom `json:"derived_from"`

	Metric           string   `json:"target_metric"`
	ConceptualStrata []string `json:"conceptual_strata"`
	FusionMinDelta   float64  `json:"fusion_min_delta"`
	Split            string   `json:"split"`

	Baselines []Baseline `json:"baselines"`
	// UnavailableBaselines is always empty and is kept for schema stability;
	// see UnavailableBaseline.
	UnavailableBaselines []UnavailableBaseline    `json:"unavailable_baselines"`
	Strata               map[string]StratumTarget `json:"strata"`

	// BundleCoverage is the SW-264 bundle-coverage gate SW-266 carried in and
	// SW-282 finally set. It is not derived from any measurement: the bar is
	// zero mechanical coverage misses, which is what the aggregate's own
	// no-miss rule already implies.
	BundleCoverage *BundleCoverage `json:"bundle_coverage,omitempty"`
}

// CandidatePipelineBaselines are the engine/retrieval pipeline modes. They are
// the CANDIDATE, never a comparator: a bar set to "the candidate plus 0.10" is
// not a bar, it is a restatement of what the candidate already does.
var CandidatePipelineBaselines = []Baseline{BaselineChunkOnly, BaselineFusion, BaselineSemanticFirst}

// ComparatorBaselines are the single baselines a target may be derived from.
var ComparatorBaselines = []Baseline{BaselineLexical, BaselineHybridV1, BaselineSemanticNameOnly}

// DerivationBaselines is the EXACT set a derivation report must carry: the
// comparators plus the oracle that supplies the ceilings. The set is CLOSED IN
// BOTH DIRECTIONS and every member must have run.
//
// "No candidate baseline" is not a comparator set, it is whatever happened to
// run, and a bar derived from whatever happened to run is not a bar. Two
// probes against the earlier open rule:
//
//   - remove hybrid_v1 and semantic_name_only from the derivation report and
//     the derivation was ACCEPTED: architecture_flow's must_reach fell from
//     0.4578575262772977 to 0.1 and the exact_identifier Top-1 floor from 1 to
//     0.75 — turning both of the quality misses this story records into
//     passes, with no gate anywhere reporting that anything had changed.
//   - add one unapproved baseline scoring perfectly on architecture_flow and
//     the derivation was ACCEPTED, manufacturing a must_reach of 1.
//
// So a missing member and an extra one are refused alike. So is a member that
// is PRESENT but did not finish ok, which is the same laundering channel and
// the only one that arrives by accident: leave semantic_name_only in the
// report as `unavailable` and the derivation used to proceed over the two
// comparators that did run, dropping the exact_identifier Top-1 floor from 1
// to 0.75 — the value the shipped pipeline scores exactly — so the miss this
// story records would have been printed as a PASS because an embedder failed
// to load. A comparator that genuinely cannot run is a BLOCKED derivation to
// report, not a bar to lower. See refuseNonComparatorReport.
var DerivationBaselines = append(append([]Baseline{}, ComparatorBaselines...), BaselineOracle)

// BundleCoveragePopulation is the population the coverage bar is set over. It
// is not configurable: SelectTaskContextDevNLBehaviour picks it and
// RunTaskContextV2 hard-wires that selector.
type BundleCoveragePopulation struct {
	Dataset  string   `json:"dataset"`
	Split    string   `json:"split"`
	Stratum  string   `json:"stratum"`
	Selector string   `json:"selector"`
	QueryIDs []string `json:"query_ids"`
	N        int      `json:"n"`
}

// BundleCoverageThreshold is the bar, expressed in WHOLE QUERIES. There is
// deliberately no share, percentage or fractional field: on a six-query
// population one query is 16.7 points, so a decimal threshold would invent
// precision the sample cannot support — the defect that produced SW-263's
// 0.00084 miss.
type BundleCoverageThreshold struct {
	CoveredQueriesRequired int `json:"covered_queries_required"`
	MaxMisses              int `json:"max_misses"`
}

// BundleCoverage is the checked-in coverage target.
type BundleCoverage struct {
	Population      BundleCoveragePopulation `json:"population"`
	TokenBudget     int                      `json:"token_budget"`
	CreditRule      string                   `json:"credit_rule"`
	Threshold       BundleCoverageThreshold  `json:"threshold"`
	Resolution      string                   `json:"resolution"`
	ResolutionNote  string                   `json:"resolution_note"`
	ThresholdSource string                   `json:"threshold_source"`
	Supplements     string                   `json:"supplements"`
}

// BundleCoverageSupplements is the statement AC-3 requires beside the target.
const BundleCoverageSupplements = "This gate SUPPLEMENTS and does not replace the qrel-blind bundle-sufficiency smoke gate (" +
	QrelBlindSmokeEvaluationName + ", SW-266 AC-7). Coverage asserts that a reviewed grade-3 span is PRESENT in the bundle bytes; " +
	"the smoke gate asserts that a reader could ANSWER from those bytes. SW-280 is the direct evidence that the two can disagree: " +
	"31 of 64 against a pre-registered k=56, with graders repeatedly locating the span outside the retrieved bytes. " +
	"The retrieval-targets gate requires BOTH and fails when either is unmet or absent."

// BundleCoverageThresholdSource explains where the bar came from, so nobody
// reads it as calibrated from the report beside it.
const BundleCoverageThresholdSource = "Not calibrated from any measurement. The bar is zero mechanical coverage misses over the whole " +
	"population, which is what the no-complete-case miss rule in docs/eval/retrieval/methodology.md already implies and what SW-264 " +
	"measured on the same six queries (grade3_span_coverage 1, coverage_resolution_fraction 1/6, " +
	"docs/eval/retrieval/runs/2026-09-02-sw264-task-context-v2-static-local/measurement.json). " +
	"If a re-measurement misses, the miss is recorded and becomes the recovery story's input; the threshold is NOT lowered to the observed value."

// TargetsNotes explains the file to a reader who has only the JSON.
const TargetsNotes = "Retrieval targets, recalibrated by SW-282 from measured COMPARATOR baselines over the DEV split alone. " +
	"Comparator set: lexical, hybrid_v1, semantic_name_only, plus oracle_upper_bound for the ceilings. " +
	"chunk_only, fusion and semantic_first are the candidate pipeline and are REFUSED as single baselines by DeriveTargets — " +
	"a bar set to the candidate plus a delta is not a bar. A report carrying any holdout query result is refused outright rather " +
	"than filtered, because a silently discarded holdout row cannot be told apart from one that was never executed; the holdout is " +
	"never used to set a target. Per stratum the file records the best single-baseline value of every aggregate metric over the dev " +
	"split, the oracle ceiling the scorer can reach, and on the conceptual strata a fusion_target: BEST SINGLE BASELINE OVER THE " +
	"DEVELOPMENT SPLIT PLUS fusion_min_delta 0.10, CAPPED AT THE ORACLE CEILING. exact_identifier carries no fusion delta; it carries " +
	"the Top-1 no_regression floor, the best single baseline's Top-1, which the shipped pipeline may not fall below. Each stratum's " +
	"dev_queries IS ITS POPULATION SIZE AND ITS RESOLUTION: every aggregate here is a plain mean over that many per-query values, so " +
	"one query moves it by 1/dev_queries and a shortfall smaller than one query's influence is not evidence of a difference. " +
	"architecture_flow has five development queries and exact_identifier four; write no bar, and read no miss, to more precision " +
	"than that. bundle_coverage " +
	"is the SW-264 gate SW-266 carried in: over the six dev nl_behaviour queries SelectTaskContextDevNLBehaviour selects, at token " +
	"budget 1200, a query is covered when any task_context/2 bundle evidence citation satisfies SpanMatches for any grade-3 " +
	"judgement, and the bar is every query covered with zero misses, expressed in whole queries because the population's resolution " +
	"is 1/6. THE BUNDLE-COVERAGE GATE SUPPLEMENTS AND DOES NOT REPLACE the qrel-blind bundle-sufficiency smoke gate: coverage " +
	"asserts a reviewed grade-3 span is PRESENT in the bundle bytes, the smoke gate asserts a reader could ANSWER from those bytes, " +
	"SW-280 is the direct evidence that the two can disagree (31 of 64 against a pre-registered k=56), and the gate requires BOTH " +
	"and fails when either is unmet or absent. Every target is enforced by `go run ./cmd/retrieval-eval -check-targets <report>` and by the release-gate " +
	"retrieval-targets runner; a target no command enforces is a note, not a target. Immutable until the story named in " +
	"immutable_until; regenerate only from a report checked in beside it (derived_from names the report and its sha256)."

// DeriveTargets computes the targets file from a report (AC-7).
//
// It refuses five report shapes outright rather than working around them
// (SW-282 AC-4):
//
//   - a report carrying a candidate-pipeline baseline (chunk_only, fusion,
//     semantic_first). Admitting the candidate as a "single baseline" would let
//     it set its own bar. This is enforced HERE, not in whatever command line
//     happened to be used, because a command-line convention is something a
//     later run can forget.
//   - a report whose baseline set is not exactly DerivationBaselines — a
//     comparator missing, a comparator listed twice, or any other baseline
//     present. The set the bar is computed over IS the bar; leaving a
//     comparator out lowers it and adding a strong stranger raises it.
//   - a report in which any comparator is present but did not finish `ok`.
//     Deriving from whichever comparators happened to run is the same defect
//     wearing a different hat, and it is the one that arrives by accident: a
//     comparator that fails to run would quietly lower the bar and turn a
//     recorded miss into a pass without anyone deciding to. A comparator that
//     genuinely cannot run is a BLOCKED derivation to report, not a bar to
//     lower.
//   - a report in which a comparator is present and `ok` but was measured over
//     an empty query population, or over a population different from its
//     peers'. Closing the set by name and by status still leaves the set open
//     by POPULATION: a comparator scored on nothing contributes no metric to
//     the best-single-baseline scan and is therefore indistinguishable from
//     one that was never in the report at all. See refusePartialComparatorSet.
//   - a report carrying a holdout query result. The previous behaviour silently
//     filtered holdout rows away, which makes "the holdout was never executed"
//     and "the holdout was executed and dropped" produce identical output. The
//     holdout may not be touched, so the honest answer is to refuse the report.
func DeriveTargets(r *Report, from DerivedFrom, date string) (*Targets, error) {
	if r == nil {
		return nil, fmt.Errorf("retrieval: no report")
	}
	if err := refuseNonComparatorReport(r); err != nil {
		return nil, err
	}
	rep := r.Reproducible
	from.Repo, from.RepoSHA = rep.Repo.Name, rep.Repo.SHA
	from.Dataset, from.DatasetSHA256 = rep.Dataset.ID, rep.Dataset.SHA256
	from.CandidateSHA, from.RunnerClass = rep.CandidateSHA, rep.RunnerClass

	out := &Targets{
		SchemaVersion: TargetsSchemaVersion, Date: date, ImmutableUntil: TargetsImmutableUntil, Notes: TargetsNotes,
		DerivedFrom: from, Metric: TargetMetric, ConceptualStrata: append([]string(nil), ConceptualStrata...),
		FusionMinDelta: FusionMinDelta, Split: SplitDev,
		UnavailableBaselines: []UnavailableBaseline{}, Strata: map[string]StratumTarget{},
	}

	// Dev-split-only per-stratum aggregates, per baseline.
	type devAgg struct {
		strata map[string]AggregateMetrics
	}
	competitors := map[Baseline]devAgg{}
	var oracle *devAgg
	for _, b := range rep.Baselines {
		// No status filter and no split filter: refuseNonComparatorReport has
		// already established that the baselines are exactly the comparators,
		// that each of them ran ok, and that every row is a development row.
		// Skipping a not-ok baseline here would be the derivation narrowing
		// its own comparator set, and filtering rows here would re-introduce
		// the shape that hides a holdout row inside a dev-looking result.
		_, strata, _ := AggregateAll(b.Queries, rep.TokenBudgets)
		a := devAgg{strata: strata}
		if b.Name == BaselineOracle {
			oracle = &a
			continue
		}
		competitors[b.Name] = a
		out.Baselines = append(out.Baselines, b.Name)
	}
	// Unreachable while the comparator set is closed; kept as the backstop
	// that would catch a future relaxation of it before a bar is written.
	if len(competitors) == 0 {
		return nil, fmt.Errorf("retrieval: no ok baseline other than the oracle; nothing to derive a target from")
	}

	conceptual := map[string]bool{}
	for _, s := range ConceptualStrata {
		conceptual[s] = true
	}
	for _, s := range Strata {
		st := StratumTarget{Best: map[string]BestValue{}, Oracle: map[string]float64{}}
		metrics := map[string]bool{}
		for _, b := range out.Baselines {
			agg := competitors[b].strata[s]
			st.DevQueries = agg.Queries
			for m := range agg.Metrics {
				metrics[m] = true
			}
		}
		names := make([]string, 0, len(metrics))
		for m := range metrics {
			names = append(names, m)
		}
		sort.Strings(names)
		for _, m := range names {
			var best *BestValue
			for _, b := range out.Baselines {
				v, ok := competitors[b].strata[s].Metrics[m]
				if !ok {
					continue
				}
				better := best == nil || (lowerIsBetter[m] && v < best.Value) || (!lowerIsBetter[m] && v > best.Value)
				if better {
					best = &BestValue{Baseline: b, Value: v}
				}
			}
			if best != nil {
				st.Best[m] = *best
			}
			if oracle != nil {
				if v, ok := oracle.strata[s].Metrics[m]; ok {
					st.Oracle[m] = v
				}
			}
		}
		if conceptual[s] {
			if b, ok := st.Best[TargetMetric]; ok {
				must := b.Value + FusionMinDelta
				if ceiling, ok := st.Oracle[TargetMetric]; ok {
					must = math.Min(must, ceiling)
				}
				st.FusionTarget = &FusionTarget{Metric: TargetMetric, BestBaseline: b.Baseline, BestValue: b.Value, MinDelta: FusionMinDelta, MustReach: must}
			}
		}
		if s == StratumExactIdentifier {
			if b, ok := st.Best[MetricTop1]; ok {
				st.NoRegression = &NoRegression{Metric: MetricTop1, Baseline: b.Baseline, Floor: b.Value}
			}
		}
		out.Strata[s] = st
	}
	coverage, err := deriveBundleCoverage(r)
	if err != nil {
		return nil, err
	}
	out.BundleCoverage = coverage
	return out, nil
}

// refuseNonComparatorReport is the derivation's closed world: EXACTLY the
// comparators, each of them run, development only.
func refuseNonComparatorReport(r *Report) error {
	candidate := map[Baseline]bool{}
	for _, b := range CandidatePipelineBaselines {
		candidate[b] = true
	}
	var offending []string
	for _, b := range r.Reproducible.Baselines {
		if candidate[b.Name] {
			offending = append(offending, string(b.Name))
		}
	}
	if len(offending) > 0 {
		sort.Strings(offending)
		return fmt.Errorf("retrieval: the report carries the candidate pipeline as a baseline (%s); a target derived from the candidate is the candidate's own value plus a delta, not a bar. Re-run with the comparator set only: %s plus %s for the ceilings",
			strings.Join(offending, ", "), joinBaselines(ComparatorBaselines), BaselineOracle)
	}
	if err := refusePartialComparatorSet(r); err != nil {
		return err
	}
	var holdout []string
	for _, b := range r.Reproducible.Baselines {
		for _, q := range b.Queries {
			if q.Split == SplitHoldout {
				holdout = append(holdout, string(b.Name)+"/"+q.ID)
			}
		}
	}
	if len(holdout) > 0 {
		sort.Strings(holdout)
		shown := holdout
		if len(shown) > 5 {
			shown = shown[:5]
		}
		return fmt.Errorf("retrieval: the report carries %d holdout query result(s) (%s%s); the holdout may not be touched by a derivation, and silently dropping the rows cannot be told apart from never having executed them. Measure a development-only slice (retrieval.SelectDevSplit)",
			len(holdout), strings.Join(shown, ", "), map[bool]string{true: ", …", false: ""}[len(holdout) > len(shown)])
	}
	return nil
}

// refusePartialComparatorSet closes the comparator set in every direction it
// can be opened: by NAME, by STATUS, and by QUERY POPULATION.
//
// The set the bar is computed over IS the bar, so the derivation may not be
// handed a different set and asked to produce "the" targets. Dropping a
// comparator lowers every best-single-baseline value it used to win, adding a
// strong stranger raises them, and listing one twice lets the second copy
// overwrite the first — three ways to move a bar with nothing on the record
// saying it moved.
//
// The name and status checks alone leave a fourth way, and it is the one that
// arrives by accident rather than by intent. A comparator can be PRESENT and
// `ok` and still have been measured over nothing, or over a different set of
// queries from its peers: a run that produced no rows, a report written or
// filtered part-way, a selector that matched nothing. Nothing downstream
// notices, because AggregateAll over an empty slice yields no metrics at all
// and the best-single-baseline scan simply skips the metrics a comparator does
// not carry. Emptying the hybrid_v1 and semantic_name_only query arrays of the
// committed derivation report — leaving all four names present and all four
// `ok` — used to be ACCEPTED, and dropped architecture_flow's must_reach from
// 0.4578575262772977 to 0.1 and the exact_identifier Top-1 floor from 1 to
// 0.75, turning both of the quality misses this story records into passes.
//
// So the population is checked too, and "the same population" is defined
// operationally as THE IDENTICAL MULTISET OF QUERY IDS, taken against the
// first comparator in DerivationBaselines. That is the strictest reading and
// the only one that makes the comparison mean anything: every value in the
// targets file is a plain mean over a stratum's per-query values, so a
// comparator measured over a different or smaller population has not answered
// the same question less well — it has answered a different question. The
// queries it was never asked cannot count against it, and the queries its
// peers were never asked cannot count against them, so comparing the two means
// lets the population rather than retrieval quality pick the winner and set
// the bar. Equal COUNTS are not enough for the same reason: two comparators
// scored on forty-four different queries each are no more comparable than one
// scored on forty-four and one on three.
func refusePartialComparatorSet(r *Report) error {
	required := map[Baseline]bool{}
	for _, b := range DerivationBaselines {
		required[b] = true
	}
	status := map[Baseline]string{}
	population := map[Baseline][]string{}
	var extra, duplicate []string
	for _, b := range r.Reproducible.Baselines {
		if !required[b.Name] {
			extra = append(extra, string(b.Name))
			continue
		}
		if _, seen := status[b.Name]; seen {
			duplicate = append(duplicate, string(b.Name))
			continue
		}
		status[b.Name] = b.Status
		population[b.Name] = measuredPopulation(b)
	}
	var missing []string
	for _, b := range DerivationBaselines {
		if _, ok := status[b]; !ok {
			missing = append(missing, string(b))
		}
	}
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "missing: "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		problems = append(problems, "not in the comparator set: "+strings.Join(extra, ", "))
	}
	if len(duplicate) > 0 {
		sort.Strings(duplicate)
		problems = append(problems, "listed more than once: "+strings.Join(duplicate, ", "))
	}
	if len(problems) > 0 {
		return fmt.Errorf("retrieval: the report's baselines are not the comparator set %s (%s); the set a target is computed over IS the target, so a derivation from whichever baselines happened to be in the report is not a bar — dropping a comparator lowers every best-single-baseline value it used to win and can turn a recorded miss into a pass, and adding one raises them. Measure the whole comparator set and derive from that; do not derive from a subset or a superset",
			joinBaselines(DerivationBaselines), strings.Join(problems, "; "))
	}
	var notOK []string
	for _, b := range DerivationBaselines {
		if st := status[b]; st != BaselineStatusOK {
			notOK = append(notOK, string(b)+" ("+st+")")
		}
	}
	if len(notOK) > 0 {
		return fmt.Errorf("retrieval: comparator(s) %s did not run; the derivation is BLOCKED, not narrowed to the ones that did. Deriving from whichever comparators happened to run is the same defect as leaving one out of the report, and it is the one that arrives by accident: the missing comparator's best values disappear from the bar, so a comparator that fails to run silently lowers it and can turn a recorded miss into a pass without anyone deciding to. If a comparator genuinely cannot run, report the blocked derivation and fix it — the previous targets stand until the whole set %s runs",
			strings.Join(notOK, ", "), joinBaselines(DerivationBaselines))
	}
	var unmeasured []string
	for _, b := range DerivationBaselines {
		if len(population[b]) == 0 {
			unmeasured = append(unmeasured, string(b))
		}
	}
	if len(unmeasured) > 0 {
		return fmt.Errorf("retrieval: comparator(s) %s are present and ok but carry NO query results; the derivation is BLOCKED. A comparator measured over an empty population contributes no metric to the best-single-baseline scan, so it is exactly as absent from the bar as one left out of the report altogether — with the difference that the report still looks complete. This arrives by accident: a comparator that ran but produced no rows, a report written or filtered part-way, a selector that matched nothing. It is refused rather than averaged around: a bar derived from an empty comparator is not a bar. Re-measure the whole comparator set %s over one query population",
			strings.Join(unmeasured, ", "), joinBaselines(DerivationBaselines))
	}
	reference := DerivationBaselines[0]
	var differing []string
	for _, b := range DerivationBaselines[1:] {
		diff := populationDifference(population[reference], population[b])
		if len(diff) == 0 {
			continue
		}
		shown := diff
		if len(shown) > 5 {
			shown = shown[:5]
		}
		differing = append(differing, fmt.Sprintf("%s measured %d quer%s to %s's %d — %s%s",
			b, len(population[b]), map[bool]string{true: "y", false: "ies"}[len(population[b]) == 1],
			reference, len(population[reference]), strings.Join(shown, ", "),
			map[bool]string{true: ", …", false: ""}[len(diff) > len(shown)]))
	}
	if len(differing) > 0 {
		return fmt.Errorf("retrieval: the comparators were not all measured over the same query population (%s); the derivation is BLOCKED. \"The same population\" means the IDENTICAL MULTISET OF QUERY IDS, compared here against %s, and equal counts are not enough — two comparators scored on different queries are not comparable however many each ran. Every value in the targets file is a plain mean over a stratum's per-query values, so a comparator measured over a different or smaller population has not answered the same question less well, it has answered a different question: the queries it was never asked cannot count against it, and its peers' unasked queries cannot count against them. Picking a best single baseline across such means lets the population, not retrieval quality, choose the winner and set the bar. Re-measure the whole comparator set %s over one population",
			strings.Join(differing, "; "), reference, joinBaselines(DerivationBaselines))
	}
	return nil
}

// measuredPopulation is the population a baseline was measured over: the sorted
// ids of its query results, duplicates kept so a query counted twice is not
// mistaken for the same population as one counted once.
func measuredPopulation(b BaselineResult) []string {
	ids := make([]string, 0, len(b.Queries))
	for _, q := range b.Queries {
		ids = append(ids, q.ID)
	}
	sort.Strings(ids)
	return ids
}

// populationDifference names the query ids whose multiplicity differs between
// two populations, as "<id> (<n here> vs <n in the reference>)". An id present
// in one and absent from the other reads as 0 on that side, and a duplicate
// reads as 2 against 1, so one format covers every way two populations can
// disagree. The result is sorted, so a refusal is reproducible.
func populationDifference(reference, got []string) []string {
	refN, gotN := map[string]int{}, map[string]int{}
	for _, id := range reference {
		refN[id]++
	}
	for _, id := range got {
		gotN[id]++
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range append(append([]string{}, reference...), got...) {
		if seen[id] || refN[id] == gotN[id] {
			continue
		}
		seen[id] = true
		out = append(out, fmt.Sprintf("%s (%d here vs %d)", id, gotN[id], refN[id]))
	}
	sort.Strings(out)
	return out
}

func joinBaselines(bs []Baseline) string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, string(b))
	}
	return strings.Join(out, ", ")
}

// deriveBundleCoverage writes the coverage target. The POPULATION comes from
// the report (the dev nl_behaviour query IDs it actually measured); the
// THRESHOLD comes from the rule — every query covered, zero misses — and from
// nothing that was observed, so the bar is not set and passed on the same
// observations.
func deriveBundleCoverage(r *Report) (*BundleCoverage, error) {
	ids := map[string]bool{}
	for _, b := range r.Reproducible.Baselines {
		for _, q := range b.Queries {
			if q.Split == SplitDev && q.Stratum == StratumNLBehaviour {
				ids[q.ID] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("retrieval: the report measured no %s/%s query, so the bundle-coverage population cannot be recorded", SplitDev, StratumNLBehaviour)
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	n := len(ordered)
	return &BundleCoverage{
		Population: BundleCoveragePopulation{
			Dataset:  r.Reproducible.Dataset.ID,
			Split:    SplitDev,
			Stratum:  StratumNLBehaviour,
			Selector: "retrieval.SelectTaskContextDevNLBehaviour: q.stratum == " + StratumNLBehaviour + " AND q.split == " + SplitDev + "; no caller-selectable split",
			QueryIDs: ordered,
			N:        n,
		},
		TokenBudget:     TaskContextTokenBudget,
		CreditRule:      TaskContextMatchingRule,
		Threshold:       BundleCoverageThreshold{CoveredQueriesRequired: n, MaxMisses: 0},
		Resolution:      fmt.Sprintf("1/%d", n),
		ResolutionNote:  fmt.Sprintf("one query moves the aggregate by 1/%d (%.1f percentage points); extra decimal places add no evidence, so the bar is stated in whole queries", n, 100/float64(n)),
		ThresholdSource: BundleCoverageThresholdSource,
		Supplements:     BundleCoverageSupplements,
	}, nil
}

// MarshalTargets renders the targets file.
func MarshalTargets(t *Targets) ([]byte, error) { return marshalStable(t) }

// Budgets (AC-8).

// BudgetsSchemaVersion pins docs/eval/retrieval-budgets.json.
const BudgetsSchemaVersion = 1

// BudgetHeadroom is the factor a measured value is multiplied by to become a
// budget: enough to absorb machine noise, small enough that a 2x regression
// is caught.
const BudgetHeadroom = 2.0

// Fixture size classes.
const (
	FixtureSmall  = "small"
	FixtureMedium = "medium"
	FixtureLarge  = "large"
)

// FixtureClasses in report order.
var FixtureClasses = []string{FixtureSmall, FixtureMedium, FixtureLarge}

// FixtureMeasurement is one measured report assigned to a size class.
type FixtureMeasurement struct {
	Class       string
	Report      *Report
	DerivedFrom DerivedFrom
}

// BudgetLine is one budget with the measurement it came from.
type BudgetLine struct {
	Measured float64  `json:"measured"`
	Baseline Baseline `json:"measured_baseline"`
	Unit     string   `json:"unit"`
	Budget   float64  `json:"budget"`
}

// FixtureBudget is one size class's row.
type FixtureBudget struct {
	Status      string       `json:"status"`
	Reason      string       `json:"reason,omitempty"`
	Repo        string       `json:"repo,omitempty"`
	RepoSHA     string       `json:"repo_sha,omitempty"`
	Files       int          `json:"files,omitempty"`
	Nodes       int          `json:"nodes,omitempty"`
	RunnerClass string       `json:"runner_class,omitempty"`
	DerivedFrom *DerivedFrom `json:"derived_from,omitempty"`

	IndexMS      *BudgetLine `json:"index_ms,omitempty"`
	P95LatencyUS *BudgetLine `json:"query_p95_us,omitempty"`
	PeakRSSMB    *BudgetLine `json:"peak_rss_mb,omitempty"`
}

// Budgets is docs/eval/retrieval-budgets.json.
type Budgets struct {
	SchemaVersion  int                      `json:"schema_version"`
	Date           string                   `json:"date"`
	ImmutableUntil string                   `json:"immutable_until"`
	Notes          string                   `json:"notes"`
	HeadroomFactor float64                  `json:"headroom_factor"`
	Fixtures       map[string]FixtureBudget `json:"fixtures"`
}

// BudgetsNotes explains the file.
const BudgetsNotes = "SW-258 retrieval performance budgets (spec open question 5), derived from the measured baselines: per fixture " +
	"size class, the cold index time, the worst query p95 across the indexed baselines (the oracle builds no index and is " +
	"excluded) and the process peak RSS, each with the measurement it came from and the budget = measured x headroom_factor. " +
	"A class with no measured report reads UNKNOWN with the reason, never a number. Budgets are only comparable within one " +
	"runner class. Immutable until the story named in immutable_until."

// DeriveBudgets computes the budgets file (AC-8). Every size class is
// present; a class without a measurement reads UNKNOWN.
func DeriveBudgets(measurements []FixtureMeasurement, date string) (*Budgets, error) {
	out := &Budgets{SchemaVersion: BudgetsSchemaVersion, Date: date, ImmutableUntil: BudgetsImmutableUntil, Notes: BudgetsNotes,
		HeadroomFactor: BudgetHeadroom, Fixtures: map[string]FixtureBudget{}}
	known := map[string]bool{}
	for _, c := range FixtureClasses {
		known[c] = true
		out.Fixtures[c] = FixtureBudget{Status: StatusUnknown, Reason: "no report was measured for this size class in this run"}
	}
	for _, m := range measurements {
		if !known[m.Class] {
			return nil, fmt.Errorf("retrieval: unknown fixture class %q (have small, medium, large)", m.Class)
		}
		if m.Report == nil {
			return nil, fmt.Errorf("retrieval: fixture class %s has no report", m.Class)
		}
		out.Fixtures[m.Class] = deriveFixtureBudget(m)
	}
	return out, nil
}

func deriveFixtureBudget(m FixtureMeasurement) FixtureBudget {
	rep := m.Report.Reproducible
	from := m.DerivedFrom
	from.Repo, from.RepoSHA = rep.Repo.Name, rep.Repo.SHA
	from.Dataset, from.DatasetSHA256 = rep.Dataset.ID, rep.Dataset.SHA256
	from.CandidateSHA, from.RunnerClass = rep.CandidateSHA, rep.RunnerClass
	fb := FixtureBudget{Status: StatusMeasured, Repo: rep.Repo.Name, RepoSHA: rep.Repo.SHA, Files: rep.Repo.Files, Nodes: rep.Repo.Nodes,
		RunnerClass: rep.RunnerClass, DerivedFrom: &from}

	ok := map[Baseline]bool{}
	for _, b := range rep.Baselines {
		ok[b.Name] = b.Status == BaselineStatusOK
	}
	pick := func(get func(BaselinePerformance) Measure, includeOracle bool, unit string) *BudgetLine {
		var best *BudgetLine
		for _, p := range m.Report.Performance {
			if !ok[p.Baseline] || (p.Baseline == BaselineOracle && !includeOracle) {
				continue
			}
			ms := get(p)
			if ms.Status != StatusMeasured || ms.Value == nil {
				continue
			}
			if best == nil || *ms.Value > best.Measured {
				best = &BudgetLine{Measured: *ms.Value, Baseline: p.Baseline, Unit: unit, Budget: math.Ceil(*ms.Value * BudgetHeadroom)}
			}
		}
		return best
	}
	fb.IndexMS = pick(func(p BaselinePerformance) Measure { return p.IndexMS }, false, "ms")
	fb.P95LatencyUS = pick(func(p BaselinePerformance) Measure { return p.QueryP95US }, false, "us")
	// Peak RSS is process-wide, so every baseline that ran contributes.
	fb.PeakRSSMB = pick(func(p BaselinePerformance) Measure { return p.PeakRSSMB }, true, "MB")
	if fb.IndexMS == nil && fb.P95LatencyUS == nil && fb.PeakRSSMB == nil {
		fb.Status = StatusUnknown
		fb.Reason = "the report carries no measured performance figure"
	}
	return fb
}

// MarshalBudgets renders the budgets file.
func MarshalBudgets(b *Budgets) ([]byte, error) { return marshalStable(b) }
