package main

// SW-127 (P0-C4): what a stall measurement MEANS against the §12.2
// `progress_stall_p95` gate.
//
// The gate is the story's regression guard, so the tests below are ordered by
// how badly a wrong answer would hurt: the silent run first, the provenance
// rules second, the threshold arithmetic last.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func stallGateMapping() gateMapping {
	return gateMapping{
		ID:         "progress_stall_p95",
		PRDMetric:  "Longest progress stall p95 (PRD 12.2)",
		Threshold:  2,
		Unit:       "s",
		Comparison: "lte",
		Repo:       "grpc-go",
		MeasuredBy: progressStallStory,
	}
}

// seriesWith builds a series in the shape the observer produces, so the gate is
// read against the same fields the harness publishes.
func seriesWith(us ...int64) *evalreport.StallSeries {
	in := make([]evalreport.StallInterval, 0, len(us))
	for i, v := range us {
		in = append(in, evalreport.StallInterval{Seq: i + 1, Phase: "parse", US: v})
	}
	s := &evalreport.StallSeries{
		Repo:       "grpc-go",
		Events:     len(us) + 1,
		Minimum:    evalreport.StallEventMinimum,
		Observable: len(us)+1 >= evalreport.StallEventMinimum,
		Intervals:  in,
	}
	s.Stalls = evalreport.RecomputeStalls(in).Stalls
	s.PerPhase = evalreport.PhaseStallsOf(in)
	return s
}

// AC-5, the point of the gate. A run that emitted no progress must never render
// as a pass — even with impeccable provenance and an empty, technically
// threshold-satisfying distribution.
func TestEvaluateStallGate_ASilentRunFailsAndNeverPasses(t *testing.T) {
	silent := &evalreport.StallSeries{
		Repo:          "grpc-go",
		Events:        0,
		Minimum:       evalreport.StallEventMinimum,
		Observable:    false,
		SilenceReason: "0 progress event(s) observed over 90.000 s, below the minimum of 2: " + evalreport.StallSilenceNote,
	}
	silent.Stalls = evalreport.RecomputeStalls(nil).Stalls

	got := evaluateStallGate(stallGateMapping(), silent, "")
	if got.Status != evalreport.StatusFail {
		t.Fatalf("status = %s over a silent index, want FAIL; `0 stalls, passed` is the outcome this gate exists to prevent", got.Status)
	}
	if got.HasMeasurement {
		t.Error("a silent run has no measurement; publishing one would invent a 0 s p95")
	}
	if got.Measured != 0 || !strings.Contains(got.Reason, "observable") && !strings.Contains(got.Reason, "OBSERVABLE") {
		t.Errorf("the FAIL reason must name the observability invariant, got %q", got.Reason)
	}
	if status := stallStatus(silent); status != evalreport.StatusFail {
		t.Errorf("series status = %s over a silent index, want FAIL", status)
	}
}

// A silent run off the reference scenario reads UNKNOWN rather than FAIL — the
// provenance rule is evaluated first, exactly as it is for the cold,
// query-latency and freshness gates. UNKNOWN is not a PASS, so the AC-5
// guarantee holds either way; the SERIES still fails.
func TestEvaluateStallGate_ASilentRunOffTheReferenceScenarioIsUnknownNotPass(t *testing.T) {
	silent := &evalreport.StallSeries{Repo: "cobra", Minimum: evalreport.StallEventMinimum}
	blocker := "this run is cobra on runner class local-sandbox (comparison), which is not the reference scenario"

	got := evaluateStallGate(stallGateMapping(), silent, blocker)
	if got.Status == evalreport.StatusPass {
		t.Fatal("a silent run passed a gate")
	}
	if got.Status != evalreport.StatusUnknown || got.Reason != blocker {
		t.Errorf("status/reason = %s / %q, want UNKNOWN with the provenance blocker", got.Status, got.Reason)
	}
	// The invariant violation is still a failure of the measurement itself.
	if status := stallStatus(silent); status != evalreport.StatusFail {
		t.Errorf("series status = %s; silence is machine-independent and fails regardless of provenance", status)
	}
}

