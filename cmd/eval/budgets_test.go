package main

import (
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func budgetFixture() (fullBudgetManifest, evalreport.FullRepoRun) {
	var manifest fullBudgetManifest
	manifest.SchemaVersion = 2
	manifest.RunnerClass = "ubuntu-latest"
	manifest.RealRepos.Selection = []string{"repo"}
	manifest.RealRepos.PerRepo = map[string]fullRepoBudget{
		"repo": {
			IndexWallclockMS: budgetThreshold{Budget: 1000},
			PeakRSSMB:        budgetThreshold{Budget: 512},
			DBSizeMB:         budgetThreshold{Budget: 2},
			WarmP95US: map[string]budgetThreshold{
				"structural":  {Budget: 500},
				"search":      {Budget: 2000},
				"agent_tools": {Budget: 20000},
			},
		},
	}
	run := evalreport.FullRepoRun{
		Name: "repo",
		Index: evalreport.IndexMetrics{
			WallclockMS: 500, PeakRSSMB: 300, DBSizeBytes: 1024 * 1024,
		},
		StablePeakRSSMB: 350,
		WarmP95US:       map[string]int64{"structural": 250, "search": 1000, "agent_tools": 10000},
		WarmSamples:     map[string]int{"structural": 1, "search": 1, "agent_tools": 1},
		WarmOps:         map[string][]string{"structural": {"impact"}, "search": {"search"}, "agent_tools": {"agent_brief"}},
	}
	return manifest, run
}

func TestEvaluateFullRunBudgetsPassesAndCatchesRegression(t *testing.T) {
	manifest, run := budgetFixture()
	checks, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 6 {
		t.Fatalf("checks = %d, want 6", len(checks))
	}
	for _, check := range checks {
		if !check.Pass {
			t.Fatalf("unexpected failed check: %+v", check)
		}
	}

	run.StablePeakRSSMB = 513
	checks, err = evaluateFullRunBudgets(manifest, "ubuntu-latest", run)
	if err != nil {
		t.Fatal(err)
	}
	foundFailure := false
	for _, check := range checks {
		if check.Name == "stable_peak_rss_mb" && !check.Pass {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatal("RSS regression did not fail its budget check")
	}
}

func TestEvaluateFullRunBudgetsFailsClosedOnContextDrift(t *testing.T) {
	manifest, run := budgetFixture()
	if _, err := evaluateFullRunBudgets(manifest, "macos-local", run); err == nil {
		t.Fatal("runner mismatch must fail closed")
	}
	run.Name = "unbudgeted"
	if _, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run); err == nil {
		t.Fatal("unbudgeted repo must fail closed")
	}
}
