package related

import (
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

func TestFilesReturnsEmptyResult(t *testing.T) {
	res, err := Files("github.com/samibel/graphi/engine/query", "both")
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

func TestFilesRequiresAnchor(t *testing.T) {
	if _, err := Files("", "both"); err == nil {
		t.Fatal("expected error for empty anchor")
	}
}
