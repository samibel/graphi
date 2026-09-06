package retrieval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// syntheticReport builds a report with known per-query scores so the
// derivation is checked against arithmetic, not against a live run.
//
// It is DEVELOPMENT-ONLY. It used to carry a holdout row (q2) to prove that
// the derivation filtered holdout rows away; since SW-282 the derivation
// REFUSES such a report instead, and syntheticReportWithHoldout below is the
// fixture for that refusal.
func syntheticReport() *Report { return syntheticReportWithSplits(false) }

// syntheticReportWithHoldout is the same report plus one holdout row.
func syntheticReportWithHoldout() *Report { return syntheticReportWithSplits(true) }

func syntheticReportWithSplits(withHoldout bool) *Report {
	mk := func(name Baseline, ndcgByQuery map[string]float64, top1ByQuery map[string]float64) BaselineResult {
		b := BaselineResult{Name: name, Status: BaselineStatusOK}
		queries := []struct{ id, stratum, split string }{
			{"q1", StratumExactIdentifier, SplitDev},
			{"q3", StratumNLBehaviour, SplitDev},
			{"q4", StratumNLBehaviour, SplitDev},
			{"q5", StratumArchitectureFlow, SplitDev},
			{"q6", StratumNoHit, SplitDev},
		}
		if withHoldout {
			queries = append(queries, struct{ id, stratum, split string }{"q2", StratumExactIdentifier, SplitHoldout})
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
	if tg.SchemaVersion != TargetsSchemaVersion || tg.Date != "2026-08-30" || tg.ImmutableUntil != TargetsImmutableUntil || tg.Notes != TargetsNotes {
		t.Errorf("header = %+v", tg)
	}
	if tg.DerivedFrom.Report != "docs/eval/retrieval/runs/x.json" || tg.DerivedFrom.SHA256 != "deadbeef" || tg.DerivedFrom.Repo != "cobra" || tg.DerivedFrom.DatasetSHA256 != "ds" {
		t.Errorf("derived_from = %+v", tg.DerivedFrom)
	}

	t.Run("targets are derived from the dev split only", func(t *testing.T) {
		// exact_identifier dev is q1 alone: lexical ndcg 1, hybrid 0.5.
		ei := tg.Strata[StratumExactIdentifier]
		if ei.DevQueries != 1 {
			t.Errorf("dev_queries = %d", ei.DevQueries)
		}
		if b := ei.Best[MetricNDCG10]; b.Baseline != BaselineLexical || !approx(b.Value, 1) {
			t.Errorf("best ndcg@10 = %+v, want lexical 1", b)
		}
	})
	t.Run("a report carrying a holdout row is REFUSED, not filtered (AC-4)", func(t *testing.T) {
		// The previous behaviour dropped holdout rows silently, which makes
		// "the holdout was never executed" and "the holdout was executed and
		// dropped" produce identical output.
		_, err := DeriveTargets(syntheticReportWithHoldout(), DerivedFrom{}, "2026-08-30")
		if err == nil {
			t.Fatal("DeriveTargets accepted a report carrying a holdout query result")
		}
		if !strings.Contains(err.Error(), "holdout") || !strings.Contains(err.Error(), "q2") {
			t.Errorf("the refusal does not name the holdout row: %v", err)
		}
	})
	t.Run("a report carrying a candidate-pipeline baseline is REFUSED (AC-4)", func(t *testing.T) {
		// A bar set to "the candidate plus 0.10" is not a bar. The exclusion
		// lives here, not in whatever command line happened to be used.
		for _, candidate := range CandidatePipelineBaselines {
			r2 := syntheticReport()
			r2.Reproducible.Baselines = append(r2.Reproducible.Baselines,
				BaselineResult{Name: candidate, Status: BaselineStatusOK})
			_, err := DeriveTargets(r2, DerivedFrom{}, "2026-08-30")
			if err == nil {
				t.Fatalf("DeriveTargets accepted a report carrying the candidate baseline %s", candidate)
			}
			if !strings.Contains(err.Error(), string(candidate)) {
				t.Errorf("the refusal does not name %s: %v", candidate, err)
			}
		}
	})
	t.Run("the coverage target is written in whole queries over the measured population", func(t *testing.T) {
		bc := tg.BundleCoverage
		if bc == nil {
			t.Fatal("no bundle_coverage object")
		}
		// The synthetic report measures q3 and q4 as dev/nl_behaviour.
		if bc.Population.N != 2 || bc.Threshold.CoveredQueriesRequired != 2 || bc.Threshold.MaxMisses != 0 {
			t.Errorf("bundle_coverage = %+v, want n=2, required=2, max_misses=0", bc)
		}
		if bc.Resolution != "1/2" || bc.TokenBudget != TaskContextTokenBudget || bc.CreditRule != TaskContextMatchingRule {
			t.Errorf("bundle_coverage = %+v", bc)
		}
	})
	t.Run("a report that measured no dev nl_behaviour query cannot record a coverage population", func(t *testing.T) {
		r2 := syntheticReport()
		for i := range r2.Reproducible.Baselines {
			var kept []QueryResult
			for _, q := range r2.Reproducible.Baselines[i].Queries {
				if q.Stratum != StratumNLBehaviour {
					kept = append(kept, q)
				}
			}
			r2.Reproducible.Baselines[i].Queries = kept
		}
		if _, err := DeriveTargets(r2, DerivedFrom{}, "2026-08-30"); err == nil {
			t.Error("DeriveTargets wrote a coverage target over an unmeasured population")
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
		if !json.Valid(raw) || !strings.Contains(string(raw), `"immutable_until": "`+TargetsImmutableUntil+`"`) {
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
	if b.SchemaVersion != BudgetsSchemaVersion || b.ImmutableUntil != BudgetsImmutableUntil || b.Date != "2026-08-30" || !approx(b.HeadroomFactor, BudgetHeadroom) {
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

// The gate's evidence used to be pinned by two constants in this file
// (ac9ReportPath, ac9CandidateSHA). They now live in the package itself
// (retrieval.GateReportPath, retrieval.GateCandidateSHA) because the release
// line evaluates the same file through `retrieval-eval -check-targets` and the
// release-gate `retrieval-targets` runner. One pin, so the PR suite and the
// release line cannot disagree about which run is the evidence.

// targetsGateExpectationsPath is the checked-in per-target verdict this test
// compares against.
//
// It exists because SW-282's recalibration is EXPECTED to produce a miss the
// shipped pipeline cannot close in this story, and there are only three ways to
// handle that in a PR-time test: pretend it passes (an exception — deleted
// here), leave the suite permanently red (which trains everyone to ignore it),
// or record the verdict and fail on DRIFT IN EITHER DIRECTION. This is the
// third. A bar that starts passing is as much a change to this record as one
// that starts failing, and the recovery story updates it deliberately.
const targetsGateExpectationsPath = "docs/eval/retrieval/targets-gate-expectations.json"

// The material facts of the recorded verdict, asserted in code as well as in
// the JSON, so regenerating the expectations file cannot quietly launder a
// changed outcome past review.
//
// The two *Observed constants were, in an earlier draft, only ever passed to
// t.Logf. That made the claim above false for them: a gate report edited to a
// different but still-missing architecture_flow value regenerated
// targets-gate-expectations.json and exited 0. They are compared for real
// below. Note what the assertion does and does not buy: it binds the digits
// this test records to the digits the gate prints from the committed report.
// What binds those digits to the WORLD is TestAC9Evidence_RoundTripsFromRaw,
// which recomputes every published metric from raw/ and dataset.json — that is
// the test an edited report fails, and it is the guard to cite.
const (
	gateExpectedMissCount                 = 3
	gateExpectedFirstMiss                 = "architecture_flow fusion_target"
	gateExpectedArchitectureFlowMustReach = 0.4578575262772977
	gateExpectedArchitectureFlowObserved  = 0.32777888533499866
	gateExpectedExactIdentifierFloor      = 1.0
	gateExpectedExactIdentifierObserved   = 0.75
)

// TargetsGateExpectations is the checked-in verdict record.
type TargetsGateExpectations struct {
	SchemaVersion     int                `json:"schema_version"`
	Story             string             `json:"story"`
	Note              string             `json:"note"`
	TargetsFileSHA256 string             `json:"targets_file_sha256"`
	Result            *TargetCheckResult `json:"result"`
}

const targetsGateExpectationsNote = "SW-282 recorded per-target verdict of docs/eval/retrieval-targets.json against the committed gate " +
	"report. The recalibrated architecture_flow bar and the recalibrated exact_identifier Top-1 floor are MISSED by the shipped " +
	"pipeline, and the qrel-blind smoke evaluation recorded RELEASE: NO. Those misses are recorded here, not fixed and not excepted: " +
	"SW-282 sets targets and may not change retrieval behaviour, and the release-gate retrieval-targets runner is RED until the " +
	"recovery story closes them. TestTargets_GateVerdictMatchesCheckedInExpectations fails on drift in EITHER direction — a bar that " +
	"starts passing must be recorded here deliberately."

// TestAC9Evidence_RoundTripsFromRaw is the fail-closed evidence-integrity
// check for the exact run the targets gate reads. A digest-consistent run.json
// is not sufficient: the indexed report must be the gate's named report, and
// every published hit list, metric and performance measure must reproduce from
// dataset.json and raw/. This test stays independently green when the evidence
// is sound even though the score gate below honestly records misses.
func TestAC9Evidence_RoundTripsFromRaw(t *testing.T) {
	reportPath := resolveRepoPath(t, GateReportPath)
	dir := filepath.Dir(reportPath)
	run, err := ReadRunDir(dir)
	if err != nil {
		t.Fatalf("targets-gate evidence directory is unreadable: %v", err)
	}
	if run.Index.Report != filepath.Base(reportPath) {
		t.Fatalf("targets-gate evidence index names report %q, but the gate reads %q; one report artifact must serve both aggregation and gating", run.Index.Report, filepath.Base(reportPath))
	}
	agg := Reproduce(run)
	if agg.ExitCode() != ExitReproduced {
		t.Fatalf("targets-gate evidence does not round-trip: status=%s checked=%d reproduced=%d discrepant=%d unknown=%d discrepancies=%v",
			agg.Status, agg.Checked, agg.Reproduced, agg.Discrepant, agg.Unknown, agg.Discrepancies)
	}
}

// TestTargets_GateVerdictMatchesCheckedInExpectations is the re-expressed AC-9
// gate (SW-282 AC-5).
//
// What changed and why: the old test compared the shipped pipeline against the
// targets file and accepted one stratum's miss through a numeric tolerance
// (0.00085) approved as an owner exception. That tolerance was four orders of
// magnitude below what a five-query stratum can resolve, so it asserted
// nothing about the world; it has been deleted rather than restated. What
// replaces it is a recorded verdict compared for drift in both directions.
func TestTargets_GateVerdictMatchesCheckedInExpectations(t *testing.T) {
	root := repoRootForTest(t)
	res, err := CheckTargets(TargetCheckInputs{
		RepoRoot:            root,
		ReportPath:          GateReportPath,
		RequireCandidateSHA: GateCandidateSHA,
	})
	if err != nil {
		t.Fatalf("CheckTargets: %v. The gate is a property of the reviewed tree: a missing, unreadable, foreign or stale artifact fails closed, it does not skip.", err)
	}

	targetsRaw, err := os.ReadFile(filepath.Join(root, "docs", "eval", "retrieval-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := &TargetsGateExpectations{
		SchemaVersion: 1, Story: "SW-282", Note: targetsGateExpectationsNote,
		TargetsFileSHA256: SHA256Hex(targetsRaw), Result: res,
	}
	raw, err := marshalStable(want)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(targetsGateExpectationsPath))
	if os.Getenv("SW282_WRITE_GATE_EXPECTATIONS") == "1" {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", targetsGateExpectationsPath)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", targetsGateExpectationsPath, err)
	}
	if string(onDisk) != string(raw) {
		t.Errorf("the per-target verdict has DRIFTED from %s.\n\ncomputed:\n%s\n\nrecorded:\n%s\n\nDrift in either direction fails: a bar that starts passing is a change to this record too. Recompute with SW282_WRITE_GATE_EXPECTATIONS=1 and review the diff.",
			targetsGateExpectationsPath, raw, onDisk)
	}

	// The material facts, asserted in code so a blind regeneration of the file
	// above cannot launder a changed outcome past review.
	if res.MissCount != gateExpectedMissCount || res.FirstMissName != gateExpectedFirstMiss {
		t.Errorf("gate verdict = %d miss(es), first %q; recorded %d and %q", res.MissCount, res.FirstMissName, gateExpectedMissCount, gateExpectedFirstMiss)
	}
	byName := map[string]TargetCheck{}
	for _, c := range res.Checks {
		byName[c.Name] = c
	}
	for name, wantMet := range map[string]bool{
		StratumNLBehaviour + " fusion_target":      true,
		StratumArchitectureFlow + " fusion_target": false,
		StratumExactIdentifier + " no_regression":  false,
		"bundle_coverage":                          true,
		"qrel_blind_smoke":                         false,
	} {
		c, ok := byName[name]
		if !ok {
			t.Errorf("target %q was not checked at all; an unchecked target is not a met target", name)
			continue
		}
		if c.Met != wantMet {
			t.Errorf("target %q: met=%v, recorded %v (required %s; observed %s; %s)", name, c.Met, wantMet, c.Required, c.Observed, c.Detail)
		}
	}

	// The two recalibrated numbers themselves. SW-263's recorded MISS of
	// -0.00084 stands as history and is NOT retroactively satisfied by the new
	// bar; this is a different bar, measured against a different comparator set
	// on a different dataset, and it is missed by 0.130 — twenty-six times one
	// query's influence on a five-query stratum.
	var tg Targets
	if err := json.Unmarshal(targetsRaw, &tg); err != nil {
		t.Fatal(err)
	}
	af := tg.Strata[StratumArchitectureFlow]
	if af.FusionTarget == nil || !approx(af.FusionTarget.MustReach, gateExpectedArchitectureFlowMustReach) {
		t.Errorf("architecture_flow must_reach = %+v, recorded %.17g", af.FusionTarget, gateExpectedArchitectureFlowMustReach)
	}
	if af.DevQueries != 5 {
		t.Errorf("architecture_flow dev_queries = %d, want 5; the aggregate is a mean over five per-query values, so a shortfall smaller than one query's influence (1/5) is not evidence of a difference", af.DevQueries)
	}
	ei := tg.Strata[StratumExactIdentifier]
	if ei.NoRegression == nil || !approx(ei.NoRegression.Floor, gateExpectedExactIdentifierFloor) {
		t.Errorf("exact_identifier no_regression = %+v, recorded floor %.17g", ei.NoRegression, gateExpectedExactIdentifierFloor)
	}
	// The observed values, ASSERTED against the digits the gate itself
	// rendered. Compared as the printed strings because those digits are the
	// record: %.17g round-trips a float64 exactly, so an inequality here is a
	// changed observation, never a formatting artefact.
	for _, want := range []struct {
		target string
		value  float64
	}{
		{StratumArchitectureFlow + " fusion_target", gateExpectedArchitectureFlowObserved},
		{StratumExactIdentifier + " no_regression", gateExpectedExactIdentifierObserved},
	} {
		got := byName[want.target].Observed
		if rendered := fmt.Sprintf("%.17g", want.value); got != rendered {
			t.Errorf("target %q: observed %s, recorded %s. The recorded observation is a fact about the committed gate report; a changed report is a new run, not an edit.", want.target, got, rendered)
		}
	}

	t.Logf("recorded MISS on %s: %s %.17g < must_reach %.17g over %d dev queries (resolution 1/%d)",
		StratumArchitectureFlow, GateBaseline, gateExpectedArchitectureFlowObserved, gateExpectedArchitectureFlowMustReach, af.DevQueries, af.DevQueries)
	t.Logf("recorded MISS on %s: %s top1 %.17g < floor %.17g over %d dev queries (resolution 1/%d; the shortfall is exactly one query)",
		StratumExactIdentifier, GateBaseline, gateExpectedExactIdentifierObserved, gateExpectedExactIdentifierFloor, ei.DevQueries, ei.DevQueries)
}

// TestTargets_NoNumericShortfallExceptionRemains is AC-5's absence check.
// Deleting the constant is not enough — the failure mode is a re-added
// exception under a different name — so the whole package source is scanned.
func TestTargets_NoNumericShortfallExceptionRemains(t *testing.T) {
	// Built by concatenation so this test's own source does not match itself.
	needles := []string{
		"ac9ArchitectureFlowApprovedShort" + "fall",
		"ac9Approved" + "Exception",
		"ApprovedShort" + "fall",
		"approvedShort" + "fall",
		"shortfallToler" + "ance",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, needle := range needles {
			if strings.Contains(string(raw), needle) {
				t.Errorf("%s reintroduces a numeric-shortfall exception (%q). A bar whose miss is below what its own sample can resolve is not excepted, it is deleted; a bar the pipeline genuinely misses is RECORDED as a miss in %s.",
					e.Name(), needle, targetsGateExpectationsPath)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no package source files; the absence check asserted nothing")
	}
}

// TestTargets_QualityComparisonHasNoEpsilon is AC-5's operational half.
//
// Deleting a named shortfall constant is worth nothing if the comparison
// operator grants one instead. An earlier draft of targetcheck.go added
// floatComparisonEpsilon = 1e-9 to the observed value before BOTH quality
// comparisons, described as a guard on the last bit of a double. It was not
// one: a target set 5e-10 above the observed architecture_flow value was
// reported PASS. That is a working numeric-shortfall exception — six orders of
// magnitude wider than one ULP near 0.33, and written where no name-based
// absence check could ever find it.
//
// A quality bar is compared exactly. A target above the observed value misses,
// however narrowly; a target the observed value exactly equals is reached.
func TestTargets_QualityComparisonHasNoEpsilon(t *testing.T) {
	root := repoRootForTest(t)
	targetsRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(TargetsFilePath)))
	if err != nil {
		t.Fatal(err)
	}
	reportAbs := resolveRepoPath(t, GateReportPath)
	coverageAbs := resolveRepoPath(t, BundleCoverageMeasurementPath)
	smokeAbs := resolveRepoPath(t, QrelBlindSmokeOutcomePath)

	const name = StratumArchitectureFlow + " fusion_target"
	const observed = gateExpectedArchitectureFlowObserved

	// The checked-in gate report and the checked-in supporting artifacts are
	// read unchanged; only the bar moves, and only in a temporary copy of the
	// targets file under a temporary root.
	checkWithBar := func(t *testing.T, mustReach float64) TargetCheck {
		t.Helper()
		var tg Targets
		if err := json.Unmarshal(targetsRaw, &tg); err != nil {
			t.Fatal(err)
		}
		st := tg.Strata[StratumArchitectureFlow]
		if st.FusionTarget == nil {
			t.Fatalf("%s carries no fusion_target", StratumArchitectureFlow)
		}
		ft := *st.FusionTarget
		ft.MustReach = mustReach
		st.FusionTarget = &ft
		tg.Strata[StratumArchitectureFlow] = st
		raw, err := marshalStable(&tg)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		dst := filepath.Join(dir, filepath.FromSlash(TargetsFilePath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := CheckTargets(TargetCheckInputs{
			RepoRoot: dir, ReportPath: reportAbs, CoveragePath: coverageAbs,
			SmokeOutcomePath: smokeAbs, RequireCandidateSHA: GateCandidateSHA,
		})
		if err != nil {
			t.Fatalf("CheckTargets: %v", err)
		}
		for _, c := range res.Checks {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("target %q was not checked", name)
		return TargetCheck{}
	}

	t.Run("a bar 5e-10 above the observed value MISSES", func(t *testing.T) {
		bar := observed + 5e-10
		if bar == observed {
			t.Fatalf("the constructed bar %.17g is not distinguishable from the observed value; the case asserts nothing", bar)
		}
		if c := checkWithBar(t, bar); c.Met {
			t.Errorf("target %q reported MET at must_reach %.17g against observed %.17g. A shortfall of 5e-10 is a shortfall: the comparison must be exact, not epsilon-widened. (required %s; observed %s)",
				name, bar, observed, c.Required, c.Observed)
		}
	})

	t.Run("a bar one ULP above the observed value MISSES", func(t *testing.T) {
		bar := math.Nextafter(observed, math.Inf(1))
		if c := checkWithBar(t, bar); c.Met {
			t.Errorf("target %q reported MET at must_reach %.17g against observed %.17g; the next representable double above the observation is still above it", name, bar, observed)
		}
	})

	t.Run("a bar exactly equal to the observed value is REACHED", func(t *testing.T) {
		// The control. Without it the two cases above would also pass if the
		// comparison were broken in the opposite direction.
		if c := checkWithBar(t, observed); !c.Met {
			t.Errorf("target %q reported MISS at must_reach exactly %.17g; >= means the bar is reached when it is met exactly (required %s; observed %s; %s)",
				name, observed, c.Required, c.Observed, c.Detail)
		}
	})
}

// TestTargetsFile_ShapeAndImmutability is AC-2 and AC-6 against the checked-in
// file rather than against a synthetic derivation.
func TestTargetsFile_ShapeAndImmutability(t *testing.T) {
	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "eval", "retrieval-targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tg Targets
	if err := json.Unmarshal(raw, &tg); err != nil {
		t.Fatal(err)
	}

	// The date pin moves with the recalibration, deliberately.
	if tg.Date != "2026-09-06" {
		t.Errorf("targets date = %q, want the recalibration date 2026-09-06", tg.Date)
	}
	if tg.ImmutableUntil == "SW-266" {
		t.Errorf("immutable_until is still SW-266; that milestone is spent — SW-282 rewrote this file under it")
	}
	if tg.ImmutableUntil != TargetsImmutableUntil {
		t.Errorf("immutable_until = %q, want the constant %q", tg.ImmutableUntil, TargetsImmutableUntil)
	}
	if tg.Notes != TargetsNotes {
		t.Errorf("the file's notes have drifted from retrieval.TargetsNotes; the prose and the constant must be the same sentence")
	}
	if tg.DerivedFrom.Report == "" || tg.DerivedFrom.SHA256 == "" {
		t.Errorf("derived_from = %+v, want a report path and its sha256", tg.DerivedFrom)
	}
	// The notes must actually carry the rule they claim to (AC-6).
	for _, phrase := range []string{
		"lexical, hybrid_v1, semantic_name_only",
		"chunk_only, fusion and semantic_first",
		"PLUS fusion_min_delta 0.10, CAPPED AT THE ORACLE CEILING",
		"no_regression floor",
		"bundle_coverage",
		"-check-targets",
	} {
		if !strings.Contains(tg.Notes, phrase) {
			t.Errorf("the targets notes do not state %q", phrase)
		}
	}

	t.Run("the derivation cited a comparator-only report", func(t *testing.T) {
		for _, b := range tg.Baselines {
			for _, candidate := range CandidatePipelineBaselines {
				if b == candidate {
					t.Errorf("the targets file admits the candidate pipeline %s as a single baseline", b)
				}
			}
		}
	})

	t.Run("the gate report is not the report the targets were derived from (AC-8)", func(t *testing.T) {
		gateRaw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(GateReportPath)))
		if err != nil {
			t.Fatal(err)
		}
		if got := SHA256Hex(gateRaw); got == tg.DerivedFrom.SHA256 {
			t.Errorf("the gate report and the derivation report are the same bytes (%s); a threshold set and passed on the same observations is not a gate", got)
		}
	})

	t.Run("bundle_coverage states the bar in whole queries and nothing else (AC-2)", func(t *testing.T) {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		bcRaw, ok := top["bundle_coverage"]
		if !ok {
			t.Fatal("the targets file carries no bundle_coverage object")
		}
		var bc map[string]any
		dec := json.NewDecoder(strings.NewReader(string(bcRaw)))
		dec.UseNumber()
		if err := dec.Decode(&bc); err != nil {
			t.Fatal(err)
		}
		wantKeys := []string{"credit_rule", "population", "resolution", "resolution_note", "supplements", "threshold", "threshold_source", "token_budget"}
		got := make([]string, 0, len(bc))
		for k := range bc {
			got = append(got, k)
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(wantKeys, ",") {
			t.Errorf("bundle_coverage keys = %v, want %v", got, wantKeys)
		}
		// Every number anywhere inside the object is an integer: no decimal
		// share, no percentage, no fractional threshold.
		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch t2 := v.(type) {
			case map[string]any:
				for k, child := range t2 {
					walk(path+"."+k, child)
				}
			case []any:
				for i, child := range t2 {
					walk(fmt.Sprintf("%s[%d]", path, i), child)
				}
			case json.Number:
				if _, err := t2.Int64(); err != nil {
					t.Errorf("bundle_coverage%s = %s is not an integer; a threshold on a %s population may not be written as a decimal share", path, t2, "six-query")
				}
			}
		}
		walk("", bc)

		threshold, _ := bc["threshold"].(map[string]any)
		if len(threshold) != 2 {
			t.Errorf("bundle_coverage.threshold = %v, want exactly covered_queries_required and max_misses", threshold)
		}
		if tg.BundleCoverage == nil {
			t.Fatal("bundle_coverage did not decode into the typed struct")
		}
		if tg.BundleCoverage.Threshold.CoveredQueriesRequired != 6 || tg.BundleCoverage.Threshold.MaxMisses != 0 {
			t.Errorf("bundle_coverage.threshold = %+v, want 6 required and 0 misses", tg.BundleCoverage.Threshold)
		}
		if tg.BundleCoverage.Population.N != 6 || tg.BundleCoverage.TokenBudget != 1200 || tg.BundleCoverage.Resolution != "1/6" {
			t.Errorf("bundle_coverage population/budget/resolution = %+v", tg.BundleCoverage)
		}
		wantIDs := []string{"cb-11", "cb-12", "cb-13", "cb-14", "cb-15", "cb-16"}
		if strings.Join(tg.BundleCoverage.Population.QueryIDs, ",") != strings.Join(wantIDs, ",") {
			t.Errorf("bundle_coverage population = %v, want %v (the population SW-264 measured, unchanged in cobra-v2)", tg.BundleCoverage.Population.QueryIDs, wantIDs)
		}
		if !strings.Contains(tg.BundleCoverage.Supplements, "SUPPLEMENTS and does not replace") {
			t.Errorf("bundle_coverage.supplements does not state that it supplements the smoke gate: %q", tg.BundleCoverage.Supplements)
		}
	})
}

// TestCheckTargets_EachGateBites builds a failure for every target
// individually. A gate seen only passing is a gate nobody has seen bite.
func TestCheckTargets_EachGateBites(t *testing.T) {
	root := repoRootForTest(t)

	// A world in which every target is met: the recorded misses are turned
	// into passes by editing the ARTIFACTS in a temporary copy of the tree,
	// never by relaxing the gate.
	base := newSyntheticGateWorld(t, root)

	t.Run("all green", func(t *testing.T) {
		res := base.check(t, "")
		if res.MissCount != 0 {
			t.Fatalf("the synthetic all-green world still reports %d miss(es): %s", res.MissCount, FormatTargetCheck(res))
		}
	})

	t.Run("a conceptual stratum below its bar is a MISS", func(t *testing.T) {
		w := base.clone(t)
		w.setStratumNDCG(t, StratumArchitectureFlow, 0.01)
		w.expectMiss(t, StratumArchitectureFlow+" fusion_target")
	})
	t.Run("exact_identifier Top-1 below the floor is a MISS", func(t *testing.T) {
		w := base.clone(t)
		w.setExactIdentifierTop1(t, 0)
		w.expectMiss(t, StratumExactIdentifier+" no_regression")
	})
	t.Run("a coverage miss is a MISS and the threshold is not lowered to it", func(t *testing.T) {
		w := base.clone(t)
		w.setCoverage(t, 5, 6)
		c := w.expectMiss(t, "bundle_coverage")
		if !strings.Contains(c.Detail, "not lowered to the observed value") {
			t.Errorf("coverage miss detail = %q", c.Detail)
		}
	})
	t.Run("an aggregate that disagrees with its own per-query records is a MISS", func(t *testing.T) {
		// The aggregate is a summary, not a second source. Reading only the
		// summary lets an edited per-query `covered` leave a stale 6/6
		// standing and the gate print PASS over a measurement that says a
		// query was missed.
		w := base.clone(t)
		w.setPerQueryCovered(t, 0, false)
		c := w.expectMiss(t, "bundle_coverage")
		if !strings.Contains(c.Detail, "per-query records") {
			t.Errorf("inconsistent-aggregate detail = %q", c.Detail)
		}
	})
	t.Run("an absent coverage measurement is a MISS, not a pass", func(t *testing.T) {
		w := base.clone(t)
		w.remove(t, w.coverageRel)
		c := w.expectMiss(t, "bundle_coverage")
		if !strings.Contains(c.Detail, "An absent measurement is a MISS") {
			t.Errorf("absent-coverage detail = %q", c.Detail)
		}
	})
	t.Run("a smoke evaluation that did not release is a MISS while coverage is green", func(t *testing.T) {
		w := base.clone(t)
		w.setSmokeRelease(t, ReleaseNo)
		w.expectMiss(t, "qrel_blind_smoke")
		if c := w.check(t, "").checkNamed("bundle_coverage"); !c.Met {
			t.Error("coverage should still be met; the two gates answer different questions")
		}
	})
	t.Run("an absent smoke evaluation is a MISS, not a pass", func(t *testing.T) {
		w := base.clone(t)
		w.remove(t, w.smokeRel)
		w.expectMiss(t, "qrel_blind_smoke")
	})
	t.Run("the derivation report may not be used as the gate report", func(t *testing.T) {
		w := base.clone(t)
		if _, err := CheckTargets(TargetCheckInputs{RepoRoot: w.root, ReportPath: w.derivationRel}); err == nil {
			t.Fatal("CheckTargets graded the targets against the report they were derived from")
		}
	})
	t.Run("a foreign candidate SHA fails closed", func(t *testing.T) {
		w := base.clone(t)
		if _, err := CheckTargets(TargetCheckInputs{RepoRoot: w.root, ReportPath: w.reportRel, RequireCandidateSHA: "some-other-tree"}); err == nil {
			t.Fatal("CheckTargets accepted a report from a tree the gate is not bound to")
		}
	})
}

// syntheticGateWorld is a temporary repository-shaped directory holding a
// targets file, a derivation report, a gate report, a coverage measurement and
// a smoke outcome that all agree. Each subtest breaks exactly one of them.
type syntheticGateWorld struct {
	root          string
	reportRel     string
	derivationRel string
	coverageRel   string
	smokeRel      string
}

func newSyntheticGateWorld(t *testing.T, repoRoot string) *syntheticGateWorld {
	t.Helper()
	w := &syntheticGateWorld{
		root:          t.TempDir(),
		reportRel:     "runs/gate-report.json",
		derivationRel: "runs/derivation-report.json",
		coverageRel:   BundleCoverageMeasurementPath,
		smokeRel:      QrelBlindSmokeOutcomePath,
	}

	// The derivation report: comparators only, development only.
	derivation := syntheticReport()
	tg, err := DeriveTargets(derivation, DerivedFrom{Report: w.derivationRel}, "2026-09-06")
	if err != nil {
		t.Fatal(err)
	}
	derivationRaw, err := MarshalReport(derivation)
	if err != nil {
		t.Fatal(err)
	}
	tg.DerivedFrom.SHA256 = SHA256Hex(derivationRaw)
	w.write(t, w.derivationRel, derivationRaw)

	targetsRaw, err := MarshalTargets(tg)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, TargetsFilePath, targetsRaw)

	// The gate report: a different execution, carrying the candidate pipeline,
	// scoring at the ceiling so every stratum target is met.
	gate := syntheticReport()
	perfect := BaselineResult{Name: GateBaseline, Status: BaselineStatusOK}
	for _, q := range gate.Reproducible.Baselines[0].Queries {
		q.Metrics = QueryMetrics{Scored: q.Stratum != StratumNoHit, NDCG10: 1, Top1: 1, Recall5: 1, Recall10: 1, MRR10: 1,
			RecallAtTokens: map[string]float64{"600": 1}}
		perfect.Queries = append(perfect.Queries, q)
	}
	perfect.Overall, perfect.Strata, perfect.Splits = AggregateAll(perfect.Queries, []int{600})
	gate.Reproducible.Baselines = append(gate.Reproducible.Baselines, perfect)
	gateRaw, err := MarshalReport(gate)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, w.reportRel, gateRaw)

	// A coverage measurement that covers the whole population.
	w.writeCoverage(t, tg.BundleCoverage.Population.N, tg.BundleCoverage.Population.N, tg.BundleCoverage.Population.QueryIDs)
	// A smoke outcome that released.
	w.writeSmoke(t, ReleaseYes)
	return w
}

func (w *syntheticGateWorld) clone(t *testing.T) *syntheticGateWorld {
	t.Helper()
	out := &syntheticGateWorld{root: t.TempDir(), reportRel: w.reportRel, derivationRel: w.derivationRel,
		coverageRel: w.coverageRel, smokeRel: w.smokeRel}
	for _, rel := range []string{TargetsFilePath, w.reportRel, w.derivationRel, w.coverageRel, w.smokeRel} {
		raw, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		out.write(t, rel, raw)
	}
	return out
}

func (w *syntheticGateWorld) write(t *testing.T, rel string, raw []byte) {
	t.Helper()
	p := filepath.Join(w.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *syntheticGateWorld) remove(t *testing.T, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(w.root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

func (w *syntheticGateWorld) writeCoverage(t *testing.T, covered, total int, ids []string) {
	t.Helper()
	// The per-query records are written too, and they agree with the
	// aggregate: the aggregate is a summary of them, and the gate recounts.
	queries := make([]TaskContextQueryResult, 0, total)
	for i, id := range ids {
		queries = append(queries, TaskContextQueryResult{ID: id, Covered: i < covered})
	}
	m := TaskContextMeasurement{
		FormatVersion: TaskContextFormatVersion, HarnessVersion: TaskContextHarnessVersion, ScorerVersion: TaskContextScorerVersion,
		EligibleForThreshold: true,
		Dataset:              TaskContextDatasetRef{QueryIDs: ids, QueryCount: total},
		Bundle:               TaskContextBundleRef{TokenBudget: TaskContextTokenBudget},
		Aggregate:            TaskContextAggregate{CoveredQueries: covered, TotalQueries: total},
		Queries:              queries,
	}
	raw, err := marshalStable(m)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, w.coverageRel, raw)
}

func (w *syntheticGateWorld) writeSmoke(t *testing.T, release string) {
	t.Helper()
	o := EvaluationOutcome{
		ContractVersion: QrelBlindSmokeContractVersion, Evaluation: QrelBlindSmokeEvaluationName,
		N: 64, K: 56, PassCount: 60, Release: release,
		Reasons: []string{"synthetic fixture"},
	}
	raw, err := marshalStable(o)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, w.smokeRel, raw)
}

// setPerQueryCovered edits ONE per-query record in the coverage measurement and
// leaves the aggregate alone, which is the shape the gate must not accept.
func (w *syntheticGateWorld) setPerQueryCovered(t *testing.T, i int, covered bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(w.coverageRel)))
	if err != nil {
		t.Fatal(err)
	}
	var m TaskContextMeasurement
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if i >= len(m.Queries) {
		t.Fatalf("coverage measurement carries %d per-query records, wanted to edit index %d", len(m.Queries), i)
	}
	m.Queries[i].Covered = covered
	out, err := marshalStable(m)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, w.coverageRel, out)
}

func (w *syntheticGateWorld) setSmokeRelease(t *testing.T, release string) {
	t.Helper()
	w.writeSmoke(t, release)
}

func (w *syntheticGateWorld) setCoverage(t *testing.T, covered, total int) {
	t.Helper()
	var tg Targets
	raw, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(TargetsFilePath)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &tg); err != nil {
		t.Fatal(err)
	}
	ids := tg.BundleCoverage.Population.QueryIDs
	tg.BundleCoverage.Population.N = total
	tg.BundleCoverage.Threshold.CoveredQueriesRequired = total
	for len(ids) < total {
		ids = append(ids, fmt.Sprintf("pad-%d", len(ids)))
	}
	tg.BundleCoverage.Population.QueryIDs = ids[:total]
	out, err := MarshalTargets(&tg)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, TargetsFilePath, out)
	w.writeCoverage(t, covered, total, tg.BundleCoverage.Population.QueryIDs)
}

// setStratumNDCG rewrites the gate report so the gated baseline scores `v` on
// every query of one stratum.
func (w *syntheticGateWorld) setStratumNDCG(t *testing.T, stratum string, v float64) {
	t.Helper()
	w.mutateGateReport(t, func(b *BaselineResult) {
		for i := range b.Queries {
			if b.Queries[i].Stratum == stratum {
				b.Queries[i].Metrics.NDCG10 = v
			}
		}
	})
}

func (w *syntheticGateWorld) setExactIdentifierTop1(t *testing.T, v float64) {
	t.Helper()
	w.mutateGateReport(t, func(b *BaselineResult) {
		for i := range b.Queries {
			if b.Queries[i].Stratum == StratumExactIdentifier {
				b.Queries[i].Metrics.Top1 = v
			}
		}
	})
}

func (w *syntheticGateWorld) mutateGateReport(t *testing.T, fn func(*BaselineResult)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(w.reportRel)))
	if err != nil {
		t.Fatal(err)
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	for i := range rep.Reproducible.Baselines {
		if rep.Reproducible.Baselines[i].Name != GateBaseline {
			continue
		}
		b := &rep.Reproducible.Baselines[i]
		fn(b)
		b.Overall, b.Strata, b.Splits = AggregateAll(b.Queries, []int{600})
	}
	out, err := MarshalReport(&rep)
	if err != nil {
		t.Fatal(err)
	}
	w.write(t, w.reportRel, out)
}

func (w *syntheticGateWorld) check(t *testing.T, reportRel string) *TargetCheckResult {
	t.Helper()
	if reportRel == "" {
		reportRel = w.reportRel
	}
	res, err := CheckTargets(TargetCheckInputs{RepoRoot: w.root, ReportPath: reportRel})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (w *syntheticGateWorld) expectMiss(t *testing.T, name string) TargetCheck {
	t.Helper()
	res := w.check(t, "")
	c := res.checkNamed(name)
	if c.Name == "" {
		t.Fatalf("target %q was not checked:\n%s", name, FormatTargetCheck(res))
	}
	if c.Met {
		t.Fatalf("target %q reported PASS on a world built to break it:\n%s", name, FormatTargetCheck(res))
	}
	if res.MissCount == 0 || res.FirstMissName == "" {
		t.Fatalf("the result reports no miss:\n%s", FormatTargetCheck(res))
	}
	return c
}

func (r *TargetCheckResult) checkNamed(name string) TargetCheck {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return TargetCheck{}
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
