package main

// SW-202 AC-8 — the six intra/parse hero suites are 5x BYTE-STABLE.
//
// The claim asserted here, stated exactly: five independent full runs of a
// suite — each one re-ingesting the fixture tree into a fresh in-memory graph
// and re-executing all sixteen scenarios — produce byte-identical answers.
//
// ONE FIELD IS EXCLUDED AND IT IS NAMED RATHER THAN QUIETLY DROPPED.
// PerScenarioResult.LatencyMS is a wall-clock measurement of the machine, not
// part of the answer; it cannot be byte-stable by construction and asserting
// that it is would be a false claim. Everything else is compared verbatim:
// id, fixture_ref, operation, area, outcome, result_size, the full ordered
// evidence list, anchor_present and tier1. If the graph, the resolver or the
// evidence ordering moved between runs, this test fails.
//
// Running the whole suite in-process is what lets json be covered here too.
// The `go run ./cmd/eval -scenarios corpus/hero-<lang>` path cannot cover it,
// because hero-json's fixture is deliberately not declared in the corpus
// manifest (see hero_json_test.go).

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

const heroIntraParseStabilityRuns = 5

func TestHeroIntraParse_ScenarioResultsAreByteStable(t *testing.T) {
	root := repoRoot(t)
	for _, rule := range intraParseSpecCiteRules {
		t.Run(rule.lang, func(t *testing.T) {
			var first []byte
			for run := 1; run <= heroIntraParseStabilityRuns; run++ {
				fixtures := heroJSONFixtures(t, root)
				results, err := runScenarios(filepath.Join(root, "corpus", rule.dir), root, fixtures, 1)
				if err != nil {
					t.Fatalf("run %d of %s: %v", run, rule.dir, err)
				}
				if len(results) != 16 {
					t.Fatalf("run %d of %s produced %d results, want the 16 declared scenarios", run, rule.dir, len(results))
				}
				stripped := make([]evalreport.PerScenarioResult, 0, len(results))
				for _, r := range results {
					r.LatencyMS = 0
					stripped = append(stripped, r)
				}
				raw, err := json.Marshal(stripped)
				if err != nil {
					t.Fatalf("marshal run %d of %s: %v", run, rule.dir, err)
				}
				if run == 1 {
					first = raw
					continue
				}
				if string(raw) != string(first) {
					t.Fatalf("run %d of %s is NOT byte-identical to run 1\nrun 1: %s\nrun %d: %s", run, rule.dir, first, run, raw)
				}
			}
			if len(first) == 0 {
				t.Fatalf("%s produced no serialized result at all", rule.dir)
			}
		})
	}
}
