package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/scorecard"
	"github.com/samibel/graphi/internal/evalreport"
)

type staticRunner struct {
	score float64
	err   error
}

func (r staticRunner) Run() (float64, error) { return r.score, r.err }

func allPassGates() map[string]Runner {
	return map[string]Runner{
		"testgate":     staticRunner{score: 100},
		"coverage":     staticRunner{score: 100},
		"privacy":      staticRunner{score: 100},
		"bench-budget": staticRunner{score: 100},
	}
}

// fakeReport builds a measured eval report with the given area scores (ux is
// supplied separately by the ux measurement).
func fakeReport(t *testing.T, areas map[string]float64) evalreport.Report {
	t.Helper()
	full := map[string]float64{
		scorecard.AreaAgentMCP:    100,
		scorecard.AreaSignal:      100,
		scorecard.AreaPerformance: 100,
		scorecard.AreaSetupTrust:  100,
		scorecard.AreaEvaluation:  100,
		scorecard.AreaUX:          0, // ux comes from the web measurement
	}
	for a, s := range areas {
		full[a] = s
	}
	res, err := scorecard.Calculate(full)
	if err != nil {
		t.Fatal(err)
	}
	provenance := map[string]string{}
	for a := range full {
		provenance[a] = "measured"
	}
	return evalreport.Report{Scorecard: res, AreaProvenance: provenance}
}

func passEval(t *testing.T) EvalReportFn {
	return func() (evalreport.Report, error) { return fakeReport(t, nil), nil }
}

func passUX() UXFn {
	return func() (evalreport.UXMetrics, error) {
		files := make([]string, len(requiredUXSuites))
		copy(files, requiredUXSuites)
		return DeriveUX(100, 100, files), nil
	}
}

func TestReleaseGatePasses(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	result, err := Run(allPassGates(), passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected gate to pass; got overall %.1f, errors %v", result.Scorecard.Overall, result.Errors)
	}
	if result.Scorecard.Overall < 90 {
		t.Fatalf("expected overall >= 90, got %.1f", result.Scorecard.Overall)
	}
}

func TestReleaseGateFailsSubEightyArea(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	evalFn := func() (evalreport.Report, error) {
		return fakeReport(t, map[string]float64{scorecard.AreaSignal: 70}), nil
	}
	result, err := Run(allPassGates(), evalFn, passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatal("expected gate to fail on sub-80 area")
	}
	if got := result.Scorecard.Breakdown[scorecard.AreaSignal].Score; got != 70 {
		t.Fatalf("expected signal score 70 from the report, got %v", got)
	}
}

func TestReleaseGateFailsTier1Regression(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	evalFn := func() (evalreport.Report, error) {
		r := fakeReport(t, nil)
		r.RegressionsVsBaseline = []evalreport.Regression{{ScenarioID: "go-symbol", Before: "pass", After: "fail"}}
		return r, nil
	}
	result, err := Run(allPassGates(), evalFn, passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatal("expected gate to fail on a Tier-1 regression")
	}
	if len(result.Regressions) != 1 {
		t.Fatalf("expected the regression to surface, got %v", result.Regressions)
	}
}

func TestReleaseGateFailsRemovedTool(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze", "removed_tool"})

	result, err := Run(allPassGates(), passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatal("expected gate to fail on removed tool")
	}
	if len(result.Removed) != 1 || result.Removed[0] != "removed_tool" {
		t.Fatalf("expected removed_tool, got %v", result.Removed)
	}
}

func TestReleaseGateFailsRedConstituentGate(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	gates := allPassGates()
	gates["coverage"] = staticRunner{err: errors.New("coverage red")}

	result, err := Run(gates, passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatal("expected gate to fail on red constituent gate")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one error, got %v", result.Errors)
	}
}

func TestReleaseGateUXFromWebSuite(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	// 90% pass fraction with every required suite present → ux 90; the ux
	// floor holds but a sub-80 fraction must floor the area.
	uxFn := func() (evalreport.UXMetrics, error) {
		files := make([]string, len(requiredUXSuites))
		copy(files, requiredUXSuites)
		return DeriveUX(100, 90, files), nil
	}
	result, err := Run(allPassGates(), passEval(t), uxFn, baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.Scorecard.Breakdown[scorecard.AreaUX].Score; got != 90 {
		t.Fatalf("expected ux score 90 from the web measurement, got %v", got)
	}
	if !result.Pass {
		t.Fatalf("expected pass at ux=90, got errors %v", result.Errors)
	}
}

func TestDeriveUXPenalizesMissingRequiredSuite(t *testing.T) {
	files := []string{"web/src/SymbolSearchPanel.test.tsx"} // 1 of 5 required
	m := DeriveUX(50, 50, files)
	if len(m.RequiredMissing) != 4 {
		t.Fatalf("expected 4 missing suites, got %v", m.RequiredMissing)
	}
	if m.Score != 20 {
		t.Fatalf("expected score 20 (100%% pass × 1/5 presence), got %v", m.Score)
	}
}

func TestPublish(t *testing.T) {
	dir := t.TempDir()
	report := fakeReport(t, nil)
	ux := DeriveUX(10, 10, requiredUXSuites)
	result := GateResult{Scorecard: report.Scorecard, Report: report, UX: &ux, Pass: true}
	if err := Publish(result, dir, "0.1.0", "abc123"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	for _, f := range []string{"release-scorecard.json", "release-scorecard.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "release-scorecard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed evalreport.Report
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.UXMetrics == nil || parsed.UXMetrics.Score != 100 {
		t.Fatalf("published report must embed ux metrics, got %+v", parsed.UXMetrics)
	}
}

func writeBaseline(t *testing.T, path string, tools []string) {
	t.Helper()
	b, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseGateUnverifiedPrivacyIsAWarning pins the platform-robustness
// contract: a privacy audit that cannot OBSERVE the network layer (macOS /
// unprivileged local run) degrades to a warning — the Linux CI deny-egress
// gate does the real verification — while an actual VIOLATED posture still
// blocks (staticRunner with a plain error, covered above).
func TestReleaseGateUnverifiedPrivacyIsAWarning(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	gates := allPassGates()
	gates["privacy"] = staticRunner{err: &UnverifiedError{Detail: "no netns isolation on this platform"}}

	result, err := Run(gates, passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected pass with unverified-platform warning, got errors %v", result.Errors)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected one warning, got %v", result.Warnings)
	}
}
