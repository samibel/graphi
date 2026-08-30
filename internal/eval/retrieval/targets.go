package retrieval

import (
	"fmt"
	"math"
	"sort"
)

// ImmutableUntil marks the checked-in targets and budgets as frozen until the
// story that may rewrite them (AC-7, AC-8).
const ImmutableUntil = "SW-266"

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

	Baselines            []Baseline               `json:"baselines"`
	UnavailableBaselines []UnavailableBaseline    `json:"unavailable_baselines"`
	Strata               map[string]StratumTarget `json:"strata"`
}

// TargetsNotes explains the file to a reader who has only the JSON.
const TargetsNotes = "SW-258 retrieval targets, written ONCE from measured baselines before any fusion exists. Per stratum: the best " +
	"single-baseline value of every aggregate metric over the DEV split (holdout is never used to set a target), the oracle " +
	"ceiling the scorer can reach, a fusion_target on the conceptual strata (best + min_delta on the target metric, capped at " +
	"the ceiling), and the Top-1 no-regression floor on exact_identifier. Immutable until the story named in immutable_until; " +
	"regenerate only from a report checked in beside it (derived_from names the report and its sha256)."

// DeriveTargets computes the targets file from a report (AC-7).
func DeriveTargets(r *Report, from DerivedFrom, date string) (*Targets, error) {
	if r == nil {
		return nil, fmt.Errorf("retrieval: no report")
	}
	rep := r.Reproducible
	from.Repo, from.RepoSHA = rep.Repo.Name, rep.Repo.SHA
	from.Dataset, from.DatasetSHA256 = rep.Dataset.ID, rep.Dataset.SHA256
	from.CandidateSHA, from.RunnerClass = rep.CandidateSHA, rep.RunnerClass

	out := &Targets{
		SchemaVersion: TargetsSchemaVersion, Date: date, ImmutableUntil: ImmutableUntil, Notes: TargetsNotes,
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
		if b.Status != BaselineStatusOK {
			out.UnavailableBaselines = append(out.UnavailableBaselines, UnavailableBaseline{Baseline: b.Name, Reason: b.Reason})
			continue
		}
		var dev []QueryResult
		for _, q := range b.Queries {
			if q.Split == SplitDev {
				dev = append(dev, q)
			}
		}
		_, strata, _ := AggregateAll(dev, rep.TokenBudgets)
		a := devAgg{strata: strata}
		if b.Name == BaselineOracle {
			oracle = &a
			continue
		}
		competitors[b.Name] = a
		out.Baselines = append(out.Baselines, b.Name)
	}
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
	return out, nil
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
	out := &Budgets{SchemaVersion: BudgetsSchemaVersion, Date: date, ImmutableUntil: ImmutableUntil, Notes: BudgetsNotes,
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
