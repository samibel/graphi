package main

// SW-126 (P0-C3): what a freshness measurement MEANS against the §12.2 gate.
//
// The gate is read from the checked-in contract, so these also pin that the
// contract still assigns `freshness_p95` to this story — a gate silently
// reassigned would leave the harness measuring something nothing reads.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

// sufficientSeries is a series that met FR-8's floor and covered every class,
// with the given freshness p95 in microseconds.
func sufficientSeries(p95us int64) *evalreport.IncrementalSeries {
	return &evalreport.IncrementalSeries{
		Repo:           "grpc-go",
		Requested:      evalreport.IncrementalChangeMinimum,
		Minimum:        evalreport.IncrementalChangeMinimum,
		Completed:      evalreport.IncrementalChangeMinimum,
		Sufficient:     true,
		ClassesCovered: true,
		Freshness: evalreport.LatencyStats{
			N: evalreport.IncrementalChangeMinimum, MinUS: 1, P50US: p95us / 2, P95US: p95us, MaxUS: p95us,
		},
	}
}

var freshnessGate = gateMapping{
	ID: "freshness_p95", PRDMetric: "Freshness p95 (PRD 12.2)",
	Threshold: 2, Unit: "s", Comparison: "lte", Repo: "grpc-go", MeasuredBy: freshnessStory,
}

// The measured value is the freshness p95 converted from microseconds to the
// contract's seconds, and a threshold miss is a FAIL — the gate can fail, which
// is what makes a pass mean anything.
func TestEvaluateFreshnessGate_PassAndFail(t *testing.T) {
	pass := evaluateFreshnessGate(freshnessGate, sufficientSeries(1_500_000), "")
	if pass.Status != evalreport.StatusPass {
		t.Fatalf("1.5 s status = %s (%s), want PASS", pass.Status, pass.Reason)
	}
	if pass.Measured != 1.5 || !pass.HasMeasurement {
		t.Errorf("measured = %v, want 1.5 s converted from microseconds", pass.Measured)
	}

	fail := evaluateFreshnessGate(freshnessGate, sufficientSeries(3_200_000), "")
	if fail.Status != evalreport.StatusFail {
		t.Fatalf("3.2 s status = %s (%s), want FAIL", fail.Status, fail.Reason)
	}
	if !strings.Contains(fail.Reason, "3.200") || !strings.Contains(fail.Reason, "2.000") {
		t.Errorf("reason %q must name both the measurement and the threshold", fail.Reason)
	}
}

// AC-1/AC-2 as gate rules: a series below the 100-change floor, or one that
// never exercised every class, reads UNKNOWN even when its p95 is comfortable.
func TestEvaluateFreshnessGate_InsufficientSequenceIsUnknownNotPass(t *testing.T) {
	undersampled := sufficientSeries(100_000)
	undersampled.Completed, undersampled.Sufficient = 12, false
	got := evaluateFreshnessGate(freshnessGate, undersampled, "")
	if got.Status != evalreport.StatusUnknown || got.HasMeasurement {
		t.Fatalf("undersampled gate = %+v, want UNKNOWN with no published measurement", got)
	}
	if !strings.Contains(got.Reason, "12") || !strings.Contains(got.Reason, "100") {
		t.Errorf("reason %q must name the count it got and the floor it missed", got.Reason)
	}

	partial := sufficientSeries(100_000)
	partial.ClassesCovered = false
	got = evaluateFreshnessGate(freshnessGate, partial, "")
	if got.Status != evalreport.StatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN when a required class was never exercised", got.Status)
	}
	for _, class := range evalreport.RequiredChangeClasses {
		if !strings.Contains(got.Reason, class) {
			t.Errorf("reason %q must name the required classes", got.Reason)
		}
	}
}

// A failed change does NOT block the gate — it is a real observation about the
// incremental path, and the completed changes still carry a distribution. It
// does have to be visible in the gate's own reason.
func TestEvaluateFreshnessGate_FailedChangesAreVisibleButDoNotBlockTheGate(t *testing.T) {
	series := sufficientSeries(1_000_000)
	series.Failed = 3
	got := evaluateFreshnessGate(freshnessGate, series, "")
	if got.Status != evalreport.StatusPass {
		t.Fatalf("status = %s (%s), want the gate readable over the completed changes", got.Status, got.Reason)
	}
	if !strings.Contains(got.Reason, "3 change(s) failed") {
		t.Errorf("reason %q must surface the failed changes", got.Reason)
	}
	// The series verdict is where the failures bite.
	if incrementalStatus(series) == evalreport.StatusPass {
		t.Error("a series with failed changes must not read PASS overall")
	}
}

