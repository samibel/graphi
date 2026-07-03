package explain

import (
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

func TestExplainReturnsEmptyResult(t *testing.T) {
	res, err := Explain("some.Symbol")
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

func TestExplainRequiresReference(t *testing.T) {
	if _, err := Explain(""); err == nil {
		t.Fatal("expected error for empty reference")
	}
}
