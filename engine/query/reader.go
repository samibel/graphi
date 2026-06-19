// Package query is graphi's single, shared, read-only structural query service.
//
// Layering: query is an engine package. It consumes core/model and a read-only
// view of core/graphstore ONLY, and it MUST NOT import surfaces/ (CI gate:
// `go list -deps ./engine/query | grep samibel/graphi/surfaces` must be empty).
// Every surface (CLI, MCP, daemon, HTTP) routes structural queries through this
// one service so the surfaces can never diverge: there is no surface-local
// traversal, sort, or serialization logic anywhere.
//
// Read-only by construction: the service depends on the Reader interface below,
// which exposes only the non-mutating subset of graphstore.Graphstore. There is
// no reachable mutation path from query, so a query can never write to the graph
// — this is a compile-time guarantee, not a convention.
//
// Determinism: every result is materialized into slices and sorted by a single
// canonical comparator (see compare.go) before it is returned, never emitted
// directly from a Go map. Combined with the canonical serializer (see
// serialize.go), identical inputs over the same graph state yield byte-identical
// output across repeated runs and across surfaces.
package query

import (
	"context"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// Reader is the read-only subset of graphstore.Graphstore consumed by the query
// service. By depending on this interface (and never on the full Graphstore),
// engine/query has no reachable mutation path: the read-only guarantee is
// enforced by the type system. graphstore.Graphstore satisfies Reader, so any
// conforming backend (in-memory, SQLite, …) can back the service unchanged.
type Reader interface {
	// GetNode returns the node with the given ID, or graphstore.ErrNotFound.
	GetNode(ctx context.Context, id model.NodeId) (model.Node, error)
	// GetEdge returns the edge with the given ID, or graphstore.ErrNotFound.
	GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error)
	// Nodes returns all nodes matching q in canonical NodeId order.
	Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error)
	// Edges returns all edges matching q in canonical EdgeId order, provenance intact.
	Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error)
}

// Compile-time proof that the full Graphstore satisfies the read-only Reader.
var _ Reader = (graphstore.Graphstore)(nil)
