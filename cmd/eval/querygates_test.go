package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

// candidateProvenance is a run that is about the frozen candidate on the
// reference scenario — the only state in which a §12.2 gate may read anything
// other than UNKNOWN.
func candidateProvenance() queryGateProvenance {
	return queryGateProvenance{
		repo: "grpc-go", runnerClass: "ubuntu-latest", runnerRole: roleReference,
		referenceScenario: true,
		measuredSHA:       "4e72637",
		candidateSHA:      "4e72637",
		candidateSource:   "docs/rc/evidence-index.yaml",
		candidateMatch:    true,
	}
}

// sufficientPool builds a pool that cleared FR-8's floor at the given p95.
func sufficientPool(gateID string, ops []string, p95us int64) evalreport.QueryPoolLatency {
	return evalreport.QueryPoolLatency{
		GateID: gateID, Operations: ops,
		Executions: evalreport.QueryExecutionMinimum,
		Minimum:    evalreport.QueryExecutionMinimum,
		Sufficient: true,
		LatencyStats: evalreport.LatencyStats{
			N: evalreport.QueryExecutionMinimum, MinUS: 1, P50US: p95us / 2, P95US: p95us, MaxUS: p95us,
		},
	}
}

// SW-125 AC-2, the heart of the story: a pool below the 1000-execution floor
// reads UNKNOWN, not PASS — even when its measured p95 is comfortably inside
// the threshold. Before SW-125 the report could not tell the two apart.
func TestEvaluateQueryGate_UndersampledIsUnknownNotPass(t *testing.T) {
	gate := gateMapping{ID: "warm_search_p95", PRDMetric: "Warm Search p95", Threshold: 100, Unit: "ms", Comparison: "lte", Operations: []string{"search"}}

	// 150 executions, 5 ms p95 — well inside the 100 ms threshold.
	pool := evalreport.QueryPoolLatency{
		GateID: gate.ID, Operations: []string{"search"},
		Executions: 150, Minimum: evalreport.QueryExecutionMinimum, Sufficient: false,
		LatencyStats: evalreport.LatencyStats{N: 150, P50US: 2000, P95US: 5000, MaxUS: 6000},
	}
	got := evaluateQueryGate(gate, pool, "")
	if got.Status != evalreport.StatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN: a comfortable p95 over 150 executions is not FR-8's measurement", got.Status)
	}
	if got.HasMeasurement {
		t.Error("an undersampled gate must not publish a measurement as if it were the gate input")
	}
	if !strings.Contains(got.Reason, "150") || !strings.Contains(got.Reason, "1000") {
		t.Errorf("reason %q must name the count it got and the floor it missed", got.Reason)
	}

	// The same gate over the same latency, once the floor is met, passes —
	// so the rule is "undersampled", not "nailed to UNKNOWN".
	if got := evaluateQueryGate(gate, sufficientPool(gate.ID, []string{"search"}, 5000), ""); got.Status != evalreport.StatusPass {
		t.Fatalf("status = %s (%s), want PASS once the floor is met", got.Status, got.Reason)
	}
}

// A missed threshold over a sufficient pool is a real FAIL — the honesty rules
// must not swallow a genuine failure.
func TestEvaluateQueryGate_SufficientAndOverThresholdFails(t *testing.T) {
	gate := gateMapping{ID: "agent_context_p95", Threshold: 500, Unit: "ms", Comparison: "lte", Operations: []string{"agent_brief"}}
	got := evaluateQueryGate(gate, sufficientPool(gate.ID, []string{"agent_brief"}, 750_000), "")
	if got.Status != evalreport.StatusFail {
		t.Fatalf("status = %s, want FAIL", got.Status)
	}
	if got.Measured != 750 {
		t.Errorf("measured = %v ms, want 750 (microseconds converted, not reinterpreted)", got.Measured)
	}
	if !got.HasMeasurement || got.Aggregate == "" {
		t.Error("a FAIL must publish its measurement and name the aggregate it came from")
	}
}

// The provenance rules SW-124 established apply here too: a number about the
// wrong artifact is not evidence about the candidate, whatever it measures.
func TestEvaluateQueryGate_ProvenanceBlockersMakeEveryGateUnknown(t *testing.T) {
	gate := gateMapping{ID: "warm_search_p95", Threshold: 100, Unit: "ms", Comparison: "lte", Operations: []string{"search"}}
	pool := sufficientPool(gate.ID, []string{"search"}, 5000) // would PASS

	cases := []struct {
		name string
		mut  func(*queryGateProvenance)
		want string
	}{
		{"comparison runner class", func(p *queryGateProvenance) {
			p.referenceScenario = false
			p.runnerClass, p.runnerRole = "local-sandbox", roleComparison
		}, "not the reference scenario"},
		{"dirty worktree", func(p *queryGateProvenance) {
			p.worktreeDirty = true
			p.measuredSHA = "4e72637+dirty"
			p.candidateMatch = false
		}, "dirty worktree"},
		{"revision other than the frozen candidate", func(p *queryGateProvenance) {
			p.measuredSHA = "deadbee"
			p.candidateMatch = false
		}, "not the frozen candidate"},
		{"candidate could not be cited", func(p *queryGateProvenance) {
			p.candidateError = "read docs/rc/evidence-index.yaml: no such file"
			p.candidateMatch = false
		}, "could not be cited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := candidateProvenance()
			tc.mut(&prov)
			got := evaluateQueryGate(gate, pool, prov.blocker())
			if got.Status != evalreport.StatusUnknown {
				t.Fatalf("gate = %s (%s), want UNKNOWN: a PASS here is a gate result about the wrong artifact", got.Status, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.want) {
				t.Errorf("reason %q does not name the blocker %q", got.Reason, tc.want)
			}
			if got.HasMeasurement {
				t.Error("a blocked gate must not publish a measurement")
			}
		})
	}
	// And the clean candidate case still passes, so the rule is a filter and
	// not a blanket refusal.
	if got := evaluateQueryGate(gate, pool, candidateProvenance().blocker()); got.Status != evalreport.StatusPass {
		t.Fatalf("clean candidate gate = %s (%s), want PASS", got.Status, got.Reason)
	}
}

