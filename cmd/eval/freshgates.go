package main

// SW-126 (P0-C3): reading a freshness measurement against the PRD §12.2
// `freshness_p95` gate.
//
// The threshold is not restated here — it comes from the SW-123 contract, which
// is the single place a PRD number lives. This file only decides what a
// measurement MEANS against it, and it applies the same honesty rules the cold
// and query-latency gates apply, through the same gateProvenance value:
//
//   - a run that is not THE reference scenario on THE reference class reads
//     UNKNOWN, because §12.2 is scoped to that scenario and nothing else;
//   - a run from a revision that is not the frozen candidate reads UNKNOWN;
//   - a series below FR-8's 100-change floor reads UNKNOWN, because a p95 over
//     twelve changes is not the p95 the PRD asked for;
//   - a series that never exercised every required change class reads UNKNOWN,
//     because a freshness figure over adds and modifies only is not the
//     measurement AC-2 describes, however many changes it contains;
//   - a missing measurement reads UNKNOWN, never PASS.
//
// The one thing that is NOT a blocker is a failed change. A change that errored
// or never converged is a real observation about the incremental path, and the
// gate must still be readable over the changes that did complete — with the
// failure visible in the series, in its warnings, and in the run's status (AC-6).

import (
	"fmt"
	"strings"

	"github.com/samibel/graphi/internal/evalreport"
)

// freshnessGateUnit is the unit the §12.2 freshness threshold is stated in. The
// samples are microseconds; the conversion is checked against the contract's
// declared unit so a threshold whose unit moved cannot be read against an old
// conversion.
const freshnessGateUnit = "s"

// readFreshnessGates evaluates the §12.2 gates the contract assigns to this
// story against the measured series. Without a contract there are no thresholds
// to read against, and the measurement stays ungated rather than inventing any.
func readFreshnessGates(scenarioPath string, series *evalreport.IncrementalSeries, prov gateProvenance) []evalreport.GateResult {
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
	blocker := prov.blocker()
	var gates []evalreport.GateResult
	for _, g := range rs.Gates {
		if g.MeasuredBy != freshnessStory {
			continue
		}
		gates = append(gates, evaluateFreshnessGate(g, series, blocker))
	}
	return gates
}

// evaluateFreshnessGate reads ONE gate against the freshness distribution.
func evaluateFreshnessGate(g gateMapping, series *evalreport.IncrementalSeries, blocker string) evalreport.GateResult {
	result := evalreport.GateResult{
		ID:         g.ID,
		PRDMetric:  g.PRDMetric,
		Threshold:  g.Threshold,
		Unit:       g.Unit,
		Comparison: g.Comparison,
		Status:     evalreport.StatusUnknown,
		Aggregate:  "incremental.freshness.p95_us",
	}
	if g.Unit != freshnessGateUnit {
		result.Reason = fmt.Sprintf("the contract declares unit %q but the harness measures %q; a threshold whose unit moved cannot be read against an old conversion", g.Unit, freshnessGateUnit)
		return result
	}
	if blocker != "" {
		result.Reason = blocker
		return result
	}
	if !series.Sufficient {
		result.Reason = fmt.Sprintf("only %d of at least %d incremental changes completed; a freshness percentile over an undersampled sequence is not the measurement FR-8 asked for",
			series.Completed, series.Minimum)
		return result
	}
	if !series.ClassesCovered {
		result.Reason = "the sequence did not complete a change in every required class (" +
			strings.Join(evalreport.RequiredChangeClasses, ", ") +
			"); a freshness percentile over an incomplete class mix is not the measurement FR-8 asked for"
		return result
	}
	if series.Freshness.N == 0 {
		result.Reason = "no change produced a freshness measurement"
		return result
	}

	result.Measured = float64(series.Freshness.P95US) / 1_000_000
	result.HasMeasurement = true
	detail := fmt.Sprintf("over %d converged change(s) of %d completed", series.Freshness.N, series.Completed)
	if series.Failed > 0 {
		// A failed change never turns a missed threshold into a pass, but the
		// gate reason must not read as if the sequence were clean either.
		detail += fmt.Sprintf(" (%d change(s) failed; see warnings)", series.Failed)
	}
	if result.Measured <= g.Threshold {
		result.Status = evalreport.StatusPass
		result.Reason = fmt.Sprintf("%.3f %s <= %.3f %s %s", result.Measured, g.Unit, g.Threshold, g.Unit, detail)
	} else {
		result.Status = evalreport.StatusFail
		result.Reason = fmt.Sprintf("%.3f %s > %.3f %s %s", result.Measured, g.Unit, g.Threshold, g.Unit, detail)
	}
	return result
}
