package retrieval

// Enforcing the targets (SW-282 AC-7).
//
// docs/eval/retrieval-targets.json used to be read by exactly one test. A file
// no command evaluates is a note, not a target, so this is the evaluator both
// `go run ./cmd/retrieval-eval -check-targets` and the release-gate
// `retrieval-targets` runner call. One implementation, so the release line and
// the PR suite cannot disagree about what the file says.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TargetsFilePath is the checked-in targets file, relative to the repository
// root.
const TargetsFilePath = "docs/eval/retrieval-targets.json"

// GateReportPath is the EXPLICIT named report the targets gate reads.
//
// It is a fixed path rather than "the newest run directory": a gate that picks
// its own evidence by filesystem mtime can be fed a stale or foreign run by a
// fresh checkout (SW-263 review / item 6). The orchestrator moves this constant
// and GateCandidateSHA together when a new gating run lands, and a mismatch
// fails closed.
//
// It is NOT the report the targets were derived from. CheckTargets refuses a
// report whose digest equals derived_from.sha256, because a bar checked against
// the observations it was computed from is arithmetic, not a gate (SW-282 AC-8).
const GateReportPath = "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/cobra-v2-dev-report.json"

// GateCandidateSHA is the candidate SHA the gate asserts the named report
// carries. A stale or foreign report fails closed.
const GateCandidateSHA = "e824197cf4610e3824587e0cb76dcb7a17d9410f+dirty"

// GateBaseline is the SHIPPED pipeline the targets are enforced against.
const GateBaseline = BaselineSemanticFirst

// BundleCoverageMeasurementPath is the task_context/2 coverage measurement the
// bundle_coverage target is enforced against.
const BundleCoverageMeasurementPath = "docs/eval/retrieval/runs/2026-09-06-sw282-coverage-local/measurement.json"

// QrelBlindSmokeOutcomePath is the qrel-blind bundle-sufficiency smoke
// evaluation the coverage gate SUPPLEMENTS. Coverage says the reviewed span is
// present in the bundle bytes; this says a reader could answer from them. Both
// are required (AC-3).
const QrelBlindSmokeOutcomePath = "docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/outcome.json"

// floatComparisonEpsilon guards against the last bit of double
// representation when a stored target is compared with a value recomputed
// from raw samples. It is NOT a shortfall tolerance: it is eleven orders of
// magnitude below what a five-query stratum can resolve, and SW-282 deleted
// the one numeric shortfall exception this package had.
const floatComparisonEpsilon = 1e-9

// TargetCheck is one target's verdict.
type TargetCheck struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required string `json:"required"`
	Observed string `json:"observed"`
	Met      bool   `json:"met"`
	Detail   string `json:"detail,omitempty"`
}

// TargetCheckResult is the whole file's verdict against one report.
type TargetCheckResult struct {
	TargetsFile   string        `json:"targets_file"`
	Report        string        `json:"report"`
	ReportSHA256  string        `json:"report_sha256"`
	CandidateSHA  string        `json:"candidate_sha"`
	Baseline      Baseline      `json:"baseline"`
	Checks        []TargetCheck `json:"checks"`
	MissCount     int           `json:"miss_count"`
	FirstMissName string        `json:"first_miss,omitempty"`
}

// TargetCheckInputs are the artifacts the gate reads. Each is a path relative
// to the repository root; an absent artifact is a MISS with a reason, never a
// silent pass.
type TargetCheckInputs struct {
	RepoRoot            string
	ReportPath          string
	CoveragePath        string
	SmokeOutcomePath    string
	RequireCandidateSHA string
}

