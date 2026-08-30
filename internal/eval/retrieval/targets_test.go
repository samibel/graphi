package retrieval

import (
	"encoding/json"
	"strings"
	"testing"
)

// syntheticReport builds a report with known per-query scores so the
// derivation is checked against arithmetic, not against a live run.
func syntheticReport() *Report {
	mk := func(name Baseline, ndcgByQuery map[string]float64, top1ByQuery map[string]float64) BaselineResult {
		b := BaselineResult{Name: name, Status: BaselineStatusOK}
		queries := []struct{ id, stratum, split string }{
			{"q1", StratumExactIdentifier, SplitDev},
			{"q2", StratumExactIdentifier, SplitHoldout},
			{"q3", StratumNLBehaviour, SplitDev},
			{"q4", StratumNLBehaviour, SplitDev},
			{"q5", StratumArchitectureFlow, SplitDev},
			{"q6", StratumNoHit, SplitDev},
		}
		for _, q := range queries {
			m := QueryMetrics{Scored: q.stratum != StratumNoHit, NDCG10: ndcgByQuery[q.id], Top1: top1ByQuery[q.id],
				Recall5: ndcgByQuery[q.id], Recall10: ndcgByQuery[q.id], MRR10: ndcgByQuery[q.id],
				RecallAtTokens: map[string]float64{"600": ndcgByQuery[q.id]}}
			if q.stratum == StratumNoHit {
				neg := name == BaselineLexical
				m.NegativeHitAt5 = &neg
			}
			b.Queries = append(b.Queries, QueryResult{ID: q.id, Stratum: q.stratum, Split: q.split, Metrics: m})
		}
		b.Overall, b.Strata, b.Splits = AggregateAll(b.Queries, []int{600})
		return b
	}
	r := &Report{FormatVersion: FormatVersion, HarnessVersion: HarnessVersion, ScorerVersion: ScorerVersion}
	r.Reproducible = Reproducible{
		CandidateSHA: "cand", RunnerClass: "test", TokenBudgets: []int{600},
		Repo:    RepoRef{Name: "cobra", SHA: "abc", Nodes: 100, Files: 10},
		Dataset: DatasetRef{ID: "cobra-v1", SHA256: "ds"},
		Baselines: []BaselineResult{
			mk(BaselineLexical,
				map[string]float64{"q1": 1, "q2": 0, "q3": 0.2, "q4": 0.4, "q5": 0.5},
				map[string]float64{"q1": 1, "q2": 0, "q3": 0, "q4": 0, "q5": 0}),
			mk(BaselineHybridV1,
				map[string]float64{"q1": 0.5, "q2": 1, "q3": 0.6, "q4": 0.8, "q5": 0.3},
				map[string]float64{"q1": 0, "q2": 1, "q3": 1, "q4": 0, "q5": 0}),
			{Name: BaselineSemanticNameOnly, Status: BaselineStatusUnavailable, Reason: "no embedder"},
			mk(BaselineOracle,
				map[string]float64{"q1": 1, "q2": 1, "q3": 1, "q4": 1, "q5": 1},
				map[string]float64{"q1": 1, "q2": 1, "q3": 1, "q4": 1, "q5": 1}),
		},
	}
	one := 100.0
	r.Performance = []BaselinePerformance{
		{Baseline: BaselineLexical, IndexMS: Measured(500, "ms"), QueryP95US: Measured(900, "us"), PeakRSSMB: Measured(300, "MB")},
		{Baseline: BaselineHybridV1, IndexMS: Measured(500, "ms"), QueryP95US: Measured(4000, "us"), PeakRSSMB: Measured(320, "MB")},
		{Baseline: BaselineSemanticNameOnly, IndexMS: Unknown("x"), QueryP95US: Unknown("x"), PeakRSSMB: Unknown("x")},
		{Baseline: BaselineOracle, IndexMS: NotApplicable("x"), QueryP95US: Measured(one, "us"), PeakRSSMB: Measured(330, "MB")},
	}
	r.Environment = Environment{GeneratedAt: "2026-08-30T00:00:00Z", OS: "darwin", Arch: "arm64", GoVersion: "go1.26.6"}
	return r
}

