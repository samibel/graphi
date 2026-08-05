package perfbaseline

import (
	"fmt"
	"sort"
	"strings"
)

// Budgets are the regression allowances the P2 PRD sets. They are expressed as
// percentages of the baseline value.
const (
	// FullIndexBudgetPct is the allowed full-index p95 regression.
	FullIndexBudgetPct = 3.0
	// WarmQueryBudgetPct is the allowed warm-query p95 regression.
	WarmQueryBudgetPct = 5.0
	// BinarySizeBudgetPct is the allowed default-binary growth.
	BinarySizeBudgetPct = 2.0
)

// MetricDiff is one metric compared between a baseline and a candidate run.
type MetricDiff struct {
	Metric string
	// Baseline and Candidate are the compared p95 values (bytes for size).
	Baseline  float64
	Candidate float64
	// DeltaPct is the signed regression percentage; positive means slower/larger.
	DeltaPct float64
	// BudgetPct is the allowance for this metric.
	BudgetPct float64
	// OverBudget reports whether the regression exceeds the allowance.
	OverBudget bool
}

// DiffReport compares two recorded baselines.
type DiffReport struct {
	BaselineCommit  string
	CandidateCommit string
	Metrics         []MetricDiff
	// Warnings record comparability problems: a diff across different fixtures,
	// toolchains, or machines is not evidence, and saying so is the whole point.
	Warnings []string
}

// Pass reports whether every metric stayed inside its budget.
func (d DiffReport) Pass() bool {
	for _, m := range d.Metrics {
		if m.OverBudget {
			return false
		}
	}
	return true
}

// Diff compares a candidate run against a baseline.
//
// It compares p95 rather than median deliberately: the PRD budgets are p95
// budgets, because an application layer that adds a small constant to every call
// shows up in the tail long before it moves the median.
func Diff(baseline, candidate Report) DiffReport {
	out := DiffReport{
		BaselineCommit:  baseline.Commit,
		CandidateCommit: candidate.Commit,
	}

	if baseline.FixtureDigest != candidate.FixtureDigest {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"fixture digest differs (%s vs %s) — the two runs measured different workloads, so these deltas are not comparable",
			short(baseline.FixtureDigest), short(candidate.FixtureDigest)))
	}
	if baseline.Toolchain != candidate.Toolchain {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"toolchain differs (%s vs %s) — attribute deltas with care", baseline.Toolchain, candidate.Toolchain))
	}
	if baseline.Environment != candidate.Environment {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"environment differs (%s vs %s) — cross-machine timing deltas are not evidence",
			baseline.Environment, candidate.Environment))
	}
	if baseline.Samples < MinSamples || candidate.Samples < MinSamples {
		out.Warnings = append(out.Warnings, fmt.Sprintf(
			"a run has fewer than %d samples (baseline %d, candidate %d) — too few to claim a regression either way",
			MinSamples, baseline.Samples, candidate.Samples))
	}

	out.Metrics = append(out.Metrics, compare("full_index_p95", baseline.FullIndex.P95MS, candidate.FullIndex.P95MS, FullIndexBudgetPct))

	ops := map[string]bool{}
	for op := range baseline.WarmQuery {
		ops[op] = true
	}
	for op := range candidate.WarmQuery {
		ops[op] = true
	}
	names := make([]string, 0, len(ops))
	for op := range ops {
		names = append(names, op)
	}
	sort.Strings(names)
	for _, op := range names {
		base, inBase := baseline.WarmQuery[op]
		cand, inCand := candidate.WarmQuery[op]
		if !inBase || !inCand {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"warm-query op %q is present in only one run — it cannot be compared", op))
			continue
		}
		out.Metrics = append(out.Metrics, compare("warm_query/"+op+"_p95", base.P95MS, cand.P95MS, WarmQueryBudgetPct))
	}

	out.Metrics = append(out.Metrics, compare("trust_report_p95", baseline.TrustReport.P95MS, candidate.TrustReport.P95MS, WarmQueryBudgetPct))

	if baseline.BinarySizeBytes > 0 && candidate.BinarySizeBytes > 0 {
		out.Metrics = append(out.Metrics, compare("binary_size_bytes",
			float64(baseline.BinarySizeBytes), float64(candidate.BinarySizeBytes), BinarySizeBudgetPct))
	}
	return out
}

func compare(metric string, baseline, candidate, budgetPct float64) MetricDiff {
	diff := MetricDiff{
		Metric: metric, Baseline: baseline, Candidate: candidate, BudgetPct: budgetPct,
	}
	if baseline > 0 {
		diff.DeltaPct = (candidate - baseline) / baseline * 100
	}
	diff.OverBudget = diff.DeltaPct > budgetPct
	return diff
}

// Format renders the comparison for CI output.
func (d DiffReport) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "performance A/B: %s (baseline) → %s (candidate)\n", d.BaselineCommit, d.CandidateCommit)
	for _, w := range d.Warnings {
		fmt.Fprintf(&b, "  ⚠ %s\n", w)
	}
	fmt.Fprintf(&b, "  %-28s %12s %12s %9s %8s\n", "metric", "baseline", "candidate", "delta", "budget")
	for _, m := range d.Metrics {
		flag := "  "
		if m.OverBudget {
			flag = "!!"
		}
		fmt.Fprintf(&b, "%s%-28s %12.2f %12.2f %8.2f%% %7.1f%%\n",
			flag, m.Metric, m.Baseline, m.Candidate, m.DeltaPct, m.BudgetPct)
	}
	if d.Pass() {
		b.WriteString("verdict: PASS — every metric is inside its budget.\n")
	} else {
		b.WriteString("verdict: FAIL — a metric regressed beyond its budget. The PRD treats this as Architecture NO-GO until the cause is explained or optimized.\n")
	}
	return b.String()
}
