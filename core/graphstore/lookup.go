package graphstore

// This file defines the CORE-01 selective read ports specified by ADR 0003
// (docs/adr/0003-selective-read-contract.md, SW-114/SP-11; master plan §4
// "Core Read Ports"). They exist so the stable hotpaths can ask
// degree-shaped questions (who touches THIS node?) without graph-shaped scans
// (load every edge of a kind and filter in Go) — the measured problem the
// TEST-01 baselines pin.
//
// Both reference backends implement both ports with identical semantics,
// enforced by the shared conformance suite in lookup_contract_test.go
// (G2: Memory and SQLite are deterministically interchangeable).

import (
	"context"

	"github.com/samibel/graphi/core/model"
)

// GraphLookup is the endpoint-selective graph read port (ADR 0003 D1). Cost of
// Incoming/Outgoing must be proportional to the node's degree, never to the
// size of an edge class or the whole graph. Canonical ordering is part of the
// contract, not an implementation courtesy: edges ascend by EdgeId, nodes by
// NodeId, so results are byte-comparable across backends and cache states.
type GraphLookup interface {
	// GetNode returns the node with the given ID, or ErrNotFound.
	GetNode(ctx context.Context, id model.NodeId) (model.Node, error)

	// NodesByID returns the nodes found for the given ids in canonical NodeId
	// order. Missing ids are silently skipped and duplicate ids collapse to one
	// result (set semantics) — callers that need strict presence use GetNode.
	// It is the anti-N+1 companion to Incoming/Outgoing for endpoint hydration.
	NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error)

	// Incoming returns every edge whose To endpoint equals id — optionally
	// restricted to the given kinds (zero kinds = all kinds) — with provenance
	// intact, in canonical EdgeId order. An unknown id yields an empty result,
	// not an error (matching "no edges" — presence is GetNode's job).
	Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error)

	// Outgoing is Incoming's mirror for the From endpoint.
	Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error)
}

// BoundedGraphLookup is the incident-read port for work-budgeted traversals.
// It is deliberately separate from GraphLookup so consumers that genuinely
// need every incident edge retain that contract while bounded consumers cannot
// accidentally materialize a high-degree adjacency list before applying their
// own cap.
//
// Both methods return at most limit edges. Explicitly kind-filtered results use
// canonical EdgeId order; zero kinds means all kinds and uses canonical
// (EdgeKind, EdgeId) order so the two production indexes can stop before a
// high-degree sort. truncated is true exactly when more matching edges exist
// than were returned. Kinds have set semantics (duplicates do not change the
// result). limit must be positive or ErrInvalidLimit is returned. Backend work
// is degree-independent: unfiltered reads inspect at most limit+1 edge rows;
// filtered reads inspect at most limit+1 rows per distinct requested kind.
type BoundedGraphLookup interface {
	IncomingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) (edges []model.Edge, truncated bool, err error)
	OutgoingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) (edges []model.Edge, truncated bool, err error)
}

// SymbolLookupPort is the selective symbol resolution port (ADR 0003 D2): the
// exact-equality lookups resolveExact performs in Go over a full node scan
// today, plus the already-selective ranked lexical search. Callers pass
// model.NormalizePath-normalized paths to SourcePath (normalization stays in
// the caller, as today).
//
// ExactName (bare final-segment name) is specified by the master plan but
// deliberately NOT part of this v1 interface: no stable hotpath consumes a
// bare-name equality today and model.Node carries no bare-name field, so its
// derivation rule would be invented without a consumer. It is added when
// CORE-02's migration surfaces one (ADR 0003 U1).
type SymbolLookupPort interface {
	// QualifiedName returns every node whose qualified name equals qn exactly,
	// in canonical NodeId order. No match yields an empty result, not an error.
	QualifiedName(ctx context.Context, qn string) ([]model.Node, error)

	// SourcePath returns every node whose (normalized) source path equals path
	// exactly, in canonical NodeId order. No match yields an empty result.
	SourcePath(ctx context.Context, path string) ([]model.Node, error)

	// Search is the ranked lexical search — Graphstore.SearchNodes's contract
	// verbatim, exposed on the port so stable consumers depend on one read
	// surface. An empty query returns no results; limit<=0 means unlimited.
	Search(ctx context.Context, text string, limit int) ([]RankedNode, error)
}

// BriefFileStats is the compact per-file aggregate consumed by agent_brief.
// SymbolCount counts graph nodes with this source path; EdgeEndpoints counts
// incident edge endpoints (a same-file edge contributes two, matching the
// original digest semantics).
type BriefFileStats struct {
	Path          string
	SymbolCount   int
	EdgeEndpoints int
}

// BriefSymbolStats is one symbol ranked by inbound edge count.
type BriefSymbolStats struct {
	Node         model.Node
	InboundEdges int
}

// BriefStats is a bounded aggregate view of a graph. Files is compact by file
// count rather than node/edge count; TopInbound is capped by the caller.
type BriefStats struct {
	TotalNodes int
	TotalEdges int
	TierCounts map[model.ConfidenceTier]int
	Files      []BriefFileStats
	TopInbound []BriefSymbolStats
}

// BriefAggregatePort serves agent_brief's genuinely aggregate query without
// materializing the whole graph or issuing one lookup per node. Implementations
// must return Files in path order and TopInbound by degree descending then
// NodeId ascending. topSymbols <= 0 returns no TopInbound rows.
type BriefAggregatePort interface {
	BriefStats(ctx context.Context, topSymbols int) (BriefStats, error)
}

// DegreeSamplePort returns a deterministic, degree-stratified function/method
// sample for real-repository evaluation. Candidates are ranked by incident
// degree descending then NodeId, divided into maxSymbols quantile buckets, and
// the first row from each bucket is returned. This prevents latency evidence
// from measuring only the first low-entropy NodeId prefix.
type DegreeSamplePort interface {
	DegreeStratifiedSymbols(ctx context.Context, maxSymbols int) ([]model.Node, error)
}

// Compile-time proof both reference backends implement both ports.
var (
	_ GraphLookup        = (*MemStore)(nil)
	_ GraphLookup        = (*SQLiteStore)(nil)
	_ BoundedGraphLookup = (*MemStore)(nil)
	_ BoundedGraphLookup = (*SQLiteStore)(nil)
	_ SymbolLookupPort   = (*MemStore)(nil)
	_ SymbolLookupPort   = (*SQLiteStore)(nil)
	_ BriefAggregatePort = (*MemStore)(nil)
	_ BriefAggregatePort = (*SQLiteStore)(nil)
	_ DegreeSamplePort   = (*MemStore)(nil)
	_ DegreeSamplePort   = (*SQLiteStore)(nil)
)
