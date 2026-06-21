package analysis_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// seedBatchedGraph: U->S->M->T (calls). impact forward(S) = {U} (dependent);
// call-chain S->T = [S->M, M->T]; metrics graph-wide.
func seedBatchedGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	names := []string{"U", "S", "M", "T"}
	ids := make(map[string]model.NodeId, len(names))
	nodes := make(map[string]model.Node, len(names))
	for _, nm := range names {
		n, err := model.NewNode("function", "b."+nm, "b/"+nm+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", nm, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", nm, err)
		}
		ids[nm] = n.ID()
		nodes[nm] = n
	}
	mk := func(from, to string, tier model.ConfidenceTier) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), string(query.EdgeKindCalls), tier, 0.9, from+"->"+to, []string{"b/" + from + ".go:1"})
		if err != nil {
			t.Fatalf("NewEdge(%s->%s): %v", from, to, err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge(%s->%s): %v", from, to, err)
		}
	}
	mk("U", "S", model.TierConfirmed)
	mk("S", "M", model.TierDerived)
	mk("M", "T", model.TierConfirmed)
	return store, ids
}

func TestBatchedCompleteness(t *testing.T) {
	store, ids := seedBatchedGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "batched", analysis.Params{
		Symbol: ids["S"], Target: ids["T"],
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	// Impact forward(S) = {U}.
	if len(res.Nodes) != 1 || res.Nodes[0].Node.QualifiedName != "b.U" {
		t.Errorf("impact section = %+v, want [b.U]", res.Nodes)
	}
	// Chain S->T = [S->M, M->T] (one path of length 2).
	if len(res.Paths) != 1 || len(res.Paths[0]) != 2 {
		t.Errorf("chain section = %+v, want 1 path of length 2", res.Paths)
	}
	// Metrics present (graph-wide).
	if len(res.Metrics) == 0 {
		t.Error("metrics section empty, want graph-wide metrics")
	}
}

func TestBatchedProvenancePreserved(t *testing.T) {
	store, ids := seedBatchedGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	batched, err := svc.Dispatch(ctx, "batched", analysis.Params{Symbol: ids["S"], Target: ids["T"]})
	if err != nil {
		t.Fatalf("batched: %v", err)
	}
	// Standalone impact (forward).
	imp, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["S"], Direction: analysis.Forward})
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	// The batched impact members must equal the standalone ones (provenance verbatim).
	if !sameReached(batched.Nodes, imp.Nodes) {
		t.Errorf("batched impact nodes != standalone impact nodes (provenance not preserved)")
	}
	// Every impact node carries provenance.
	for _, rn := range batched.Nodes {
		if rn.ReachedVia.ID == "" || !rn.ReachedVia.Tier.Valid() || rn.ReachedVia.Reason == "" {
			t.Errorf("batched node %s lost provenance through aggregation", rn.Node.QualifiedName)
		}
	}
	// Every chain step carries provenance.
	for _, p := range batched.Paths {
		for _, e := range p {
			if e.ID == "" || !e.Tier.Valid() || e.Reason == "" || len(e.Evidence) == 0 {
				t.Errorf("batched chain step lost provenance: %+v", e)
			}
		}
	}
	// Every metric carries EdgeCount provenance where applicable.
	for _, m := range batched.Metrics {
		if m.Node.ID == "" {
			t.Errorf("batched metric lost node identity: %+v", m)
		}
	}
}

