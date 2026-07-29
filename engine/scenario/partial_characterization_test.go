package scenario

// SW-134 characterization baseline: the P0 baseline's 25-execution shortfall,
// reproduced offline.
//
// The eval harness invokes the three agent-context operations through exactly
// this engine (cmd/eval/querylatency.go:442 -> FixtureEngine.InvokeContract),
// passing only {"symbol": <id>} and no cap (cmd/eval/querylatency.go:440). This
// file pins what that argument set resolves to for EACH operation separately —
// AC-2 forbids assuming the three behave alike — and shows the same call with
// the cap every shipped surface supplies (shape.DefaultMaxItems) is "found".
//
// No network, no corpus clone, no CGo: an in-memory graphstore is enough,
// because the mechanism is a comparison between an item count and an integer.

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// evalHarnessItemCap is the cap engine/scenario/fixture.go applies when the
// caller supplies none — which the eval harness never does. It is deliberately
// re-stated rather than exported: if fixture.go's default moves, the assertions
// below change outcome and this file fails, which is the point of a baseline.
const evalHarnessItemCap = 10

// fanInEngine builds a graph where p.Hot has `callers` distinct callers, each
// in its own file, and p.Cold has `cold` of them. Distinct files matter:
// related_files scores by file, so its item cardinality is the number of
// distinct neighbour files, not the number of edges.
func fanInEngine(t *testing.T, hotCallers, coldCallers int) *FixtureEngine {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	mkNode := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", qn, err)
		}
		return n
	}
	mkEdge := func(from, to model.Node, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), "calls", model.TierConfirmed, 0.9, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}

	attach := func(target model.Node, prefix string, n int) {
		for i := range n {
			path := fmt.Sprintf("%s%02d.go", prefix, i)
			caller := mkNode("function", prefix+"."+strconv.Itoa(i), path, 3)
			mkEdge(caller, target, path+":4")
		}
	}
	attach(mkNode("function", "p.Hot", "hot/target.go", 7), "hot", hotCallers)
	attach(mkNode("function", "p.Cold", "cold/target.go", 7), "cold", coldCallers)

	return NewFixtureEngine(resolve.Deps{Query: query.New(store), Search: search.New(store)})
}

func invokeContract(t *testing.T, e *FixtureEngine, op string, args map[string]string) *contract.Result {
	t.Helper()
	res, err := e.InvokeContract(context.Background(), op, args)
	if err != nil {
		t.Fatalf("InvokeContract(%s, %v): %v", op, args, err)
	}
	if res == nil {
		t.Fatalf("InvokeContract(%s, %v): nil result", op, args)
	}
	return res
}

