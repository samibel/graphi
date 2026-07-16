package query

import (
	"context"
	"errors"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// Edge-kind conventions consumed by the structural queries. core/model leaves
// EdgeKind an open string; the query layer fixes the canonical relationship
// vocabulary it navigates so every surface agrees on what "callers" means.
const (
	// EdgeKindCalls — a "X calls Y" relationship (From=caller, To=callee).
	EdgeKindCalls model.EdgeKind = "calls"
	// EdgeKindReferences — a non-call reference to a symbol (From=referrer, To=referent).
	EdgeKindReferences model.EdgeKind = "references"
	// EdgeKindDefines — a "X defines/declares Y" relationship (From=definer, To=defined).
	EdgeKindDefines model.EdgeKind = "defines"

	// EdgeKindImplements — a "X implements/embeds interface Y" relationship
	// (From=implementer, To=interface). Populated at ingest for the class-hierarchy
	// model (epic EP-011, gap G2).
	EdgeKindImplements model.EdgeKind = "implements"
	// EdgeKindInherits — a "X embeds/extends concrete type Y" relationship
	// (From=embedder, To=embedded). Populated at ingest (EP-011 G2).
	EdgeKindInherits model.EdgeKind = "inherits"
	// EdgeKindOverrides — method M on type X overrides method M declared on a
	// supertype (From=overriding method, To=overridden method). Derived from the
	// implements/inherits graph at ingest (EP-011 G2).
	EdgeKindOverrides model.EdgeKind = "overrides"
)

// MaxNeighborhoodDepth is the single documented upper bound on neighborhood
// traversal depth, enforced in this shared service. A neighborhood request with
// a depth greater than this constant is CLAMPED down to MaxNeighborhoodDepth
// (never rejected); the effective depth is reported back in Result.Depth. The
// bound guarantees traversal always terminates and keeps result size and token
// cost predictable for AI-agent callers.
const MaxNeighborhoodDepth = 5

// kindPackage is the interned package-node kind (WP-01), excluded from
// neighborhood traversal; mirrors core/parse.KindPackage without importing parse.
const kindPackage = "package"

// kindExternal is the interned external-symbol node kind (WP-03), excluded from
// every structural query surface. Unlike package nodes (which only sit on
// `imports` edges, so callers/callees/references were already clean), external
// nodes sit on `calls`/`references` edges and so WOULD leak into structural
// results — they are heuristic linker artifacts (stdlib / 3rd-party targets with
// empty source path), not navigable symbols. The taint analyzer reads nodes
// directly (not via this service), so filtering here does not hide them from
// taint. Mirrors core/parse.KindExternal without importing parse.
const kindExternal = "external"

// ErrSelectiveLookupUnavailable is returned by the structural hotpaths when the
// backing Reader does not provide the CORE-02 selective read port required by
// that operation (GraphLookup; bounded traversals additionally require
// BoundedGraphLookup). Stable operations refuse to fall back to whole-graph
// scans — a silent fallback would resurrect exactly the full-scan plan the
// SW-110 baselines pinned and ADR 0003 retired. Both shipped backends implement
// both ports.
var ErrSelectiveLookupUnavailable = errors.New("query: reader lacks a required selective graph lookup port; stable hotpaths do not fall back to full scans")

// Service is graphi's single shared, read-only structural query service. It is
// the one place callers/callees/references/definition/neighborhood logic lives;
// surfaces hold no query logic of their own. It is safe for concurrent use when
// the underlying Reader is (the shipped backends are).
type Service struct {
	reader Reader
	// lookup is the CORE-02 selective read port (ADR 0003): every structural
	// hotpath reads through it, so cost scales with the symbol's degree instead
	// of an edge class or the whole graph. nil when the Reader does not provide
	// it — the hotpaths then fail with ErrSelectiveLookupUnavailable.
	lookup graphstore.GraphLookup
}

// New constructs a Service over the given read-only Reader. graphstore.Graphstore
// satisfies Reader, so a Service can be built directly from any backend while
// remaining mutation-free by construction. Backends additionally providing
// graphstore.GraphLookup (both shipped backends do) serve the structural
// operations selectively (CORE-02).
func New(reader Reader) *Service {
	lookup, _ := reader.(graphstore.GraphLookup)
	return &Service{reader: reader, lookup: lookup}
}

// Reader returns the read-only Reader the Service traverses. It lets sibling
// engine packages (e.g. engine/query/compound) reuse the SAME backing store
// without re-deriving it, and without query.Service importing those packages
// (which would create an import cycle, since compound imports query).
func (s *Service) Reader() Reader { return s.reader }

// resolve looks up the symbol node. It returns (node, true, nil) when the symbol
// exists, (_, false, nil) when it is genuinely absent (so callers can return an
// explicit NotFound result — NOT an error), and a non-nil error only for real
// infrastructure failures (closed store, cancelled context, …). It reads through
// the selective port (a bounded point read) rather than Reader.GetNode, whose
// SQLite implementation materializes the whole-graph hot cache.
func (s *Service) resolve(ctx context.Context, id model.NodeId) (model.Node, bool, error) {
	if s.lookup == nil {
		return model.Node{}, false, ErrSelectiveLookupUnavailable
	}
	ns, err := s.lookup.NodesByID(ctx, []model.NodeId{id})
	if err != nil {
		return model.Node{}, false, err
	}
	if len(ns) == 0 {
		return model.Node{}, false, nil
	}
	return ns[0], true, nil
}

// notFound builds the explicit, typed not-found result for an unresolved symbol.
func notFound(operation string, id model.NodeId) Result {
	return Result{
		Operation: operation,
		Symbol:    id,
		Outcome:   OutcomeNotFound,
		Nodes:     []ResultNode{},
		Edges:     []ResultEdge{},
	}
}

// finalize materializes-then-sorts the collected nodes/edges with the canonical
// comparator and stamps the resolution outcome (Found vs Empty). It is the only
// exit path for a resolved query, so ordering and outcome semantics are uniform.
func finalize(operation string, id model.NodeId, depth *int, nodes []ResultNode, edges []ResultEdge) Result {
	if nodes == nil {
		nodes = []ResultNode{}
	}
	if edges == nil {
		edges = []ResultEdge{}
	}
	sortNodes(nodes)
	sortEdges(edges)
	outcome := OutcomeFound
	if len(nodes) == 0 && len(edges) == 0 {
		outcome = OutcomeEmpty
	}
	return Result{
		Operation: operation,
		Symbol:    id,
		Outcome:   outcome,
		Depth:     depth,
		Nodes:     nodes,
		Edges:     edges,
	}
}

// directedLookup is the shared body of callers/callees/references/definition: it
// collects edges of edgeKind incident to the symbol on the requested side and
// attaches the matching opposite-endpoint nodes with the edge provenance intact.
//
// inbound=true  → edges whose To == symbol (e.g. callers, edges INTO the symbol).
// inbound=false → edges whose From == symbol (e.g. callees, edges OUT of the symbol).
func (s *Service) directedLookup(ctx context.Context, operation string, id model.NodeId, edgeKind model.EdgeKind, inbound bool) (Result, error) {
	_, ok, err := s.resolve(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return notFound(operation, id), nil
	}

	// CORE-02 (ADR 0003 D7): endpoint-selective read — only the edges incident
	// to the symbol on the requested side are fetched, never the whole kind.
	var edges []model.Edge
	if inbound {
		edges, err = s.lookup.Incoming(ctx, id, edgeKind)
	} else {
		edges, err = s.lookup.Outgoing(ctx, id, edgeKind)
	}
	if err != nil {
		return Result{}, err
	}

	type edgeEndpoint struct {
		edge     ResultEdge
		endpoint model.NodeId
	}
	var (
		pairs   []edgeEndpoint
		nodeIDs = map[model.NodeId]struct{}{}
	)
	for _, e := range edges {
		endpoint := e.From()
		if !inbound {
			endpoint = e.To()
		}
		pairs = append(pairs, edgeEndpoint{edge: edgeToResult(e), endpoint: endpoint})
		nodeIDs[endpoint] = struct{}{}
	}

	resNodes, err := s.collectNodes(ctx, nodeIDs)
	if err != nil {
		return Result{}, err
	}
	// WP-03 query hygiene: interned external nodes are heuristic linker artifacts
	// on calls/references edges, not navigable symbols. Drop them AND the edges
	// that reach only them so a caller never sees a dangling endpoint.
	external := externalIDs(resNodes)
	resNodes = dropExternalNodes(resNodes, external)
	resEdges := make([]ResultEdge, 0, len(pairs))
	for _, p := range pairs {
		if _, isExt := external[p.endpoint]; isExt {
			continue
		}
		resEdges = append(resEdges, p.edge)
	}
	return finalize(operation, id, nil, resNodes, resEdges), nil
}

// externalIDs returns the set of node ids in the slice whose kind is the interned
// external kind (WP-03).
func externalIDs(nodes []ResultNode) map[model.NodeId]struct{} {
	out := map[model.NodeId]struct{}{}
	for _, n := range nodes {
		if n.Kind == kindExternal {
			out[n.ID] = struct{}{}
		}
	}
	return out
}

// dropExternalNodes returns nodes with the external-kind entries removed.
func dropExternalNodes(nodes []ResultNode, external map[model.NodeId]struct{}) []ResultNode {
	if len(external) == 0 {
		return nodes
	}
	out := nodes[:0]
	for _, n := range nodes {
		if _, isExt := external[n.ID]; isExt {
			continue
		}
		out = append(out, n)
	}
	return out
}

// collectNodes fetches the given node ids in ONE batched selective read
// (NodesByID skips ids that no longer exist — referential drift) and returns
// them as result nodes. Order is irrelevant here; the caller sorts canonically.
func (s *Service) collectNodes(ctx context.Context, ids map[model.NodeId]struct{}) ([]ResultNode, error) {
	if len(ids) == 0 {
		return []ResultNode{}, nil
	}
	list := make([]model.NodeId, 0, len(ids))
	for nid := range ids {
		list = append(list, nid)
	}
	nodes, err := s.lookup.NodesByID(ctx, list)
	if err != nil {
		return nil, err
	}
	out := make([]ResultNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeToResult(n))
	}
	return out, nil
}

