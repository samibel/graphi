package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func budgetFixture() (fullBudgetManifest, evalreport.FullRepoRun) {
	var manifest fullBudgetManifest
	manifest.SchemaVersion = budgetSchemaVersion
	manifest.RunnerClass = "ubuntu-latest"
	manifest.Historical = true
	manifest.HistoricalReason = "previous-harness ceilings; retained fail-closed, not comparable"
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

// SW-123 (AC-5): the historical/ratcheting declaration is part of the
// contract, and an older schema is rejected rather than read as a ratchet.
func TestEvaluateFullRunBudgetsEnforcesTheHistoricalDeclaration(t *testing.T) {
	manifest, run := budgetFixture()
	manifest.SchemaVersion = 2
	if _, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run); err == nil {
		t.Fatal("a pre-v3 budget manifest must fail closed rather than be assumed a ratchet")
	}

	manifest, run = budgetFixture()
	manifest.Ratcheting = true
	if _, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run); err == nil {
		t.Fatal("historical AND ratcheting must fail closed")
	}

	manifest, run = budgetFixture()
	manifest.HistoricalReason = ""
	if _, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run); err == nil {
		t.Fatal("a historical manifest without a reason must fail closed")
	}
}

// SW-123 (AC-8): the budget file itself stays fail-closed on disk — missing
// and malformed are both errors, and a zero budget is now one too.
func TestCheckFullRunBudgetsFailsClosedOnDisk(t *testing.T) {
	_, run := budgetFixture()
	if _, err := checkFullRunBudgets(filepath.Join(t.TempDir(), "absent.json"), "ubuntu-latest", run); err == nil {
		t.Fatal("a missing budget file must fail closed")
	}

	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkFullRunBudgets(broken, "ubuntu-latest", run); err == nil {
		t.Fatal("a malformed budget file must fail closed")
	}

	zeroed := filepath.Join(t.TempDir(), "zeroed.json")
	body := `{"schema_version":3,"runner_class":"ubuntu-latest","historical":true,"historical_reason":"x",
"real_repos":{"selection":["repo"],"per_repo":{"repo":{"index_wallclock_ms":{"budget":0}}}}}`
	if err := os.WriteFile(zeroed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkFullRunBudgets(zeroed, "ubuntu-latest", run); err == nil {
		t.Fatal("a zero budget on disk must fail closed before it can render as met")
	}
}
