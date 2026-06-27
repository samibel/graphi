package eval

import (
	"encoding/json"
	"testing"
)

func TestCapabilities_CanonicalSet(t *testing.T) {
	caps := Capabilities()
	want := []string{"callees", "callers", "definition", "implementers", "implements", "neighborhood", "overrides", "references", "search", "subtypes", "supertypes"}
	if len(caps) != len(want) {
		t.Fatalf("Capabilities() = %v, want %v", caps, want)
	}
	for i, c := range caps {
		if c != want[i] {
			t.Errorf("Capabilities()[%d] = %q, want %q (full: %v)", i, c, want[i], caps)
		}
	}
}

func TestTokenize_Deterministic(t *testing.T) {
	text := "the quick brown fox\nthe quick brown fox"
	a := CountTokens(text)
	b := CountTokens(text)
	if a != b || a != 8 {
		t.Errorf("CountTokens not deterministic/stable: a=%d b=%d", a, b)
	}
}

func TestRun_PerCaseRatiosAndAggregate(t *testing.T) {
	ds, err := LoadDataset()
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(ds, false, DefaultClaimThreshold)
	if err != nil {
		t.Fatal(err)
	}
	var sumG, sumB int
	for _, c := range rep.Cases {
		if c.Ratio != float64(c.BaselineTokens)/float64(c.GraphiTokens) {
			t.Errorf("case %s ratio wrong: %f", c.ID, c.Ratio)
		}
		sumG += c.GraphiTokens
		sumB += c.BaselineTokens
	}
	if rep.AggregateRatio != float64(sumB)/float64(sumG) {
		t.Errorf("aggregate ratio wrong: %f", rep.AggregateRatio)
	}
}

func TestRun_AllCapabilitiesCovered(t *testing.T) {
	ds, _ := LoadDataset()
	rep, err := Run(ds, false, DefaultClaimThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Uncovered) != 0 {
		t.Errorf("committed dataset has uncovered capabilities: %v", rep.Uncovered)
	}
	for _, cap := range Capabilities() {
		if len(rep.Coverage[cap]) == 0 {
			t.Errorf("capability %s has no coverage", cap)
		}
	}
}

func TestRun_ClaimSupportedOnCommittedDataset(t *testing.T) {
	ds, _ := LoadDataset()
	rep, err := Run(ds, true, DefaultClaimThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ClaimSupported {
		t.Fatalf("committed dataset should support the ~50x claim (aggregate=%.2f threshold=%.0f); enlarge baseline contexts or lower the threshold honestly", rep.AggregateRatio, rep.ClaimThreshold)
	}
	if rep.ClaimHeldBack {
		t.Error("claim should not be held back on committed dataset")
	}
}

func TestClaim_HeldBackBelowThreshold(t *testing.T) {
	ds, _ := LoadDataset()
	rep, err := Run(ds, true, 1e9) // impossibly high threshold
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ClaimHeldBack {
		t.Error("expected claim held back under impossible threshold")
	}
	if rep.Pass {
		t.Error("expected overall FAIL in claim mode when held back")
	}
}

func TestCoverage_DriftRegressionCaught(t *testing.T) {
	d := CheckDrift([]string{"a"}, []string{"a", "b"})
	if !d.Regressed || len(d.Lost) != 1 || d.Lost[0] != "b" {
		t.Errorf("expected regression with lost=[b], got %+v", d)
	}
}

func TestCoverage_NoRegressionOnGain(t *testing.T) {
	d := CheckDrift([]string{"a", "b", "c"}, []string{"a", "b"})
	if d.Regressed {
		t.Errorf("gain should not regress, got %+v", d)
	}
	if len(d.Gained) != 1 || d.Gained[0] != "c" {
		t.Errorf("expected gained=[c], got %+v", d)
	}
}

func TestDeterministic_ByteIdenticalReport(t *testing.T) {
	ds, _ := LoadDataset()
	r1, _ := Run(ds, true, DefaultClaimThreshold)
	r2, _ := Run(ds, true, DefaultClaimThreshold)
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Error("report not byte-identical across runs (non-deterministic)")
	}
}

func TestDataset_RejectsUndeclaredCapability(t *testing.T) {
	bad := &Dataset{Version: "x", Cases: []Case{{ID: "c1", Capability: "bogus", GraphiContext: "a", BaselineContext: "b"}}}
	if err := bad.Validate(); err == nil {
		t.Error("expected Validate to reject undeclared capability")
	}
}

func TestBaselineVersion_StampEnforced(t *testing.T) {
	if err := AssertBaselineVersion(FixtureBaselineVersion); err != nil {
		t.Errorf("matching version should pass: %v", err)
	}
	if err := AssertBaselineVersion("stale-v0"); err == nil {
		t.Error("expected drift error for stale pinned version")
	}
}
