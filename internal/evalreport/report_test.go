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
