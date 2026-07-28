package main

// SW-125 (P0-C2): reading a query-latency measurement against the PRD §12.2
// warm gates.
//
// The thresholds are not restated here — they are read from the SW-123
// contract, which is the single place a PRD number lives. This file only
// decides what a measurement MEANS against them, and it applies SW-124's
// honesty rules to latency, in the same order:
//
//   - a run that is not THE reference scenario on THE reference class reads
//     UNKNOWN, because §12.2 is scoped to that scenario and nothing else;
//   - a run from a revision that is not the frozen candidate reads UNKNOWN,
//     because a gate result about an artifact no user installs is not evidence
//     about the candidate;
//   - a pool below FR-8's execution floor reads UNKNOWN, because a p95 over 150
//     executions is not the p95 the PRD asked for — this is AC-2, and it is the
//     rule the pre-SW-125 harness had no way to express;
//   - a missing measurement reads UNKNOWN, never PASS.
//
// The first two are PROVENANCE rules and the third is a SUFFICIENCY rule; the
// split mirrors coldgates.go so the two harnesses cannot disagree about what
// disqualifies a number.

import (
	"fmt"
	"strings"

	"github.com/samibel/graphi/internal/evalreport"
)

// queryGateProvenance is everything about THIS run that decides whether its
// numbers are about the thing §12.2 is scoped to. It is assembled by the caller
// from facts that are already final before any gate is read.
type queryGateProvenance struct {
	repo              string
	runnerClass       string
	runnerRole        string
	referenceScenario bool
	measuredSHA       string
	worktreeDirty     bool
	candidateSHA      string
	candidateSource   string
	candidateMatch    bool
	// candidateError is why the frozen candidate could not be cited at all. It
	// blocks every gate: a run that cannot say what artifact it measured is
	// not evidence about any artifact.
	candidateError string
}

// blocker returns the reason this run's numbers are not about the frozen
// candidate on the reference scenario, or "" when they are.
func (p queryGateProvenance) blocker() string {
	switch {
	case !p.referenceScenario:
		return fmt.Sprintf("this run is %s on runner class %s (%s), which is not the reference scenario; PRD §12.2 is scoped to the reference scenario only",
			p.repo, p.runnerClass, p.runnerRole)
	case p.candidateError != "":
		return "the frozen candidate could not be cited (" + p.candidateError + "), so this run cannot say which artifact it measured"
	case p.worktreeDirty:
		return fmt.Sprintf("the measuring binary was built from a dirty worktree (%s); a gate result that cannot be tied to a commit is not evidence", p.measuredSHA)
	case !p.candidateMatch:
		return fmt.Sprintf("measured revision %s is not the frozen candidate %s; a gate result about a different artifact is not evidence for the candidate",
			p.measuredSHA, p.candidateSHA)
	}
	return ""
}

// queryGateUnit is the unit every §12.2 warm-latency threshold is stated in.
// The samples are microseconds; the conversion is checked against the
// contract's declared unit so a threshold whose unit moved cannot be read
// against an old conversion.
const queryGateUnit = "ms"

// readQueryGates evaluates the §12.2 gates the contract assigns to this story
// against the measured pools. Without a contract there are no thresholds to
// read against, and the measurement stays ungated rather than inventing any.
func readQueryGates(scenarioPath string, series *evalreport.QueryLatencySeries, prov queryGateProvenance) []evalreport.GateResult {
	if scenarioPath == "" || series == nil {
		return nil
	}
	rs, err := loadReferenceScenario(scenarioPath)
	if err != nil {
		return []evalreport.GateResult{{
			ID:     "reference_scenario_contract",
			Status: evalreport.StatusUnknown,
			Reason: "the reference-scenario contract could not be read: " + err.Error(),
		}}
	}
	pools := map[string]evalreport.QueryPoolLatency{}
	for _, p := range series.Pools {
		pools[p.GateID] = p
	}
	blocker := prov.blocker()
	var gates []evalreport.GateResult
	for _, g := range rs.Gates {
		if g.MeasuredBy != queryLatencyStory {
			continue
		}
		gates = append(gates, evaluateQueryGate(g, pools[g.ID], blocker))
	}
	return gates
}

// evaluateQueryGate reads ONE gate. pool is the zero value when the harness
// measured nothing for it, which is a missing measurement — UNKNOWN, and named
// as such rather than rendered as a zero-millisecond latency.
func evaluateQueryGate(g gateMapping, pool evalreport.QueryPoolLatency, blocker string) evalreport.GateResult {
	result := evalreport.GateResult{
		ID:         g.ID,
		PRDMetric:  g.PRDMetric,
		Threshold:  g.Threshold,
		Unit:       g.Unit,
		Comparison: g.Comparison,
		Status:     evalreport.StatusUnknown,
	}
	if g.Unit != queryGateUnit {
		result.Reason = fmt.Sprintf("the contract declares unit %q but the harness measures %q; a threshold whose unit moved cannot be read against an old conversion", g.Unit, queryGateUnit)
		return result
	}
	if len(pool.Operations) == 0 {
		result.Reason = "no query-latency pool is bound to this gate; the contract's operation list is empty or names no measured operation"
		return result
	}
	result.Aggregate = "query_latency.pools." + g.ID + ".p95_us"
	if blocker != "" {
		result.Reason = blocker
		return result
	}
	if !pool.Sufficient {
		// AC-2. This is the state the pre-SW-125 report could not express: a
		// p95 over too few executions looked exactly like a p95 over enough.
		result.Reason = fmt.Sprintf("only %d timed executions were pooled over %s, below FR-8's floor of %d; a percentile over an undersampled pool is not the measurement the PRD asked for",
			pool.Executions, strings.Join(pool.Operations, "/"), pool.Minimum)
		return result
	}
	if pool.N == 0 {
		result.Reason = "the pool retained no measurements"
		return result
	}
	result.Measured = float64(pool.P95US) / 1000
	result.HasMeasurement = true
	if result.Measured <= g.Threshold {
		result.Status = evalreport.StatusPass
		result.Reason = fmt.Sprintf("%.3f %s <= %.3f %s over %d executions of %s", result.Measured, g.Unit, g.Threshold, g.Unit, pool.N, strings.Join(pool.Operations, "/"))
	} else {
		result.Status = evalreport.StatusFail
		result.Reason = fmt.Sprintf("%.3f %s > %.3f %s over %d executions of %s", result.Measured, g.Unit, g.Threshold, g.Unit, pool.N, strings.Join(pool.Operations, "/"))
	}
	return result
}

// queryLatencyStatus applies PRD §8.2 to the measurement: FAIL beats UNKNOWN
// beats PASS. A gate that failed is a failure even when another was unmeasured,
// and anything unmeasured — including an undersampled class — stops the series
// from reading green.
func queryLatencyStatus(series *evalreport.QueryLatencySeries) string {
	if series == nil {
		return evalreport.StatusUnknown
	}
	for _, g := range series.Gates {
		if g.Status == evalreport.StatusFail {
			return evalreport.StatusFail
		}
	}
	if !series.Sufficient || len(series.Gates) == 0 {
		return evalreport.StatusUnknown
	}
	for _, g := range series.Gates {
		if g.Status != evalreport.StatusPass {
			return evalreport.StatusUnknown
		}
	}
	return evalreport.StatusPass
}
