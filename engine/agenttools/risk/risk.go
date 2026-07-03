package risk

import (
	"errors"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// Assess returns a change-risk evaluation in the C1 contract shape. This is a
// scaffold implementation: live engine integration is pending.
func Assess(target, diff string) (*contract.Result, error) {
	if target == "" && diff == "" {
		return nil, errors.New("missing target or diff")
	}
	return &contract.Result{
		Outcome: contract.OutcomeEmpty,
		Summary: "change_risk scaffold: live engine integration pending; prefer unknown over guessing",
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"unknown": 1.0},
			Top:          "unknown",
			Method:       "stub",
		},
		Limits: contract.Limits{},
	}, nil
}
