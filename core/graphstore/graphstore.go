// Package graphstore defines graphi's pluggable durable graph backend contract
// and its reference implementations.
//
// Layering: graphstore is a core leaf library. It consumes core/model only and
// MUST NOT import engine/ or surfaces/. It uses pure-Go/stdlib dependencies plus
// the CGo-free modernc.org/sqlite driver. There is zero outbound network
// activity in any operation.
//
// Backend model (architecture §7.2): SQLite (WAL journal mode, FTS5 over
// searchable text fields) is the durable source of truth. An in-memory "memgraph"
// hot cache sits in front of it; the cache is evictable and rebuilt from SQLite
// on demand, and is never authoritative — evicting it never loses data because
// SQLite holds the canonical state. Writes commit to SQLite FIRST, then update
// the cache. The Graphstore is exposed behind an interface so alternate backends
// (e.g. the in-memory test double) satisfy the same contract test suite.
package graphstore

import (
	"context"
	"errors"

	"github.com/samibel/graphi/core/model"
)

// Errors returned by Graphstore implementations. They are typed sentinels so
// callers and the contract suite can match with errors.Is.
var (
	// ErrNotFound is returned by GetNode/GetEdge when no record exists for the
	// requested ID.
	ErrNotFound = errors.New("graphstore: not found")

	// ErrClosed is returned by any operation invoked after Close.
	ErrClosed = errors.New("graphstore: store is closed")

	// ErrUnknownEdgeEndpoint is returned by PutEdge when From or To does not
	// reference an already-stored node. Edges require their endpoints to exist so
	// the durable graph stays referentially consistent.
	ErrUnknownEdgeEndpoint = errors.New("graphstore: edge references unknown node")
)

// Query constrains a node/edge listing or search. The zero Query matches
// everything. All filters are ANDed together. Text, when non-empty, runs against
// the backend's full-text index (SQLite FTS5) or its in-memory equivalent and is
// always passed as a bound parameter — never string-concatenated into SQL.
type Query struct {
	// NodeKind, when non-empty, restricts results to nodes of this kind.
	NodeKind string
	// EdgeKind, when non-empty, restricts results to edges of this kind.
	EdgeKind string
	// Text, when non-empty, full-text searches the searchable text fields
	// (node name/qualified name and edge reason).
	Text string
}

// Graphstore is graphi's pluggable durable graph backend. Implementations must
// be safe for concurrent use. The contract test suite in this package exercises
// every implementation identically via a backend factory, so any conforming
// backend behaves indistinguishably at the contract boundary.
//
// Determinism contract: every listing/search method returns results in a defined
// canonical order (nodes by NodeId ascending, edges by EdgeId ascending) so that
// results are byte-for-byte comparable across cache hits, cache rebuilds, and
// fresh stores.
type Graphstore interface {
	// PutNode durably stores (or replaces) a node. It commits to the durable
	// layer before updating any in-memory cache.
	PutNode(ctx context.Context, n model.Node) error

	// PutEdge durably stores (or replaces) an edge, preserving its full
	// provenance (confidence_tier, reason, evidence) verbatim. Both endpoints
	// must already exist (ErrUnknownEdgeEndpoint otherwise). It commits to the
	// durable layer before updating any in-memory cache.
	PutEdge(ctx context.Context, e model.Edge) error

	// DeleteNode durably removes the node with the given ID and EVERY edge
	// incident to it (as From or To endpoint), so the graph can never be left with
	// a dangling edge referencing a deleted node — the same referential invariant
	// PutEdge enforces on the way in. Deleting a node that does not exist is a
	// no-op (not an error), making the operation idempotent. Like every write it
	// commits to the durable layer FIRST, then updates the in-memory cache, so an
	// interrupted delete leaves the durable store authoritative and the cache
	// merely invalidated (crash-safe, mirroring PutNode).
	//
	// DeleteNode is the destructive counterpart to PutNode introduced for SW-036:
	// rename/move/signature-change mint a NEW NodeId (NodeId is content-addressed
	// over Kind+QualifiedName+SourcePath) while the old node survives, so the
	// incremental re-index MUST delete the old node or it is orphaned and the
	// byte-identical-to-full-re-index invariant cannot hold.
	DeleteNode(ctx context.Context, id model.NodeId) error

	// DeleteEdge durably removes the edge with the given ID. Deleting an edge that
	// does not exist is a no-op (not an error). It commits to the durable layer
	// before updating any in-memory cache (crash-safe, mirroring PutEdge).
	DeleteEdge(ctx context.Context, id model.EdgeId) error

	// GetNode returns the node with the given ID, or ErrNotFound.
	GetNode(ctx context.Context, id model.NodeId) (model.Node, error)

	// GetEdge returns the edge with the given ID (provenance intact), or
	// ErrNotFound.
	GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error)

	// Nodes returns all nodes matching q, in canonical NodeId order.
	Nodes(ctx context.Context, q Query) ([]model.Node, error)

	// Edges returns all edges matching q, in canonical EdgeId order, with
	// provenance preserved exactly.
	Edges(ctx context.Context, q Query) ([]model.Edge, error)

	// SearchNodes returns nodes matching the full-text query, ranked by the
	// backend's full-text engine and tie-broken deterministically. A limit of
	// zero or negative means unlimited. An empty query returns no results (not
	// an error).
	SearchNodes(ctx context.Context, text string, limit int) ([]RankedNode, error)

	// Snapshot serializes the durable state to the given path using the portable,
	// versioned, deterministic format defined by this package (NOT a raw .db
	// copy). It is written atomically (temp + rename). Two snapshots of the same
	// logical state are byte-identical.
	Snapshot(ctx context.Context, path string) error

	// Load restores a snapshot written by Snapshot into this store. Load is
	// fail-closed and atomic: on any error (unknown/incompatible version,
	// malformed content, path-escape, validation failure) the store is left
	// unmodified. On success the store's contents are exactly the snapshot's
	// nodes/edges/metadata, and the full-text index is re-derived (not trusted
	// from the file).
	Load(ctx context.Context, path string) error

	// EvictCache drops the in-memory hot cache. Subsequent reads transparently
	// rebuild it from the durable layer and return identical results. For backends
	// without a cache this is a no-op. It never loses data.
	EvictCache(ctx context.Context) error

	// Close releases all resources. Subsequent operations return ErrClosed.
	Close() error
}