// CheckTargets evaluates every target in the targets file against the named
// report and the named supporting artifacts.
func CheckTargets(in TargetCheckInputs) (*TargetCheckResult, error) {
	targetsRaw, err := os.ReadFile(filepath.Join(in.RepoRoot, filepath.FromSlash(TargetsFilePath)))
	if err != nil {
		return nil, fmt.Errorf("retrieval: read %s: %w", TargetsFilePath, err)
	}
	var tg Targets
	if err := json.Unmarshal(targetsRaw, &tg); err != nil {
		return nil, fmt.Errorf("retrieval: parse %s: %w", TargetsFilePath, err)
	}
	if tg.FusionMinDelta != FusionMinDelta {
		return nil, fmt.Errorf("retrieval: %s fusion_min_delta = %v but the package constant is %v; the file and the code have drifted", TargetsFilePath, tg.FusionMinDelta, FusionMinDelta)
	}

	reportPath := in.ReportPath
	if reportPath == "" {
		reportPath = GateReportPath
	}
	reportRaw, err := os.ReadFile(resolveUnderRoot(in.RepoRoot, reportPath))
	if err != nil {
		return nil, fmt.Errorf("retrieval: read report %s: %w", reportPath, err)
	}
	var rep Report
	if err := json.Unmarshal(reportRaw, &rep); err != nil {
		return nil, fmt.Errorf("retrieval: parse report %s: %w", reportPath, err)
	}
	if err := CheckReportVersion(&rep); err != nil {
		return nil, fmt.Errorf("retrieval: report %s: %w", reportPath, err)
	}
	reportSHA := SHA256Hex(reportRaw)
	if tg.DerivedFrom.SHA256 != "" && reportSHA == tg.DerivedFrom.SHA256 {
		return nil, fmt.Errorf("retrieval: report %s is the report the targets were DERIVED from (sha256 %s); a bar checked against the observations it was computed from is arithmetic, not a gate. Name a separately committed report",
			reportPath, reportSHA)
	}
	if tg.DerivedFrom.Dataset != "" && rep.Reproducible.Dataset.ID != tg.DerivedFrom.Dataset {
		return nil, fmt.Errorf("retrieval: report %s measures dataset %q but the targets were derived on %q; a bar from one dataset does not bind a measurement on another",
			reportPath, rep.Reproducible.Dataset.ID, tg.DerivedFrom.Dataset)
	}
	if in.RequireCandidateSHA != "" && rep.Reproducible.CandidateSHA != in.RequireCandidateSHA {
		return nil, fmt.Errorf("retrieval: report %s carries candidate_sha %q, the gate is bound to %q; the gate is a property of the reviewed tree, not of whatever the filesystem holds",
			reportPath, rep.Reproducible.CandidateSHA, in.RequireCandidateSHA)
	}

	res := &TargetCheckResult{
		TargetsFile: TargetsFilePath, Report: reportPath, ReportSHA256: reportSHA,
		CandidateSHA: rep.Reproducible.CandidateSHA, Baseline: GateBaseline,
	}

	perStratum, baselineErr := gateBaselineDevStrata(&rep)
	for _, stratum := range tg.ConceptualStrata {
		st := tg.Strata[stratum]
		check := TargetCheck{Name: stratum + " fusion_target", Kind: "fusion_target"}
		if st.FusionTarget == nil {
			check.Required, check.Observed = "a fusion_target", "absent"
			check.Detail = "the targets file carries no fusion_target for this conceptual stratum"
			res.Checks = append(res.Checks, check)
			continue
		}
		ft := st.FusionTarget
		check.Required = fmt.Sprintf("%s %s >= %.17g (best %s %.17g + min_delta %.2f, ceiling %v)",
			GateBaseline, ft.Metric, ft.MustReach, ft.BestBaseline, ft.BestValue, ft.MinDelta, st.Oracle[ft.Metric])
		if baselineErr != nil {
			check.Observed, check.Detail = "absent", baselineErr.Error()
			res.Checks = append(res.Checks, check)
			continue
		}
		v, ok := perStratum[stratum].Metrics[ft.Metric]
		if !ok {
			check.Observed = "absent"
			check.Detail = fmt.Sprintf("the report carries no %s for %s/%s over the development split", ft.Metric, GateBaseline, stratum)
			res.Checks = append(res.Checks, check)
			continue
		}
		check.Observed = fmt.Sprintf("%.17g", v)
		check.Met = v+floatComparisonEpsilon >= ft.MustReach
		if !check.Met {
			check.Detail = fmt.Sprintf("shortfall %.17g over a %d-query stratum (resolution 1/%d)", ft.MustReach-v, st.DevQueries, st.DevQueries)
		}
		res.Checks = append(res.Checks, check)
	}

	ei := tg.Strata[StratumExactIdentifier]
	nrCheck := TargetCheck{Name: StratumExactIdentifier + " no_regression", Kind: "no_regression"}
	switch {
	case ei.NoRegression == nil:
		nrCheck.Required, nrCheck.Observed = "a no_regression floor", "absent"
		nrCheck.Detail = "the targets file carries no Top-1 no-regression floor on exact_identifier"
	default:
		nrCheck.Required = fmt.Sprintf("%s %s >= %.17g (best single baseline %s)", GateBaseline, ei.NoRegression.Metric, ei.NoRegression.Floor, ei.NoRegression.Baseline)
		if baselineErr != nil {
			nrCheck.Observed, nrCheck.Detail = "absent", baselineErr.Error()
			break
		}
		v, ok := perStratum[StratumExactIdentifier].Metrics[ei.NoRegression.Metric]
		if !ok {
			nrCheck.Observed = "absent"
			nrCheck.Detail = fmt.Sprintf("the report carries no %s for %s/%s over the development split", ei.NoRegression.Metric, GateBaseline, StratumExactIdentifier)
			break
		}
		nrCheck.Observed = fmt.Sprintf("%.17g", v)
		nrCheck.Met = v+floatComparisonEpsilon >= ei.NoRegression.Floor
	}
	res.Checks = append(res.Checks, nrCheck)

	res.Checks = append(res.Checks, checkBundleCoverage(&tg, in))
	res.Checks = append(res.Checks, checkQrelBlindSmokeGate(in))

	for _, c := range res.Checks {
		if !c.Met {
			res.MissCount++
			if res.FirstMissName == "" {
				res.FirstMissName = c.Name
			}
		}
	}
	return res, nil
}

