package taint

// Recall gate for the labeled taint ground-truth corpus (PB-001 §10: "taint 100%
// recall on labeled set"). This is the analogue of the eval-claim gate in
// internal/eval: it turns CI RED whenever the analyzer fails to recover every
// labeled flow (recall < 100%) or starts reporting flows on a negative case
// (precision regression). It runs in the default CGO_ENABLED=0 lane like any
// other go test — no extra CI plumbing.

import (
	"context"
	"testing"
)

// findingMatches reports whether a produced finding satisfies a labeled
// expectation. Matching is on config-derived facts (sink category + sink
// qualified name), never on content-derived node ids.
func findingMatches(f Finding, want expectedFlow) bool {
	return f.SinkCategory == want.sinkCategory && f.SinkName == want.sinkName
}

// TestTaintRecall_LabeledSet enforces 100% recall over every positive labeled
// case and zero findings over every negative case. Any drift — a regressed
// analyzer that drops a flow, or an over-tainting analyzer that invents one —
// fails this gate.
func TestTaintRecall_LabeledSet(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ContentHash = "labeled-recall"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	analyzer := New(cfg, DefaultCaps(), nil)

	var totalExpected, totalRecovered int

	for _, c := range labeledCorpus() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			nodes, edges := c.build(t)
			reader := &testReader{nodes: nodes, edges: edges}

			result, err := analyzer.Run(context.Background(), reader)
			if err != nil {
				t.Fatalf("analyzer.Run: %v", err)
			}

			if c.negative {
				if len(c.want) != 0 {
					t.Fatalf("malformed corpus: negative case %q must have empty want", c.name)
				}
				if len(result.Findings) != 0 {
					t.Errorf("negative case %q: expected 0 findings, got %d (false positive — precision regression)",
						c.name, len(result.Findings))
				}
				return
			}

			if len(c.want) == 0 {
				t.Fatalf("malformed corpus: positive case %q must label ≥1 expected flow", c.name)
			}

			// Recall: every labeled flow must appear among the findings.
			for _, want := range c.want {
				totalExpected++
				matched := false
				for _, f := range result.Findings {
					if findingMatches(f, want) {
						matched = true
						break
					}
				}
				if matched {
					totalRecovered++
				} else {
					t.Errorf("recall miss in %q: labeled flow %s→%q not reported (recall < 100%%)",
						c.name, want.sinkCategory, want.sinkName)
				}
			}
		})
	}

	if totalExpected == 0 {
		t.Fatal("no labeled positive flows exercised — corpus is vacuous")
	}
	if totalRecovered != totalExpected {
		t.Errorf("taint recall %d/%d (%.1f%%) — gate requires 100%%",
			totalRecovered, totalExpected, 100*float64(totalRecovered)/float64(totalExpected))
	}
}

// TestTaintCorpus_NonEmpty prevents a silently-shrinking corpus from vacuously
// passing the recall gate: it asserts the case floor and that every default sink
// category is represented by at least one positive labeled flow.
func TestTaintCorpus_NonEmpty(t *testing.T) {
	corpus := labeledCorpus()
	if len(corpus) < minCorpusCases {
		t.Fatalf("corpus shrank to %d cases (floor %d) — silent coverage regression", len(corpus), minCorpusCases)
	}

	labeledCategories := map[string]struct{}{}
	positives := 0
	for _, c := range corpus {
		if c.negative {
			continue
		}
		positives++
		for _, w := range c.want {
			labeledCategories[w.sinkCategory] = struct{}{}
		}
	}
	if positives == 0 {
		t.Fatal("corpus has no positive cases")
	}

	for _, sink := range DefaultConfig().Sinks {
		if _, ok := labeledCategories[sink.Category]; !ok {
			t.Errorf("default sink category %q has no labeled positive case — recall gate would miss it", sink.Category)
		}
	}
}
