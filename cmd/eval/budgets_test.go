package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// ---------------------------------------------------------------------------
// SW-191 (EVALBUDGET-001 closure) — the comparison class as a NON-RATCHETING
// ceiling source.
// ---------------------------------------------------------------------------

// ceilingFixture is budgetFixture plus a well-formed historical_ceilings block.
// The ceiling repo is DELIBERATELY not the real_repos repo: the two tables must
// be independent, and a fixture that shared a name could not tell which table a
// check was scored against.
func ceilingFixture() (fullBudgetManifest, evalreport.FullRepoRun) {
	manifest, run := budgetFixture()
	run.Name = "ceilingrepo"
	no := false
	manifest.HistoricalCeilings = &historicalCeilingBlock{
		SchemaVersion: historicalCeilingSchemaVersion,
		RunnerClass:   comparisonRunnerClass,
		RunnerRole:    "comparison",
		Ratcheting:    &no,
		Notes:         "comparison-class upper limits; bounds a run, never freezes one",
		Selection:     []string{"ceilingrepo"},
		PerRepo: map[string]fullRepoBudget{
			"ceilingrepo": {
				IndexWallclockMS: budgetThreshold{Baseline: 500, Budget: 1000},
				PeakRSSMB:        budgetThreshold{Baseline: 350, Budget: 512},
				DBSizeMB:         budgetThreshold{Baseline: 1, Budget: 2},
				WarmP95US: map[string]budgetThreshold{
					"structural":  {Baseline: 250, Budget: 500},
					"search":      {Baseline: 1000, Budget: 2000},
					"agent_tools": {Baseline: 10000, Budget: 20000},
				},
			},
		},
	}
	return manifest, run
}

// TestEvaluateFullRunBudgets_ComparisonClassIsServedFromTheCeilingBlock is the
// AC-4 pin: a comparison-class run is scored, and it is scored from
// historical_ceilings — not from real_repos.
func TestEvaluateFullRunBudgets_ComparisonClassIsServedFromTheCeilingBlock(t *testing.T) {
	manifest, run := ceilingFixture()
	checks, err := evaluateFullRunBudgets(manifest, comparisonRunnerClass, run)
	if err != nil {
		t.Fatalf("a comparison-class run with a well-formed ceiling block must be scored: %v", err)
	}
	if len(checks) != 6 {
		t.Fatalf("checks = %d, want 6", len(checks))
	}
	for _, c := range checks {
		if !c.Pass {
			t.Errorf("unexpected failed check: %+v", c)
		}
	}

	// The ceiling table is the ONLY table the comparison class reads: the
	// real_repos name must not be reachable from it.
	manifest, run = ceilingFixture()
	run.Name = "repo" // present in real_repos, absent from the ceiling block
	if _, err := evaluateFullRunBudgets(manifest, comparisonRunnerClass, run); err == nil {
		t.Fatal("a comparison-class run scored itself against a REFERENCE-class budget; the " +
			"two tables must not be interchangeable")
	}

	// And the reference class must not reach the ceiling table.
	manifest, run = ceilingFixture()
	if _, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run); err == nil {
		t.Fatal("a reference-class run was scored against a comparison-class ceiling; that is " +
			"exactly the substitution EVALBUDGET-001's gate exists to prevent")
	}

	// SW-191 review MIN-5. The reference-class happy path must stay covered for
	// BOTH declarations, not only for a historical manifest: a RATCHETING
	// manifest scored on its own declared reference class is unchanged
	// pre-SW-191 behaviour, and the ceiling route must not have disturbed it.
	// This arm was carried over from the deleted
	// TestEvaluateFullRunBudgetsAcceptsLocalSandboxOnlyWhenHistorical (case 5).
	manifest, run = budgetFixture()
	manifest.Historical = false
	manifest.HistoricalReason = ""
	manifest.Ratcheting = true
	if _, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run); err != nil {
		t.Fatalf("a ratcheting budget on the reference class must keep working: %v", err)
	}
	// The same ratcheting manifest is still refused on the comparison class —
	// with no ceiling block there is nothing for it to be scored against.
	if _, err := evaluateFullRunBudgets(manifest, comparisonRunnerClass, run); err == nil {
		t.Fatal("a ratcheting manifest with no historical_ceilings block was accepted on the " +
			"comparison class; developer-machine numbers must never become a ratchet")
	}
}