// gateBaselineDevStrata collects the gated baseline's per-stratum aggregates
// over the development split.
func gateBaselineDevStrata(rep *Report) (map[string]AggregateMetrics, error) {
	for _, b := range rep.Reproducible.Baselines {
		if b.Name != GateBaseline {
			continue
		}
		if b.Status != BaselineStatusOK {
			return nil, fmt.Errorf("baseline %s did not run (status %s: %s); an unavailable candidate is not a passing candidate", GateBaseline, b.Status, b.Reason)
		}
		var dev []QueryResult
		for _, q := range b.Queries {
			if q.Split == SplitDev {
				dev = append(dev, q)
			}
		}
		if len(dev) == 0 {
			return nil, fmt.Errorf("baseline %s carries no development query result", GateBaseline)
		}
		_, strata, _ := AggregateAll(dev, rep.Reproducible.TokenBudgets)
		return strata, nil
	}
	return nil, fmt.Errorf("baseline %s is absent from the report; an absent candidate is not a passing candidate", GateBaseline)
}

// checkBundleCoverage enforces the whole-query coverage bar against the
// committed task_context/2 measurement.
func checkBundleCoverage(tg *Targets, in TargetCheckInputs) TargetCheck {
	check := TargetCheck{Name: "bundle_coverage", Kind: "bundle_coverage"}
	if tg.BundleCoverage == nil {
		check.Required, check.Observed = "a bundle_coverage target", "absent"
		check.Detail = "the targets file carries no bundle_coverage object; an absent target is not a met target"
		return check
	}
	bc := tg.BundleCoverage
	check.Required = fmt.Sprintf("%d of %d queries covered, at most %d miss(es), at token budget %d",
		bc.Threshold.CoveredQueriesRequired, bc.Population.N, bc.Threshold.MaxMisses, bc.TokenBudget)

	path := in.CoveragePath
	if path == "" {
		path = BundleCoverageMeasurementPath
	}
	raw, err := os.ReadFile(resolveUnderRoot(in.RepoRoot, path))
	if err != nil {
		check.Observed = "absent"
		check.Detail = fmt.Sprintf("the coverage measurement %s is unreadable: %v. An absent measurement is a MISS, not a pass — a gate that cannot see its evidence has not been satisfied", path, err)
		return check
	}
	var m TaskContextMeasurement
	if err := json.Unmarshal(raw, &m); err != nil {
		check.Observed = "unreadable"
		check.Detail = fmt.Sprintf("parse %s: %v", path, err)
		return check
	}
	check.Observed = fmt.Sprintf("%d of %d covered (%s)", m.Aggregate.CoveredQueries, m.Aggregate.TotalQueries, path)
	switch {
	case m.FormatVersion != TaskContextFormatVersion || m.HarnessVersion != TaskContextHarnessVersion || m.ScorerVersion != TaskContextScorerVersion:
		check.Detail = fmt.Sprintf("%s was produced by %s/%s format %d, this build enforces %s/%s format %d",
			path, m.HarnessVersion, m.ScorerVersion, m.FormatVersion, TaskContextHarnessVersion, TaskContextScorerVersion, TaskContextFormatVersion)
	case !m.EligibleForThreshold:
		check.Detail = fmt.Sprintf("%s is not eligible_for_threshold; a measurement that failed its own preconditions cannot satisfy a target", path)
	case m.Bundle.TokenBudget != bc.TokenBudget:
		check.Detail = fmt.Sprintf("the measurement ran at token budget %d, the target is set at %d", m.Bundle.TokenBudget, bc.TokenBudget)
	case !sameStringSet(m.Dataset.QueryIDs, bc.Population.QueryIDs):
		check.Detail = fmt.Sprintf("the measurement covers queries %s, the target's population is %s; a coverage bar binds one population",
			strings.Join(m.Dataset.QueryIDs, ", "), strings.Join(bc.Population.QueryIDs, ", "))
	case m.Aggregate.TotalQueries != bc.Population.N:
		check.Detail = fmt.Sprintf("the measurement scored %d queries, the target's population is %d", m.Aggregate.TotalQueries, bc.Population.N)
	case m.Aggregate.CoveredQueries < bc.Threshold.CoveredQueriesRequired:
		check.Detail = fmt.Sprintf("%d of %d covered; %d misses exceeds the maximum of %d. The bar is whole queries and is not lowered to the observed value",
			m.Aggregate.CoveredQueries, m.Aggregate.TotalQueries, m.Aggregate.TotalQueries-m.Aggregate.CoveredQueries, bc.Threshold.MaxMisses)
	case m.Aggregate.TotalQueries-m.Aggregate.CoveredQueries > bc.Threshold.MaxMisses:
		check.Detail = fmt.Sprintf("%d misses exceeds the maximum of %d", m.Aggregate.TotalQueries-m.Aggregate.CoveredQueries, bc.Threshold.MaxMisses)
	default:
		check.Met = true
	}
	return check
}

