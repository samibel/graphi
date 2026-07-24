package graphstore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// Conformance suite for the GraphScanner capability and its package-level
// fallbacks (NodeIDsOf/ForEachNode/ForEachEdge): one suite, parameterized by
// Factory, runs identically against MemStore and SQLiteStore — the streamed
// sequence must be element-for-element identical to the canonical
// Nodes/Edges(Query{}) listings, and a callback error must stop the scan and
// surface verbatim.

func scanFactories() map[string]Factory {
	return map[string]Factory{
		"mem":    MemFactory,
		"sqlite": SQLiteFactory,
	}
}

func TestGraphScanner_MatchesListings(t *testing.T) {
	for name, factory := range scanFactories() {
		t.Run(name, func(t *testing.T) {
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			seedLookupFixture(t, st)
			ctx := context.Background()

			wantNodes, err := st.Nodes(ctx, Query{})
			if err != nil {
				t.Fatalf("Nodes: %v", err)
			}
			wantEdges, err := st.Edges(ctx, Query{})
			if err != nil {
				t.Fatalf("Edges: %v", err)
			}

			var gotNodes []model.Node
			if err := ForEachNode(ctx, st, func(n model.Node) error {
				gotNodes = append(gotNodes, n)
				return nil
			}); err != nil {
				t.Fatalf("ForEachNode: %v", err)
			}
			if !reflect.DeepEqual(gotNodes, wantNodes) {
				t.Errorf("ForEachNode stream diverges from Nodes(Query{}):\n got %d nodes %v\nwant %d nodes %v",
					len(gotNodes), gotNodes, len(wantNodes), wantNodes)
			}

			var gotEdges []model.Edge
			if err := ForEachEdge(ctx, st, func(e model.Edge) error {
				gotEdges = append(gotEdges, e)
				return nil
			}); err != nil {
				t.Fatalf("ForEachEdge: %v", err)
			}
			if !reflect.DeepEqual(gotEdges, wantEdges) {
				t.Errorf("ForEachEdge stream diverges from Edges(Query{}):\n got %d edges\nwant %d edges", len(gotEdges), len(wantEdges))
			}

			gotIDs, err := NodeIDsOf(ctx, st)
			if err != nil {
				t.Fatalf("NodeIDsOf: %v", err)
			}
			wantIDs := make([]model.NodeId, 0, len(wantNodes))
			for _, n := range wantNodes {
				wantIDs = append(wantIDs, n.ID())
			}
			if !reflect.DeepEqual(gotIDs, wantIDs) {
				t.Errorf("NodeIDsOf diverges from Nodes(Query{}) ids:\n got %v\nwant %v", gotIDs, wantIDs)
			}
		})
	}
}

func TestGraphScanner_CallbackErrorStopsScan(t *testing.T) {
	sentinel := errors.New("stop here")
	for name, factory := range scanFactories() {
		t.Run(name, func(t *testing.T) {
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			seedLookupFixture(t, st)
			ctx := context.Background()

			calls := 0
			if err := ForEachNode(ctx, st, func(model.Node) error {
				calls++
				return sentinel
			}); !errors.Is(err, sentinel) {
				t.Errorf("ForEachNode: want sentinel error back, got %v", err)
			}
			if calls != 1 {
				t.Errorf("ForEachNode: callback ran %d times after erroring, want 1", calls)
			}

			calls = 0
			if err := ForEachEdge(ctx, st, func(model.Edge) error {
				calls++
				return sentinel
			}); !errors.Is(err, sentinel) {
				t.Errorf("ForEachEdge: want sentinel error back, got %v", err)
			}
			if calls != 1 {
				t.Errorf("ForEachEdge: callback ran %d times after erroring, want 1", calls)
			}
		})
	}
}

// TestSQLiteScan_DoesNotBuildCache pins the reason the scan ports exist: a
// whole-graph traversal through them must not materialize the memGraph hot
// cache (the listing reads' cache "hit" costs a full-graph rebuild first).
func TestSQLiteScan_DoesNotBuildCache(t *testing.T) {
	st, err := SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedLookupFixture(t, st)
	sq := st.(*SQLiteStore)
	ctx := context.Background()
	if err := sq.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	before := sq.CacheRebuilds()

	if _, err := NodeIDsOf(ctx, st); err != nil {
		t.Fatalf("NodeIDsOf: %v", err)
	}
	if err := ForEachNode(ctx, st, func(model.Node) error { return nil }); err != nil {
		t.Fatalf("ForEachNode: %v", err)
	}
	if err := ForEachEdge(ctx, st, func(model.Edge) error { return nil }); err != nil {
		t.Fatalf("ForEachEdge: %v", err)
	}
	if got := sq.CacheRebuilds(); got != before {
		t.Errorf("scans rebuilt the hot cache %d time(s); scans must bypass it entirely", got-before)
	}
}
