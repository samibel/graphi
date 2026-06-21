package analysis_test

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// seedCallChainGraph builds a deterministic calls-edge graph plus one
// references-only edge:
//
//	calls:      A->B, B->C, A->C, B->A (A<->B cycle), C->D
//	references: E->F   (no calls path E..F)
//
// Call-chain oracles are derived from the calls edges only.
func seedCallChainGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	names := []string{"A", "B", "C", "D", "E", "F"}
	ids := make(map[string]model.NodeId, len(names))
	nodes := make(map[string]model.Node, len(names))
	for _, n := range names {
		nd, err := model.NewNode("function", "cc."+n, "cc/"+n+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", n, err)
		}
		if err := store.PutNode(ctx, nd); err != nil {
			t.Fatalf("PutNode(%s): %v", n, err)
		}
		ids[n] = nd.ID()
		nodes[n] = nd
	}
	mkEdge := func(from, to, kind string, tier model.ConfidenceTier, reason string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, 0.9, reason, []string{"cc/" + from + ".go:1"})
		if err != nil {
			t.Fatalf("NewEdge(%s->%s %s): %v", from, to, kind, err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge(%s->%s %s): %v", from, to, kind, err)
		}
	}
	mkEdge("A", "B", string(query.EdgeKindCalls), model.TierConfirmed, "A calls B")
	mkEdge("B", "C", string(query.EdgeKindCalls), model.TierDerived, "B calls C")
	mkEdge("A", "C", string(query.EdgeKindCalls), model.TierHeuristic, "A calls C")
	mkEdge("B", "A", string(query.EdgeKindCalls), model.TierDerived, "B calls A")
	mkEdge("C", "D", string(query.EdgeKindCalls), model.TierConfirmed, "C calls D")
	mkEdge("E", "F", string(query.EdgeKindReferences), model.TierDerived, "E references F")
	return store, ids
}

func TestCallChainMultiPathRanked(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	// A to C: two paths — direct [A->C] (len 1) and [A->B, B->C] (len 2).
	// Shortest first; both concrete caller->callee sequences.
	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["A"], Target: ids["C"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("paths = %d, want 2", len(res.Paths))
	}
	if len(res.Paths[0]) != 1 || len(res.Paths[1]) != 2 {
		t.Fatalf("path lengths = %d, %d; want 1 then 2 (shortest first)", len(res.Paths[0]), len(res.Paths[1]))
	}
	// First path must be the direct A->C edge.
	if res.Paths[0][0].From != ids["A"] || res.Paths[0][0].To != ids["C"] {
		t.Fatalf("first path is %s->%s, want A->C", res.Paths[0][0].From, res.Paths[0][0].To)
	}
}

func TestCallChainCycleSourceTarget(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	// source==target A: the A<->B cycle yields the path [A->B, B->A] (len 2).
	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["A"], Target: ids["A"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found (cycle path exists)", res.Outcome)
	}
	if len(res.Paths) != 1 || len(res.Paths[0]) != 2 {
		t.Fatalf("want 1 cycle path of length 2, got %d paths (first len %d)", len(res.Paths), func() int {
			if len(res.Paths) > 0 {
				return len(res.Paths[0])
			}
			return -1
		}())
	}
}