// checkQrelBlindSmokeGate enforces the bundle-sufficiency smoke evaluation.
// AC-3: the retrieval-targets gate requires BOTH this and coverage, and fails
// when either is unmet or absent. They answer different questions and SW-280
// is the direct evidence that they can disagree.
func checkQrelBlindSmokeGate(in TargetCheckInputs) TargetCheck {
	check := TargetCheck{
		Name:     "qrel_blind_smoke",
		Kind:     "qrel_blind_smoke",
		Required: fmt.Sprintf("%s outcome present and RELEASE: %s", QrelBlindSmokeEvaluationName, ReleaseYes),
	}
	path := in.SmokeOutcomePath
	if path == "" {
		path = QrelBlindSmokeOutcomePath
	}
	raw, err := os.ReadFile(resolveUnderRoot(in.RepoRoot, path))
	if err != nil {
		check.Observed = "absent"
		check.Detail = fmt.Sprintf("the smoke-evaluation outcome %s is unreadable: %v. Coverage alone does not satisfy this gate: coverage asserts the span is PRESENT in the bundle bytes, the smoke gate asserts a reader could ANSWER from them", path, err)
		return check
	}
	var outcome EvaluationOutcome
	if err := json.Unmarshal(raw, &outcome); err != nil {
		check.Observed = "unreadable"
		check.Detail = fmt.Sprintf("parse %s: %v", path, err)
		return check
	}
	check.Observed = fmt.Sprintf("RELEASE: %s (%d of %d passed against a pre-registered k=%d)", outcome.Release, outcome.PassCount, outcome.N, outcome.K)
	switch {
	case outcome.ContractVersion != QrelBlindSmokeContractVersion:
		check.Detail = fmt.Sprintf("%s was produced under contract %q, this build enforces %q", path, outcome.ContractVersion, QrelBlindSmokeContractVersion)
	case outcome.Release != ReleaseYes:
		check.Detail = fmt.Sprintf("the smoke evaluation recorded RELEASE: %s. There is no override: %s", outcome.Release, strings.Join(outcome.Reasons, "; "))
	default:
		check.Met = true
	}
	return check
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func resolveUnderRoot(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// FormatTargetCheck renders the verdict for a terminal, one line per target.
func FormatTargetCheck(res *TargetCheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "retrieval targets: %s against %s (candidate %s, baseline %s)\n", res.TargetsFile, res.Report, res.CandidateSHA, res.Baseline)
	for _, c := range res.Checks {
		state := "MISS"
		if c.Met {
			state = "PASS"
		}
		fmt.Fprintf(&b, "  %-4s %-34s required %s; observed %s\n", state, c.Name, c.Required, c.Observed)
		if c.Detail != "" {
			fmt.Fprintf(&b, "         %s\n", c.Detail)
		}
	}
	if res.MissCount == 0 {
		fmt.Fprintf(&b, "  %d target(s) checked, all met\n", len(res.Checks))
	} else {
		fmt.Fprintf(&b, "  %d of %d target(s) missed; first miss: %s\n", res.MissCount, len(res.Checks), res.FirstMissName)
	}
	return b.String()
}
