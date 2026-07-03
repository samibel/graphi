package related

import (
	"errors"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// Files returns a deterministically ranked read-first file list in the C1
// contract shape. This is a scaffold implementation: live engine integration is
// pending.
func Files(anchor, direction string) (*contract.Result, error) {
	if anchor == "" {
		return nil, errors.New("missing anchor")
	}
	return &contract.Result{
		Outcome: contract.OutcomeEmpty,
		Summary: "related_files scaffold: live engine integration pending",
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"empty": 1.0},
			Top:          "empty",
			Method:       "stub",
		},
		Limits: contract.Limits{},
	}, nil
}
