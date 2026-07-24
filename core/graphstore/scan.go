package graphstore

import (
	"context"

	"github.com/samibel/graphi/core/model"
)

// GraphScanner is an optional capability interface (pattern: Batcher): bulk
// streaming reads over the whole graph that bypass any whole-graph hot cache.
// The full-listing Nodes/Edges(Query{}) reads materialize the entire graph
// twice over on the SQLite backend — the memGraph cache mirror plus the
// returned slice — which is exactly the residency the ingest pipeline must
// avoid; these ports hand the caller one element at a time straight from the
// durable layer instead.
//
// Order contract: identical to the listing methods — nodes by NodeId
// ascending, edges by EdgeId ascending — so a streamed consumer observes the
// same canonical sequence a slice consumer would. A non-nil error from fn
// stops the scan and is returned verbatim. fn must not write to the store
// while the scan is in flight (collect first, then write).
type GraphScanner interface {
	// NodeIDs returns every node id in canonical ascending order — the
	// whole-graph id set without reconstructing a single node.
	NodeIDs(ctx context.Context) ([]model.NodeId, error)
	// ScanNodes streams every node (meta included) in canonical NodeId order.
	ScanNodes(ctx context.Context, fn func(model.Node) error) error
	// ScanEdges streams every edge (provenance intact) in canonical EdgeId
	// order.
	ScanEdges(ctx context.Context, fn func(model.Edge) error) error
}

// NodeIDsOf returns s's native id scan when supported, else derives the ids
// from a full Nodes listing. Callers get the cache-bypassing read where the
// backend can provide it without excluding backends that cannot.
func NodeIDsOf(ctx context.Context, s Graphstore) ([]model.NodeId, error) {
	if sc, ok := s.(GraphScanner); ok {
		return sc.NodeIDs(ctx)
	}
	nodes, err := s.Nodes(ctx, Query{})
	if err != nil {
		return nil, err
	}
	ids := make([]model.NodeId, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID())
	}
	return ids, nil
}

// ForEachNode streams every node through s's native scan when supported, else
// falls back to iterating a full Nodes listing (same canonical order, same
// fn-error semantics).
func ForEachNode(ctx context.Context, s Graphstore, fn func(model.Node) error) error {
	if sc, ok := s.(GraphScanner); ok {
		return sc.ScanNodes(ctx, fn)
	}
	nodes, err := s.Nodes(ctx, Query{})
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if err := fn(n); err != nil {
			return err
		}
	}
	return nil
}

// ForEachEdge streams every edge through s's native scan when supported, else
// falls back to iterating a full Edges listing (same canonical order, same
// fn-error semantics).
func ForEachEdge(ctx context.Context, s Graphstore, fn func(model.Edge) error) error {
	if sc, ok := s.(GraphScanner); ok {
		return sc.ScanEdges(ctx, fn)
	}
	edges, err := s.Edges(ctx, Query{})
	if err != nil {
		return err
	}
	for _, e := range edges {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}
