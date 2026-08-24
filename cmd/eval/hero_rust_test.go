package main

// SW-197 (language-GA program G6): the hero-Rust suite (corpus/hero-rust) is
// the executable correctness contract for the 12 frozen stable operations
// run against the multi-file Rust fixture (corpus/fixtures/hero-rust). Rust
// has no typed binder at this tier — the rustResolver at
// engine/link/resolve_rust.go is the language's clause-keyed heuristic
// (clause = SECOND-to-last `::` segment of `use` path), so this gate does
// NOT set GRAPHI_RUST_TYPERESOLVE. The G2SUB substitution for Rust is the
// heuristic resolver — same operations, same discipline, same frozen stable
// set, just at the heuristic tier.
//
// The 16 scenarios at corpus/hero-rust/hrust-01..16 are the SW-197
// deliverable for AC-5 (every one of the 12 stable ops returns a
// well-formed honest result on Rust, including the empty / partial /
// ambiguous / not_found classes; no operation lies).
//
// Invariants pinned here:
//   - every hero-rust task is executable and PASSES against its tier-1
//     fixture (the rustResolver's clause-keyed core wires the cross-module
//     edges the scenarios pivot on);
//   - the union of exercised operations equals EXACTLY the frozen stable
//     set (SCOPE-01) — no stable op unmeasured, no non-stable op smuggled
//     in;
//   - the failure classes (ambiguous, partial, empty, not_found) and at
//     least one negative (absent) anchor are represented.
//
// Budgets are deliberately absent (ADR 0003 U5: absolute numbers freeze
// from a reproducible CI run, never invented here).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroRustScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-rust", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-rust dir: %v", err)
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

func TestHeroRustSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroRustScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-rust suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-rust task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-rust task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroRustSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroRustScenarios(t)
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
			t.Errorf("failure class %q has no hero-rust task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-rust task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-rust tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroRustSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios against
// the rust passkey. Rust has no typed binder and the clause-keyed heuristic
// resolver is the default product. There is no GRAPHI_RUST_TYPERESOLVE
// switch to set — the default binary IS the gate, and the rustResolver's
// clause-keyed core (clause = SECOND-to-last `::` segment of `use` path)
// wires the cross-module edges the scenarios pivot on.
func TestHeroRustSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-rust"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-rust suite: %v", err)
	}
	scenarios := loadHeroRustScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-rust tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-rust task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
