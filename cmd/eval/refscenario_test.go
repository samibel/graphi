package main

// SW-123 (P0-A4): the reference-scenario contract is data, so its tests are
// mostly assertions ABOUT the checked-in data. A gate mapped to a repository
// that is not in the corpus manifest, a second class calling itself the
// reference, or a §12.2 gate that quietly disappears are all drift this file
// exists to make red.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func refScenarioPaths(t *testing.T) (artifact, manifest, budgets string) {
	t.Helper()
	root := repoRoot(t)
	return filepath.Join(root, "docs", "eval", "reference-scenario.json"),
		filepath.Join(root, "corpus", "manifest.json"),
		filepath.Join(root, "docs", "eval", "hero-budgets.json")
}

// loadCheckedIn is the fixture for every "mutate one field, expect a failure"
// case below: it starts from the real artifact, so a test can never pass
// against a shape the repository does not actually ship.
func loadCheckedIn(t *testing.T) (referenceScenario, map[string]bool) {
	t.Helper()
	artifact, manifest, _ := refScenarioPaths(t)
	rs, err := loadReferenceScenario(artifact)
	if err != nil {
		t.Fatalf("load checked-in artifact: %v", err)
	}
	repos, err := corpusRepoNames(manifest)
	if err != nil {
		t.Fatalf("load corpus manifest: %v", err)
	}
	return rs, repos
}

// AC-1, AC-2, AC-3, AC-6, AC-7: the checked-in artifact is the contract, and
// it must satisfy every rule the validator encodes.
func TestReferenceScenario_CheckedInArtifactIsValid(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	if err := validateReferenceScenario(rs, repos); err != nil {
		t.Fatalf("checked-in reference scenario is invalid: %v", err)
	}

	reference := 0
	comparison := 0
	for _, c := range rs.RunnerClasses {
		switch c.Role {
		case roleReference:
			reference++
		case roleComparison:
			comparison++
		}
	}
	if reference != 1 {
		t.Errorf("reference runner classes = %d, want exactly 1", reference)
	}
	if comparison < 1 {
		t.Error("no comparison runner class is declared; the second class must be labelled, not omitted")
	}

	if _, ok := repos[rs.ReferenceScenario.Repo]; !ok {
		t.Errorf("reference scenario repo %q is not in the corpus manifest", rs.ReferenceScenario.Repo)
	}

	// AC-3: every §12.2 gate is mapped BY NAME to a manifest repository.
	seen := map[string]bool{}
	for _, g := range rs.Gates {
		seen[g.ID] = true
		if _, ok := repos[g.Repo]; !ok {
			t.Errorf("gate %q maps to repo %q, which is not in the corpus manifest", g.ID, g.Repo)
		}
	}
	for _, id := range prdPerformanceGateIDs {
		if !seen[id] {
			t.Errorf("PRD §12.2 gate %q is not mapped to a repository", id)
		}
	}

	// AC-7: the scope limitation travels with the data, not only in prose.
	if rs.ScopeLimitation == "" {
		t.Error("scope_limitation is empty; FR-8's scope limit must be inline")
	}
}

// AC-8: a missing or malformed artifact is a failure, never a skip.
func TestReferenceScenario_FailsClosed(t *testing.T) {
	if _, err := loadReferenceScenario(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing reference-scenario artifact must fail closed")
	}
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReferenceScenario(broken); err == nil {
		t.Fatal("a malformed reference-scenario artifact must fail closed")
	}
}

// AC-1 / AC-2: exactly one reference, everything else explicitly comparison.
func TestReferenceScenario_RunnerClassRolesAreExclusive(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	for i := range rs.RunnerClasses {
		if rs.RunnerClasses[i].Role == roleComparison {
			rs.RunnerClasses[i].Role = roleReference
			break
		}
	}
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("two reference classes must be rejected")
	}

	rs, repos = loadCheckedIn(t)
	for i := range rs.RunnerClasses {
		if rs.RunnerClasses[i].Role == roleReference {
			rs.RunnerClasses[i].Role = roleComparison
		}
	}
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("zero reference classes must be rejected")
	}

	rs, repos = loadCheckedIn(t)
	rs.RunnerClasses[0].Role = "baseline"
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("an unknown runner-class role must be rejected")
	}
}