func TestBatchedTokenBudget(t *testing.T) {
	store, ids := seedBatchedGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// Unbounded (default budget) reference size.
	unbounded, err := svc.Dispatch(ctx, "batched", analysis.Params{Symbol: ids["S"], Target: ids["T"]})
	if err != nil {
		t.Fatalf("unbounded: %v", err)
	}
	unboundedBytes, _ := analysis.Marshal(unbounded)

	// A tiny token budget forces trimming. The trimmed response must be flagged
	// Truncated and materially smaller than the unbounded one.
	res, err := svc.Dispatch(ctx, "batched", analysis.Params{
		Symbol: ids["S"], Target: ids["T"], MaxTokens: 15,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected Truncated=true under a tight token budget")
	}
	trimmedBytes, err := analysis.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(trimmedBytes) >= len(unboundedBytes) {
		t.Errorf("trimmed bytes = %d, unbounded = %d (budget did not reduce size)", len(trimmedBytes), len(unboundedBytes))
	}
	// The trimmed estimate (bytes/4, the structured-payload token measure) must
	// be within budget UNLESS only the untrimmable chain remains.
	estimate := (len(trimmedBytes) + 3) / 4
	if estimate > 15 && len(res.Paths) == 0 {
		t.Errorf("trimmed estimate = %d tokens, want <= 15 (budget not respected)", estimate)
	}
}

func TestBatchedPartialResults(t *testing.T) {
	store, ids := seedBatchedGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// (a) No chain: target given but no calls path S->U (U is upstream of S).
	res, err := svc.Dispatch(ctx, "batched", analysis.Params{Symbol: ids["S"], Target: ids["U"]})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Errorf("no-chain outcome = %s, want found (impact+metrics still present)", res.Outcome)
	}
	if len(res.Paths) != 0 {
		t.Errorf("no-chain Paths = %d, want 0 (empty section, not failure)", len(res.Paths))
	}

	// (b) All sections can still yield found via metrics even if impact empty.
	res2, err := svc.Dispatch(ctx, "batched", analysis.Params{Symbol: ids["T"]}) // T has no dependents
	if err != nil {
		t.Fatalf("Dispatch T: %v", err)
	}
	if res2.Outcome != query.OutcomeFound {
		t.Errorf("isolated-impact outcome = %s, want found (metrics present)", res2.Outcome)
	}

	// (c) Unknown symbol -> not_found (propagated from impact).
	res3, err := svc.Dispatch(ctx, "batched", analysis.Params{Symbol: model.NodeId("zzzzzzzzzzzzzzzz")})
	if err != nil {
		t.Fatalf("Dispatch unknown: %v", err)
	}
	if res3.Outcome != query.OutcomeNotFound {
		t.Errorf("unknown symbol outcome = %s, want not_found", res3.Outcome)
	}
}

func TestBatchedDeterminism(t *testing.T) {
	ctx := context.Background()
	s1, ids1 := seedBatchedGraph(t)
	svc1 := analysis.NewDefaultService(s1)
	first, err := svc1.Dispatch(ctx, "batched", analysis.Params{Symbol: ids1["S"], Target: ids1["T"]})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	firstBytes, _ := analysis.Marshal(first)
	for i := 0; i < 30; i++ {
		res, err := svc1.Dispatch(ctx, "batched", analysis.Params{Symbol: ids1["S"], Target: ids1["T"]})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b, _ := analysis.Marshal(res)
		if !bytes.Equal(firstBytes, b) {
			t.Fatalf("iteration %d non-byte-identical (determinism violated)", i)
		}
	}
	s2, ids2 := seedBatchedGraph(t)
	svc2 := analysis.NewDefaultService(s2)
	r2, err := svc2.Dispatch(ctx, "batched", analysis.Params{Symbol: ids2["S"], Target: ids2["T"]})
	if err != nil {
		t.Fatalf("svc2: %v", err)
	}
	b2, _ := analysis.Marshal(r2)
	if !bytes.Equal(firstBytes, b2) {
		t.Fatal("two independent services produced non-identical batched output")
	}
}

func TestBatchedRegistered(t *testing.T) {
	store, _ := seedBatchedGraph(t)
	svc := analysis.NewDefaultService(store)
	names := svc.Names()
	sort.Strings(names)
	for _, want := range []string{"impact", "call-chain", "metrics", "batched"} {
		if !containsAnalyzer(names, want) {
			t.Errorf("analyzer %q not registered; names = %v", want, names)
		}
	}
}

// sameReached compares two reached-node sets by (node id, reaching edge id).
func sameReached(a, b []analysis.ReachedNode) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(r []analysis.ReachedNode) []string {
		out := make([]string, 0, len(r))
		for _, x := range r {
			out = append(out, string(x.Node.ID)+"|"+string(x.ReachedVia.ID))
		}
		sort.Strings(out)
		return out
	}
	ka, kb := key(a), key(b)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}
