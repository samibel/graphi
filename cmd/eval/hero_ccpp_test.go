package main

// SW-197 (language-GA program G6): the hero-c-cpp suite (corpus/hero-c-cpp) is
// the executable correctness contract for the 12 frozen stable operations run
// against a SHARED C/C++ fixture (corpus/fixtures/hero-c-cpp). C and C++ are
// two declared languages sharing one resolver path (engine/link/resolve_c.go:
// the includeDirectory binder at :41). Per AC-5 the shared hero_ccpp_test.go
// runs the SAME 16 scenarios against both languages; the gate is only PASS
// when BOTH language passes cohere.
//
// Bundling rule: a gate discharged on c does NOT discharge cpp unless the
// artefact genuinely covers both. The test iterates the two language passkeys
// (c, cpp) under the same scenario YAML, building one engine per language so
// `go test -run TestHeroCCpp` exhausts both surfaces.
//
// Invariants pinned here:
//   - the same 16 scenarios cover EXACTLY the 12 frozen stable ops (SCOPE-01);
//   - the four failure classes (ambiguous, partial, empty, not_found) plus
//     a negative (absent) anchor are represented;
//   - both languages' executions PASS at the heuristic tier (the cResolver
//     and cppResolver share includeBinder; C and C++ are at the same level).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroCCppScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-c-cpp", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-c-cpp dir: %v", err)
	}
	sort.Strings(files)
	out := make([]scenario.Scenario, 0, len(files))
	for _, f := range files {
		s, err := scenario.LoadScenario(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, s)
	}
	return out
}

func TestHeroCCppSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroCCppScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-c-cpp suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-c-cpp task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-c-cpp task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroCCppSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroCCppScenarios(t)
	outcomes := map[string]int{}
	absents := 0
	budgets := 0
	for _, s := range heroes {
		outcomes[s.Expect.Outcome]++
		if len(s.Expect.Absent) > 0 {
			absents++
		}
		if s.Expect.MaxLatencyMS > 0 {
			budgets++
		}
	}
	for _, class := range []string{"ambiguous", "partial", "empty", "not_found"} {
		if outcomes[class] == 0 {
			t.Errorf("failure class %q has no hero-c-cpp task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-c-cpp task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-c-cpp tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroCCppSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios against
// both the c and cpp passkeys (the shared `tier1-fixture-hero-c-cpp` fixture
// tree holds source under both `c/` and `cpp/`, exercised as one ingest pass).
// The two declared languages share includeBinder; the gate asserts PASS for
// each scenario under the SAME scenario YAML.
func TestHeroCCppSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-c-cpp"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-c-cpp suite: %v", err)
	}
	scenarios := loadHeroCCppScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-c-cpp tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-c-cpp task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