// AC-1: the reference class is only declared once its environment is.
func TestReferenceScenario_ReferenceClassNeedsFullEnvironment(t *testing.T) {
	fields := map[string]func(*runnerClass){
		"cpu":         func(c *runnerClass) { c.CPU = "" },
		"cpu_cores":   func(c *runnerClass) { c.CPUCores = 0 },
		"ram_gb":      func(c *runnerClass) { c.RAMGB = 0 },
		"os":          func(c *runnerClass) { c.OS = "" },
		"kernel":      func(c *runnerClass) { c.Kernel = "" },
		"go_version":  func(c *runnerClass) { c.GoVersion = "" },
		"filesystem":  func(c *runnerClass) { c.Filesystem = "" },
		"cache_state": func(c *runnerClass) { c.CacheState = "" },
	}
	for name, blank := range fields {
		rs, repos := loadCheckedIn(t)
		for i := range rs.RunnerClasses {
			if rs.RunnerClasses[i].Role == roleReference {
				blank(&rs.RunnerClasses[i])
			}
		}
		if err := validateReferenceScenario(rs, repos); err == nil {
			t.Errorf("reference class without %s must be rejected", name)
		}
	}
}

// AC-3: this is the drift the mapping exists to prevent.
func TestReferenceScenario_RejectsUnpinnedRepositories(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	rs.Gates[0].Repo = "not-in-the-manifest"
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("a gate pointing at a repository that is not pinned must be rejected")
	}

	rs, repos = loadCheckedIn(t)
	rs.ReferenceScenario.Repo = "not-in-the-manifest"
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("a reference scenario repo that is not pinned must be rejected")
	}
}

// AC-3: dropping or inventing a gate is rejected — the mapping is complete or
// it is not a mapping.
func TestReferenceScenario_GateSetMatchesThePRDTable(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	rs.Gates = rs.Gates[1:]
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("a missing §12.2 gate must be rejected")
	}

	rs, repos = loadCheckedIn(t)
	rs.Gates = append(rs.Gates, gateMapping{
		ID: "invented_gate", PRDMetric: "x", Threshold: 1, Unit: "s",
		Comparison: "lte", Repo: rs.ReferenceScenario.Repo, MeasuredBy: "SW-999",
	})
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("a gate this PRD does not define must be rejected")
	}
}

// AC-5's rule applied to the gate table itself: a zero threshold is only ever
// the OOM count gate, never a forgotten value that reads as met.
func TestReferenceScenario_RejectsSilentZeroThresholds(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	for i := range rs.Gates {
		if rs.Gates[i].ID != oomGateID {
			rs.Gates[i].Threshold = 0
			break
		}
	}
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("a zero threshold on a magnitude gate must be rejected")
	}
}

// AC-4: the OOM gate is a method, not a statement of intent.
func TestReferenceScenario_OOMCheckIsAMethod(t *testing.T) {
	blanks := map[string]func(*oomCheck){
		"host":           func(o *oomCheck) { o.Host = "" },
		"limit_bytes":    func(o *oomCheck) { o.LimitBytes = 0 },
		"impose":         func(o *oomCheck) { o.Impose = "" },
		"verify":         func(o *oomCheck) { o.Verify = "" },
		"failure_signal": func(o *oomCheck) { o.FailureSignal = "" },
	}
	for name, blank := range blanks {
		rs, repos := loadCheckedIn(t)
		blank(&rs.OOMCheck)
		if err := validateReferenceScenario(rs, repos); err == nil {
			t.Errorf("OOM check without %s must be rejected", name)
		}
	}

	rs, repos := loadCheckedIn(t)
	rs.OOMCheck.Repo = "not-in-the-manifest"
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("an OOM check on an unpinned repository must be rejected")
	}
}

// FR-8: the 4 GB stop rule is strictly wider than the 2 GB reference gate and
// never a milder alternative to it.
func TestReferenceScenario_StopRuleStaysWiderThanTheGate(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	rs.StopRule.ThresholdGB = 1
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("a stop rule tighter than the peak-RSS gate must be rejected")
	}
}

// AC-7: the scope limitation is part of the artifact's validity.
func TestReferenceScenario_ScopeLimitationIsRequired(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	rs.ScopeLimitation = ""
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("an artifact without the FR-8 scope limitation must be rejected")
	}
}