// Callers returns the symbols that CALL symbolID (inbound "calls" edges), each
// with the calling edge's provenance attached. An unresolved symbol yields an
// explicit not-found result; a resolved symbol with no callers yields an empty
// result. Read-only.
func (s *Service) Callers(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, "callers", symbolID, EdgeKindCalls, true)
}

// Callees returns the symbols that symbolID CALLS (outbound "calls" edges) with
// provenance attached. Read-only.
func (s *Service) Callees(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, "callees", symbolID, EdgeKindCalls, false)
}

// References returns the symbols that REFERENCE symbolID (inbound "references"
// edges) with provenance attached. Read-only.
func (s *Service) References(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, "references", symbolID, EdgeKindReferences, true)
}

// Definition returns the node(s) that define symbolID: the sources of inbound
// "defines" edges, with provenance attached. Ingest emits defines as
// definer/container -> defined symbol (for top-level declarations, file ->
// symbol), so definition must traverse the edge in reverse. Read-only.
func (s *Service) Definition(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, "definition", symbolID, EdgeKindDefines, true)
}

// Implementers returns the types that IMPLEMENT/EMBED symbolID (inbound
// "implements" edges), with provenance attached. Answers "who-implements" over
// the EP-011 G2 hierarchy graph. An unresolved symbol yields an explicit
// not-found result. Read-only.
func (s *Service) Implementers(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, OpImplementers, symbolID, EdgeKindImplements, true)
}

