package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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

// ac9ReportPath is the explicit named report the AC-9 gate reads.
// SW-263 review / item 6 binds the gate to a fixed report path rather
// than picking the latest by filesystem mtime — a fresh checkout can
// therefore gate a stale or foreign run if the orchestrator committed
// one with a different name. The path points at the SW-263 conformance
// re-run the orchestrator regenerates AFTER the conformance fix lands;
// the orchestrator updates this constant alongside its SHA, and a
// mismatch fails closed. Skipping is reserved for the no-such-file case.
const ac9ReportPath = "docs/eval/retrieval/runs/2026-08-31-conformance-local/cobra-v1-report.json"

// ac9CandidateSHA is the candidate SHA the AC-9 gate asserts the named
// report carries. The orchestrator updates this constant AFTER the
// conformance re-run lands; the test refuses to pass on a stale or
// foreign CandidateSHA — the gate is a property of the reviewed tree,
// not of whatever report the filesystem holds.
const ac9CandidateSHA = "e7a3c7b0285df1b00a595cc43914ee189f650741"

// ac9PlaceholderSHA is the sentinel value ac9CandidateSHA holds before
// the orchestrator has committed the AC-9 eval re-run. The gate
// refuses to pass while the placeholder is in place — a green suite
// that asserts nothing is the same failure mode the SW-263 reviewer
// already rejected twice (silent skip on a missing file; silent pass
// on a stale SHA). The placeholder is therefore a hard failure with a
// message that names the only legitimate fix path: update
// ac9CandidateSHA to the SHA the orchestrator just committed.
const ac9PlaceholderSHA = "PENDING_REVIEW_RUN_SHA"

// TestAC9Evidence_RoundTripsFromRaw is the fail-closed evidence-integrity
// check for the exact run selected by the AC-9 gate. A digest-consistent
// run.json is not sufficient: the indexed report must be the gate's named
// report, and every published hit list, metric and performance measure must
// reproduce from dataset.json and raw/. This test stays independently green
// when the evidence is sound even though the score gate below honestly fails.
func TestAC9Evidence_RoundTripsFromRaw(t *testing.T) {
	reportPath := resolveRepoPath(t, ac9ReportPath)
	dir := filepath.Dir(reportPath)
	run, err := ReadRunDir(dir)
	if err != nil {
		t.Fatalf("AC-9 evidence directory is unreadable: %v", err)
	}
	if run.Index.Report != filepath.Base(reportPath) {
		t.Fatalf("AC-9 evidence index names report %q, but the gate reads %q; one report artifact must serve both aggregation and gating", run.Index.Report, filepath.Base(reportPath))
	}
	agg := Reproduce(run)
	if agg.ExitCode() != ExitReproduced {
		t.Fatalf("AC-9 evidence does not round-trip: status=%s checked=%d reproduced=%d discrepant=%d unknown=%d discrepancies=%v",
			agg.Status, agg.Checked, agg.Reproduced, agg.Discrepant, agg.Unknown, agg.Discrepancies)
	}
}

