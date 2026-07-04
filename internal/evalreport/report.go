package evalreport

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/samibel/graphi/engine/scorecard"
)

// Header contains provenance metadata.
type Header struct {
	Timestamp string `json:"timestamp"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	// CorpusVersion is the version stamp of the corpus manifest the run used.
	CorpusVersion int `json:"corpus_version,omitempty"`
}

// PerRepoMetric is a lightweight metric per corpus fixture.
type PerRepoMetric struct {
	Name       string `json:"name"`
	Tier       int    `json:"tier"`
	DurationMS int64  `json:"duration_ms"`
	Pass       bool   `json:"pass"`
}

// PerScenarioResult is one scenario outcome.
type PerScenarioResult struct {
	ID            string   `json:"id"`
	FixtureRef    string   `json:"fixture_ref"`
	Operation     string   `json:"operation"`
	Outcome       string   `json:"outcome"`
	ResultSize    int      `json:"result_size"`
	Evidence      []string `json:"evidence"`
	Confidence    *float64 `json:"confidence,omitempty"`
	LatencyMS     int64    `json:"latency_ms"`
	AnchorPresent bool     `json:"anchor_present"`
	Tier1         bool     `json:"tier1"`
}

// Regression is a pass→fail change on a Tier-1 scenario.
type Regression struct {
	ScenarioID string `json:"scenario_id"`
	Before     string `json:"before"`
	After      string `json:"after"`
}

// Report is the union of provenance, results, scorecard, and regressions.
type Report struct {
	Header             Header              `json:"header"`
	PerRepoMetrics     []PerRepoMetric     `json:"per_repo_metrics"`
	PerScenarioResults []PerScenarioResult `json:"per_scenario_results"`
	Scorecard          scorecard.Result    `json:"scorecard"`
	// AreaProvenance records, per scorecard area, whether its input score was
	// "measured" by this run or "carried" from the baseline (not yet measured
	// by the harness). Carried areas are also listed in PerfWarnings so the
	// report never silently presents a baseline number as a measurement.
	AreaProvenance        map[string]string `json:"area_provenance,omitempty"`
	Baseline              float64           `json:"baseline"`
	Target                float64           `json:"target"`
	RegressionsVsBaseline []Regression      `json:"regressions_vs_baseline"`
	PerfWarnings          []string          `json:"perf_warnings,omitempty"`
}

// NewHeader builds a header from runtime info.
func NewHeader(version, commit string) Header {
	return Header{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Version:   version,
		Commit:    commit,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// HasTier1Regression returns true if any Tier-1 scenario regressed pass→fail.
func (r Report) HasTier1Regression() bool {
	return len(r.RegressionsVsBaseline) > 0
}

// BaselineRecord is the persisted baseline artifact.
type BaselineRecord struct {
	Version   float64            `json:"version"`
	Baseline  float64            `json:"baseline"`
	Target    float64            `json:"target"`
	Scenarios []BaselineScenario `json:"scenarios"`
}

// BaselineScenario records the expected outcome for a scenario.
type BaselineScenario struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Tier1   bool   `json:"tier1"`
}

// DiffAgainstBaseline computes regressions and perf warnings.
func DiffAgainstBaseline(report Report, baseline BaselineRecord) ([]Regression, []string) {
	byID := make(map[string]BaselineScenario)
	for _, b := range baseline.Scenarios {
		byID[b.ID] = b
	}
	var regressions []Regression
	var perfWarnings []string
	for _, r := range report.PerScenarioResults {
		b, ok := byID[r.ID]
		if !ok {
			regressions = append(regressions, Regression{ScenarioID: r.ID, Before: "absent", After: r.Outcome})
			continue
		}
		if b.Tier1 && b.Outcome == "pass" && r.Outcome != "pass" {
			regressions = append(regressions, Regression{ScenarioID: r.ID, Before: b.Outcome, After: r.Outcome})
		}
		if !b.Tier1 && b.Outcome == "pass" && r.Outcome != "pass" {
			perfWarnings = append(perfWarnings, fmt.Sprintf("non-tier-1 scenario %s regressed (%s → %s)", r.ID, b.Outcome, r.Outcome))
		}
	}
	sort.Slice(regressions, func(i, j int) bool { return regressions[i].ScenarioID < regressions[j].ScenarioID })
	sort.Strings(perfWarnings)
	return regressions, perfWarnings
}

// WriteJSON writes the report as stable JSON.
func WriteJSON(r Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// WriteMarkdown writes the report as Markdown using a fixed template.
func WriteMarkdown(r Report, path string) error {
	const tmpl = `# Eval Report

**Timestamp:** {{.Header.Timestamp}}
**Version:** {{.Header.Version}}
**Commit:** {{.Header.Commit}}
**OS/Arch:** {{.Header.OS}}/{{.Header.Arch}}
**Corpus version:** {{.Header.CorpusVersion}}

## Scorecard

**Overall:** {{printf "%.1f" .Scorecard.Overall}}  
**Pass:** {{.Scorecard.Pass}}  
**Baseline → Target:** {{printf "%.1f" .Baseline}} → {{printf "%.1f" .Target}}

### Breakdown

| Area | Score | Weight | Contribution | Below Floor | Provenance |
|------|-------|--------|--------------|-------------|------------|
{{range $k, $v := .Scorecard.Breakdown}}| {{$k}} | {{printf "%.1f" $v.Score}} | {{$v.Weight}} | {{printf "%.2f" $v.Contribution}} | {{$v.BelowFloor}} | {{index $.AreaProvenance $k}} |
{{end}}

## Per-Repo Metrics

| Name | Tier | Pass | Duration (ms) |
|------|------|------|---------------|
{{range .PerRepoMetrics}}| {{.Name}} | {{.Tier}} | {{.Pass}} | {{.DurationMS}} |
{{end}}

## Per-Scenario Results

| ID | Fixture | Operation | Outcome | Size | Latency (ms) | Anchor Present | Tier 1 |
|----|---------|-----------|---------|------|--------------|----------------|--------|
{{range .PerScenarioResults}}| {{.ID}} | {{.FixtureRef}} | {{.Operation}} | {{.Outcome}} | {{.ResultSize}} | {{.LatencyMS}} | {{.AnchorPresent}} | {{.Tier1}} |
{{end}}

## Regressions vs Baseline

{{if .RegressionsVsBaseline}}
{{range .RegressionsVsBaseline}}- {{.ScenarioID}}: {{.Before}} → {{.After}}
{{end}}
{{else}}No regressions detected.
{{end}}

{{if .PerfWarnings}}
## Performance Warnings

{{range .PerfWarnings}}- {{.}}
{{end}}
{{end}}
`
	var b strings.Builder
	t := template.Must(template.New("report").Parse(tmpl))
	if err := t.Execute(&b, r); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// DefaultBaseline returns the checked-in ~6.5/10 baseline.
func DefaultBaseline() BaselineRecord {
	return BaselineRecord{
		Version:  1,
		Baseline: 65.0,
		Target:   90.0,
		Scenarios: []BaselineScenario{
			{ID: "go-symbol", Outcome: "pass", Tier1: true},
			{ID: "ts-symbol", Outcome: "pass", Tier1: true},
			{ID: "python-symbol", Outcome: "pass", Tier1: true},
			{ID: "empty-result", Outcome: "pass", Tier1: true},
			{ID: "anchor-absent", Outcome: "pass", Tier1: true},
		},
	}
}

// DefaultScorecard returns a valid scorecard for testing/reporting.
func DefaultScorecard() scorecard.Result {
	res, _ := scorecard.Calculate(BaselineAreaScores())
	return res
}

// BaselineAreaScores is the checked-in ~6.5/10 baseline per area. It is the
// carry-forward source for areas the harness cannot measure yet.
func BaselineAreaScores() map[string]float64 {
	return map[string]float64{
		scorecard.AreaAgentMCP:    70,
		scorecard.AreaSignal:      68,
		scorecard.AreaPerformance: 66,
		scorecard.AreaSetupTrust:  65,
		scorecard.AreaEvaluation:  60,
		scorecard.AreaUX:          62,
	}
}

// DeriveAreaScores computes scorecard area inputs from the run's actual data,
// carrying baseline values for areas the harness does not measure yet.
//
// Measured areas:
//   - agent_mcp: pass fraction of the EP-020 agent-tool scenarios × 100
//   - eval:      pass fraction of ALL scenarios × 100
//   - perf:      fraction of per-repo/tier runs that passed × 100 (only when
//     repo metrics exist)
//
// Everything else (signal, setup_trust, ux) is carried from baseline. The
// returned provenance map records "measured" or "carried" per area, and the
// warnings list names every carried area explicitly.
func DeriveAreaScores(scenarios []PerScenarioResult, repos []PerRepoMetric) (map[string]float64, map[string]string, []string) {
	scores := BaselineAreaScores()
	provenance := map[string]string{}
	for area := range scores {
		provenance[area] = "carried"
	}

	agentToolOps := map[string]bool{
		"explain_symbol": true, "related_files": true, "change_risk": true, "agent_brief": true,
	}
	var total, passed, agentTotal, agentPassed int
	for _, s := range scenarios {
		total++
		pass := s.Outcome == "pass"
		if pass {
			passed++
		}
		if agentToolOps[s.Operation] {
			agentTotal++
			if pass {
				agentPassed++
			}
		}
	}
	if total > 0 {
		scores[scorecard.AreaEvaluation] = float64(passed) / float64(total) * 100
		provenance[scorecard.AreaEvaluation] = "measured"
	}
	if agentTotal > 0 {
		scores[scorecard.AreaAgentMCP] = float64(agentPassed) / float64(agentTotal) * 100
		provenance[scorecard.AreaAgentMCP] = "measured"
	}
	if len(repos) > 0 {
		repoPassed := 0
		for _, r := range repos {
			if r.Pass {
				repoPassed++
			}
		}
		scores[scorecard.AreaPerformance] = float64(repoPassed) / float64(len(repos)) * 100
		provenance[scorecard.AreaPerformance] = "measured"
	}

	var warnings []string
	areas := make([]string, 0, len(provenance))
	for area := range provenance {
		areas = append(areas, area)
	}
	sort.Strings(areas)
	for _, area := range areas {
		if provenance[area] == "carried" {
			warnings = append(warnings, fmt.Sprintf("area %s carried from baseline (%.0f), not measured by this run", area, scores[area]))
		}
	}
	return scores, provenance, warnings
}
