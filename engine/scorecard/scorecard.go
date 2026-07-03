package scorecard

import (
	"fmt"
	"math"
	"sort"
)

// Area keys map 1:1 to the program's six milestone areas.
const (
	AreaAgentMCP   = "agent_mcp"
	AreaSignal     = "signal"
	AreaPerformance = "performance"
	AreaSetupTrust = "setup_trust"
	AreaEvaluation = "evaluation"
	AreaUX         = "ux"
)

// Weights are named constants and must sum to exactly 100.
const (
	WeightAgentMCP   = 25
	WeightSignal     = 20
	WeightPerformance = 20
	WeightSetupTrust = 15
	WeightEvaluation = 10
	WeightUX         = 10
)

var (
	areaWeights = map[string]int{
		AreaAgentMCP:   WeightAgentMCP,
		AreaSignal:     WeightSignal,
		AreaPerformance: WeightPerformance,
		AreaSetupTrust: WeightSetupTrust,
		AreaEvaluation: WeightEvaluation,
		AreaUX:         WeightUX,
	}
	areaKeys = []string{
		AreaAgentMCP,
		AreaSignal,
		AreaPerformance,
		AreaSetupTrust,
		AreaEvaluation,
		AreaUX,
	}
)

// AreaResult is the per-area breakdown.
type AreaResult struct {
	Score      float64 `json:"score"`
	Weight     int     `json:"weight"`
	Contribution float64 `json:"contribution"`
	BelowFloor bool    `json:"below_floor"`
}

// Result is the overall scorecard result.
type Result struct {
	Overall      float64               `json:"overall"`
	Pass         bool                  `json:"pass"`
	FlooredAreas []string              `json:"floored_areas"`
	Breakdown    map[string]AreaResult `json:"breakdown"`
}

// Calculate turns six normalized area scores into a weighted 0–100 result.
// It is a pure function: no I/O, no clock, no network.
func Calculate(scores map[string]float64) (Result, error) {
	if len(scores) != len(areaKeys) {
		return Result{}, fmt.Errorf("scorecard: expected %d areas, got %d", len(areaKeys), len(scores))
	}
	for k := range scores {
		if _, ok := areaWeights[k]; !ok {
			return Result{}, fmt.Errorf("scorecard: unknown area %q", k)
		}
	}
	for _, k := range areaKeys {
		s := scores[k]
		if s < 0 || s > 100 {
			return Result{}, fmt.Errorf("scorecard: area %q score %f out of range [0,100]", k, s)
		}
	}

	breakdown := make(map[string]AreaResult, len(areaKeys))
	var overall float64
	var floored []string
	for _, k := range areaKeys {
		w := areaWeights[k]
		s := scores[k]
		contrib := s * float64(w) / 100.0
		below := s < 80.0
		if below {
			floored = append(floored, k)
		}
		breakdown[k] = AreaResult{
			Score:      s,
			Weight:     w,
			Contribution: contrib,
			BelowFloor: below,
		}
		overall += contrib
	}
	sort.Strings(floored)
	pass := overall >= 90.0 && len(floored) == 0

	return Result{
		Overall:      overall,
		Pass:         pass,
		FlooredAreas: floored,
		Breakdown:    breakdown,
	}, nil
}

// RoundForDisplay rounds a score to one decimal place, half-up.
func RoundForDisplay(v float64) float64 {
	return math.Round(v*10) / 10
}

// TotalWeight returns the sum of all weights (should be 100).
func TotalWeight() int {
	var total int
	for _, w := range areaWeights {
		total += w
	}
	return total
}
