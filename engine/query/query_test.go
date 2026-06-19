package query_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// seedGraph builds a deterministic synthetic graph in an in-memory store with
// calls/references/defines edges carrying full provenance.
//
// Topology (by qualified name):
//
//	pkg.A --calls--> pkg.B --calls--> pkg.C
//	pkg.A --calls--> pkg.C            (A also calls C directly)
//	pkg.D --references--> pkg.B
//	pkg.A --defines--> pkg.A_inner
//
// Returns the store and a name→NodeId map.
func seedGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(name string) model.Node {
		n, err := model.NewNode("function", name, "pkg/"+name+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", name, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", name, err)
		}
		return n
	}

	names := []string{"pkg.A", "pkg.B", "pkg.C", "pkg.D", "pkg.A_inner"}
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}
	for _, name := range names {
		n := mk(name)
		ids[name] = n.ID()
		nodes[name] = n
	}

	mkEdge := func(from, to, kind string, tier model.ConfidenceTier, conf float64, reason string, ev []string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, conf, reason, ev)
		if err != nil {
			t.Fatalf("NewEdge(%s->%s %s): %v", from, to, kind, err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge(%s->%s %s): %v", from, to, kind, err)
		}
	}

	mkEdge("pkg.A", "pkg.B", query.EdgeKindCalls, model.TierConfirmed, 1.0, "direct call A->B", []string{"pkg/A.go:5"})
	mkEdge("pkg.B", "pkg.C", query.EdgeKindCalls, model.TierDerived, 0.8, "resolved call B->C", []string{"pkg/B.go:9"})
	mkEdge("pkg.A", "pkg.C", query.EdgeKindCalls, model.TierHeuristic, 0.4, "heuristic call A->C", []string{"pkg/A.go:7"})
	mkEdge("pkg.D", "pkg.B", query.EdgeKindReferences, model.TierDerived, 0.7, "reference D->B", []string{"pkg/D.go:3"})
	mkEdge("pkg.A", "pkg.A_inner", query.EdgeKindDefines, model.TierConfirmed, 1.0, "A defines inner", []string{"pkg/A.go:2"})

	return store, ids
}

func mustHaveNode(t *testing.T, res query.Result, id model.NodeId) {
	t.Helper()
	for _, n := range res.Nodes {
		if n.ID == id {
			return
		}
	}
	t.Fatalf("expected node %s in result nodes %+v", id, res.Nodes)
}

func findEdge(res query.Result, from, to model.NodeId, kind string) (query.ResultEdge, bool) {
	for _, e := range res.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return e, true
		}
	}
	return query.ResultEdge{}, false
}

// AC1 + refinement AC3: callers/callees/references/definition return matching
// nodes with each edge's provenance attached verbatim.
func TestCallers_ProvenanceAttached(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	res, err := svc.Callers(ctx, ids["pkg.C"])
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	// C is called by A and B.
	mustHaveNode(t, res, ids["pkg.A"])
	mustHaveNode(t, res, ids["pkg.B"])

	e, ok := findEdge(res, ids["pkg.B"], ids["pkg.C"], query.EdgeKindCalls)
	if !ok {
		t.Fatalf("missing B->C calls edge")
	}
	if e.Tier != model.TierDerived || e.Confidence != 0.8 || e.Reason != "resolved call B->C" {
		t.Fatalf("provenance not attached verbatim: %+v", e)
	}
	if len(e.Evidence) != 1 || e.Evidence[0] != "pkg/B.go:9" {
		t.Fatalf("evidence not passed through: %+v", e.Evidence)
	}
}

func TestCallees(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	res, err := svc.Callees(ctx, ids["pkg.A"])
	if err != nil {
		t.Fatal(err)
	}
	// A calls B and C.
	mustHaveNode(t, res, ids["pkg.B"])
	mustHaveNode(t, res, ids["pkg.C"])
	if len(res.Edges) != 2 {
		t.Fatalf("want 2 callee edges, got %d", len(res.Edges))
	}
}

