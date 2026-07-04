package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/scorecard"
)

type staticRunner struct {
	score float64
	err   error
}

func (r staticRunner) Run() (float64, error) { return r.score, r.err }

func allPassRunners() map[string]Runner {
	return map[string]Runner{
		"testgate": staticRunner{score: 100},
		"eval":     staticRunner{score: 100},
		"coverage": staticRunner{score: 100},
		"privacy":  staticRunner{score: 100},
		"perf":     staticRunner{score: 100},
		"web":      staticRunner{score: 100},
	}
}

func TestReleaseGatePasses(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	result, err := Run(allPassRunners(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected gate to pass; got overall %.1f, floored %v", result.Scorecard.Overall, result.Scorecard.FlooredAreas)
	}
	if len(result.Removed) != 0 {
		t.Fatalf("expected no removed tools, got %v", result.Removed)
	}
}

func TestReleaseGateFailsSubEightArea(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	runners := allPassRunners()
	runners["privacy"] = staticRunner{score: 70} // below 80 floor

	result, err := Run(runners, baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatalf("expected gate to fail on sub-8 area")
	}
	if result.Scorecard.Breakdown[scorecard.AreaSetupTrust].Score != 70 {
		t.Fatalf("expected setup_trust score 70, got %v", result.Scorecard.Breakdown[scorecard.AreaSetupTrust])
	}
}

func TestReleaseGateFailsRemovedTool(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze", "removed_tool"})

	result, err := Run(allPassRunners(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatalf("expected gate to fail on removed tool")
	}
	want := []string{"removed_tool"}
	if len(result.Removed) != 1 || result.Removed[0] != want[0] {
		t.Fatalf("expected removed tools %v, got %v", want, result.Removed)
	}
}

func TestReleaseGateFailsRedConstituentGate(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	runners := allPassRunners()
	runners["coverage"] = staticRunner{err: errors.New("coverage red")}

	result, err := Run(runners, baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatalf("expected gate to fail on red constituent gate")
	}
	if result.Scorecard.Breakdown[scorecard.AreaAgentMCP].Score != 0 {
		t.Fatalf("expected agent_mcp score 0 after failure, got %v", result.Scorecard.Breakdown[scorecard.AreaAgentMCP])
	}
}

func TestPublish(t *testing.T) {
	dir := t.TempDir()
	scores := map[string]float64{}
	for _, a := range []string{
		scorecard.AreaAgentMCP, scorecard.AreaSignal, scorecard.AreaPerformance,
		scorecard.AreaSetupTrust, scorecard.AreaEvaluation, scorecard.AreaUX,
	} {
		scores[a] = 100
	}
	res, _ := scorecard.Calculate(scores)
	result := GateResult{Scorecard: res, Pass: true}
	if err := Publish(result, dir, "0.1.0", "abc123"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "release-scorecard.json")); err != nil {
		t.Fatalf("missing json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "release-scorecard.md")); err != nil {
		t.Fatalf("missing md: %v", err)
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
