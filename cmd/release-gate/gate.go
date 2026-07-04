package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/engine/scorecard"
	"github.com/samibel/graphi/internal/evalreport"
	"github.com/samibel/graphi/surfaces/mcp"
)

// AreaScore is a simple pass/fail score for a single area.
type AreaScore struct {
	Area  string
	Score float64
	Error string
}

// GateResult captures the scorecard plus any failures.
type GateResult struct {
	Scorecard scorecard.Result
	Removed   []string
	Errors    []string
	Pass      bool
}

// Run executes the full release gate and returns the result.
func Run(runners map[string]Runner, baselinePath string) (GateResult, error) {
	var scores = map[string]float64{}
	var errors []string

	areaMapping := []struct {
		name   string
		area   string
		weight int
	}{
		{"testgate", scorecard.AreaEvaluation, scorecard.WeightEvaluation},
		{"eval", scorecard.AreaSignal, scorecard.WeightSignal},
		{"coverage", scorecard.AreaAgentMCP, scorecard.WeightAgentMCP},
		{"privacy", scorecard.AreaSetupTrust, scorecard.WeightSetupTrust},
		{"perf", scorecard.AreaPerformance, scorecard.WeightPerformance},
		{"web", scorecard.AreaUX, scorecard.WeightUX},
	}

	for _, m := range areaMapping {
		r, ok := runners[m.name]
		if !ok {
			scores[m.area] = 0
			errors = append(errors, fmt.Sprintf("runner %s missing", m.name))
			continue
		}
		s, err := r.Run()
		if err != nil {
			scores[m.area] = 0
			errors = append(errors, fmt.Sprintf("%s failed: %v", m.name, err))
			continue
		}
		scores[m.area] = s
	}

	removed, err := checkToolBaseline(baselinePath)
	if err != nil {
		errors = append(errors, fmt.Sprintf("tool baseline check: %v", err))
	}

	res, err := scorecard.Calculate(scores)
	if err != nil {
		return GateResult{}, fmt.Errorf("scorecard calculation: %w", err)
	}

	return GateResult{
		Scorecard: res,
		Removed:   removed,
		Errors:    errors,
		Pass:      res.Pass && len(removed) == 0 && len(errors) == 0,
	}, nil
}

func checkToolBaseline(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var baseline []string
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, err
	}
	current := mcp.ToolNames()
	set := make(map[string]bool, len(current))
	for _, c := range current {
		set[c] = true
	}
	var removed []string
	for _, b := range baseline {
		if !set[b] {
			removed = append(removed, b)
		}
	}
	return removed, nil
}

// Runner executes a single constituent gate and returns a 0-100 score.
type Runner interface {
	Run() (float64, error)
}

// Publish writes the scorecard evidence to docs/.
func Publish(result GateResult, docsDir, version, commit string) error {
	header := evalreport.NewHeader(version, commit)
	report := evalreport.Report{
		Header:    header,
		Scorecard: result.Scorecard,
		Baseline:  90.0,
		Target:    90.0,
	}
	if err := evalreport.WriteJSON(report, filepath.Join(docsDir, "release-scorecard.json")); err != nil {
		return err
	}
	return evalreport.WriteMarkdown(report, filepath.Join(docsDir, "release-scorecard.md"))
}

// FormatVerdict returns a human-readable summary.
func FormatVerdict(result GateResult) string {
	var b string
	b += fmt.Sprintf("Release gate: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[result.Pass])
	b += fmt.Sprintf("Overall: %.1f/100\n", result.Scorecard.Overall)
	var keys []string
	for k := range result.Scorecard.Breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := result.Scorecard.Breakdown[k]
		b += fmt.Sprintf("  %s: %.1f/100 (weight %d, below_floor=%v)\n", k, v.Score, v.Weight, v.BelowFloor)
	}
	for _, r := range result.Removed {
		b += fmt.Sprintf("  removed tool: %s\n", r)
	}
	for _, e := range result.Errors {
		b += fmt.Sprintf("  error: %s\n", e)
	}
	return b
}