// Provenance: every blocker the shared rule knows about makes the gate UNKNOWN,
// and none of them can turn a missed threshold into a pass.
func TestReadFreshnessGates_ProvenanceBlockersMakeItUnknown(t *testing.T) {
	scenarioPath := filepath.Join(repoRoot(t), "docs", "eval", "reference-scenario.json")

	ok := readFreshnessGates(scenarioPath, sufficientSeries(1_000_000), candidateProvenance())
	if len(ok) != 1 {
		t.Fatalf("got %d gates, want exactly the one freshness gate this story owns: %+v", len(ok), ok)
	}
	if ok[0].ID != "freshness_p95" {
		t.Fatalf("gate id = %s, want freshness_p95", ok[0].ID)
	}
	if ok[0].Status != evalreport.StatusPass {
		t.Fatalf("a candidate reference run at 1 s must PASS, got %s: %s", ok[0].Status, ok[0].Reason)
	}

	for _, tc := range []struct {
		name string
		mut  func(*gateProvenance)
	}{
		{"comparison runner class", func(p *gateProvenance) { p.referenceScenario = false; p.runnerRole = roleComparison }},
		{"dirty worktree", func(p *gateProvenance) { p.worktreeDirty = true; p.candidateMatch = false }},
		{"another revision", func(p *gateProvenance) { p.measuredSHA = "deadbee"; p.candidateMatch = false }},
		{"candidate could not be cited", func(p *gateProvenance) { p.candidateError = "evidence index unreadable" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := candidateProvenance()
			tc.mut(&prov)
			gates := readFreshnessGates(scenarioPath, sufficientSeries(1_000_000), prov)
			if len(gates) != 1 || gates[0].Status != evalreport.StatusUnknown {
				t.Fatalf("gates = %+v, want a single UNKNOWN", gates)
			}
			if gates[0].Reason == "" {
				t.Error("an UNKNOWN gate must say why")
			}
		})
	}
}

// The contract still assigns freshness_p95 to this story, and still states it
// in seconds. Either drifting would leave the harness measuring something no
// gate reads, or reading a threshold through the wrong conversion.
func TestReferenceScenario_AssignsFreshnessToThisStory(t *testing.T) {
	rs, err := loadReferenceScenario(filepath.Join(repoRoot(t), "docs", "eval", "reference-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, g := range rs.Gates {
		if g.ID != "freshness_p95" {
			continue
		}
		found = true
		if g.MeasuredBy != freshnessStory {
			t.Errorf("freshness_p95 is measured_by %q, but this harness answers %q", g.MeasuredBy, freshnessStory)
		}
		if g.Unit != freshnessGateUnit {
			t.Errorf("freshness_p95 unit = %q, harness converts to %q", g.Unit, freshnessGateUnit)
		}
		if g.Threshold <= 0 {
			t.Errorf("freshness_p95 threshold = %v", g.Threshold)
		}
	}
	if !found {
		t.Fatal("the contract no longer maps freshness_p95")
	}

	// The incremental update p50/p95 is measured but deliberately carries no
	// §12.2 gate; the contract must keep saying so, or "no gate" would look like
	// an omission.
	var recorded bool
	for _, note := range rs.MeasuredNotGated {
		if strings.Contains(note, "incremental update") {
			recorded = true
		}
	}
	if !recorded {
		t.Error("measured_not_gated no longer records that incremental update p50/p95 has no §12.2 gate")
	}
}

// An unreadable contract yields one UNKNOWN gate rather than silence: a run
// that could not read its own thresholds must not look ungated by choice.
func TestReadFreshnessGates_UnreadableContractIsUnknown(t *testing.T) {
	gates := readFreshnessGates(filepath.Join(t.TempDir(), "missing.json"), sufficientSeries(1_000_000), candidateProvenance())
	if len(gates) != 1 || gates[0].Status != evalreport.StatusUnknown {
		t.Fatalf("gates = %+v, want a single UNKNOWN", gates)
	}
	if gates == nil {
		t.Error("a missing contract must not read as no gates")
	}
}
