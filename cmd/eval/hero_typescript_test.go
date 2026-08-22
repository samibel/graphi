package main

// SW-182 (language-GA program G2 / G3 / G4): the hero-TypeScript suite
// (corpus/hero-typescript) is the executable correctness contract for the
// 12 frozen stable operations run against a TypeScript-family fixture.
// The TS family (typescript / tsx / javascript) shares one resolver
// implementation registered three times (engine/link/resolve_typescript.go),
// so this single gate covers the whole family at the heuristic tier.
//
// Unlike the JVM twin (hero_jvm_test.go) — which sets GRAPHI_JVM_TYPERESOLVE
// to index the fixture at the typed-confirmed capability — and unlike the
// Python twin (hero_python_test.go) — which mirrors this gate verbatim
// because Python has no typed binder either — the TypeScript family has no
// GRAPHI_*_TYPERESOLVE switch exposed as a default product surface. The
// heuristic resolver's named-import bare binding
// (`import { core } from "../impl"`, registered in both bareNameDirs and
// selBaseDirs at engine/link/resolve_typescript.go:69-71) is the family-
// native synthesis path, and the heuristic tier IS the language's honest
// level. No env override is required.
//
// The 16 scenarios at corpus/hero-typescript/hts-01..16 are the SW-182
// deliverable for AC-5 (every one of the 12 stable ops returns a well-
// formed honest result on TypeScript, including the empty / partial /
// ambiguous / not_found classes; no operation lies). One TS-specific
// shape: the TS parser extracts `references` edges for type_identifier
// references that resolve to in-file type definitions
// (core/parse/parser_tswalk.go:88 + 180-183), so hts-09 anchors on
// `impl._format` (a TypeScript interface) and asserts `impl.core` as the
// in-file referrer — a FOUND scenario, where the Python twin's hpy-09
// is empty. The deliberate fixture choice — making `_format` an
// interface rather than the helper function the Python twin uses — is
// documented in corpus/fixtures/hero-typescript/impl/English.ts.
//
// Invariants pinned here:
//   - every hero-typescript task is executable and PASSES against its
//     tier-1 fixture (the heuristic resolver's named-import bare binding
//     wires the cross-module edges the scenarios pivot on);
//   - the union of exercised operations equals EXACTLY the frozen
//     stable set (SCOPE-01) — no stable op unmeasured, no non-stable
//     op smuggled in;
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

// loadHeroTypeScriptScenarios loads every *.yaml scenario in
// corpus/hero-typescript in deterministic order, failing the test if a
// file is malformed.
func loadHeroTypeScriptScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-typescript", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-typescript dir: %v", err)
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

// TestHeroTypeScriptSuite_CoversTheFrozenStableOps asserts that the
// hero-typescript suite covers EVERY stable operation EXACTLY ONCE (no
// stable op left unmeasured, no non-stable op smuggled in). Mirror of
// the JVM and Python twin gates; SW-122 SCOPE-01 invariant.
func TestHeroTypeScriptSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroTypeScriptScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-typescript suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-typescript task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-typescript task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

// TestHeroTypeScriptSuite_FailureClassesRepresented asserts that the
// hero-typescript suite exercises ALL FOUR failure classes
// (ambiguous, partial, empty, not_found) AND at least one negative
// (absent) anchor — the SW-122 failure-class coverage invariant.
// Budgets are checked to be ABSENT (no max_latency_ms declared),
// because absolute numbers freeze from a reproducible CI run,
// never invented here (ADR 0003 U5).
func TestHeroTypeScriptSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroTypeScriptScenarios(t)
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
			t.Errorf("failure class %q has no hero-typescript task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-typescript task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-typescript tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroTypeScriptSuite_AllTasksPassAtHeuristicTier executes every
// hero-typescript scenario against the tier-1 fixture and asserts the
// runner reports "pass" for each. The TS family has no
// GRAPHI_*_TYPERESOLVE switch exposed as a default product surface —
// no t.Setenv override is needed — so the default binary IS the gate.
// The heuristic resolver's named-import bare binding
// (`import { core } from "../impl"`) wires the cross-module edges the
// scenarios pivot on, the same way the Python twin gate runs without
// an env override (hero_python_test.go).
func TestHeroTypeScriptSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-typescript"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-typescript suite: %v", err)
	}
	scenarios := loadHeroTypeScriptScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-typescript tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	// The runner already validated each scenario's declared expectation
	// (outcome + anchors + absent) and reports "pass"/"fail".
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-typescript task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
