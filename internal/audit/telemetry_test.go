package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/internal/canary"
)

// TestCheckNoTelemetry_RealScan_Pass proves the no-telemetry check is now backed by
// a REAL scan (SW-055 AC#5), not a declared string: a clean canary gate verdict
// yields a VERIFIED pass whose evidence names the scan, not a hard-coded "OK".
func TestCheckNoTelemetry_RealScan_Pass(t *testing.T) {
	c := checkNoTelemetryWithGate(func() (canary.GateResult, error) {
		return canary.GateResult{Verdict: "pass"}, nil
	})
	if c.Status != StatusPass {
		t.Fatalf("clean gate should PASS, got %s (%s)", c.Status, c.Evidence)
	}
	if c.Evidence == "declared: graphi ships no telemetry SDKs and makes no analytics calls (local-first binary)" {
		t.Fatal("no-telemetry check is still a declared string, not a real scan")
	}
}

// TestCheckNoTelemetry_RealScan_FailsOnFinding proves the check FAILS (names the
// offender) when the canary gate reports a telemetry import — a regression cannot
// hide behind a declared PASS anymore.
func TestCheckNoTelemetry_RealScan_FailsOnFinding(t *testing.T) {
	c := checkNoTelemetryWithGate(func() (canary.GateResult, error) {
		return canary.GateResult{
			Verdict: "fail",
			Findings: []canary.TelemetryFinding{
				{Kind: "telemetry-import", Import: "github.com/evil/analytics", Reason: "telemetry SDK"},
			},
		}, nil
	})
	if c.Status != StatusFail {
		t.Fatalf("telemetry finding should FAIL, got %s", c.Status)
	}
	found := false
	for _, off := range c.Offenders {
		if off == "telemetry-import: github.com/evil/analytics" {
			found = true
		}
	}
	if !found {
		t.Fatalf("FAIL must name the offending telemetry import, got %v", c.Offenders)
	}
}

// TestCheckNoTelemetry_GateError_Unverified proves a gate execution error never
// yields a false PASS — it degrades to UNVERIFIED (AC-6 false-green prevention).
func TestCheckNoTelemetry_GateError_Unverified(t *testing.T) {
	c := checkNoTelemetryWithGate(func() (canary.GateResult, error) {
		return canary.GateResult{}, errors.New("go list failed")
	})
	if c.Status != StatusUnverified {
		t.Fatalf("gate error should be UNVERIFIED (never a false PASS), got %s", c.Status)
	}
}

// TestNoTelemetry_DefaultGraph_RealGate_NoFindings runs the REAL canary static gate
// over the actual default build graph (the new default-tier parsers included) and
// asserts zero telemetry imports / zero unsanctioned outbound dials. This is the
// live baseline: a telemetry SDK or a dial introduced in core/parse (or anywhere in
// the default graph) would break it.
func TestNoTelemetry_DefaultGraph_RealGate_NoFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live go-list/AST gate in -short mode")
	}
	c := checkNoTelemetry()
	if c.Status != StatusPass {
		t.Fatalf("real no-telemetry gate over the default graph FAILED: %s — offenders: %v", c.Evidence, c.Offenders)
	}
}

var _ = context.Background // keep context import stable for future runtime exercises
