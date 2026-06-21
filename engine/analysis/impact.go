package analysis

import (
	"context"
	"errors"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// DefaultMaxNodes is the documented output budget for impact reachability. A
// request with MaxNodes <= 0 uses this cap. The traversal itself is
// cycle-guarded and terminates on any finite graph regardless of this cap; the
// cap bounds the RESULT (and thus the token cost) so a high-fan-out seed yields
// a bounded, ranked set rather than an unbounded explosion. When the reachable
// set exceeds the cap, the result is truncated to the top-ranked nodes and
// Analysis.Truncated is set.
const DefaultMaxNodes = 1024

// dependencyKinds are the edge kinds traversed for impact when Params.Kinds is
// empty. They are exactly the canonical relationship vocabulary fixed by
// engine/query (calls, references, defines), so "dependency" means the same
// thing across the structural query layer and the analysis layer.
var dependencyKinds = []string{
	string(query.EdgeKindCalls),
	string(query.EdgeKindReferences),
	string(query.EdgeKindDefines),
}

// impactAnalyzer computes forward (downstream dependents / blast radius) and
// reverse (upstream dependencies) transitive reachability over the read-only
// graph. It is the reference analyzer that established the engine/analysis
// package and registry (SW-022); sibling EP-004 analyzers (call-chain SW-023,
// concept SW-024, metrics SW-025) are registered against the same contract.
type impactAnalyzer struct{}

func (impactAnalyzer) Name() string { return "impact" }

// Analyze resolves the seed and runs a cycle-guarded bounded traversal in the
// requested direction, attaching the best-tier reaching edge to every reached
// node, then materializes-sorts-truncates for a deterministic, ranked result.
func (a impactAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "impact"

	// Resolve the seed. A missing symbol is an explicit not-found result, NEVER
	// an error (parity with query.Service.resolve).
	if _, err := r.GetNode(ctx, p.Symbol); err != nil {
		if errors.Is(err, graphstore.ErrNotFound) {
			return notFound(op, p.Symbol), nil
		}
		return Analysis{}, err
	}

	direction := p.Direction
	if direction == "" {
		direction = Forward
	}

	max := p.MaxNodes
	if max <= 0 {
		max = DefaultMaxNodes
	}

	kinds := p.Kinds
	if len(kinds) == 0 {
		kinds = dependencyKinds
	}

	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Analysis{}, err
	}
	want := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		want[k] = struct{}{}
	}

	// Build directed adjacency once. For each kept edge e (From -> To):
	//   Forward (dependents / blast radius)   : a node's neighbors are the FROM
	//     endpoints of its INCOMING edges (everything pointing AT it).
	//   Reverse (dependencies)                : a node's neighbors are the TO
	//     endpoints of its OUTGOING edges (everything it points at).
	type nbr struct {
		id  model.NodeId
		via query.ResultEdge
	}
	adj := make(map[model.NodeId][]nbr)
	for _, e := range edges {
		if _, ok := want[e.Kind()]; !ok {
			continue
		}
		re := edgeToResult(e)
		switch direction {
		case Reverse:
			adj[e.From()] = append(adj[e.From()], nbr{id: e.To(), via: re})
		default: // Forward
			adj[e.To()] = append(adj[e.To()], nbr{id: e.From(), via: re})
		}
	}

	// BFS over the adjacency. Cycle guard: expand each node at most once. The
	// seed is excluded from its own result (a node is neither its own dependent
	// nor its own dependency). The graph is finite, so this always terminates;
	// the MaxNodes cap is applied to the OUTPUT after sorting.
	reached := make(map[model.NodeId]ReachedNode)
	depth := map[model.NodeId]int{p.Symbol: 0}
	expanded := map[model.NodeId]struct{}{}
	frontier := []model.NodeId{p.Symbol}

	for len(frontier) > 0 {
		var next []model.NodeId
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			for _, nb := range adj[cur] {
				if nb.id == p.Symbol {
					continue
				}
				if existing, seen := reached[nb.id]; seen {
					// Already reached: keep the best-tier reaching edge
					// (deterministic tie-break by edge id) so the most
					// trustworthy reason leads and the choice is stable.
					if edgeBetter(nb.via, existing.ReachedVia) {
						existing.ReachedVia = nb.via
						reached[nb.id] = existing
					}
					continue
				}
				n, err := r.GetNode(ctx, nb.id)
				if err != nil {
					if errors.Is(err, graphstore.ErrNotFound) {
						continue // referential drift: endpoint no longer exists
					}
					return Analysis{}, err
				}
				reached[nb.id] = ReachedNode{
					Node:       nodeToResult(n),
					ReachedVia: nb.via,
					Depth:      depth[cur] + 1,
				}
				depth[nb.id] = depth[cur] + 1
				next = append(next, nb.id)
			}
		}
		frontier = next
	}

	outcome := query.OutcomeFound
	if len(reached) == 0 {
		outcome = query.OutcomeEmpty
	}

	nodes := make([]ReachedNode, 0, len(reached))
	for _, rn := range reached {
		nodes = append(nodes, rn)
	}
	sortReached(nodes)

	truncated := false
	if len(nodes) > max {
		nodes = nodes[:max]
		truncated = true
	}

	return Analysis{
		Analyzer:  op,
		Outcome:   outcome,
		Symbol:    p.Symbol,
		Truncated: truncated,
		Nodes:     nodes,
	}, nil
}