// TestReport_MeetsAC9GateAgainstTargetsFile is the AC-9 comparison test: it
// loads the IMMUTABLE docs/eval/retrieval-targets.json (the SW-258 pin
// that this story may NOT edit) and the EXPLICIT NAMED AC-9 evaluation
// report at ac9ReportPath, and asserts:
//
//   - the report's CandidateSHA matches ac9CandidateSHA (the reviewed
//     SHA); a stale or foreign run fails closed;
//   - on every conceptual stratum the targets file lists (nl_behaviour,
//     architecture_flow), the fusion baseline's ndcg@10 over the dev split
//     meets or exceeds must_reach (best + 0.10, capped at the ceiling);
//   - on exact_identifier, the fusion baseline's Top-1 over the dev split
//     meets or exceeds the no-regression floor the targets file pins.
//
// Fail-closed posture (SW-263 review / item 6, second finding):
// a missing report, an unreadable report, an unparseable report, a
// version mismatch, a CandidateSHA mismatch, AND the placeholder
// ac9CandidateSHA ALL fail the test loudly. A passing gate that
// skipped its checks is the same defect the reviewer rejected on
// the missing-report path earlier in this track; extending it to
// every other way the gate could silently no-op is the same fix.
//
// The path and SHA are fixed. The orchestrator renames the report
// (and updates the SHA) only when a new AC-9 run lands. SW-263
// review / item 6 makes the gate a property of the reviewed tree,
// not of filesystem mtime.
//
// This is the test pattern AC-9 calls for ("extend the existing
// internal/eval/retrieval/targets_test.go pattern rather than inventing a
// parallel one"); the same fixtures and the same DeriveTargets derivation
// it pins are reused.
func TestReport_MeetsAC9GateAgainstTargetsFile(t *testing.T) {
	targetsPath := resolveRepoPath(t, "docs/eval/retrieval-targets.json")
	targetsRaw, err := os.ReadFile(targetsPath)
	if err != nil {
		t.Fatalf("read %s: %v", targetsPath, err)
	}
	var tg Targets
	if err := json.Unmarshal(targetsRaw, &tg); err != nil {
		t.Fatalf("json.Unmarshal targets(%s): %v", targetsPath, err)
	}
	if tg.FusionMinDelta != FusionMinDelta {
		t.Errorf("targets fusion_min_delta = %v, want %v (drift between files)", tg.FusionMinDelta, FusionMinDelta)
	}

	// The placeholder SHA must fail loudly before any file IO. A green
	// gate on PENDING_REVIEW_RUN_SHA is exactly the silent-pass defect
	// the reviewer rejected: the test would assert nothing about the
	// review, but the orchestrator would see PASS and ship the story.
	// Refuse the placeholder explicitly.
	if ac9CandidateSHA == ac9PlaceholderSHA {
		t.Fatalf("AC-9 gate CandidateSHA is still the placeholder %q. The orchestrator must update ac9CandidateSHA in internal/eval/retrieval/targets_test.go to the SHA of the committed AC-9 re-run BEFORE this gate can pass; a placeholder pass is the silent-skip defect the SW-263 reviewer already rejected.",
			ac9PlaceholderSHA)
	}

	reportPath := resolveRepoPath(t, ac9ReportPath)
	reportRaw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("AC-9 report %s unreadable: %v. The gate is a property of the reviewed tree (SW-263 review / item 6); a missing report fails closed — the orchestrator regenerates the report under the named path, not at the latest mtime, and a missing report is a build error, not a skip.",
			reportPath, err)
	}
	var rep Report
	if err := json.Unmarshal(reportRaw, &rep); err != nil {
		t.Fatalf("json.Unmarshal report(%s): %v", reportPath, err)
	}
	if err := CheckReportVersion(&rep); err != nil {
		t.Fatalf("report version: %v", err)
	}

	// Bind the gate to the reviewed candidate SHA. A stale or foreign
	// CandidateSHA fails closed: the report is whatever the filesystem
	// holds, but the gate is a property of the reviewed tree, not a
	// property of the filesystem (SW-263 review / item 6).
	if rep.Reproducible.CandidateSHA != ac9CandidateSHA {
		t.Fatalf("AC-9 gate CandidateSHA mismatch: report = %q, gate = %q. The report was generated against a different tree; re-run the eval against the reviewed commit or update the gate constant alongside the new report.",
			rep.Reproducible.CandidateSHA, ac9CandidateSHA)
	}

	// Confirm the report cites the dataset the targets were derived from.
	if rep.Reproducible.Dataset.ID != "cobra-v1" {
		t.Errorf("report dataset = %q, want cobra-v1", rep.Reproducible.Dataset.ID)
	}

	// Collect the per-baseline dev-split strata aggregates the test compares
	// against, indexed by baseline name. A baseline that did not run (status
	// != ok) is absent and the test fails with the typed reason.
	type devStrata struct {
		perStratum map[string]AggregateMetrics
	}
	per := map[Baseline]devStrata{}
	for _, b := range rep.Reproducible.Baselines {
		if b.Status != BaselineStatusOK {
			continue
		}
		var dev []QueryResult
		for _, q := range b.Queries {
			if q.Split == SplitDev {
				dev = append(dev, q)
			}
		}
		_, strata, _ := AggregateAll(dev, rep.Reproducible.TokenBudgets)
		per[b.Name] = devStrata{perStratum: strata}
	}

	// The targets file lists the conceptual strata fusion must improve on.
	for _, stratum := range tg.ConceptualStrata {
		st := tg.Strata[stratum]
		if st.FusionTarget == nil {
			t.Errorf("stratum %s: targets file has no fusion_target (the SW-258 derivation set one for every conceptual stratum)", stratum)
			continue
		}
		ft := st.FusionTarget
		// fusion is the headline metric. fusion+graph is reported for
		// visibility — it is NOT a target the targets file pins, so a
		// miss on it is informational, not a gate failure.
		for _, bname := range []Baseline{BaselineFusion, BaselineFusionGraph} {
			devStrat, ok := per[bname]
			if !ok {
				t.Errorf("stratum %s, baseline %s: missing from the report (the baseline did not run with status=ok)", stratum, bname)
				continue
			}
			agg := devStrat.perStratum[stratum]
			v, ok := agg.Metrics[ft.Metric]
			if !ok {
				t.Errorf("stratum %s, baseline %s: no %s in dev aggregate", stratum, bname, ft.Metric)
				continue
			}
			if bname == BaselineFusion {
				if v+1e-9 < ft.MustReach {
					t.Errorf("AC-9 MISS on %s: fusion %s = %.6f < must_reach %.6f (best=%.6f + min_delta=%.2f, ceiling=%v)",
						stratum, ft.Metric, v, ft.MustReach, ft.BestValue, ft.MinDelta, tg.Strata[stratum].Oracle[ft.Metric])
				}
			}
		}
	}

	// exact_identifier Top-1 must not regress.
	ei := tg.Strata[StratumExactIdentifier]
	if ei.NoRegression == nil {
		t.Errorf("stratum exact_identifier: targets file has no no_regression floor")
	} else {
		floor := ei.NoRegression.Floor
		for _, bname := range []Baseline{BaselineFusion, BaselineFusionGraph} {
			devStrat, ok := per[bname]
			if !ok {
				continue
			}
			agg := devStrat.perStratum[StratumExactIdentifier]
			top1, ok := agg.Metrics[MetricTop1]
			if !ok {
				continue
			}
			if top1+1e-9 < floor {
				t.Errorf("AC-9 REGRESSION on exact_identifier Top-1: baseline %s = %.4f < floor %.4f (best_baseline=%s)",
					bname, top1, floor, ei.NoRegression.Baseline)
			}
		}
	}
}

// listRunDirs lists every <date>-<runner>-local run directory under
// docs/eval/retrieval/runs/. Kept as a small helper the gate could
// fall back to (the named-path gate above is the one the SW-263 review
// requires; this list is for diagnostic messages only).
func listRunDirs(t *testing.T) []string {
	t.Helper()
	root := resolveRepoPath(t, "docs/eval/retrieval/runs")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), "-local") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// resolveRepoPath walks up from the test cwd to the directory holding
// go.mod and returns the absolute path of the given repo-relative path.
// It mirrors the helpers in engine/retrieval/byte_parity_test.go.
func resolveRepoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", dir)
		}
		dir = parent
	}
}
