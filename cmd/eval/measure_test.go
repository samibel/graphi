package main

import (
	"strings"
	"testing"
)

// TestMeasureSignal pins the ground-truth diagnostics measurement: exactly
// one true positive survives the default gates, nothing false, no unsafe
// actions — signal quality scores 100 by construction, and any suppression
// or action-gating regression drags it down.
func TestMeasureSignal(t *testing.T) {
	m, err := measureSignal()
	if err != nil {
		t.Fatalf("measureSignal: %v", err)
	}
	if m.DefaultCount != 1 {
		t.Fatalf("expected exactly 1 default finding, got %d", m.DefaultCount)
	}
	if m.FalsePositives != 0 || m.FalsePositiveRate != 0 {
		t.Fatalf("expected zero false positives, got %d (rate %v)", m.FalsePositives, m.FalsePositiveRate)
	}
	if m.UnsafeActions != 0 {
		t.Fatalf("expected zero unsafe actions, got %d", m.UnsafeActions)
	}
	if m.Score < 90 {
		t.Fatalf("signal score %v below 90 — noise gates regressed", m.Score)
	}
	if m.SuppressedTotal < 4 {
		t.Fatalf("expected >=4 suppressed candidates, got %d (%v)", m.SuppressedTotal, m.SuppressedByCategory)
	}
}

// TestMeasureSetupTrust pins the doctor-behavior assertions over controlled
// fixtures; every assertion is expected to hold on a healthy tree.
func TestMeasureSetupTrust(t *testing.T) {
	score, m, err := measureSetupTrust()
	if err != nil {
		t.Fatalf("measureSetupTrust: %v", err)
	}
	for _, a := range m.Assertions {
		if !a.Pass {
			t.Errorf("assertion %s failed: %s", a.Name, a.Detail)
		}
	}
	if score != 100 {
		t.Fatalf("setup/trust score %v, want 100", score)
	}
}

// TestMeasurePerformance runs the tier-1 budget checks against the pinned
// fixture; every check must hold with wide margin on any healthy machine.
func TestMeasurePerformance(t *testing.T) {
	m, err := measurePerformanceAt("../../corpus/fixtures/go")
	if err != nil {
		t.Fatalf("measurePerformance: %v", err)
	}
	if len(m.Checks) != 5 {
		t.Fatalf("expected 5 budget checks, got %d", len(m.Checks))
	}
	for _, c := range m.Checks {
		if !c.Pass {
			t.Errorf("budget check %s failed: measured %v %s > budget %v", c.Name, c.Measured, c.Unit, c.Budget)
		}
	}
	if m.Score != 100 {
		t.Fatalf("performance score %v, want 100", m.Score)
	}
}

// SW-126 AC-5: the fixture path is DELIMITED from the P0 path. Every check this
// file produces carries the fixture prefix, so a fixture number can never be
// quoted as a reference-scenario one — `db_size` and `incremental_update` are
// both PRD §12.2 concepts, and two identically named measurements over two
// completely different subjects is the conflation this story removes.
func TestMeasurePerformance_ChecksAreScopedToTheFixture(t *testing.T) {
	m, err := measurePerformanceAt("../../corpus/fixtures/go")
	if err != nil {
		t.Fatalf("measurePerformance: %v", err)
	}
	for _, c := range m.Checks {
		if !strings.HasPrefix(c.Name, fixtureCheckPrefix) {
			t.Errorf("check %q is not scoped to the fixture: a PR-gate smoke number must not be readable as P0 evidence", c.Name)
		}
	}
	var incremental bool
	for _, c := range m.Checks {
		if c.Name == fixtureCheckPrefix+"incremental_update" {
			incremental = true
		}
	}
	if !incremental {
		t.Error("the fixture incremental check is gone; SW-126 keeps it for the PR gate, it only stops it being P0 evidence")
	}
	// And the fixture path measures no freshness at all — freshness is the
	// change-to-answer interval, which a single re-ingest call cannot produce.
	for _, c := range m.Checks {
		if strings.Contains(c.Name, "freshness") {
			t.Errorf("check %q claims a freshness measurement the fixture path does not make", c.Name)
		}
	}
}