func TestDeriveTargets(t *testing.T) {
	r := syntheticReport()
	tg, err := DeriveTargets(r, DerivedFrom{Report: "docs/eval/retrieval/runs/x.json", SHA256: "deadbeef"}, "2026-08-30")
	if err != nil {
		t.Fatal(err)
	}
	if tg.SchemaVersion != TargetsSchemaVersion || tg.Date != "2026-08-30" || tg.ImmutableUntil != ImmutableUntil {
		t.Errorf("header = %+v", tg)
	}
	if tg.DerivedFrom.Report != "docs/eval/retrieval/runs/x.json" || tg.DerivedFrom.SHA256 != "deadbeef" || tg.DerivedFrom.Repo != "cobra" || tg.DerivedFrom.DatasetSHA256 != "ds" {
		t.Errorf("derived_from = %+v", tg.DerivedFrom)
	}

	t.Run("targets are derived from the dev split only", func(t *testing.T) {
		// exact_identifier dev is q1 alone: lexical ndcg 1, hybrid 0.5. With the
		// holdout q2 included hybrid would tie; it must not.
		ei := tg.Strata[StratumExactIdentifier]
		if ei.DevQueries != 1 {
			t.Errorf("dev_queries = %d", ei.DevQueries)
		}
		if b := ei.Best[MetricNDCG10]; b.Baseline != BaselineLexical || !approx(b.Value, 1) {
			t.Errorf("best ndcg@10 = %+v, want lexical 1", b)
		}
	})
	t.Run("conceptual strata carry a fusion target above the best baseline", func(t *testing.T) {
		nl := tg.Strata[StratumNLBehaviour]
		// hybrid dev ndcg mean = (0.6+0.8)/2 = 0.7, lexical (0.2+0.4)/2 = 0.3.
		if b := nl.Best[MetricNDCG10]; b.Baseline != BaselineHybridV1 || !approx(b.Value, 0.7) {
			t.Errorf("best = %+v", b)
		}
		if nl.FusionTarget == nil {
			t.Fatal("nl_behaviour has no fusion target")
		}
		if nl.FusionTarget.Metric != MetricNDCG10 || !approx(nl.FusionTarget.MinDelta, FusionMinDelta) || !approx(nl.FusionTarget.MustReach, 0.7+FusionMinDelta) {
			t.Errorf("fusion target = %+v", nl.FusionTarget)
		}
		if tg.Strata[StratumArchitectureFlow].FusionTarget == nil {
			t.Error("architecture_flow has no fusion target")
		}
		if tg.Strata[StratumExactIdentifier].FusionTarget != nil {
			t.Error("exact_identifier must not carry a fusion delta; it carries the no-regression floor")
		}
	})
	t.Run("the fusion target is capped at the oracle ceiling", func(t *testing.T) {
		r2 := syntheticReport()
		for i := range r2.Reproducible.Baselines[1].Queries {
			r2.Reproducible.Baselines[1].Queries[i].Metrics.NDCG10 = 0.95
		}
		b := &r2.Reproducible.Baselines[1]
		b.Overall, b.Strata, b.Splits = AggregateAll(b.Queries, []int{600})
		tg2, err := DeriveTargets(r2, DerivedFrom{}, "2026-08-30")
		if err != nil {
			t.Fatal(err)
		}
		if ft := tg2.Strata[StratumNLBehaviour].FusionTarget; !approx(ft.MustReach, 1) {
			t.Errorf("must_reach = %v, want the oracle ceiling 1", ft.MustReach)
		}
	})
	t.Run("exact_identifier carries the Top-1 no-regression floor", func(t *testing.T) {
		nr := tg.Strata[StratumExactIdentifier].NoRegression
		if nr == nil || nr.Metric != MetricTop1 || nr.Baseline != BaselineLexical || !approx(nr.Floor, 1) {
			t.Errorf("no_regression = %+v", nr)
		}
	})
	t.Run("no_hit is judged on the negative hit rate where lower is better", func(t *testing.T) {
		nh := tg.Strata[StratumNoHit]
		if b, ok := nh.Best[MetricNegativeHitRate5]; !ok || b.Baseline != BaselineHybridV1 || !approx(b.Value, 0) {
			t.Errorf("best negative_hit_rate@5 = %+v, want hybrid_v1 0", b)
		}
	})
	t.Run("the oracle is reported as the ceiling, never as a competitor", func(t *testing.T) {
		for s, st := range tg.Strata {
			for m, b := range st.Best {
				if b.Baseline == BaselineOracle {
					t.Errorf("%s %s: the oracle won the best-baseline slot", s, m)
				}
			}
		}
		if !approx(tg.Strata[StratumNLBehaviour].Oracle[MetricNDCG10], 1) {
			t.Errorf("oracle ceiling = %v", tg.Strata[StratumNLBehaviour].Oracle)
		}
	})
	t.Run("unavailable baselines are listed, not silently dropped", func(t *testing.T) {
		if len(tg.UnavailableBaselines) != 1 || tg.UnavailableBaselines[0].Baseline != BaselineSemanticNameOnly {
			t.Errorf("unavailable = %+v", tg.UnavailableBaselines)
		}
	})
	t.Run("the file is stable JSON", func(t *testing.T) {
		raw, err := MarshalTargets(tg)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(raw) || !strings.Contains(string(raw), `"immutable_until": "SW-266"`) {
			t.Errorf("targets json = %s", raw)
		}
	})
	t.Run("a report without an ok non-oracle baseline is an error", func(t *testing.T) {
		r3 := syntheticReport()
		r3.Reproducible.Baselines = r3.Reproducible.Baselines[2:]
		if _, err := DeriveTargets(r3, DerivedFrom{}, "2026-08-30"); err == nil {
			t.Error("DeriveTargets over only unavailable/oracle baselines = nil error")
		}
	})
}

