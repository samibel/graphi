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

// GateResult captures the measured scorecard plus every blocking condition.
type GateResult struct {
	Scorecard   scorecard.Result
	Report      evalreport.Report // the eval scorecard report the gate consumed
	UX          *evalreport.UXMetrics
	Removed     []string // MCP tools present in the baseline but missing live
	Regressions []evalreport.Regression
	Errors      []string
	Pass        bool
}

// Runner executes one hard constituent gate. A non-nil error blocks the
// release; the returned score is informational only — the 9/10 verdict comes
// from the MEASURED eval scorecard report, never from runner pass/fail
// averaging.
type Runner interface {
	Run() (float64, error)
}

// EvalReportFn produces the measured eval scorecard report (cmd/eval
// -manifest ... -tier 1). Injectable for tests.
type EvalReportFn func() (evalreport.Report, error)

// UXFn produces the measured web-suite UX metrics. Injectable for tests.
type UXFn func() (evalreport.UXMetrics, error)

// Run executes the release gate:
//
//  1. every hard gate (testgate, coverage, privacy) must succeed;
//  2. the eval scorecard report supplies the MEASURED area scores;
//  3. the web suite supplies the measured ux score;
//  4. the final scorecard is recomputed from those inputs.
//
// The release is blocked when any hard gate fails, an MCP tool was removed
// against the baseline, the report carries a Tier-1 regression, any area is
// below the 80 floor, or the overall score is below 90.
func Run(gates map[string]Runner, evalFn EvalReportFn, uxFn UXFn, baselinePath string) (GateResult, error) {
	var res GateResult

	gateNames := make([]string, 0, len(gates))
	for name := range gates {
		gateNames = append(gateNames, name)
	}
	sort.Strings(gateNames)
	for _, name := range gateNames {
		if _, err := gates[name].Run(); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s failed: %v", name, err))
		}
	}

	report, err := evalFn()
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("eval report: %v", err))
	}
	res.Report = report
	res.Regressions = report.RegressionsVsBaseline

	scores := map[string]float64{}
	for area, ar := range report.Scorecard.Breakdown {
		scores[area] = ar.Score
	}

	ux, err := uxFn()
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("ux measurement: %v", err))
	} else {
		res.UX = &ux
		scores[scorecard.AreaUX] = ux.Score
		if report.AreaProvenance == nil {
			report.AreaProvenance = map[string]string{}
		}
		report.AreaProvenance[scorecard.AreaUX] = "measured"
		res.Report.AreaProvenance = report.AreaProvenance
	}

	removed, err := checkToolBaseline(baselinePath)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("tool baseline check: %v", err))
	}
	res.Removed = removed

	// An incomplete score set (failed eval run) must not panic the gate; fill
	// missing areas with zero so the calculation names them as floored.
	for _, area := range []string{
		scorecard.AreaAgentMCP, scorecard.AreaSignal, scorecard.AreaPerformance,
		scorecard.AreaSetupTrust, scorecard.AreaEvaluation, scorecard.AreaUX,
	} {
		if _, ok := scores[area]; !ok {
			scores[area] = 0
		}
	}
	final, err := scorecard.Calculate(scores)
	if err != nil {
		return GateResult{}, fmt.Errorf("scorecard calculation: %w", err)
	}
	res.Scorecard = final

	res.Pass = final.Pass &&
		len(res.Errors) == 0 &&
		len(res.Removed) == 0 &&
		len(res.Regressions) == 0
	return res, nil
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

// Publish writes the scorecard evidence to docs/: the full measured eval
// report with the gate's recomputed scorecard and ux metrics embedded.
func Publish(result GateResult, docsDir, version, commit string) error {
	report := result.Report
	report.Header = evalreport.NewHeader(version, commit)
	report.Scorecard = result.Scorecard
	report.UXMetrics = result.UX
	report.Target = 90.0
	if err := evalreport.WriteJSON(report, filepath.Join(docsDir, "release-scorecard.json")); err != nil {
		return err
	}
	return evalreport.WriteMarkdown(report, filepath.Join(docsDir, "release-scorecard.md"))
}

// FormatVerdict returns a human-readable summary.
func FormatVerdict(result GateResult) string {
	var b string
	b += fmt.Sprintf("Release gate: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[result.Pass])
	b += fmt.Sprintf("Overall: %.1f/100 (pass needs >= 90 overall, every area >= 80)\n", result.Scorecard.Overall)
	var keys []string
	for k := range result.Scorecard.Breakdown {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := result.Scorecard.Breakdown[k]
		prov := result.Report.AreaProvenance[k]
		if prov == "" {
			prov = "unknown"
		}
		b += fmt.Sprintf("  %s: %.1f/100 (weight %d, below_floor=%v, %s)\n", k, v.Score, v.Weight, v.BelowFloor, prov)
	}
	for _, r := range result.Regressions {
		b += fmt.Sprintf("  tier-1 regression: %s (%s → %s)\n", r.ScenarioID, r.Before, r.After)
	}
	for _, r := range result.Removed {
		b += fmt.Sprintf("  removed tool: %s\n", r)
	}
	for _, e := range result.Errors {
		b += fmt.Sprintf("  error: %s\n", e)
	}
	return b
}