// A gate the harness measured nothing for is UNKNOWN and says so, rather than
// rendering a zero-millisecond latency that reads as very fast indeed.
func TestEvaluateQueryGate_MissingPoolIsUnknown(t *testing.T) {
	gate := gateMapping{ID: "caller_callee_impact_p95", Threshold: 200, Unit: "ms", Comparison: "lte", Operations: []string{"callers"}}
	got := evaluateQueryGate(gate, evalreport.QueryPoolLatency{}, "")
	if got.Status != evalreport.StatusUnknown || got.HasMeasurement {
		t.Fatalf("gate = %+v, want an UNKNOWN with no measurement", got)
	}
	// A unit the harness does not produce also blocks, rather than being
	// converted by guesswork.
	seconds := gateMapping{ID: "warm_search_p95", Threshold: 0.1, Unit: "s", Comparison: "lte", Operations: []string{"search"}}
	if got := evaluateQueryGate(seconds, sufficientPool("warm_search_p95", []string{"search"}, 5000), ""); got.Status != evalreport.StatusUnknown {
		t.Fatalf("a gate declared in %q read %s; a unit the harness does not measure must block", seconds.Unit, got.Status)
	}
}

// readQueryGates over the REAL checked-in contract: exactly the three §12.2
// warm rows are read, and none of them can pass off the frozen candidate.
func TestReadQueryGates_OverTheCheckedInContract(t *testing.T) {
	scenarioPath := filepath.Join(repoRoot(t), "docs", "eval", "reference-scenario.json")
	series := &evalreport.QueryLatencySeries{
		Repo: "grpc-go", Minimum: evalreport.QueryExecutionMinimum, Sufficient: true,
		Pools: []evalreport.QueryPoolLatency{
			sufficientPool("warm_search_p95", []string{"search"}, 5_000),
			sufficientPool("caller_callee_impact_p95", []string{"callees", "callers", "impact"}, 9_000),
			sufficientPool("agent_context_p95", []string{"agent_brief", "change_risk", "explain_symbol", "related_files"}, 40_000),
		},
	}

	gates := readQueryGates(scenarioPath, series, candidateProvenance())
	if len(gates) != 3 {
		t.Fatalf("read %d gates, want the three §12.2 warm rows: %+v", len(gates), gates)
	}
	for _, g := range gates {
		if g.Status != evalreport.StatusPass {
			t.Errorf("gate %s = %s (%s), want PASS on a clean candidate series", g.ID, g.Status, g.Reason)
		}
	}
	series.Gates = gates
	if got := queryLatencyStatus(series); got != evalreport.StatusPass {
		t.Errorf("series status = %s, want PASS", got)
	}

	// Off the candidate, no gate may pass — the whole path, not just the unit.
	off := candidateProvenance()
	off.measuredSHA, off.candidateMatch = "0000000", false
	for _, g := range readQueryGates(scenarioPath, series, off) {
		if g.Status == evalreport.StatusPass {
			t.Errorf("gate %s = PASS off the frozen candidate; no §12.2 gate may pass on evidence about another artifact", g.ID)
		}
	}
	// And with no contract there are no gates at all, rather than invented ones.
	if got := readQueryGates("", series, candidateProvenance()); got != nil {
		t.Errorf("without a contract the harness must read no gates, got %+v", got)
	}
}

// PRD §8.2 at the series level: FAIL beats UNKNOWN beats PASS, and an
// undersampled series can never read green even if every gate it managed to
// read passed.
func TestQueryLatencyStatus_FailBeatsUnknownBeatsPass(t *testing.T) {
	pass := evalreport.GateResult{ID: "a", Status: evalreport.StatusPass}
	unknown := evalreport.GateResult{ID: "b", Status: evalreport.StatusUnknown}
	fail := evalreport.GateResult{ID: "c", Status: evalreport.StatusFail}

	cases := []struct {
		name string
		s    *evalreport.QueryLatencySeries
		want string
	}{
		{"nil series", nil, evalreport.StatusUnknown},
		{"no gates read", &evalreport.QueryLatencySeries{Sufficient: true}, evalreport.StatusUnknown},
		{"undersampled with passing gates", &evalreport.QueryLatencySeries{Gates: []evalreport.GateResult{pass}}, evalreport.StatusUnknown},
		{"sufficient with an unknown gate", &evalreport.QueryLatencySeries{Sufficient: true, Gates: []evalreport.GateResult{pass, unknown}}, evalreport.StatusUnknown},
		{"a failure outranks an unknown", &evalreport.QueryLatencySeries{Sufficient: true, Gates: []evalreport.GateResult{unknown, fail}}, evalreport.StatusFail},
		{"a failure outranks undersampling", &evalreport.QueryLatencySeries{Gates: []evalreport.GateResult{fail}}, evalreport.StatusFail},
		{"all pass", &evalreport.QueryLatencySeries{Sufficient: true, Gates: []evalreport.GateResult{pass}}, evalreport.StatusPass},
	}
	for _, tc := range cases {
		if got := queryLatencyStatus(tc.s); got != tc.want {
			t.Errorf("%s: status = %s, want %s", tc.name, got, tc.want)
		}
	}
}
