package main

// SW-202 (language-GA program G6): the hero-json suite (corpus/hero-json) is
// the executable correctness contract for the 12 frozen stable operations run
// against a json fixture. json is the PARSE-ONLY member of the six
// intra/parse residual languages (SW-185/SW-199/SW-202), and per
// docs/plan/2026-08-per-language-ga-template-v1.md §5.5's language-spec test
// JSON defines NO cross-file construct: RFC 8259 (and ECMA-404) defines
// objects, arrays, numbers, strings and the three literals and nothing else,
// and `$ref` is JSON Schema's / JSON Reference's construct layered ON json,
// not json's own — structurally identical to `include:` being Ansible's
// layered on YAML. The abstention is therefore the LANGUAGE'S.
//
// WHAT MAKES THIS SUITE DIFFERENT FROM THE OTHER FIVE, STATED UP FRONT.
// json is graded parse-only, not intra-file-only: the json parser wires no
// SymbolExtractor, so a json document parses successfully and contributes
// ZERO nodes and ZERO edges to the graph. Two consequences follow and this
// file records both rather than hiding either.
//
//  1. The `ambiguous` failure class is UNREACHABLE for json, not merely
//     unwritten. resolve.Strict can only report ambiguous when two or more
//     nodes claim one name, and json mints no node at all. This file does not
//     skip the class and does not weaken the four-class rule for anybody
//     else: it replaces the assertion with a STRICTER one — a positive proof,
//     in TestHeroJsonSuite_AmbiguousIsUnreachable, that the fixture graph is
//     empty, which is what makes the class unreachable. If json ever starts
//     minting nodes that proof fails and the omission stops being defensible.
//
//  2. json's fixture CANNOT be declared in corpus/manifest.json. Every
//     manifest entry must carry at least one `expect_nonempty` search
//     (internal/corpus/corpus.go: "a smoke run must prove the index is
//     non-trivial"), and no search over a json-only tree can ever be
//     non-empty. That invariant is correct and is deliberately left intact;
//     the honest move is not to weaken it but to register this one fixture
//     here, beside the explanation, so the exception is visible in the gate
//     that depends on it rather than buried as a false claim in data.
//
// Because of (1) the G6 evidence row for json is NOT flipped to PASS by this
// story. See docs/rc/evidence-index.yaml, GA-LANG-json-G6.
//
// Invariants pinned here:
//   - the suite covers EXACTLY the 12 frozen stable ops (SCOPE-01);
//   - the three REACHABLE failure classes (partial, empty, not_found) plus a
//     negative (absent) anchor are represented, and the fourth is proven
//     unreachable rather than assumed away;
//   - every scenario PASSES against the tier-1 fixture at the heuristic tier.

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/coverage"
)

// heroJSONFixtureRef and heroJSONFixturePath register the hero-json fixture
// for the scenario runner. See point (2) in this file's header for why this
// one fixture is not in corpus/manifest.json.
const (
	heroJSONFixtureRef  = "tier1-fixture-hero-json"
	heroJSONFixturePath = "corpus/fixtures/hero-json"
)

// heroJSONFixtures returns the manifest fixture index with the hero-json
// fixture added.
func heroJSONFixtures(t *testing.T, root string) map[string]fixtureInfo {
	t.Helper()
	_, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	addBuiltinFixtures(fixtures)
	if _, clash := fixtures[heroJSONFixtureRef]; clash {
		t.Fatalf("%s is now declared in corpus/manifest.json — delete this local registration and its header note rather than shadowing the manifest", heroJSONFixtureRef)
	}
	fixtures[heroJSONFixtureRef] = fixtureInfo{Path: heroJSONFixturePath, Tier: 1}
	return fixtures
}