// Implements returns the interfaces/types that symbolID IMPLEMENTS (outbound
// "implements" edges), with provenance attached. Read-only.
func (s *Service) Implements(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, OpImplements, symbolID, EdgeKindImplements, false)
}

// Overrides returns the methods that OVERRIDE symbolID (inbound "overrides"
// edges), with provenance attached. Answers "what-overrides". Read-only.
func (s *Service) Overrides(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, OpOverrides, symbolID, EdgeKindOverrides, true)
}

// Subtypes returns the subtypes of symbolID: nodes with an inbound "inherits" OR
// "implements" edge to symbolID (the class-hierarchy subtype relation composed
// from both edge kinds), with provenance attached. Read-only.
func (s *Service) Subtypes(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.multiKindLookup(ctx, OpSubtypes, symbolID, []model.EdgeKind{EdgeKindInherits, EdgeKindImplements}, true)
}

// Supertypes returns the supertypes of symbolID: nodes reachable via outbound
// "inherits" OR "implements" edges (the composed supertype relation), with
// provenance attached. Read-only.
func (s *Service) Supertypes(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.multiKindLookup(ctx, OpSupertypes, symbolID, []model.EdgeKind{EdgeKindInherits, EdgeKindImplements}, false)
}

// multiKindLookup is the multi-edge-kind generalization of directedLookup: it
// collects edges of ANY of the given kinds incident to the symbol on the
// requested side and attaches the matching opposite-endpoint nodes with provenance
// intact. Used by Subtypes/Supertypes to compose the inherits+implements
// relation. Ordering and outcome semantics are identical to directedLookup via
// the shared finalize() comparator.
func (s *Service) multiKindLookup(ctx context.Context, operation string, id model.NodeId, kinds []model.EdgeKind, inbound bool) (Result, error) {
	_, ok, err := s.resolve(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return notFound(operation, id), nil
	}
	// CORE-02 (ADR 0003 D7): the multi-kind union is one endpoint-selective read.
	var edges []model.Edge
	if inbound {
		edges, err = s.lookup.Incoming(ctx, id, kinds...)
	} else {
		edges, err = s.lookup.Outgoing(ctx, id, kinds...)
	}
	if err != nil {
		return Result{}, err
	}
	type edgeEndpoint struct {
		edge     ResultEdge
		endpoint model.NodeId
	}
	var (
		pairs   []edgeEndpoint
		nodeIDs = map[model.NodeId]struct{}{}
	)
	for _, e := range edges {
		endpoint := e.From()
		if !inbound {
			endpoint = e.To()
		}
		pairs = append(pairs, edgeEndpoint{edge: edgeToResult(e), endpoint: endpoint})
		nodeIDs[endpoint] = struct{}{}
	}
	resNodes, err := s.collectNodes(ctx, nodeIDs)
	if err != nil {
		return Result{}, err
	}
	// WP-03 query hygiene: drop interned external endpoints (and their edges).
	external := externalIDs(resNodes)
	resNodes = dropExternalNodes(resNodes, external)
	resEdges := make([]ResultEdge, 0, len(pairs))
	for _, p := range pairs {
		if _, isExt := external[p.endpoint]; isExt {
			continue
		}
		resEdges = append(resEdges, p.edge)
	}
	return finalize(operation, id, nil, resNodes, resEdges), nil
}

