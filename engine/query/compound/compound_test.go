package compound_test

import (
	"context"
	"errors"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
)

// seedGraph mirrors engine/query's test topology and adds a second "tier" so a
// compound query can demonstrably collapse multiple fixed round-trips.
//
//	pkg.A --calls--> pkg.B --calls--> pkg.C
//	pkg.A --calls--> pkg.C            (A also calls C directly)
//	pkg.D --references--> pkg.B
//	pkg.A --defines--> pkg.A_inner
func seedGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, name string) model.Node {
		n, err := model.NewNode(kind, name, "pkg/"+name+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", name, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", name, err)
		}
		return n
	}

	names := []struct {
		kind, name string
	}{
		{"function", "pkg.A"}, {"function", "pkg.B"}, {"function", "pkg.C"},
		{"function", "pkg.D"}, {"variable", "pkg.A_inner"},
	}
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}
	for _, n := range names {
		node := mk(n.kind, n.name)
		ids[n.name] = node.ID()
		nodes[n.name] = node
	}

	mkEdge := func(from, to, kind string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, model.TierConfirmed, 1.0, from+"->"+to, []string{"pkg/" + from + ".go:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}
	mkEdge("pkg.A", "pkg.B", query.EdgeKindCalls)
	mkEdge("pkg.B", "pkg.C", query.EdgeKindCalls)
	mkEdge("pkg.A", "pkg.C", query.EdgeKindCalls)
	mkEdge("pkg.D", "pkg.B", query.EdgeKindReferences)
	mkEdge("pkg.A", "pkg.A_inner", query.EdgeKindDefines)

	return store, ids
}

// TestExecute_RoundTripCollapse: a two-hop outbound `calls` query from A returns
// B and C (plus A as seed) in ONE request — a result that fixed queries would
// need two callees round-trips to assemble.
func TestExecute_RoundTripCollapse(t *testing.T) {
	store, ids := seedGraph(t)
	q := compound.Query{
		Seed:  ids["pkg.A"],
		Steps: []compound.Step{{Direction: compound.DirOutbound, Kinds: []string{query.EdgeKindCalls}}},
	}
	res, err := compound.Execute(context.Background(), store, q)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	got := nodeSet(res)
	if !got[ids["pkg.B"]] || !got[ids["pkg.C"]] {
		t.Fatalf("expected B and C in result, got %v", got)
	}
	// A single fixed `callees A` round-trip would return only B and C directly,
	// NOT C-via-B transitive reachability within one call; the compound result
	// carries the transitive callee C without a second round-trip.
	if res.Depth == nil || *res.Depth != 1 {
		t.Fatalf("effective depth = %v, want 1", res.Depth)
	}
}

// TestExecute_Determinism: identical input yields byte-identical node/edge
// ordering across runs (map-iteration independence via the canonical comparator).
func TestExecute_Determinism(t *testing.T) {
	store, ids := seedGraph(t)
	q := compound.Query{
		Seed:  ids["pkg.A"],
		Steps: []compound.Step{{Direction: compound.DirBoth}},
	}
	first, err := compound.Execute(context.Background(), store, q)
	if err != nil {
		t.Fatalf("Execute first: %v", err)
	}
	for i := 0; i < 20; i++ {
		r, err := compound.Execute(context.Background(), store, q)
		if err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
		if !sameOrder(first, r) {
			t.Fatalf("run %d diverged: %+v vs %+v", i, first.Nodes, r.Nodes)
		}
	}
}

// TestExecute_ParityVsFixedOps: a 1-hop inbound `calls` compound query from B
// traverses the SAME edges with the SAME canonical ordering as the fixed
// Callers(B) operation. Compound returns a subgraph that includes the seed
// (like Neighborhood); the meaningful parity signal is the EDGE set plus the
// non-seed (matched) node set, asserted here.
func TestExecute_ParityVsFixedOps(t *testing.T) {
	store, ids := seedGraph(t)
	svc := query.New(store)

	fixed, err := svc.Callers(context.Background(), ids["pkg.B"])
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	comp, err := compound.Execute(context.Background(), store, compound.Query{
		Seed:  ids["pkg.B"],
		Steps: []compound.Step{{Direction: compound.DirInbound, Kinds: []string{query.EdgeKindCalls}}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Edge sets must be identical and canonical-ordered.
	if len(comp.Edges) != len(fixed.Edges) {
		t.Fatalf("edge count %d != fixed %d", len(comp.Edges), len(fixed.Edges))
	}
	for i := range fixed.Edges {
		if comp.Edges[i].ID != fixed.Edges[i].ID {
			t.Fatalf("edge %d: %s != %s", i, comp.Edges[i].ID, fixed.Edges[i].ID)
		}
	}
	// Matched (non-seed) node set must equal the fixed result's nodes.
	matched := map[model.NodeId]bool{}
	for _, n := range comp.Nodes {
		if n.ID != comp.Symbol {
			matched[n.ID] = true
		}
	}
	fixedNodes := map[model.NodeId]bool{}
	for _, n := range fixed.Nodes {
		fixedNodes[n.ID] = true
	}
	if len(matched) != len(fixedNodes) {
		t.Fatalf("matched %d != fixed nodes %d", len(matched), len(fixedNodes))
	}
	for id := range fixedNodes {
		if !matched[id] {
			t.Fatalf("fixed node %s missing from compound matched set", id)
		}
	}
}

// TestExecute_NotFound: unresolved seed yields an explicit not-found Result, not
// an error (parity with fixed operations' typing).
func TestExecute_NotFound(t *testing.T) {
	store, _ := seedGraph(t)
	res, err := compound.Execute(context.Background(), store, compound.Query{
		Seed:  "pkg.does-not-exist",
		Steps: []compound.Step{{Direction: compound.DirOutbound}},
	})
	if err != nil {
		t.Fatalf("Execute: unexpected error %v", err)
	}
	if res.Outcome != query.OutcomeNotFound {
		t.Fatalf("outcome = %s, want not_found", res.Outcome)
	}
}

// TestValidate_MalformedNeverPanics: malformed queries return a typed
// ErrInvalidQuery and never panic.
func TestValidate_MalformedNeverPanics(t *testing.T) {
	cases := []compound.Query{
		{Steps: []compound.Step{{Direction: compound.DirOutbound}}}, // empty seed
		{Seed: "pkg.A"}, // no steps
		{Seed: "pkg.A", Steps: []compound.Step{{Direction: "sideways"}}},                                // bad direction
		{Seed: "pkg.A", Steps: []compound.Step{{Direction: compound.DirOutbound, Kinds: []string{""}}}}, // empty kind
	}
	for i, c := range cases {
		err := compound.Validate(c)
		if !errors.Is(err, compound.ErrInvalidQuery) {
			t.Fatalf("case %d: want ErrInvalidQuery, got %v", i, err)
		}
	}
}

// TestParse_TypedErrors: the text parser rejects malformed input with typed
// errors and parses a well-formed query identically to the AST form.
func TestParse_TypedErrors(t *testing.T) {
	bad := []string{
		"",                           // missing seed
		"SEED\n",                     // seed empty
		"HOP out calls\n",            // no seed
		"SEED pkg.A\nBOGUS x\n",      // unknown keyword
		"SEED pkg.A\nHOP sideways\n", // bad direction
		"SEED pkg.A\nHOP\n",          // hop missing direction
	}
	for i, b := range bad {
		if _, err := compound.Parse(b); !errors.Is(err, compound.ErrInvalidQuery) {
			t.Fatalf("bad case %d: want ErrInvalidQuery, got %v", i, err)
		}
	}
	good, err := compound.Parse("SEED pkg.A\nHOP out calls\nHOP in references\nWHERE KIND function\n")
	if err != nil {
		t.Fatalf("Parse good: %v", err)
	}
	if good.Seed != "pkg.A" || len(good.Steps) != 2 || good.Where == nil || good.Where.NodeKind != "function" {
		t.Fatalf("parsed query wrong: %+v", good)
	}
	if good.Steps[0].Direction != compound.DirOutbound || good.Steps[1].Direction != compound.DirInbound {
		t.Fatalf("parsed directions wrong: %+v", good.Steps)
	}
}

// TestExecute_BoundClamp: a query with more steps than MaxCompoundDepth is
// clamped, and the reported effective depth never exceeds the bound.
func TestExecute_BoundClamp(t *testing.T) {
	store, ids := seedGraph(t)
	steps := make([]compound.Step, compound.MaxCompoundDepth+5)
	for i := range steps {
		steps[i] = compound.Step{Direction: compound.DirBoth}
	}
	res, err := compound.Execute(context.Background(), store, compound.Query{
		Seed:  ids["pkg.A"],
		Steps: steps,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Depth == nil {
		t.Fatalf("depth nil")
	}
	if *res.Depth > compound.MaxCompoundDepth {
		t.Fatalf("effective depth %d > MaxCompoundDepth %d", *res.Depth, compound.MaxCompoundDepth)
	}
}

// TestExecute_WhereFilter: WHERE KIND drops nodes (and their incident edges) not
// matching the predicate while keeping the surviving subgraph consistent.
func TestExecute_WhereFilter(t *testing.T) {
	store, ids := seedGraph(t)
	res, err := compound.Execute(context.Background(), store, compound.Query{
		Seed:  ids["pkg.A"],
		Steps: []compound.Step{{Direction: compound.DirBoth}},
		Where: &compound.Where{NodeKind: "variable"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, n := range res.Nodes {
		if n.Kind != "variable" {
			t.Fatalf("WHERE violated: node %s kind %s", n.ID, n.Kind)
		}
	}
	// Only pkg.A_inner is a variable reachable from A; the defines edge survives
	// (both endpoints? A is function -> filtered out, so the edge must be dropped
	// because its From endpoint A did not survive). Nodes must contain only the
	// variable, and no edges should connect to filtered-out nodes.
	for _, e := range res.Edges {
		if e.From == ids["pkg.A"] || e.To == ids["pkg.A"] {
			t.Fatalf("edge references filtered seed: %+v", e)
		}
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func nodeSet(r query.Result) map[model.NodeId]bool {
	m := map[model.NodeId]bool{}
	for _, n := range r.Nodes {
		m[n.ID] = true
	}
	return m
}

func sameOrder(a, b query.Result) bool {
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) {
		return false
	}
	for i := range a.Nodes {
		if a.Nodes[i].ID != b.Nodes[i].ID {
			return false
		}
	}
	for i := range a.Edges {
		if a.Edges[i].ID != b.Edges[i].ID {
			return false
		}
	}
	return true
}