// AC-5: the dead budgets are labelled, and the label is enforced rather than
// decorative.
func TestReferenceScenario_HistoricalBudgetsAreLabelled(t *testing.T) {
	rs, repos := loadCheckedIn(t)
	if !rs.Budgets.Historical || rs.Budgets.Ratcheting {
		t.Fatalf("budgets must be declared historical and non-ratcheting, got %+v", rs.Budgets)
	}
	rs.Budgets.Ratcheting = true
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("budgets cannot be historical AND ratcheting")
	}

	rs, repos = loadCheckedIn(t)
	rs.Budgets.Reason = ""
	if err := validateReferenceScenario(rs, repos); err == nil {
		t.Fatal("historical budgets without a recorded reason must be rejected")
	}
}

// The budget manifest and the reference scenario must agree on which class is
// the reference — otherwise budgets could be frozen from a comparison run.
func TestReferenceScenario_BudgetRunnerClassIsTheReferenceClass(t *testing.T) {
	_, _, budgetPath := refScenarioPaths(t)
	rs, _ := loadCheckedIn(t)
	raw, err := os.ReadFile(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	var m fullBudgetManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	ref, ok := rs.referenceClass()
	if !ok {
		t.Fatal("no reference class")
	}
	if m.RunnerClass != ref.ID {
		t.Fatalf("budget runner_class = %q, reference class = %q", m.RunnerClass, ref.ID)
	}
}

// AC-8: the operator check runs green against the checked-in artifacts, and
// fails closed — not silently green — when one of them cannot be read.
func TestReferenceScenarioCheck_GreenOnTheCheckedInArtifacts(t *testing.T) {
	artifact, manifest, budgets := refScenarioPaths(t)
	var out strings.Builder
	if code := runReferenceScenarioCheck(artifact, manifest, budgets, &out); code != 0 {
		t.Fatalf("-check-reference-scenario exit code = %d, want 0", code)
	}
	for _, want := range []string{"reference runner class:", "reference scenario:", "comparison class:", "stop rule:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("check summary is missing %q:\n%s", want, out.String())
		}
	}
	for _, id := range prdPerformanceGateIDs {
		if !strings.Contains(out.String(), id) {
			t.Errorf("check summary does not report gate %q", id)
		}
	}

	missing := filepath.Join(t.TempDir(), "absent.json")
	if code := runReferenceScenarioCheck(missing, manifest, budgets, io.Discard); code != 2 {
		t.Errorf("missing artifact exit code = %d, want 2", code)
	}
	if code := runReferenceScenarioCheck(artifact, missing, budgets, io.Discard); code != 2 {
		t.Errorf("missing corpus manifest exit code = %d, want 2", code)
	}
	if code := runReferenceScenarioCheck(artifact, manifest, missing, io.Discard); code != 2 {
		t.Errorf("missing budget artifact exit code = %d, want 2", code)
	}
}

// AC-5: no latency budget of `0` may participate in a pass/fail decision. The
// rule is enforced structurally — a zero ANYWHERE in the budget artifact is
// either dead data or a silent pass, and neither is allowed.
func TestHeroBudgets_CarryNoSilentZero(t *testing.T) {
	_, _, budgetPath := refScenarioPaths(t)
	raw, err := os.ReadFile(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNoSilentZeroBudgets(raw); err != nil {
		t.Fatalf("checked-in budgets still carry a silent zero: %v", err)
	}

	if err := validateNoSilentZeroBudgets([]byte(`{"a":{"b":[1,0]}}`)); err == nil {
		t.Fatal("a zero nested in the budget artifact must be reported")
	}
	if err := validateNoSilentZeroBudgets([]byte(`{"measured_max_latency_ms_per_op":{"search":0}}`)); err == nil {
		t.Fatal("the retired zero-latency map must be reported")
	}
}

// AC-5 / AC-8: the budget artifact declares itself historical, and the loader
// enforces the declaration instead of trusting prose.
func TestHeroBudgets_AreDeclaredHistoricalAndNonRatcheting(t *testing.T) {
	_, _, budgetPath := refScenarioPaths(t)
	raw, err := os.ReadFile(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	var m fullBudgetManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != budgetSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", m.SchemaVersion, budgetSchemaVersion)
	}
	if !m.Historical || m.Ratcheting {
		t.Fatalf("budgets must declare historical=true, ratcheting=false, got historical=%v ratcheting=%v", m.Historical, m.Ratcheting)
	}
	if m.HistoricalReason == "" {
		t.Fatal("a historical budget artifact must record why it is not a ratchet")
	}
}
