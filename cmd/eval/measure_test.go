package main

import "testing"

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
