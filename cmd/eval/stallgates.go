package main

// SW-127 (P0-C4): reading a stall measurement against the PRD §12.2
// `progress_stall_p95` gate.
//
// The threshold is not restated here — it comes from the SW-123 contract, which
// is the single place a PRD number lives. This file only decides what a
// measurement MEANS against it, and it applies the same honesty rules the cold,
// query-latency and freshness gates apply, through the same gateProvenance
// value:
//
//   - a run that is not THE reference scenario on THE reference class reads
//     UNKNOWN, because §12.2 is scoped to that scenario and nothing else;
//   - a run from a revision that is not the frozen candidate reads UNKNOWN;
//   - a missing measurement reads UNKNOWN, never PASS.
//
// And then the rule that belongs to this story alone (AC-5):
//
//   - a run that emitted no progress NEVER READS PASS. Not on the reference
//     scenario, not from the candidate, not with a perfect empty distribution.
//     A gate that renders "0 stalls, passed" over an index nobody could watch is
//     worse than no gate, because it certifies the exact regression
//     `context/standards.md` forbids.
//
// The provenance blocker is still evaluated FIRST, so a silent run off the
// reference scenario reads UNKNOWN rather than FAIL. That is deliberate and it
// costs nothing: UNKNOWN is not a PASS either, the story permits either verdict,
// and asserting a §12.2 FAILURE about a machine the contract does not scope
// would be the same over-claiming the other three gates refuse to do. The
// SERIES status is where a silent run fails unconditionally — silence is an
// invariant violation, not a threshold claim — see stallStatus.

import (
	"fmt"

	"github.com/samibel/graphi/internal/evalreport"
)

// stallGateUnit is the unit the §12.2 stall threshold is stated in. The samples
// are microseconds; the conversion is checked against the contract's declared
// unit so a threshold whose unit moved cannot be read against an old conversion.
const stallGateUnit = "s"

// readStallGates evaluates the §12.2 gates the contract assigns to this story
// against the measured series. Without a contract there are no thresholds to
// read against, and the measurement stays ungated rather than inventing any.
func readStallGates(scenarioPath string, series *evalreport.StallSeries, prov gateProvenance) []evalreport.GateResult {
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
		if g.MeasuredBy != progressStallStory {
			continue
		}
		gates = append(gates, evaluateStallGate(g, series, blocker))
	}
	return gates
}

// evaluateStallGate reads ONE gate against the stall distribution.
func evaluateStallGate(g gateMapping, series *evalreport.StallSeries, blocker string) evalreport.GateResult {
	result := evalreport.GateResult{
		ID:         g.ID,
		PRDMetric:  g.PRDMetric,
		Threshold:  g.Threshold,
		Unit:       g.Unit,
		Comparison: g.Comparison,
		Status:     evalreport.StatusUnknown,
		Aggregate:  "stalls.stalls.p95_us",
	}
	if g.Unit != stallGateUnit {
		result.Reason = fmt.Sprintf("the contract declares unit %q but the harness measures %q; a threshold whose unit moved cannot be read against an old conversion", g.Unit, stallGateUnit)
		return result
	}
	if blocker != "" {
		result.Reason = blocker
		return result
	}
	if !series.Observable {
		// AC-5, the reason this gate exists. A silent index is a FAILURE on the
		// reference scenario, never an empty distribution that renders green.
		result.Status = evalreport.StatusFail
		result.Reason = series.SilenceReason
		if result.Reason == "" {
			result.Reason = evalreport.StallSilenceNote
		}
		return result
	}
	if series.Stalls.N == 0 {
		// Belt and braces: `observable` already implies at least one interval,
		// so reaching here would mean the two disagreed. Report the disagreement
		// rather than dividing by a distribution that is not there.
		result.Reason = fmt.Sprintf("the series reports %d event(s) but retained no intervals; nothing can be read against the threshold", series.Events)
		return result
	}

	result.Measured = float64(series.Stalls.P95US) / 1_000_000
	result.HasMeasurement = true
	detail := fmt.Sprintf("over %d interval(s) between %d progress event(s); longest stall %.3f %s",
		series.Stalls.N, series.Events, float64(series.Stalls.MaxUS)/1_000_000, g.Unit)
	if result.Measured <= g.Threshold {
		result.Status = evalreport.StatusPass
		result.Reason = fmt.Sprintf("%.3f %s <= %.3f %s %s", result.Measured, g.Unit, g.Threshold, g.Unit, detail)
	} else {
		result.Status = evalreport.StatusFail
		result.Reason = fmt.Sprintf("%.3f %s > %.3f %s %s", result.Measured, g.Unit, g.Threshold, g.Unit, detail)
	}
	return result
}
