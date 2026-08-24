package main

// SW-197 (language-GA program G6): the hero-sql suite (corpus/hero-sql) is
// the executable correctness contract for the 12 frozen stable operations run
// against a SQL fixture. SQL is one of the nine cross-file-heuristic residual
// languages (SW-184/SW-197), and per docs/plan/2026-08-per-language-ga-
// template-v1.md §5.5's language-spec test SQL defines NO file-inclusion
// construct (ISO/IEC 9075 has no #include or `source`; the parser at
// core/parse/parser_sql.go:77 returns empty Imports/References by design).
// G6 is therefore satisfied through parse-determinism honest-empty: the
// cross-file operations (callers/callees/references/impact/related_files
// across files) return well-formed empty outcomes, recorded in the scenario
// descriptions per AC-4. Same-directory intra-file operations remain askable
// and are exercised through search/definition/agent_brief/explain_symbol.
//
// Invariants pinned here:
//   - the suite covers EXACTLY the 12 frozen stable ops (SCOPE-01);
//   - the four failure classes (ambiguous, partial, empty, not_found) plus
//     a negative (absent) anchor are represented;
//   - every scenario PASSES against the tier-1 fixture at the heuristic tier
//     (SQL has no GRAPHI_*_TYPERESOLVE switch — the default binary IS the
//     language's honest level, the parse-determinism honest-empty doctrine).

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

func loadHeroSqlScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-sql", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-sql dir: %v", err)
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

func TestHeroSqlSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroSqlScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-sql suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-sql task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-sql task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroSqlSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroSqlScenarios(t)
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
			t.Errorf("failure class %q has no hero-sql task", t.Name())
		}
	}
	if absents == 0 {
		t.Error("no hero-sql task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-sql tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroSqlSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios against
// the SQL tier-1 fixture. SQL has no typed binder and no
// GRAPHI_*_TYPERESOLVE switch — the heuristic tier IS the language's honest
// level (§5.5: parse-determinism honest-empty). The default binary is the
// gate.
func TestHeroSqlSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-sql"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-sql suite: %v", err)
	}
	scenarios := loadHeroSqlScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-sql tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-sql task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
