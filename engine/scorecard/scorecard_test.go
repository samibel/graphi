package scorecard

import (
	"math"
	"testing"
)

func TestTotalWeight(t *testing.T) {
	if got := TotalWeight(); got != 100 {
		t.Fatalf("weights must sum to 100, got %d", got)
	}
}

func TestCalculateGoldenInput(t *testing.T) {
	scores := map[string]float64{
		AreaAgentMCP:   95,
		AreaSignal:     92,
		AreaPerformance: 88,
		AreaSetupTrust: 85,
		AreaEvaluation: 90,
		AreaUX:         87,
	}
	res, err := Calculate(scores)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 95*0.25 + 92*0.20 + 88*0.20 + 85*0.15 + 90*0.10 + 87*0.10
	if math.Abs(res.Overall-want) > 1e-9 {
		t.Fatalf("overall = %v, want %v", res.Overall, want)
	}
	if !res.Pass {
		t.Fatal("expected pass")
	}
}

func TestCalculateExactlyAtPassBoundary(t *testing.T) {
	scores := all(90.0)
	res, err := Calculate(scores)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected pass at overall 90 with all areas >= 80")
	}
}

func TestCalculateFailsBelowOverall(t *testing.T) {
	scores := all(89.99)
	res, err := Calculate(scores)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected fail at 89.99 overall")
	}
}

func TestCalculateFailsAreaBelowFloor(t *testing.T) {
	scores := all(95.0)
	scores[AreaSignal] = 79.0
	res, err := Calculate(scores)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected fail when an area is below 80")
	}
	if len(res.FlooredAreas) != 1 || res.FlooredAreas[0] != AreaSignal {
		t.Fatalf("expected AreaSignal floored, got %v", res.FlooredAreas)
	}
}

func TestCalculateRejectsMissingArea(t *testing.T) {
	scores := map[string]float64{
		AreaAgentMCP: 95,
	}
	if _, err := Calculate(scores); err == nil {
		t.Fatal("expected error for missing areas")
	}
}

func TestCalculateRejectsUnknownArea(t *testing.T) {
	scores := all(80.0)
	scores["unknown"] = 80.0
	if _, err := Calculate(scores); err == nil {
		t.Fatal("expected error for unknown area")
	}
}

func TestCalculateRejectsOutOfRangeScore(t *testing.T) {
	scores := all(80.0)
	scores[AreaUX] = 101.0
	if _, err := Calculate(scores); err == nil {
		t.Fatal("expected error for out-of-range score")
	}
}

func TestRoundForDisplay(t *testing.T) {
	if got := RoundForDisplay(89.95); got != 90.0 {
		t.Fatalf("RoundForDisplay(89.95) = %v, want 90.0", got)
	}
	if got := RoundForDisplay(89.94); got != 89.9 {
		t.Fatalf("RoundForDisplay(89.94) = %v, want 89.9", got)
	}
}

func all(v float64) map[string]float64 {
	return map[string]float64{
		AreaAgentMCP:    v,
		AreaSignal:      v,
		AreaPerformance: v,
		AreaSetupTrust:  v,
		AreaEvaluation:  v,
		AreaUX:          v,
	}
}