func TestReferences(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	res, err := svc.References(ctx, ids["pkg.B"])
	if err != nil {
		t.Fatal(err)
	}
	mustHaveNode(t, res, ids["pkg.D"])
	if _, ok := findEdge(res, ids["pkg.D"], ids["pkg.B"], query.EdgeKindReferences); !ok {
		t.Fatalf("missing D->B references edge")
	}
}

func TestDefinition(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	res, err := svc.Definition(ctx, ids["pkg.A"])
	if err != nil {
		t.Fatal(err)
	}
	mustHaveNode(t, res, ids["pkg.A_inner"])
}

// AC4 + refinement AC3: unresolved symbol → explicit not-found, no error.
func TestNotFound_NoError(t *testing.T) {
	ctx := context.Background()
	store, _ := seedGraph(t)
	svc := query.New(store)

	for _, op := range query.Operations {
		res, err := svc.Dispatch(ctx, op, model.NodeId("does-not-exist"), 2)
		if err != nil {
			t.Fatalf("%s: unexpected error for missing symbol: %v", op, err)
		}
		if res.Outcome != query.OutcomeNotFound {
			t.Fatalf("%s: outcome = %s, want not_found", op, res.Outcome)
		}
		if res.Found() {
			t.Fatalf("%s: Found() = true for missing symbol", op)
		}
		if len(res.Nodes) != 0 || len(res.Edges) != 0 {
			t.Fatalf("%s: not-found result must be empty, got %d nodes %d edges", op, len(res.Nodes), len(res.Edges))
		}
	}
}

// Empty (resolved, zero results) is distinct from not-found.
func TestEmpty_DistinctFromNotFound(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	// pkg.C has no callees.
	res, err := svc.Callees(ctx, ids["pkg.C"])
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("outcome = %s, want empty", res.Outcome)
	}
	if !res.Found() {
		t.Fatalf("resolved-but-empty must report Found() = true")
	}
}

// AC2 + refinement AC4: neighborhood within N hops, cycle-guarded, deterministic.
func TestNeighborhood_Hops(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	// depth 1 from A: A plus its direct neighbors B, C, A_inner.
	res1, err := svc.Neighborhood(ctx, ids["pkg.A"], 1)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Depth == nil || *res1.Depth != 1 {
		t.Fatalf("depth = %v, want 1", res1.Depth)
	}
	mustHaveNode(t, res1, ids["pkg.A"])
	mustHaveNode(t, res1, ids["pkg.B"])
	mustHaveNode(t, res1, ids["pkg.C"])
	mustHaveNode(t, res1, ids["pkg.A_inner"])
	// D references B but is 2 hops from A (A-B-D), must NOT appear at depth 1.
	for _, n := range res1.Nodes {
		if n.ID == ids["pkg.D"] {
			t.Fatalf("D should not be within 1 hop of A")
		}
	}

	// depth 2 reaches D (A->B, D->B undirected).
	res2, err := svc.Neighborhood(ctx, ids["pkg.A"], 2)
	if err != nil {
		t.Fatal(err)
	}
	mustHaveNode(t, res2, ids["pkg.D"])
}

// refinement AC4: depth boundary at max-1 / max / max+1 (clamp at max+1).
func TestNeighborhood_DepthClamp(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	cases := []struct {
		req  int
		want int
	}{
		{query.MaxNeighborhoodDepth - 1, query.MaxNeighborhoodDepth - 1},
		{query.MaxNeighborhoodDepth, query.MaxNeighborhoodDepth},
		{query.MaxNeighborhoodDepth + 1, query.MaxNeighborhoodDepth}, // clamped
		{1000, query.MaxNeighborhoodDepth},                           // clamped
		{-3, 0},                                                      // floored
	}
	for _, c := range cases {
		res, err := svc.Neighborhood(ctx, ids["pkg.A"], c.req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Depth == nil || *res.Depth != c.want {
			t.Fatalf("req depth %d: effective depth = %v, want %d", c.req, res.Depth, c.want)
		}
	}
}

// AC3: byte-stability across repeated runs.
func TestByteStable_RepeatedRuns(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	var first []byte
	for i := 0; i < 25; i++ {
		res, err := svc.Neighborhood(ctx, ids["pkg.A"], query.MaxNeighborhoodDepth)
		if err != nil {
			t.Fatal(err)
		}
		b, err := query.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
			continue
		}
		if string(b) != string(first) {
			t.Fatalf("run %d differs:\n%s\n!=\n%s", i, b, first)
		}
	}
}