func loadHeroJSONScenarios(t *testing.T) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", "hero-json", "*.yaml"))
	if err != nil {
		t.Fatalf("glob hero-json dir: %v", err)
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

func TestHeroJsonSuite_CoversTheFrozenStableOps(t *testing.T) {
	heroes := loadHeroJSONScenarios(t)
	if len(heroes) < 12 {
		t.Fatalf("hero-json suite has %d tasks, want at least the 12 stable ops", len(heroes))
	}
	covered := map[string]bool{}
	for _, s := range heroes {
		covered[s.Operation.Name] = true
	}
	stableSet := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stableSet[op] = true
		if !covered[op] {
			t.Errorf("stable operation %q has no hero-json task", op)
		}
	}
	for op := range covered {
		if !stableSet[op] {
			t.Errorf("hero-json task exercises %q, which is not a frozen stable operation", op)
		}
	}
}

func TestHeroJsonSuite_FailureClassesRepresented(t *testing.T) {
	heroes := loadHeroJSONScenarios(t)
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
	// The three REACHABLE classes. `ambiguous` is handled by
	// TestHeroJsonSuite_AmbiguousIsUnreachable, which proves the reason
	// instead of asserting the class.
	for _, class := range []string{"partial", "empty", "not_found"} {
		if outcomes[class] == 0 {
			t.Errorf("failure class %q has no hero-json task", class)
		}
	}
	if outcomes["ambiguous"] != 0 {
		t.Errorf("hero-json declares %d ambiguous task(s); if json can now produce ambiguous, TestHeroJsonSuite_AmbiguousIsUnreachable is stale and this suite should assert all four classes like every other hero suite", outcomes["ambiguous"])
	}
	if absents == 0 {
		t.Error("no hero-json task declares a negative (absent) anchor")
	}
	if budgets != 0 {
		t.Errorf("%d hero-json tasks declare max_latency_ms — budgets are frozen from a reproducible CI run (ADR 0003 U5), not invented here", budgets)
	}
}

// TestHeroJsonSuite_AmbiguousIsUnreachable is the substitute for the fourth
// failure class, and it is a stronger claim than the class it replaces: the
// hero-json fixture graph holds no node and no edge at all, so no two nodes
// can ever contend for one name and resolve.Strict can never report
// ambiguous. The moment json starts contributing anything to the graph this
// test fails, and hero-json must then carry a real ambiguous scenario.
func TestHeroJsonSuite_AmbiguousIsUnreachable(t *testing.T) {
	root := repoRoot(t)
	eng, err := buildFixtureEngine(filepath.Join(root, heroJSONFixturePath))
	if err != nil {
		t.Fatalf("build hero-json fixture engine: %v", err)
	}
	reader := eng.Deps.Query.Reader()
	nodes, err := reader.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("read nodes: %v", err)
	}
	edges, err := reader.Edges(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Fatalf("hero-json fixture graph has %d nodes and %d edges, want 0 and 0 — json is graded parse-only, so either the grading changed or the fixture picked up a non-json file; the ambiguous-class omission in TestHeroJsonSuite_FailureClassesRepresented is no longer justified either way", len(nodes), len(edges))
	}
}

// TestHeroJsonSuite_AllTasksPassAtHeuristicTier runs the 16 scenarios against
// the json tier-1 fixture. json has no typed binder and no
// GRAPHI_*_TYPERESOLVE switch — the heuristic tier IS the language's honest
// level. The default binary is the gate.
func TestHeroJsonSuite_AllTasksPassAtHeuristicTier(t *testing.T) {
	root := repoRoot(t)
	fixtures := heroJSONFixtures(t, root)
	results, err := runScenarios(filepath.Join(root, "corpus", "hero-json"), root, fixtures, 1)
	if err != nil {
		t.Fatalf("run hero-json suite: %v", err)
	}
	scenarios := loadHeroJSONScenarios(t)
	if len(results) != len(scenarios) {
		t.Fatalf("ran %d hero-json tasks, want %d (tier-1 filter must not drop any)", len(results), len(scenarios))
	}
	for _, r := range results {
		if r.Outcome != "pass" {
			t.Errorf("hero-json task %s (%s): outcome %q, evidence: %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
}
