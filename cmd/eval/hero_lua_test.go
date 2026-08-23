package main

// SW-197 (language-GA program G6): the hero-lua suite (corpus/hero-lua) is
// the executable correctness contract for the 12 frozen stable operations
// run against a Lua fixture. Lua is `cross-file-heuristic` (the luaResolver
// at engine/link/resolve_ruby.go:38 uses requireBinder with the .lua
// extension), so the heuristic tier is the language's honest level — same
// pattern as the Python twin (hero_python_test.go). The manifest entry
// `tier1-fixture-hero-lua` (corpus/manifest.json) wires the fixture to
// this scenario suite.
//
// Invariants pinned here:
//   - the same 16 scenarios cover EXACTLY the 12 frozen stable ops (SCOPE-01);
//   - the four failure classes (ambiguous, partial, empty, not_found) plus
//     a negative (absent) anchor are represented;
//   - 0 max_latency_ms budgets are asserted (ADR 0003 U5: budgets freeze
//     from a reproducible CI run, never invented here);
//   - the heuristic resolver's requireBinder ambient-dir fallback
//     (engine/link/resolve_common.go:436) wires the cross-file callers
//     every positive scenario pivots on.

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroLuaScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-lua", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-lua dir: %v", err)
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

// TestHeroLuaSuite_CoversTheFrozenStableOps: every one of the 12 frozen
// stable ops has a hero-lua scenario, and no non-stable op is smuggled in.
func TestHeroLuaSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroLuaScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-lua suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-lua task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-lua task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

// TestHeroLuaSuite_FailureClassesRepresented: the four declared failure
// classes (ambiguous, partial, empty, not_found) each appear at least
// once; at least one scenario declares a negative (absent) anchor; no
// scenario invents a max_latency_ms budget (those freeze from a CI run).
func TestHeroLuaSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroLuaScenarios(t)
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
			t.Errorf("failure class %q has no hero-lua task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-lua task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-lua tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroLuaSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios
// against the hero-lua fixture at tier 1 (the heuristic tier is Lua's
// honest level — there is no typed binder for Lua, so the luaResolver
// at engine/link/resolve_ruby.go:38 with requireBinder + .lua extension
// is the gate). The runner already validated each scenario's declared
// expectation (outcome + anchors + absent); this gate asserts PASS for
// every scenario against the same fixture and the same scenario YAML.
func TestHeroLuaSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-lua"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-lua suite: %v", err)
	}
	scenarios := loadHeroLuaScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-lua tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-lua task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
