package main

// SW-197 (language-GA program G6 follow-up): the hero-csharp suite
// (corpus/hero-csharp) is the executable correctness contract for the
// 12 frozen stable operations run against a C# fixture. C# is a
// declared language registered as `c_sharp` (core/parse/parser_csharp.go:29)
// with its own heuristic resolver (engine/link/resolve_csharp.go).
// The resolver models `using Shop;` as ambient namespace clauses
// (clause = last `.` segment of the using path), so a selector call
// `Price.Of()` from app/Caller.cs resolves to `Shop.Of` via
// crossModule("Shop", Of) at the heuristic tier.
//
// The hero-csharp gate is the C# twin of the ccpp/python/typescript
// gates (hero_ccpp_test.go, hero_python_test.go, hero_typescript_test.go),
// kept SEPARATE so each language's binder-on contract is untouched.
// The 16 scenarios at corpus/hero-csharp/hcs-01..16 are the SW-197
// deliverable for AC-5 (every one of the 12 stable ops returns a
// well-formed honest result on C#, including the empty / partial /
// ambiguous / not_found classes; no operation lies).
//
// Invariants pinned here:
//   - every hero-csharp task is executable and PASSES against its
//     tier-1 fixture (the csharpResolver's ambient-clause fallback
//     wires the cross-module edges the scenarios pivot on);
//   - the union of exercised operations equals EXACTLY the frozen
//     stable set (SCOPE-01) — no stable op unmeasured, no non-stable
//     op smuggled in;
//   - the failure classes (ambiguous, partial, empty, not_found) and
//     at least one negative (absent) anchor are represented.
//
// Budgets are deliberately absent (ADR 0003 U5: absolute numbers
// freeze from a reproducible CI run, never invented here).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroCsharpScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-csharp", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-csharp dir: %v", err)
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

func TestHeroCsharpSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroCsharpScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-csharp suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-csharp task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-csharp task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroCsharpSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroCsharpScenarios(t)
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
			t.Errorf("failure class %q has no hero-csharp task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-csharp task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-csharp tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroCsharpSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios
// against the C# tier-1 fixture and asserts PASS for each. C# has no
// GRAPHI_*_TYPERESOLVE switch exposed as a default product surface —
// the default binary IS the gate, and the csharpResolver's
// ambient-clause fallback wires the cross-module edges the scenarios
// pivot on. No env override is required.
func TestHeroCsharpSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-csharp"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-csharp suite: %v", err)
	}
	scenarios := loadHeroCsharpScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-csharp tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-csharp task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
