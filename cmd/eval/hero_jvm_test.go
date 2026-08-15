package main

// WP-J6 (language-GA program G6): the hero-JVM suite (corpus/hero-jvm) is the
// executable correctness contract for the 12 frozen stable operations run
// against a JVM (Java+Kotlin) fixture with the declared-type binder LIVE. It is
// the JVM twin of the Go hero suite (hero_test.go), kept SEPARATE so the Go
// suite's frozen exactly-20 contract is untouched.
//
// The binder is default-off in the product; this gate sets
// GRAPHI_JVM_TYPERESOLVE so the fixture is indexed at the language's target
// capability (typed-confirmed) — callers/callees/references over declared-type
// receivers, which the heuristic linker alone cannot resolve.
//
// Invariants pinned here:
//   - every hero-jvm task is executable and PASSES against its tier-1 fixture;
//   - the union of exercised operations equals EXACTLY the frozen stable set
//     (SCOPE-01) — no stable op unmeasured, no non-stable op smuggled in;
//   - the failure classes (ambiguous, partial, empty, not_found) and at least
//     one negative (absent) anchor are represented.
//
// Budgets are deliberately absent (ADR 0003 U5: absolute numbers freeze from a
// reproducible CI run, never invented here).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroJVMScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-jvm", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-jvm dir: %v", err)
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

func TestHeroJVMSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroJVMScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-jvm suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-jvm task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-jvm task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroJVMSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroJVMScenarios(t)
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
			t.Errorf("failure class %q has no hero-jvm task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-jvm task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-jvm tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

func TestHeroJVMSuite_AllTasksPassWithBinderLive(t *testing.T) {
	t.Setenv("GRAPHI_JVM_TYPERESOLVE", "1") // index the fixture at the typed-confirmed capability
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-jvm"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-jvm suite: %v", err)
	}
	scenarios := loadHeroJVMScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-jvm tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	// The runner already validated each scenario's declared expectation
	// (outcome + anchors + absent) and reports "pass"/"fail".
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-jvm task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
