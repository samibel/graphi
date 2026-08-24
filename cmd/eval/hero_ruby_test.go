package main

// SW-197 (language-GA program G6): the hero-ruby suite (corpus/hero-ruby) is
// the executable correctness contract for the 12 frozen stable operations run
// against a SHARED Ruby fixture (corpus/fixtures/hero-ruby). The rubyResolver
// at engine/link/resolve_ruby.go drives the cross-file wiring through its
// requireBinder (resolve_common.go:332): `require_relative '../lib/util'` in
// app/main.rb joins to `lib/util.rb`, minting a file→file `imports` edge and
// an ambient lookup dir (`lib`), through which bare `core(...)` / `run(...)`
// references resolve to lib.core / lib.run at the heuristic tier.
//
// The 16 scenarios at corpus/hero-ruby/hrub-01..16 cover the 12 frozen stable
// operations, the four failure classes (ambiguous, partial, empty, not_found),
// and at least one negative (absent) anchor. The gate is only PASS when every
// scenario passes at the heuristic tier — same shape as the ccpp / python
// gates (hero_ccpp_test.go, hero_python_test.go).
//
// Invariants pinned here:
//   - the 16 scenarios cover EXACTLY the 12 frozen stable ops (SCOPE-01);
//   - the four failure classes (ambiguous, partial, empty, not_found) plus a
//     negative (absent) anchor are represented;
//   - the gate passes at the heuristic tier (the rubyResolver's requireBinder
//     is the cross-file mechanism; Ruby is at the same level as PHP/Lua).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroRubyScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-ruby", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-ruby dir: %v", err)
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

func TestHeroRubySuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroRubyScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-ruby suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-ruby task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-ruby task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroRubySuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroRubyScenarios(t)
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
			t.Errorf("failure class %q has no hero-ruby task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-ruby task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-ruby tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroRubySuite_AllTasksPassAtHeuristicTier runs the 16 scenarios against
// the heuristic rubyResolver — no GRAPHI_*_TYPERESOLVE switch to set; the
// default binary IS the gate, and the rubyResolver's requireBinder wires the
// cross-file edges the scenarios pivot on. The gate asserts PASS for every
// scenario under the SAME scenario YAML.
func TestHeroRubySuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-ruby"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-ruby suite: %v", err)
	}
	scenarios := loadHeroRubyScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-ruby tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-ruby task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
