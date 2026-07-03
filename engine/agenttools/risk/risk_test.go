package risk

import (
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

func TestAssessTargetReturnsEmptyResult(t *testing.T) {
	res, err := Assess("some.Symbol", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty outcome, got %s", res.Outcome)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestAssessRequiresInput(t *testing.T) {
	if _, err := Assess("", ""); err == nil {
		t.Fatal("expected error for missing target and diff")
	}
}
