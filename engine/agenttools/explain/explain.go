package explain

import (
	"errors"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// Explain returns a compact symbol-identity summary in the C1 contract shape.
// This is a scaffold implementation: live engine integration is pending.
func Explain(ref string) (*contract.Result, error) {
	if ref == "" {
		return nil, errors.New("missing symbol reference")
	}
	return &contract.Result{
		Outcome: contract.OutcomeEmpty,
		Summary: "explain_symbol scaffold: live engine integration pending; pass a qualified id or file:line reference",
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"empty": 1.0},
			Top:          "empty",
			Method:       "stub",
		},
		Limits: contract.Limits{},
	}, nil
}
