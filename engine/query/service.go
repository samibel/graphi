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

// Service is graphi's single shared, read-only structural query service. It is
// the one place callers/callees/references/definition/neighborhood logic lives;
// surfaces hold no query logic of their own. It is safe for concurrent use when
// the underlying Reader is (the shipped backends are).
type Service struct {
	reader Reader
}

// New constructs a Service over the given read-only Reader. graphstore.Graphstore
// satisfies Reader, so a Service can be built directly from any backend while
// remaining mutation-free by construction.
func New(reader Reader) *Service {
	return &Service{reader: reader}
}

// resolve looks up the symbol node. It returns (node, true, nil) when the symbol
// exists, (_, false, nil) when it is genuinely absent (so callers can return an
// explicit NotFound result — NOT an error), and a non-nil error only for real
// infrastructure failures (closed store, cancelled context, …).
func (s *Service) resolve(ctx context.Context, id model.NodeId) (model.Node, bool, error) {
	n, err := s.reader.GetNode(ctx, id)
	if err != nil {
		if errors.Is(err, graphstore.ErrNotFound) {
			return model.Node{}, false, nil
		}
		return model.Node{}, false, err
	}
	return n, true, nil
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

	edges, err := s.reader.Edges(ctx, graphstore.Query{EdgeKind: edgeKind})
	if err != nil {
		return Result{}, err
	}

	var (
		resEdges []ResultEdge
		nodeIDs  = map[model.NodeId]struct{}{}
	)
	for _, e := range edges {
		var endpoint model.NodeId
		if inbound {
			if e.To() != id {
				continue
			}
			endpoint = e.From()
		} else {
			if e.From() != id {
				continue
			}
			endpoint = e.To()
		}
		resEdges = append(resEdges, edgeToResult(e))
		nodeIDs[endpoint] = struct{}{}
	}

	resNodes, err := s.collectNodes(ctx, nodeIDs)
	if err != nil {
		return Result{}, err
	}
	return finalize(operation, id, nil, resNodes, resEdges), nil
}

// collectNodes fetches the given node ids (read-only), skipping any that no
// longer exist in the store (referential drift), and returns them as result
// nodes. Order is irrelevant here; the caller sorts canonically.
func (s *Service) collectNodes(ctx context.Context, ids map[model.NodeId]struct{}) ([]ResultNode, error) {
	out := make([]ResultNode, 0, len(ids))
	for nid := range ids {
		n, err := s.reader.GetNode(ctx, nid)
		if err != nil {
			if errors.Is(err, graphstore.ErrNotFound) {
				continue
			}
			return nil, err
		}
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

// Definition returns the definition(s) of symbolID: the targets of outbound
// "defines" edges from symbolID, with provenance attached. (A symbol points at
// what it defines.) Read-only.
func (s *Service) Definition(ctx context.Context, symbolID model.NodeId) (Result, error) {
	return s.directedLookup(ctx, "definition", symbolID, EdgeKindDefines, false)
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
	kindSet := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		kindSet[k] = struct{}{}
	}
	edges, err := s.reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Result{}, err
	}
	var (
		resEdges []ResultEdge
		nodeIDs  = map[model.NodeId]struct{}{}
	)
	for _, e := range edges {
		if _, want := kindSet[e.Kind()]; !want {
			continue
		}
		var endpoint model.NodeId
		if inbound {
			if e.To() != id {
				continue
			}
			endpoint = e.From()
		} else {
			if e.From() != id {
				continue
			}
			endpoint = e.To()
		}
		resEdges = append(resEdges, edgeToResult(e))
		nodeIDs[endpoint] = struct{}{}
	}
	resNodes, err := s.collectNodes(ctx, nodeIDs)
	if err != nil {
		return Result{}, err
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

	// Load all edges once; build adjacency over the undirected graph. Reading the
	// full edge set keeps the traversal backend-agnostic and read-only.
	allEdges, err := s.reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Result{}, err
	}

	visitedNodes := map[model.NodeId]model.Node{seed.ID(): seed}
	collectedEdges := map[model.EdgeId]model.Edge{}

	frontier := []model.NodeId{symbolID}
	expanded := map[model.NodeId]struct{}{} // cycle guard: expand each node once

	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		var next []model.NodeId
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			for _, e := range allEdges {
				var other model.NodeId
				switch cur {
				case e.From():
					other = e.To()
				case e.To():
					other = e.From()
				default:
					continue
				}
				collectedEdges[e.ID()] = e
				if _, seen := visitedNodes[other]; !seen {
					n, err := s.reader.GetNode(ctx, other)
					if err != nil {
						if errors.Is(err, graphstore.ErrNotFound) {
							continue
						}
						return Result{}, err
					}
					visitedNodes[other] = n
					next = append(next, other)
				}
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
