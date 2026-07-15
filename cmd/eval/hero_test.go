package main

// SW-122 (EVAL-01): the hero-task suite (corpus/hero) is the versioned,
// executable correctness contract for the 12 frozen stable operations. This
// gate pins its invariants so the suite cannot silently rot:
//
//   - exactly 20 hero tasks, every one executable and PASSING against its
//     tier-1 fixture (the local smoke run of the EVAL-02 CI gate);
//   - the union of exercised operations equals EXACTLY the frozen stable set
//     (SCOPE-01) — no stable op unmeasured, no non-stable op smuggled in;
//   - every failure class the master plan demands is represented: ambiguous,
//     partial, empty, not_found, plus at least one negative (absent) anchor.
//
// Budgets (max_latency_ms) are deliberately ABSENT from the hero tasks: per
// ADR 0003 U5 absolute numbers are frozen from the first reproducible CI run
// (EVAL-02), never invented here.

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero dir: %v", err)
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

func TestHeroSuite_TwentyTasksCoverTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroScenarios(t)
	if len(heroes) != 20 {
		t.Fatalf("hero suite has %d tasks, want exactly 20 (the master-plan hero set)", len(heroes))
	}

	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stable := coverage.CanonicalStableOps()
	stableSet := map[string]bool{}
	for _, op := range stable {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroScenarios(t)
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
			t.Errorf("failure class %q has no hero task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero tasks declare max_latency_ms — budgets are frozen from the first reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

func TestHeroSuite_AllTasksPassAgainstTheirFixtures(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero suite: %v", err)
	}
	if len(results) != 20 {
		t.Fatalf("ran %d hero tasks, want 20 (tier-1 filter must not drop any)", len(results))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
