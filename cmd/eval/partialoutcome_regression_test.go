package main

// SW-136 regression test for D1 — the single defect SW-135's decision record
// (docs/decisions/2026-07-p0-candidate-decision.md §3.1) named as forcing the P0
// candidate to move:
//
//	the agent-tools countability rule at cmd/eval/querylatency.go:430-435 omits
//	"partial" from the `allowed` set of explain_symbol, change_risk and
//	related_files, while agent_brief — the fourth member of the same FR-8 pool
//	(agent_context_p95) — declares it countable.
//
// Written and committed BEFORE the correcting change (SW-136 AC-2): it is RED at
// the commit that introduces it and green at the commit that corrects the rule.
// That ordering is the acceptance criterion, verifiable in the commit history.
//
// Three things are pinned, and the third matters as much as the first two:
//
//  1. One pool, one rule. Every operation in the agent_context_p95 pool counts a
//     "partial" execution. A percentile reported over four operations whose
//     observations were admitted under different rules is not measuring what it
//     reports (decision record §2.1).
//
//  2. The correction is load-bearing on the PUBLISHED data. Replaying the
//     committed grpc-go tallies through the live sets recovers all 25 rejected
//     executions and reaches FR-8's floor of 1000. A correction that did not
//     recover exactly 25 would not be the correction the diagnosis predicted.
//
//  3. The correction is BOUNDED. "A fast wrong answer is not a fast answer"
//     (querylatency.go:102-105) still holds: not_found / ambiguous / error stay
//     uncountable, "empty" is not smuggled into the two operations that never
//     allowed it, and no operation outside this pool gained an outcome.
//
// What this test does NOT claim: nothing here is evidence that gate 9 will PASS.
// It says the instrument now admits the observations it was wrongly discarding;
// what those observations measure is a fresh baseline's job (SW-143–145), and
// both the recovered executions and the shipped item cap push the measured
// distribution upward (decision record §5).
//
// It needs no index, no corpus clone and no network.

import (
	"context"
	"slices"
	"sort"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/internal/corpus"
)

var agentContextPool = []string{
	scenario.OpAgentBrief,
	scenario.OpExplainSymbol,
	scenario.OpChangeRisk,
	scenario.OpRelatedFiles,
}

func declaredAllowed(t *testing.T) map[string][]string {
	t.Helper()
	ops := buildWarmOperations(context.Background(), nil, corpus.Entry{}, []string{"n0", "n1"}, 2)
	out := map[string][]string{}
	for _, w := range ops {
		x := w.prepare(0)
		allowed := append([]string(nil), x.allowed...)
		sort.Strings(allowed)
		out[w.op] = allowed
	}
	return out
}

// TestAgentContextPool_EveryOperationCountsPartial is D1 stated as an assertion.
//
// The pool membership is the one docs/eval/reference-scenario.json gives
// agent_context_p95; agentContextPool (SW-134's characterization file) holds it.
func TestAgentContextPool_EveryOperationCountsPartial(t *testing.T) {
	allowed := declaredAllowed(t)
	for _, op := range agentContextPool {
		got, ok := allowed[op]
		if !ok {
			t.Fatalf("%s is not a measured warm operation: the agent_context_p95 pool moved", op)
		}
		if !slices.Contains(got, "partial") {
			t.Errorf("%s allowed = %v, want it to contain %q: one FR-8 pool cannot admit observations "+
				"under two countability rules (D1, decision record §3.1)", op, got, "partial")
		}
	}
}

// TestAgentContextPool_CorrectionIsBoundedToPartial guards the other direction.
//
// The failure mode a countability correction invites is widening the sets until
// the pool fills. Every admitted outcome is pinned exactly, so recovering the 25
// executions by admitting an error, a miss or an ambiguity fails here.
func TestAgentContextPool_CorrectionIsBoundedToPartial(t *testing.T) {
	allowed := declaredAllowed(t)

	// The corrected declarations, exactly. sorted, as declaredAllowed returns them.
	want := map[string][]string{
		scenario.OpAgentBrief:    {"found", "partial"},
		scenario.OpExplainSymbol: {"found", "partial"},
		scenario.OpChangeRisk:    {"found", "partial"},
		scenario.OpRelatedFiles:  {"empty", "found", "partial"},
	}
	for op, w := range want {
		if got := allowed[op]; !slices.Equal(got, w) {
			t.Errorf("%s allowed = %v, want %v", op, got, w)
		}
	}

	// "A fast wrong answer is not a fast answer" — unchanged, for every measured
	// operation, not only this pool.
	for op, got := range allowed {
		for _, forbidden := range []string{"not_found", "ambiguous", "error", "invalid"} {
			if slices.Contains(got, forbidden) {
				t.Errorf("%s allowed = %v: %q is not a valid answer and must not contribute a latency sample",
					op, got, forbidden)
			}
		}
	}

	// Nothing outside the agent-context pool moved. These are the pre-correction
	// declarations of every other measured operation, verbatim.
	untouched := map[string][]string{
		scenario.OpDefinition:   {"found"},
		scenario.OpCallers:      {"empty", "found"},
		scenario.OpCallees:      {"empty", "found"},
		scenario.OpReferences:   {"empty", "found"},
		scenario.OpNeighborhood: {"empty", "found"},
		scenario.OpImpact:       {"empty", "found"},
	}
	for op, w := range untouched {
		got, ok := allowed[op]
		if !ok {
			t.Fatalf("%s is no longer a measured warm operation", op)
		}
		if !slices.Equal(got, w) {
			t.Errorf("%s allowed = %v, want %v: the correction is scoped to the agent-context pool", op, got, w)
		}
	}
}