// RankedNode pairs a node with its full-text search rank. Lower rank values
// indicate better matches (SQLite FTS5 bm25 returns negative values for better
// matches; callers should sort by rank ascending).
type RankedNode struct {
	Node model.Node
	Rank float64
}

// Factory constructs a fresh, empty Graphstore. The contract test suite is
// parameterized by Factory so one suite runs unchanged against every backend.
// dir is a writable temporary directory the backend may use for any on-disk
// state.
type Factory func(dir string) (Graphstore, error)

// Writer is the mutation subset of Graphstore. Both a Graphstore and a Batch
// satisfy it, so write-heavy passes (engine/ingest) can run unchanged over
// either the one-transaction-per-call methods or a batched session.
type Writer interface {
	PutNode(ctx context.Context, n model.Node) error
	PutEdge(ctx context.Context, e model.Edge) error
	DeleteNode(ctx context.Context, id model.NodeId) error
	DeleteEdge(ctx context.Context, id model.EdgeId) error
}

// Batch is a single-writer batched write session: many puts/deletes amortized
// into ONE durable transaction with prepared statements. Writes become durable
// only at Commit; Rollback discards them. Reads on the parent store do NOT see
// uncommitted batch writes. Endpoint checking (ErrUnknownEdgeEndpoint) is
// preserved exactly, evaluated against committed state ∪ batch-local puts.
// A Batch is NOT safe for concurrent use; it holds the store's single-writer
// discipline for its lifetime, so sessions must be short-lived (one per ingest
// phase) and MUST end in exactly one Commit or Rollback — a non-batch write
// issued while a batch is open blocks until the batch ends.
type Batch interface {
	Writer
	// Commit makes every buffered write durable atomically and ends the session.
	Commit(ctx context.Context) error
	// Rollback discards the session. Calling it after Commit is a no-op, so
	// `defer b.Rollback()` is safe.
	Rollback() error
}

// Batcher is an optional capability interface: a store that natively supports
// batched write sessions.
type Batcher interface {
	BeginBatch(ctx context.Context) (Batch, error)
}

// BeginBatch returns s's native batch when supported, else a pass-through
// fallback that forwards each call to the store's one-transaction-per-call
// methods (Commit and Rollback are then no-ops). Callers get batching where
// the backend can provide it without excluding backends that cannot.
func BeginBatch(ctx context.Context, s Graphstore) (Batch, error) {
	if b, ok := s.(Batcher); ok {
		return b.BeginBatch(ctx)
	}
	return passthroughBatch{s: s}, nil
}

// passthroughBatch adapts a non-Batcher store to the Batch shape. Every write
// is immediately durable via the store's own methods; Commit/Rollback are
// no-ops (there is nothing buffered to end).
type passthroughBatch struct{ s Graphstore }

func (p passthroughBatch) PutNode(ctx context.Context, n model.Node) error {
	return p.s.PutNode(ctx, n)
}
func (p passthroughBatch) PutEdge(ctx context.Context, e model.Edge) error {
	return p.s.PutEdge(ctx, e)
}
func (p passthroughBatch) DeleteNode(ctx context.Context, id model.NodeId) error {
	return p.s.DeleteNode(ctx, id)
}
func (p passthroughBatch) DeleteEdge(ctx context.Context, id model.EdgeId) error {
	return p.s.DeleteEdge(ctx, id)
}
func (p passthroughBatch) Commit(context.Context) error { return nil }
func (p passthroughBatch) Rollback() error              { return nil }