func TestDeriveBudgets(t *testing.T) {
	r := syntheticReport()
	small := FixtureMeasurement{Class: FixtureSmall, Report: r, DerivedFrom: DerivedFrom{Report: "small.json", SHA256: "aa"}}
	b, err := DeriveBudgets([]FixtureMeasurement{small}, "2026-08-30")
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion != BudgetsSchemaVersion || b.ImmutableUntil != ImmutableUntil || b.Date != "2026-08-30" || !approx(b.HeadroomFactor, BudgetHeadroom) {
		t.Errorf("header = %+v", b)
	}
	s := b.Fixtures[FixtureSmall]
	if s.Status != StatusMeasured || s.Repo != "cobra" || s.DerivedFrom == nil || s.DerivedFrom.SHA256 != "aa" {
		t.Errorf("small = %+v", s)
	}
	// p95 is the worst indexed baseline (hybrid 4000us), never the oracle.
	if s.P95LatencyUS == nil || !approx(s.P95LatencyUS.Measured, 4000) || s.P95LatencyUS.Baseline != BaselineHybridV1 || !approx(s.P95LatencyUS.Budget, 8000) {
		t.Errorf("p95 = %+v", s.P95LatencyUS)
	}
	if s.IndexMS == nil || !approx(s.IndexMS.Measured, 500) || !approx(s.IndexMS.Budget, 1000) {
		t.Errorf("index = %+v", s.IndexMS)
	}
	if s.PeakRSSMB == nil || !approx(s.PeakRSSMB.Measured, 330) || !approx(s.PeakRSSMB.Budget, 660) {
		t.Errorf("rss = %+v (peak RSS is process-wide, so the max across all baselines counts)", s.PeakRSSMB)
	}
	for _, class := range []string{FixtureMedium, FixtureLarge} {
		f, ok := b.Fixtures[class]
		if !ok || f.Status != StatusUnknown || f.Reason == "" {
			t.Errorf("%s = %+v, want UNKNOWN with a reason", class, f)
		}
	}
	t.Run("every size class is measured when every class has a report (AC-8)", func(t *testing.T) {
		all := []FixtureMeasurement{
			{Class: FixtureSmall, Report: r, DerivedFrom: DerivedFrom{Report: "small.json", SHA256: "aa"}},
			{Class: FixtureMedium, Report: r, DerivedFrom: DerivedFrom{Report: "medium.json", SHA256: "bb"}},
			{Class: FixtureLarge, Report: r, DerivedFrom: DerivedFrom{Report: "large.json", SHA256: "cc"}},
		}
		b3, err := DeriveBudgets(all, "2026-08-30")
		if err != nil {
			t.Fatal(err)
		}
		for _, class := range FixtureClasses {
			f := b3.Fixtures[class]
			if f.Status != StatusMeasured || f.IndexMS == nil || f.P95LatencyUS == nil || f.PeakRSSMB == nil || f.DerivedFrom == nil {
				t.Errorf("%s = %+v, want measured with all three budget lines and a citation", class, f)
			}
		}
		if b3.Fixtures[FixtureLarge].DerivedFrom.SHA256 != "cc" {
			t.Errorf("large derived_from = %+v", b3.Fixtures[FixtureLarge].DerivedFrom)
		}
	})
	t.Run("an unknown fixture class is refused", func(t *testing.T) {
		if _, err := DeriveBudgets([]FixtureMeasurement{{Class: "huge", Report: r}}, "2026-08-30"); err == nil {
			t.Error("DeriveBudgets(huge) = nil error")
		}
	})
	t.Run("a report with no measured index figure yields no budget line, not a zero", func(t *testing.T) {
		r2 := syntheticReport()
		for i := range r2.Performance {
			r2.Performance[i].IndexMS = Unknown("x")
		}
		b2, err := DeriveBudgets([]FixtureMeasurement{{Class: FixtureSmall, Report: r2}}, "2026-08-30")
		if err != nil {
			t.Fatal(err)
		}
		if b2.Fixtures[FixtureSmall].IndexMS != nil {
			t.Errorf("index budget = %+v, want none", b2.Fixtures[FixtureSmall].IndexMS)
		}
	})
}