// AC-2: each of the three operations is shown separately, because each fills
// the item list from a different population:
//
//	explain_symbol  1 definition + one item per caller/callee/reference edge row
//	change_risk     one item per resolved seed + one per inbound edge
//	related_files   one item per distinct related FILE (edges are folded per file)
//
// Same downgrade site (engine/agenttools/shape/shape.go:170-172), same cap, but
// three different cardinalities — which is why the three operations contribute
// different numbers of rejected executions to the same pool.
func TestEvalHarnessArgs_ProducePartialForEachAgentContextOperation(t *testing.T) {
	// One caller over the harness's cap for the item population of each of the
	// three operations: 11 callers -> 12 explain items, 12 risk items, 11
	// related files. All three exceed 10; none exceeds 20.
	eng := fanInEngine(t, 11, 2)

	cases := []struct {
		op     string
		target string
		// wantHarness is the outcome for the harness's argument set: symbol
		// only, no cap -> engine/scenario/fixture.go's default of 10.
		wantHarness contract.Outcome
		// wantShipped is the outcome for the same call with the cap every
		// shipped surface supplies: 0, which shape.Finish reads as 20.
		wantShipped contract.Outcome
		capArg      string
	}{
		{OpExplainSymbol, "p.Hot", contract.OutcomePartial, contract.OutcomeFound, "max_items"},
		{OpChangeRisk, "p.Hot", contract.OutcomePartial, contract.OutcomeFound, "max_items"},
		{OpRelatedFiles, "p.Hot", contract.OutcomePartial, contract.OutcomeFound, "max_files"},

		// The control: the identical call on a target below the cap is "found"
		// for all three. The operation is not broken — the answer size is what
		// decides.
		{OpExplainSymbol, "p.Cold", contract.OutcomeFound, contract.OutcomeFound, "max_items"},
		{OpChangeRisk, "p.Cold", contract.OutcomeFound, contract.OutcomeFound, "max_items"},
		{OpRelatedFiles, "p.Cold", contract.OutcomeFound, contract.OutcomeFound, "max_files"},
	}

	for _, tc := range cases {
		t.Run(tc.op+"/"+tc.target, func(t *testing.T) {
			// Exactly the harness's argument map: cmd/eval/querylatency.go:440.
			got := invokeContract(t, eng, tc.op, map[string]string{"symbol": tc.target})
			if got.Outcome != tc.wantHarness {
				t.Errorf("harness args: outcome = %q, want %q (limits %+v)", got.Outcome, tc.wantHarness, got.Limits)
			}
			if tc.wantHarness == contract.OutcomePartial {
				if !got.Limits.Truncated {
					t.Errorf("harness args: partial without Limits.Truncated — a producer other than the item cap")
				}
				if got.Limits.CapApplied != evalHarnessItemCap {
					t.Errorf("harness args: CapApplied = %d, want %d", got.Limits.CapApplied, evalHarnessItemCap)
				}
			}

			// The same question asked the way every shipped surface asks it.
			shipped := invokeContract(t, eng, tc.op, map[string]string{
				"symbol":  tc.target,
				tc.capArg: strconv.Itoa(shape.DefaultMaxItems),
			})
			if shipped.Outcome != tc.wantShipped {
				t.Errorf("shipped default cap %d: outcome = %q, want %q (limits %+v)",
					shape.DefaultMaxItems, shipped.Outcome, tc.wantShipped, shipped.Limits)
			}
		})
	}
}

// The harness's cap is 10 and every shipped surface's is 20. That divergence is
// not cosmetic: it decides the outcome for every answer whose natural size lands
// between the two, and therefore decides how many executions the FR-8 pool
// keeps. Pinned as a fact, not as a verdict.
func TestEvalHarnessCap_IsHalfTheShippedDefault(t *testing.T) {
	eng := fanInEngine(t, 15, 1) // 16 explain items: over 10, under 20.

	harness := invokeContract(t, eng, OpExplainSymbol, map[string]string{"symbol": "p.Hot"})
	if harness.Limits.CapApplied != evalHarnessItemCap {
		t.Fatalf("harness CapApplied = %d, want %d — engine/scenario/fixture.go's default moved",
			harness.Limits.CapApplied, evalHarnessItemCap)
	}
	if harness.Outcome != contract.OutcomePartial {
		t.Fatalf("harness outcome = %q, want partial", harness.Outcome)
	}

	shipped := invokeContract(t, eng, OpExplainSymbol, map[string]string{
		"symbol": "p.Hot", "max_items": strconv.Itoa(shape.DefaultMaxItems),
	})
	if shipped.Outcome != contract.OutcomeFound {
		t.Fatalf("shipped-default outcome = %q, want found: the same answer is complete at cap %d",
			shipped.Outcome, shape.DefaultMaxItems)
	}
	if evalHarnessItemCap*2 != shape.DefaultMaxItems {
		t.Fatalf("cap relation changed: harness %d, shipped %d", evalHarnessItemCap, shape.DefaultMaxItems)
	}
}

// agent_brief sits in the same FR-8 pool and is the harness's only agent-context
// operation that never lost an execution (250/250 found in every published run,
// every repo). It is invoked with MaxItems 0 (engine/scenario/fixture.go:303) —
// no 10 — so it is not exposed to the harness cap the other three are.
func TestAgentBrief_IsNotSubjectToTheHarnessItemCap(t *testing.T) {
	eng := fanInEngine(t, 15, 1)
	eng.ProjectName = "fixture"

	res := invokeContract(t, eng, OpAgentBrief, map[string]string{"topic": "p.Hot"})
	if res.Limits.CapApplied == evalHarnessItemCap {
		t.Fatalf("agent_brief CapApplied = %d: it is now subject to the harness cap the other three carry",
			res.Limits.CapApplied)
	}
}