// Neighborhood returns every node and edge reachable within depthN undirected
// hops of symbolID. depthN is clamped to [0, MaxNeighborhoodDepth]; the
// effective (clamped) depth is reported in Result.Depth. Traversal is
// cycle-guarded (each node expanded at most once) so it always terminates, and
// the result is materialized-then-sorted by the canonical comparator. An
// unresolved symbol yields an explicit not-found result. Read-only.
func (s *Service) Neighborhood(ctx context.Context, symbolID model.NodeId, depthN int) (Result, error) {
	const op = "neighborhood"

	seed, ok, err := s.resolve(ctx, symbolID)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return notFound(op, symbolID), nil
	}

	depth := depthN
	if depth < 0 {
		depth = 0
	}
	if depth > MaxNeighborhoodDepth {
		depth = MaxNeighborhoodDepth // documented clamp
	}

	// CORE-02 (ADR 0003 D7): per-hop endpoint-selective expansion. Each frontier
	// node contributes exactly its incident edges (Incoming ∪ Outgoing, all
	// kinds); unseen opposite endpoints are hydrated in ONE NodesByID batch per
	// hop. Cost scales with the visited component's degree sum, never with the
	// whole edge set.
	visitedNodes := map[model.NodeId]model.Node{seed.ID(): seed}
	collectedEdges := map[model.EdgeId]model.Edge{}

	frontier := []model.NodeId{symbolID}
	expanded := map[model.NodeId]struct{}{} // cycle guard: expand each node once

	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		type candidate struct {
			edge  model.Edge
			other model.NodeId
		}
		var cands []candidate
		unseen := map[model.NodeId]struct{}{}
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			incoming, err := s.lookup.Incoming(ctx, cur)
			if err != nil {
				return Result{}, err
			}
			outgoing, err := s.lookup.Outgoing(ctx, cur)
			if err != nil {
				return Result{}, err
			}
			for _, e := range append(incoming, outgoing...) {
				other := e.From()
				if other == cur {
					other = e.To()
				}
				if _, seen := visitedNodes[other]; !seen {
					unseen[other] = struct{}{}
				}
				cands = append(cands, candidate{edge: e, other: other})
			}
		}

		// Hydrate every unseen endpoint once; ids NodesByID skips no longer exist
		// (referential drift) and their edges are dropped below, matching the old
		// per-edge GetNode/ErrNotFound behavior.
		fetched := map[model.NodeId]model.Node{}
		if len(unseen) > 0 {
			ids := make([]model.NodeId, 0, len(unseen))
			for id := range unseen {
				ids = append(ids, id)
			}
			ns, err := s.lookup.NodesByID(ctx, ids)
			if err != nil {
				return Result{}, err
			}
			for _, n := range ns {
				fetched[n.ID()] = n
			}
		}

		var next []model.NodeId
		for _, c := range cands {
			otherNode, seen := visitedNodes[c.other]
			if !seen {
				n, ok := fetched[c.other]
				if !ok {
					continue // referential drift: endpoint no longer exists
				}
				otherNode = n
			}
			// WP-01 query hygiene: interned `package` nodes are structural
			// linking artifacts, not navigable symbols. Skipping them keeps
			// them out of neighborhoods AND prevents a popular package from
			// acting as an import hub that pulls in every co-importer.
			if otherNode.Kind() == kindPackage {
				continue
			}
			// WP-03: interned external nodes are terminal linker artifacts, not
			// navigable symbols — skip so they never appear as neighbors nor are
			// traversed into.
			if otherNode.Kind() == kindExternal {
				continue
			}
			collectedEdges[c.edge.ID()] = c.edge
			if !seen {
				visitedNodes[c.other] = otherNode
				next = append(next, c.other)
			}
		}
		frontier = next
	}

	resNodes := make([]ResultNode, 0, len(visitedNodes))
	for _, n := range visitedNodes {
		resNodes = append(resNodes, nodeToResult(n))
	}
	resEdges := make([]ResultEdge, 0, len(collectedEdges))
	for _, e := range collectedEdges {
		resEdges = append(resEdges, edgeToResult(e))
	}

	eff := depth
	return finalize(op, symbolID, &eff, resNodes, resEdges), nil
}