// refinement AC2: insertion-order permutation must not change output bytes.
func TestByteStable_InsertionOrderPermutation(t *testing.T) {
	ctx := context.Background()

	// Build two stores with the same logical graph but different insertion order.
	build := func(reverse bool) []byte {
		store := graphstore.NewMemStore()
		type ndef struct{ name string }
		names := []string{"x.A", "x.B", "x.C", "x.D"}
		nodes := map[string]model.Node{}
		mkNodes := func(order []string) {
			for _, name := range order {
				n, _ := model.NewNode("function", name, "x/"+name+".go", 1, 1)
				_ = store.PutNode(ctx, n)
				nodes[name] = n
			}
		}
		order := append([]string{}, names...)
		if reverse {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		}
		mkNodes(order)

		type edef struct {
			from, to, kind string
			tier           model.ConfidenceTier
			conf           float64
			reason         string
			ev             []string
		}
		edges := []edef{
			{"x.A", "x.B", query.EdgeKindCalls, model.TierConfirmed, 1, "ab", []string{"e1"}},
			{"x.A", "x.C", query.EdgeKindCalls, model.TierDerived, 0.8, "ac", []string{"e2"}},
			{"x.A", "x.D", query.EdgeKindCalls, model.TierHeuristic, 0.4, "ad", []string{"e3"}},
		}
		if reverse {
			for i, j := 0, len(edges)-1; i < j; i, j = i+1, j-1 {
				edges[i], edges[j] = edges[j], edges[i]
			}
		}
		for _, e := range edges {
			ed, _ := model.NewEdge(nodes[e.from].ID(), nodes[e.to].ID(), e.kind, e.tier, e.conf, e.reason, e.ev)
			_ = store.PutEdge(ctx, ed)
		}

		svc := query.New(store)
		res, err := svc.Callees(ctx, nodes["x.A"].ID())
		if err != nil {
			t.Fatal(err)
		}
		b, err := query.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	forward := build(false)
	reverse := build(true)
	if string(forward) != string(reverse) {
		t.Fatalf("insertion order affected output:\n%s\n!=\n%s", forward, reverse)
	}
}

// refinement AC2: tie-break determinism — edges ordered confirmed<derived<heuristic.
func TestComparator_TierTieBreak(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	// A's callees: A->B (confirmed), A->C (heuristic). Confirmed must come first.
	res, err := svc.Callees(ctx, ids["pkg.A"])
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 2 {
		t.Fatalf("want 2 edges, got %d", len(res.Edges))
	}
	if res.Edges[0].Tier != model.TierConfirmed {
		t.Fatalf("first edge tier = %s, want confirmed (most confident first)", res.Edges[0].Tier)
	}
	if res.Edges[1].Tier != model.TierHeuristic {
		t.Fatalf("second edge tier = %s, want heuristic", res.Edges[1].Tier)
	}
}

// Read-only guarantee smoke test: running every query leaves the store unchanged.
func TestReadOnly_StoreUnchanged(t *testing.T) {
	ctx := context.Background()
	store, ids := seedGraph(t)
	svc := query.New(store)

	before, _ := store.Nodes(ctx, graphstore.Query{})
	beforeEdges, _ := store.Edges(ctx, graphstore.Query{})

	for _, op := range query.Operations {
		if _, err := svc.Dispatch(ctx, op, ids["pkg.A"], query.MaxNeighborhoodDepth); err != nil {
			t.Fatal(err)
		}
	}

	after, _ := store.Nodes(ctx, graphstore.Query{})
	afterEdges, _ := store.Edges(ctx, graphstore.Query{})
	if len(before) != len(after) || len(beforeEdges) != len(afterEdges) {
		t.Fatalf("store mutated: nodes %d->%d edges %d->%d", len(before), len(after), len(beforeEdges), len(afterEdges))
	}
}