func TestCallChainOnPathCycleTerminates(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	// A to D traverses A->B->C->D; B->A is a back-edge but A is visited, so no
	// node repeats and the traversal terminates. Exactly one path.
	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["A"], Target: ids["D"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	// Assert no path visits a node twice.
	for pi, p := range res.Paths {
		seen := map[model.NodeId]bool{}
		prev := ids["A"]
		for _, e := range p {
			if e.From != prev {
				t.Fatalf("path %d: edge From %s != expected %s (non-contiguous)", pi, e.From, prev)
			}
			if seen[e.From] {
				t.Fatalf("path %d: node %s repeats (cycle within path)", pi, e.From)
			}
			seen[e.From] = true
			prev = e.To
		}
	}
}

func TestCallChainNoChainDisconnected(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	// A to F: F is only reachable via a references edge from E; no calls path.
	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["A"], Target: ids["F"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("outcome = %s, want empty ('no chain')", res.Outcome)
	}
	if len(res.Paths) != 0 {
		t.Fatalf("expected no paths, got %d", len(res.Paths))
	}
}

func TestCallChainReferencesOnlyNoChain(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	// E to F: only a references edge connects them; resolver must not traverse
	// non-calls edges -> 'no chain'.
	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["E"], Target: ids["F"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("references-only outcome = %s, want empty", res.Outcome)
	}
}

func TestCallChainNotFound(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// Unknown source -> not_found (no error, no traversal).
	res, err := svc.Dispatch(ctx, "call-chain", analysis.Params{
		Symbol: model.NodeId("zzzzzzzzzzzzzzzz"), Target: ids["C"],
	})
	if err != nil {
		t.Fatalf("unknown source should not error: %v", err)
	}
	if res.Outcome != query.OutcomeNotFound {
		t.Fatalf("unknown source outcome = %s, want not_found", res.Outcome)
	}

	// Unknown target -> not_found.
	res, err = svc.Dispatch(ctx, "call-chain", analysis.Params{
		Symbol: ids["A"], Target: model.NodeId("zzzzzzzzzzzzzzzz"),
	})
	if err != nil {
		t.Fatalf("unknown target should not error: %v", err)
	}
	if res.Outcome != query.OutcomeNotFound {
		t.Fatalf("unknown target outcome = %s, want not_found", res.Outcome)
	}
}

func TestCallChainProvenancePerStep(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["A"], Target: ids["C"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for pi, p := range res.Paths {
		// First edge From == source; last edge To == target.
		if p[0].From != ids["A"] {
			t.Fatalf("path %d: first edge From %s != source A", pi, p[0].From)
		}
		if p[len(p)-1].To != ids["C"] {
			t.Fatalf("path %d: last edge To %s != target C", pi, p[len(p)-1].To)
		}
		for ei, e := range p {
			if e.ID == "" {
				t.Fatalf("path %d step %d: empty edge id (no provenance)", pi, ei)
			}
			if !e.Tier.Valid() {
				t.Fatalf("path %d step %d: invalid tier %q", pi, ei, e.Tier)
			}
			if strings.TrimSpace(e.Reason) == "" {
				t.Fatalf("path %d step %d: empty reason", pi, ei)
			}
			if len(e.Evidence) == 0 {
				t.Fatalf("path %d step %d: empty evidence", pi, ei)
			}
			// Contiguity: edge i To == edge i+1 From.
			if ei+1 < len(p) && e.To != p[ei+1].From {
				t.Fatalf("path %d: non-contiguous edges at step %d", pi, ei)
			}
		}
	}
}

func TestCallChainBoundedMaxPaths(t *testing.T) {
	store, ids := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)

	// A->C has 2 paths; cap at 1 -> exactly 1 (the shortest), terminates.
	res, err := svc.Dispatch(context.Background(), "call-chain", analysis.Params{
		Symbol: ids["A"], Target: ids["C"], MaxPaths: 1,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Paths) != 1 {
		t.Fatalf("MaxPaths=1 -> got %d paths, want 1", len(res.Paths))
	}
	if len(res.Paths[0]) != 1 {
		t.Fatalf("bounded result should be the shortest (len 1), got len %d", len(res.Paths[0]))
	}
}

func TestCallChainDeterminismRepeatedAndTwoServices(t *testing.T) {
	ctx := context.Background()

	s1, ids1 := seedCallChainGraph(t)
	svc1 := analysis.NewDefaultService(s1)
	first, err := svc1.Dispatch(ctx, "call-chain", analysis.Params{Symbol: ids1["A"], Target: ids1["C"]})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	firstBytes, _ := analysis.Marshal(first)

	// 30 repeated runs on the same service.
	for i := 0; i < 30; i++ {
		res, err := svc1.Dispatch(ctx, "call-chain", analysis.Params{Symbol: ids1["A"], Target: ids1["C"]})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b, _ := analysis.Marshal(res)
		if !bytes.Equal(firstBytes, b) {
			t.Fatalf("iteration %d non-byte-identical (determinism violated)", i)
		}
	}

	// Two independent services over identical snapshots.
	s2, ids2 := seedCallChainGraph(t)
	svc2 := analysis.NewDefaultService(s2)
	r2, err := svc2.Dispatch(ctx, "call-chain", analysis.Params{Symbol: ids2["A"], Target: ids2["C"]})
	if err != nil {
		t.Fatalf("svc2: %v", err)
	}
	b2, _ := analysis.Marshal(r2)
	if !bytes.Equal(firstBytes, b2) {
		t.Fatalf("two independent services produced non-identical call-chain output")
	}
}

func TestCallChainRegisteredInDefaultService(t *testing.T) {
	store, _ := seedCallChainGraph(t)
	svc := analysis.NewDefaultService(store)
	names := svc.Names()
	if !containsAnalyzer(names, "call-chain") {
		t.Fatalf("call-chain analyzer not registered; names = %v", names)
	}
	if !containsAnalyzer(names, "impact") {
		t.Fatalf("impact analyzer disappeared after SW-023 registration; names = %v", names)
	}
}

func containsAnalyzer(names []string, want string) bool {
	sort.Strings(names)
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