// The threshold arithmetic, both directions, over a real distribution.
func TestEvaluateStallGate_ReadsTheP95AgainstTheContractThreshold(t *testing.T) {
	under := seriesWith(1_000, 500_000, 1_900_000)
	got := evaluateStallGate(stallGateMapping(), under, "")
	if got.Status != evalreport.StatusPass {
		t.Fatalf("status = %s (%s), want PASS for a 1.9 s p95 under a 2 s threshold", got.Status, got.Reason)
	}
	if !got.HasMeasurement || got.Measured != 1.9 {
		t.Errorf("measured = %v (has=%v), want 1.9 s", got.Measured, got.HasMeasurement)
	}
	if !strings.Contains(got.Reason, "longest stall") {
		t.Errorf("FR-8 asks for the longest stall; the gate reason does not report it: %q", got.Reason)
	}
	if got.Aggregate == "" {
		t.Error("the gate must name the aggregate it read, so the input can be recomputed as well as the verdict")
	}

	over := seriesWith(1_000, 500_000, 2_100_000)
	if got := evaluateStallGate(stallGateMapping(), over, ""); got.Status != evalreport.StatusFail {
		t.Fatalf("status = %s (%s), want FAIL for a 2.1 s p95", got.Status, got.Reason)
	}
}

// A threshold whose unit moved cannot be read against an old conversion.
func TestEvaluateStallGate_RefusesAThresholdInAnotherUnit(t *testing.T) {
	g := stallGateMapping()
	g.Unit = "ms"
	got := evaluateStallGate(g, seriesWith(1_000), "")
	if got.Status != evalreport.StatusUnknown || got.HasMeasurement {
		t.Fatalf("status = %s (has=%v), want UNKNOWN with no measurement", got.Status, got.HasMeasurement)
	}
	if !strings.Contains(got.Reason, "unit") {
		t.Errorf("reason = %q, want it to name the unit mismatch", got.Reason)
	}
}

// Only this story's gates are read. The contract maps ten §12.2 rows to four
// stories; a harness that read someone else's row would publish a number about
// a measurement it never took.
func TestReadStallGates_ReadsOnlyTheGateTheContractAssignsToThisStory(t *testing.T) {
	root := repoRoot(t)
	scenario := filepath.Join(root, "docs", "eval", "reference-scenario.json")
	rs, err := loadReferenceScenario(scenario)
	if err != nil {
		t.Fatal(err)
	}
	mine := 0
	for _, g := range rs.Gates {
		if g.MeasuredBy == progressStallStory {
			mine++
			if g.Unit != stallGateUnit {
				t.Errorf("gate %s is declared in %q but the harness measures %q", g.ID, g.Unit, stallGateUnit)
			}
			if g.Repo != rs.ReferenceScenario.Repo {
				t.Errorf("gate %s maps to %q, not the reference scenario %q", g.ID, g.Repo, rs.ReferenceScenario.Repo)
			}
		}
	}
	if mine != 1 {
		t.Fatalf("the contract assigns %d gate(s) to %s, want exactly 1 (progress_stall_p95)", mine, progressStallStory)
	}

	prov := gateProvenance{repo: "grpc-go", referenceScenario: true, candidateMatch: true, measuredSHA: "abc"}
	gates := readStallGates(scenario, seriesWith(1_000, 2_000), prov)
	if len(gates) != 1 || gates[0].ID != "progress_stall_p95" {
		t.Fatalf("readStallGates returned %+v, want exactly the progress_stall_p95 row", gates)
	}
	// Without a contract there is no threshold, and the harness invents none.
	if got := readStallGates("", seriesWith(1_000), prov); got != nil {
		t.Errorf("readStallGates without a contract returned %+v, want nil", got)
	}
}

// PRD §8.2 over the series: FAIL beats UNKNOWN beats PASS, and an ungated
// observable run is UNKNOWN rather than green.
func TestStallStatus_AppliesTheUnknownIsNotAPassRule(t *testing.T) {
	observable := seriesWith(1_000, 2_000)
	if got := stallStatus(observable); got != evalreport.StatusUnknown {
		t.Errorf("an observable run with no gates = %s, want UNKNOWN (nothing was read against a threshold)", got)
	}

	observable.Gates = []evalreport.GateResult{{ID: "progress_stall_p95", Status: evalreport.StatusPass}}
	if got := stallStatus(observable); got != evalreport.StatusPass {
		t.Errorf("status = %s, want PASS", got)
	}

	observable.Gates = []evalreport.GateResult{
		{ID: "progress_stall_p95", Status: evalreport.StatusUnknown},
	}
	if got := stallStatus(observable); got != evalreport.StatusUnknown {
		t.Errorf("status = %s, want UNKNOWN", got)
	}

	observable.Gates = []evalreport.GateResult{
		{ID: "progress_stall_p95", Status: evalreport.StatusFail},
		{ID: "other", Status: evalreport.StatusUnknown},
	}
	if got := stallStatus(observable); got != evalreport.StatusFail {
		t.Errorf("status = %s, want FAIL to beat UNKNOWN", got)
	}

	if got := stallStatus(nil); got != evalreport.StatusUnknown {
		t.Errorf("a nil series = %s, want UNKNOWN (the phase never ran)", got)
	}
}
