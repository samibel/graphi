package analysis

import (
	"context"

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

// externalNodeKind is the interned external-symbol node kind (WP-03), excluded
// from impact reachability results. Mirrors core/parse.KindExternal.
const externalNodeKind = "external"

// dependencyKinds are the edge kinds traversed for impact when Params.Kinds is
// empty: the canonical calls/references vocabulary fixed by engine/query.
// `defines` is deliberately NOT a default kind — a file "defining" a symbol is
// containment, not dependency, and including it put a file node in every
// symbol's blast radius as depth-1 noise. Pass Kinds explicitly (e.g.
// -kinds calls,references,defines) to opt back in.
var dependencyKinds = []string{
	string(query.EdgeKindCalls),
	string(query.EdgeKindReferences),
}

// impactAnalyzer computes reverse (downstream dependents / blast radius) and
// forward (upstream dependencies) transitive reachability over the read-only
// graph. It is the reference analyzer that established the engine/analysis
// package and registry (SW-022); sibling EP-004 analyzers (call-chain SW-023,
// concept SW-024, metrics SW-025) are registered against the same contract.
type impactAnalyzer struct{}

func (impactAnalyzer) Name() string { return "impact" }

// Analyze resolves the seed and runs a cycle-guarded bounded traversal in the
// requested direction, attaching the best-tier reaching edge to every reached
// node, then materializes-sorts-truncates for a deterministic, ranked result.
// CORE-02 (ADR 0003 D7): the traversal expands each frontier node through the
// endpoint-selective port, so cost scales with the reached component's degree
// sum, never with the whole edge set.
func (a impactAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "impact"

	lookup, ok := r.(graphstore.GraphLookup)
	if !ok {
		return Analysis{}, query.ErrSelectiveLookupUnavailable
	}

	// Resolve the seed. A missing symbol is an explicit not-found result, NEVER
	// an error (parity with query.Service.resolve). The point read goes through
	// the port (bounded) rather than Reader.GetNode (whole-graph cache on SQLite).
	if seed, err := lookup.NodesByID(ctx, []model.NodeId{p.Symbol}); err != nil {
		return Analysis{}, err
	} else if len(seed) == 0 {
		return notFound(op, p.Symbol), nil
	}

	// Default direction is Reverse (dependents / blast radius): "impact of X"
	// colloquially means "who is affected if X changes", and it is what the TUI
	// blast panel and the edit planner need. The engine owns this default;
	// surfaces pass the direction through verbatim (empty = this default).
	direction := p.Direction
	if direction == "" {
		direction = Reverse
	}

	max := p.MaxNodes
	if max <= 0 {
		max = DefaultMaxNodes
	}

	kinds := p.Kinds
	if len(kinds) == 0 {
		kinds = dependencyKinds
	}

	// BFS with per-node selective expansion. For each frontier node:
	//   Reverse (dependents / blast radius)   : neighbors are the FROM endpoints
	//     of its INCOMING edges (everything pointing AT it) — the
	//     reverse-dependency (rdeps) convention.
	//   Forward (dependencies)                : neighbors are the TO endpoints of
	//     its OUTGOING edges (everything it points at) — traversal ALONG edge
	//     direction.
	// Cycle guard: expand each node at most once. The seed is excluded from its
	// own result (a node is neither its own dependent nor its own dependency).
	// The graph is finite, so this always terminates; the MaxNodes cap is
	// applied to the OUTPUT after sorting.
	type nbr struct {
		id  model.NodeId
		via query.ResultEdge
	}
	reached := make(map[model.NodeId]ReachedNode)
	depth := map[model.NodeId]int{p.Symbol: 0}
	expanded := map[model.NodeId]struct{}{}
	frontier := []model.NodeId{p.Symbol}

	for len(frontier) > 0 {
		// Gather this hop's neighbor candidates via endpoint-selective reads.
		var cands []nbr
		fetchIDs := map[model.NodeId]struct{}{}
		var next []model.NodeId
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			var (
				edges []model.Edge
				err   error
			)
			if direction == Forward {
				edges, err = lookup.Outgoing(ctx, cur, kinds...)
			} else {
				edges, err = lookup.Incoming(ctx, cur, kinds...)
			}
			if err != nil {
				return Analysis{}, err
			}
			for _, e := range edges {
				id := e.From()
				if direction == Forward {
					id = e.To()
				}
				if id == p.Symbol {
					continue
				}
				cands = append(cands, nbr{id: id, via: edgeToResult(e)})
				if _, seen := reached[id]; !seen {
					fetchIDs[id] = struct{}{}
				}
				// Depth is BFS-layer semantics: every candidate discovered while
				// expanding cur sits one hop past cur.
				if _, has := depth[id]; !has {
					depth[id] = depth[cur] + 1
				}
			}
		}

		// Hydrate unseen neighbors in one batch; ids NodesByID skips no longer
		// exist (referential drift) and are dropped below, matching the old
		// per-edge GetNode/ErrNotFound behavior.
		fetched := map[model.NodeId]model.Node{}
		if len(fetchIDs) > 0 {
			ids := make([]model.NodeId, 0, len(fetchIDs))
			for id := range fetchIDs {
				ids = append(ids, id)
			}
			ns, err := lookup.NodesByID(ctx, ids)
			if err != nil {
				return Analysis{}, err
			}
			for _, n := range ns {
				fetched[n.ID()] = n
			}
		}

		for _, nb := range cands {
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
			n, ok := fetched[nb.id]
			if !ok {
				continue // referential drift: endpoint no longer exists
			}
			// WP-03 query hygiene: interned external nodes are terminal heuristic
			// linker artifacts (stdlib / 3rd-party targets), not part of the
			// dependency graph a user reasons about — exclude them from the blast
			// radius. They have no outgoing edges, so not enqueuing them also stops
			// traversal cleanly.
			if n.Kind() == externalNodeKind {
				continue
			}
			reached[nb.id] = ReachedNode{
				Node:       nodeToResult(n),
				ReachedVia: nb.via,
				Depth:      depth[nb.id],
			}
			next = append(next, nb.id)
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
