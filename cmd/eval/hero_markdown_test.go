package main

// SW-202 (language-GA program G6): the hero-markdown suite (corpus/hero-markdown) is
// the executable correctness contract for the 12 frozen stable operations run
// against a markdown fixture. markdown is one of the six intra/parse residual
// languages (SW-185/SW-199/SW-202), and per
// docs/plan/2026-08-per-language-ga-template-v1.md §5.5's language-spec test
// Markdown DOES define a cross-file construct — the inline link
// `[text](./target.md)`, in the CommonMark specification — and graphi
// declines to resolve link destinations, so the abstention carried here is
// GRAPHI'S, not Markdown's, and the scenarios say so.
// G6 is therefore satisfied through the §5.5 ABSTENTION SHAPE: the fixture
// exercises what the language CAN express (declarations, parse determinism,
// the four failure classes) and the scenarios assert the honest-empty pattern
// for the relations it cannot, each with the LANGUAGE SPEC cited in its
// description rather than graphi's parser comment (the LANGHONEST-001
// circular-abstention defect class).
//
// Invariants pinned here:
//   - the suite covers EXACTLY the 12 frozen stable ops (SCOPE-01);
//   - the four failure classes (ambiguous, partial, empty, not_found) plus
//     a negative (absent) anchor are represented;
//   - every scenario PASSES against the tier-1 fixture at the heuristic tier
//     (markdown has no GRAPHI_*_TYPERESOLVE switch — the default binary IS the
//     language's honest level).
//
// Two further gates guard this suite against the vacuity failure mode and
// against a circular abstention, and both live in cross-cutting files so a
// per-language suite cannot opt out of them:
// hero_intraparse_nonvacuity_test.go and hero_intraparse_speccite_test.go.

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroMarkdownScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-markdown", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-markdown dir: %v", err)
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

func TestHeroMarkdownSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroMarkdownScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-markdown suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-markdown task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-markdown task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroMarkdownSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroMarkdownScenarios(t)
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
			t.Errorf("failure class %q has no hero-markdown task", class)
		}
	}
	if absents == 0 {
		t.Error("no hero-markdown task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-markdown tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroMarkdownSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios against
// the markdown tier-1 fixture. markdown has no typed binder and no
// GRAPHI_*_TYPERESOLVE switch — the heuristic tier IS the language's honest
// level. The default binary is the gate.
func TestHeroMarkdownSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-markdown"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-markdown suite: %v", err)
	}
	scenarios := loadHeroMarkdownScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-markdown tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-markdown task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
