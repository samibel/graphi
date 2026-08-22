package main

// SW-181 (language-GA program G3): the hero-Python suite (corpus/hero-python)
// is the executable correctness contract for the 12 frozen stable operations
// run against a Python fixture. Python is `cross-file-heuristic`, so unlike
// the JVM twin (WP-J6) this gate does NOT set GRAPHI_JVM_TYPERESOLVE — no
// binder exists for Python, and the level is the heuristic resolver at
// engine/link/resolve_python.go. This is the G2SUB substitution in its
// concrete form: the same operations, the same discipline, the same frozen
// stable set, just at a different resolution tier.
//
// The Python hero gate is the JVM twin's structure (hero_jvm_test.go), kept
// SEPARATE so the JVM suite's binder-on contract is untouched. The 16
// scenarios at corpus/hero-python/hpy-01..16 are the SW-181 deliverable
// for AC-5 (every one of the 12 stable ops returns a well-formed honest
// result on Python, including the empty / partial / ambiguous / not_found
// classes; no operation lies).
//
// Invariants pinned here:
//   - every hero-python task is executable and PASSES against its tier-1
//     fixture (the heuristic resolver's from-import bare binding wires the
//     cross-module edges the scenarios pivot on);
//   - the union of exercised operations equals EXACTLY the frozen stable
//     set (SCOPE-01) — no stable op unmeasured, no non-stable op smuggled
//     in;
//   - the failure classes (ambiguous, partial, empty, not_found) and at
//     least one negative (absent) anchor are represented.
//
// Budgets are deliberately absent (ADR 0003 U5: absolute numbers freeze from
// a reproducible CI run, never invented here).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroPythonScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-python", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-python dir: %v", err)
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

func TestHeroPythonSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroPythonScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-python suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-python task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-python task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroPythonSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroPythonScenarios(t)
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
			t.Errorf("failure class %q has no hero-python task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-python task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-python tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

func TestHeroPythonSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	// Python has no typed binder and the heuristic resolver is the default
	// product. There is no GRAPHI_*_TYPERESOLVE switch to set — the
	// default binary IS the gate, and the heuristic resolver's
	// from-import bare binding wires the cross-module edges the
	// scenarios pivot on. The JVM gate (hero_jvm_test.go) sets
	// GRAPHI_JVM_TYPERESOLVE so the JVM fixture indexes at its
	// typed-confirmed target; Python's target IS the heuristic, so
	// no env override is required.
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-python"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-python suite: %v", err)
	}
	scenarios := loadHeroPythonScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-python tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	// The runner already validated each scenario's declared expectation
	// (outcome + anchors + absent) and reports "pass"/"fail".
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-python task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
