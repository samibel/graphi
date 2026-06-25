package query_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// hierarchyGraph builds a small G2 hierarchy in an in-memory store:
//
//	shop.Reader (interface) <--implements-- shop.Collector (interface)
//	shop.Base   (struct)    <--inherits---- shop.Impl      (struct)
//	shop.Reader <--implements-- shop.Collector2
//
// All edges TierConfirmed with file:line evidence.
func hierarchyGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	mk := func(name string) model.Node {
		n, err := model.NewNode("type", name, "shop/"+name+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", name, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", name, err)
		}
		return n
	}
	names := []string{"shop.Reader", "shop.Collector", "shop.Collector2", "shop.Base", "shop.Impl"}
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}
	for _, n := range names {
		node := mk(n)
		ids[n] = node.ID()
		nodes[n] = node
	}
	mkEdge := func(from, to, kind string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, model.TierConfirmed, 1.0, from+" "+kind+" "+to, []string{"shop/" + from + ".go:2"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}
	mkEdge("shop.Collector", "shop.Reader", query.EdgeKindImplements)
	mkEdge("shop.Collector2", "shop.Reader", query.EdgeKindImplements)
	mkEdge("shop.Impl", "shop.Base", query.EdgeKindInherits)
	return store, ids
}

func TestDispatch_Implementers(t *testing.T) {
	store, ids := hierarchyGraph(t)
	svc := query.New(store)
	res, err := svc.Dispatch(context.Background(), query.OpImplementers, ids["shop.Reader"], 0)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	got := nodeIDSet(res.Nodes)
	if !got[ids["shop.Collector"]] || !got[ids["shop.Collector2"]] {
		t.Fatalf("expected both implementers, got %v", got)
	}
}

func TestDispatch_Implements(t *testing.T) {
	store, ids := hierarchyGraph(t)
	svc := query.New(store)
	res, err := svc.Dispatch(context.Background(), query.OpImplements, ids["shop.Collector"], 0)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got := nodeIDSet(res.Nodes)
	if len(got) != 1 || !got[ids["shop.Reader"]] {
		t.Fatalf("expected only Reader as implemented interface, got %v", got)
	}
}

func TestDispatch_Subtypes_CombinesKinds(t *testing.T) {
	store, ids := hierarchyGraph(t)
	svc := query.New(store)
	// Reader's subtypes = Collector + Collector2 (implements).
	res, err := svc.Subtypes(context.Background(), ids["shop.Reader"])
	if err != nil {
		t.Fatalf("Subtypes: %v", err)
	}
	got := nodeIDSet(res.Nodes)
	if len(got) != 2 || !got[ids["shop.Collector"]] || !got[ids["shop.Collector2"]] {
		t.Fatalf("Reader subtypes wrong: %v", got)
	}
	// Base's subtypes = Impl (inherits).
	res2, err := svc.Subtypes(context.Background(), ids["shop.Base"])
	if err != nil {
		t.Fatalf("Subtypes Base: %v", err)
	}
	got2 := nodeIDSet(res2.Nodes)
	if len(got2) != 1 || !got2[ids["shop.Impl"]] {
		t.Fatalf("Base subtypes wrong: %v", got2)
	}
}

func TestDispatch_Supertypes(t *testing.T) {
	store, ids := hierarchyGraph(t)
	svc := query.New(store)
	res, err := svc.Supertypes(context.Background(), ids["shop.Impl"])
	if err != nil {
		t.Fatalf("Supertypes: %v", err)
	}
	got := nodeIDSet(res.Nodes)
	if len(got) != 1 || !got[ids["shop.Base"]] {
		t.Fatalf("Impl supertypes wrong: %v", got)
	}
}

func TestDispatch_HierarchyNotFound(t *testing.T) {
	store, _ := hierarchyGraph(t)
	svc := query.New(store)
	for _, op := range []string{query.OpImplementers, query.OpImplements, query.OpOverrides, query.OpSubtypes, query.OpSupertypes} {
		res, err := svc.Dispatch(context.Background(), op, "shop.does-not-exist", 0)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", op, err)
		}
		if res.Outcome != query.OutcomeNotFound {
			t.Fatalf("%s: outcome %s want not_found", op, res.Outcome)
		}
	}
}

func TestDispatch_HierarchyDeterminism(t *testing.T) {
	store, ids := hierarchyGraph(t)
	svc := query.New(store)
	q := func() query.Result {
		r, err := svc.Subtypes(context.Background(), ids["shop.Reader"])
		if err != nil {
			t.Fatal(err)
		}
		return r
	}()
	for i := 0; i < 20; i++ {
		r, err := svc.Subtypes(context.Background(), ids["shop.Reader"])
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Nodes) != len(q.Nodes) {
			t.Fatalf("run %d: node count drift", i)
		}
		for j := range q.Nodes {
			if q.Nodes[j].ID != r.Nodes[j].ID {
				t.Fatalf("run %d: ordering drift at %d", i, j)
			}
		}
	}
}

func nodeIDSet(ns []query.ResultNode) map[model.NodeId]bool {
	m := map[model.NodeId]bool{}
	for _, n := range ns {
		m[n.ID] = true
	}
	return m
}