// TestEvaluateFullRunBudgets_ManifestCannotClaimTheComparisonClassAsItsOwn is
// SW-191 review MIN-4. evaluateFullRunBudgets routes on a switch whose first
// arm is the manifest's own runner_class and whose second is the comparison
// class; Go takes the first match. A manifest that named the comparison class
// as its OWN runner_class would therefore shadow the ceiling arm entirely, and
// a comparison-class run would be scored against real_repos with none of
// comparisonCeilings' nine guards ever executing — the exact reference-class
// substitution the split exists to prevent.
func TestEvaluateFullRunBudgets_ManifestCannotClaimTheComparisonClassAsItsOwn(t *testing.T) {
	manifest, run := ceilingFixture()
	manifest.RunnerClass = comparisonRunnerClass
	// The bend also puts the ceiling repo in the REFERENCE table. Without that,
	// the run would fail with a vacuous "not in budget selection" and the test
	// would pass whether or not the guard exists. With it, the un-guarded code
	// scores the run happily — and green — against reference-class figures.
	manifest.RealRepos.Selection = append(manifest.RealRepos.Selection, run.Name)
	manifest.RealRepos.PerRepo[run.Name] = manifest.RealRepos.PerRepo["repo"]

	err := errFrom(evaluateFullRunBudgets(manifest, comparisonRunnerClass, run))
	if err == nil {
		t.Fatal("a manifest whose own runner_class IS the comparison class was accepted; the first " +
			"switch arm shadows the ceiling block and scores a comparison-class run against real_repos")
	}
	if !strings.Contains(err.Error(), comparisonRunnerClass) || !strings.Contains(err.Error(), "historical_ceilings") {
		t.Fatalf("the refusal does not name the collision it protects against: %v", err)
	}

	// It is refused at LOAD time as well, so a manifest on disk cannot carry the
	// collision even into a command that never reaches evaluateFullRunBudgets.
	collided := filepath.Join(t.TempDir(), "collided.json")
	body := `{"schema_version":3,"runner_class":"local-sandbox","historical":true,"historical_reason":"x",
"real_repos":{"selection":["repo"],"per_repo":{"repo":{"index_wallclock_ms":{"budget":1}}}}}`
	if err := os.WriteFile(collided, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBudgetManifest(collided); err == nil {
		t.Fatal("a budget manifest declaring the comparison class as its own runner_class loaded cleanly")
	}
}

// errFrom drops the value half of a (value, error) pair so a single-expression
// call can be asserted on its error.
func errFrom[T any](_ T, err error) error { return err }

// TestEvaluateFullRunBudgets_CeilingBlockCannotBeRepointed is the adversarial
// pin the story's test notes ask for by name: the ceiling shape must not be
// silently re-pointable at the reference class, and its non-ratcheting property
// must be written down rather than inferred.
func TestEvaluateFullRunBudgets_CeilingBlockCannotBeRepointed(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		bend func(*fullBudgetManifest)
	}{
		{"no ceiling block at all", func(m *fullBudgetManifest) { m.HistoricalCeilings = nil }},
		{"ceiling re-pointed at the reference class", func(m *fullBudgetManifest) {
			m.HistoricalCeilings.RunnerClass = "ubuntu-latest"
		}},
		{"ceiling claims the reference ROLE", func(m *fullBudgetManifest) {
			m.HistoricalCeilings.RunnerRole = "reference"
		}},
		{"ratcheting undeclared", func(m *fullBudgetManifest) { m.HistoricalCeilings.Ratcheting = nil }},
		{"ceiling declares itself ratcheting", func(m *fullBudgetManifest) {
			m.HistoricalCeilings.Ratcheting = &yes
		}},
		{"manifest itself ratchets", func(m *fullBudgetManifest) {
			m.Historical = false
			m.HistoricalReason = ""
			m.Ratcheting = true
		}},
		{"unknown ceiling schema", func(m *fullBudgetManifest) { m.HistoricalCeilings.SchemaVersion = 99 }},
		{"selection without a per_repo entry", func(m *fullBudgetManifest) {
			m.HistoricalCeilings.Selection = append(m.HistoricalCeilings.Selection, "ghost")
		}},
		{"empty ceiling table", func(m *fullBudgetManifest) {
			m.HistoricalCeilings.PerRepo = map[string]fullRepoBudget{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, run := ceilingFixture()
			tc.bend(&manifest)
			if _, err := evaluateFullRunBudgets(manifest, comparisonRunnerClass, run); err == nil {
				t.Fatalf("%s was accepted; the ceiling gate must be fail-closed", tc.name)
			}
		})
	}

	// A runner class that is neither the reference class nor the comparison
	// class stays refused, with or without a ceiling block.
	manifest, run := ceilingFixture()
	if _, err := evaluateFullRunBudgets(manifest, "macos-local", run); err == nil {
		t.Fatal("only the declared reference class and the comparison class are accepted")
	}
}

// TestHeroBudgets_HistoricalCeilingsSchema is AC-5: the CHECKED-IN
// docs/eval/hero-budgets.json must carry the section, it must be well-formed,
// and its non-ratcheting property must be asserted on the real file rather than
// only on a fixture.
func TestHeroBudgets_HistoricalCeilingsSchema(t *testing.T) {
	manifest, err := loadBudgetManifest(filepath.Join("..", "..", "docs", "eval", "hero-budgets.json"))
	if err != nil {
		t.Fatalf("load hero-budgets.json: %v", err)
	}
	block, err := manifest.comparisonCeilings()
	if err != nil {
		t.Fatalf("docs/eval/hero-budgets.json historical_ceilings: %v", err)
	}
	if block.Ratcheting == nil || *block.Ratcheting {
		t.Fatal("historical_ceilings must declare ratcheting=false: a ceiling bounds a run and never freezes one")
	}
	if block.RunnerClass != comparisonRunnerClass || block.RunnerRole != "comparison" {
		t.Fatalf("historical_ceilings runner declaration = %q/%q, want %q/comparison",
			block.RunnerClass, block.RunnerRole, comparisonRunnerClass)
	}
	// The six non-Go pins SW-177 / SW-191 measured must all be present, with a
	// positive budget on every metric the gate reads.
	for _, repo := range []string{"guava", "okio", "kotlinx_serialization", "flask", "ky", "express"} {
		budget, ok := block.PerRepo[repo]
		if !ok {
			t.Errorf("historical_ceilings has no entry for %s", repo)
			continue
		}
		if !slices.Contains(block.Selection, repo) {
			t.Errorf("%s has a per_repo entry but is not in the ceiling selection", repo)
		}
		for name, th := range map[string]budgetThreshold{
			"index_wallclock_ms": budget.IndexWallclockMS,
			"peak_rss_mb":        budget.PeakRSSMB,
			"db_size_mb":         budget.DBSizeMB,
		} {
			if th.Budget <= 0 {
				t.Errorf("%s.%s budget is %.3f; a non-positive budget renders as met while carrying no signal", repo, name, th.Budget)
			}
			if th.Baseline > th.Budget {
				t.Errorf("%s.%s baseline %.3f exceeds its own budget %.3f", repo, name, th.Baseline, th.Budget)
			}
		}
		for _, class := range []string{"structural", "search", "agent_tools"} {
			if th, ok := budget.WarmP95US[class]; !ok || th.Budget <= 0 {
				t.Errorf("%s missing a positive warm_p95_us.%s ceiling", repo, class)
			}
		}
	}
	// The ceiling selection and the reference selection are DIFFERENT sets on
	// purpose: something measured ONLY on the comparison class must not be able
	// to reach the reference-class table.
	//
	// SW-191 review MIN-6. The overlap is stated here as DATA with its reason,
	// rather than as `&& repo != "guava" && repo != "flask"` inline — that form
	// carved out precisely the two names that could ever fire, so the loop
	// asserted nothing, and it would have silently stopped asserting anything
	// about any name added to real_repos later. guava and flask are legitimately
	// in both tables because both have a REFERENCE-class ratchet of their own
	// (they are FR-2 corpus pins) as well as a comparison-class ceiling; the
	// four names below have only ever been measured on the comparison class.
	referenceAlsoMeasured := []string{"guava", "flask"}
	comparisonOnly := make([]string, 0, len(block.Selection))
	for _, repo := range block.Selection {
		if !slices.Contains(manifest.RealRepos.Selection, repo) {
			comparisonOnly = append(comparisonOnly, repo)
			continue
		}
		if !slices.Contains(referenceAlsoMeasured, repo) {
			t.Errorf("%s appears in both the reference selection and the comparison ceilings, and is "+
				"not one of the pins (%v) that carry a reference-class ratchet of their own; a "+
				"comparison-class figure must not become a reference-class ratchet by table membership",
				repo, referenceAlsoMeasured)
		}
	}
	// Non-vacuity: the two tables must actually differ, and they must differ by
	// the four pins that exist only on the comparison class. Without this, a
	// future edit that merged the tables would leave the loop above green.
	for _, repo := range []string{"okio", "kotlinx_serialization", "ky", "express"} {
		if !slices.Contains(comparisonOnly, repo) {
			t.Errorf("%s is not comparison-class-only; it is either missing from the ceiling "+
				"selection or has leaked into the reference selection", repo)
		}
	}
	if len(comparisonOnly) == 0 {
		t.Fatal("the ceiling selection and the reference selection are identical sets; the split is vacuous")
	}
	// And the reverse direction: every name the reference table ratchets that is
	// NOT in referenceAlsoMeasured must be absent from the ceiling block.
	for _, repo := range manifest.RealRepos.Selection {
		if slices.Contains(referenceAlsoMeasured, repo) {
			continue
		}
		if slices.Contains(block.Selection, repo) {
			t.Errorf("reference-class pin %s acquired a comparison-class ceiling without being "+
				"declared as measured on both", repo)
		}
	}
}
