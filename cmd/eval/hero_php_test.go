package main

// SW-197 (language-GA program G6): the hero-php suite (corpus/hero-php) is
// the executable correctness contract for the 12 frozen stable operations
// run against a PHP fixture. PHP is one of the nine cross-file-heuristic
// residual languages (SW-184 / SW-197); its resolver
// (engine/link/resolve_ruby.go:19 phpResolver) models
// `require 'x.php'` / `include 'x.php'` as a `require` imports edge via
// the shared requireBinder (engine/link/resolve_common.go:332), and
// `use Foo\Bar` brings `Foo` in as an ambient namespace. The hero-php
// gate mirrors the hero-bash / hero-python posture: no
// `GRAPHI_*_TYPERESOLVE` switch is set, the default binary IS the
// heuristic-tier gate, and the same 16 scenarios cover EXACTLY the 12
// frozen stable ops (SCOPE-01).
//
// Invariants pinned here:
//   - the 16 scenarios cover EXACTLY the 12 frozen stable operations
//     (SCOPE-01);
//   - the four failure classes (ambiguous, partial, empty, not_found)
//     plus a negative (absent) anchor are represented;
//   - every scenario PASSES at the heuristic tier against the tier-1
//     fixture (the requireBinder's ambient dir wires the cross-file
//     edges the witness scenarios pivot on);
//   - zero `max_latency_ms` budgets are declared (ADR 0003 U5:
//     absolute numbers freeze from a reproducible CI run, never
//     invented here).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroPhpScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-php", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-php dir: %v", err)
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

func TestHeroPhpSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroPhpScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-php suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-php task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-php task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroPhpSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroPhpScenarios(t)
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
			t.Errorf("failure class %q has no hero-php task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-php task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-php tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroPhpSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios
// against the tier-1 PHP fixture. PHP has no typed binder; the
// requireBinder's ambient dir wires the cross-file edges. No env
// override is required.
func TestHeroPhpSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-php"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-php suite: %v", err)
	}
	scenarios := loadHeroPhpScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-php tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-php task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
