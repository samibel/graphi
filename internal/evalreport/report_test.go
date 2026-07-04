package evalreport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/scorecard"
)

func TestDiffAgainstBaseline(t *testing.T) {
	report := Report{
		PerScenarioResults: []PerScenarioResult{
			{ID: "pass-to-fail", Outcome: "fail", Tier1: true},
			{ID: "stable-pass", Outcome: "pass", Tier1: true},
			{ID: "non-tier-fail", Outcome: "fail", Tier1: false},
		},
	}
	baseline := BaselineRecord{
		Scenarios: []BaselineScenario{
			{ID: "pass-to-fail", Outcome: "pass", Tier1: true},
			{ID: "stable-pass", Outcome: "pass", Tier1: true},
			{ID: "non-tier-fail", Outcome: "pass", Tier1: false},
		},
	}
	regs, warnings := DiffAgainstBaseline(report, baseline)
	if len(regs) != 1 || regs[0].ScenarioID != "pass-to-fail" {
		t.Fatalf("expected one tier-1 regression, got %v", regs)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one perf warning, got %v", warnings)
	}
}

func TestWriteJSONDeterminism(t *testing.T) {
	report := Report{
		Header: NewHeader("v", "c"),
		Scorecard: scorecard.Result{
			Overall: 65.0,
			Breakdown: map[string]scorecard.AreaResult{
				scorecard.AreaAgentMCP: {Score: 70, Weight: 25, Contribution: 17.5, BelowFloor: true},
			},
		},
	}
	dir := t.TempDir()
	p1 := filepath.Join(dir, "r1.json")
	p2 := filepath.Join(dir, "r2.json")
	if err := WriteJSON(report, p1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := WriteJSON(report, p2); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	b1, err := os.ReadFile(p1)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	b2, err := os.ReadFile(p2)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("JSON reports are not deterministic")
	}
}

func TestWriteMarkdown(t *testing.T) {
	report := Report{
		Header: NewHeader("v", "c"),
		Scorecard: scorecard.Result{
			Overall: 65.0,
			Breakdown: map[string]scorecard.AreaResult{
				scorecard.AreaAgentMCP: {Score: 70, Weight: 25, Contribution: 17.5, BelowFloor: true},
			},
		},
		Baseline: 65.0,
		Target:   90.0,
	}
	p := filepath.Join(t.TempDir(), "r.md")
	if err := WriteMarkdown(report, p); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("markdown file missing: %v", err)
	}
}

func TestDeriveAreaScores(t *testing.T) {
	scenarios := []PerScenarioResult{
		{ID: "a", Operation: "explain_symbol", Outcome: "pass"},
		{ID: "b", Operation: "change_risk", Outcome: "fail"},
		{ID: "c", Operation: "search", Outcome: "pass"},
		{ID: "d", Operation: "search", Outcome: "pass"},
	}
	repos := []PerRepoMetric{{Name: "r1", Pass: true}, {Name: "r2", Pass: false}}

	scores, provenance, warnings := DeriveAreaScores(scenarios, repos)
	if got := scores[scorecard.AreaAgentMCP]; got != 50 {
		t.Fatalf("agent_mcp = %v, want 50 (1/2 agent-tool scenarios pass)", got)
	}
	if got := scores[scorecard.AreaEvaluation]; got != 75 {
		t.Fatalf("evaluation = %v, want 75 (3/4 scenarios pass)", got)
	}
	if got := scores[scorecard.AreaPerformance]; got != 50 {
		t.Fatalf("performance = %v, want 50 (1/2 repos pass)", got)
	}
	for _, area := range []string{scorecard.AreaAgentMCP, scorecard.AreaEvaluation, scorecard.AreaPerformance} {
		if provenance[area] != "measured" {
			t.Fatalf("area %s provenance = %q, want measured", area, provenance[area])
		}
	}
	baseline := BaselineAreaScores()
	for _, area := range []string{scorecard.AreaSignal, scorecard.AreaSetupTrust, scorecard.AreaUX} {
		if provenance[area] != "carried" {
			t.Fatalf("area %s provenance = %q, want carried", area, provenance[area])
		}
		if scores[area] != baseline[area] {
			t.Fatalf("carried area %s must keep baseline score", area)
		}
	}
	if len(warnings) != 3 {
		t.Fatalf("expected 3 carried-area warnings, got %v", warnings)
	}
}

func TestDeriveAreaScores_NoData(t *testing.T) {
	scores, provenance, warnings := DeriveAreaScores(nil, nil)
	baseline := BaselineAreaScores()
	for area, want := range baseline {
		if scores[area] != want {
			t.Fatalf("area %s = %v, want baseline %v", area, scores[area], want)
		}
		if provenance[area] != "carried" {
			t.Fatalf("area %s provenance = %q, want carried", area, provenance[area])
		}
	}
	if len(warnings) != len(baseline) {
		t.Fatalf("every area must be flagged carried, got %v", warnings)
	}
}
