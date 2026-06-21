package analysis

import (
	"context"
	"errors"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// DefaultMaxPathsChain is the documented count bound on call-chain path
// enumeration. A request with MaxPaths <= 0 uses this cap. Path enumeration is
// exponential in the worst case, so this bound (plus DefaultMaxChainDepth)
// guarantees the result is finite and the cost predictable.
const DefaultMaxPathsChain = 10

// DefaultMaxChainDepth is the documented hop bound on a single call chain. It
// caps recursion depth so very long (or deep-cycle) graphs terminate promptly.
// Together with the path-local visited set it makes enumeration finite.
const DefaultMaxChainDepth = 8

// callchainAnalyzer reconstructs caller→callee paths between a source and a
// target symbol over the "calls" edge class. It is the second analyzer on the
// SW-022 registry; scope is path reconstruction only — it is not flow-sensitive
// (no conditions/taint — EP-005) and traverses only EdgeKindCalls.
type callchainAnalyzer struct{}

func (callchainAnalyzer) Name() string { return "call-chain" }

// Analyze resolves source and target, then runs a bounded, cycle-safe simple-path
// DFS over calls edges, recording complete paths (including cycles back to the
// source when source==target), and returns them shortest-first in a stable order.
func (a callchainAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "call-chain"

	// Resolve both endpoints. Either missing -> typed not-found (no error), and
	// no traversal is attempted.
	if _, err := r.GetNode(ctx, p.Symbol); err != nil {
		if errors.Is(err, graphstore.ErrNotFound) {
			return notFound(op, p.Symbol), nil
		}
		return Analysis{}, err
	}
	if p.Target != "" {
		if _, err := r.GetNode(ctx, p.Target); err != nil {
			if errors.Is(err, graphstore.ErrNotFound) {
				return notFound(op, p.Symbol), nil
			}
			return Analysis{}, err
		}
	}

	maxPaths := p.MaxPaths
	if maxPaths <= 0 {
		maxPaths = DefaultMaxPathsChain
	}

	// Build calls-only adjacency. Each node's neighbors are sorted by edge id so
	// DFS explores in a stable order regardless of map-iteration nondeterminism.
	// (The output is also sorted at the end; sorted adjacency makes the DFS
	// itself reproducible and bounds how many near-duplicate prefixes expand.)
	edges, err := r.Edges(ctx, graphstore.Query{EdgeKind: string(query.EdgeKindCalls)})
	if err != nil {
		return Analysis{}, err
	}
	type nbr struct {
		id  model.NodeId
		via query.ResultEdge
	}
	adj := make(map[model.NodeId][]nbr)
	for _, e := range edges {
		re := edgeToResult(e)
		adj[e.From()] = append(adj[e.From()], nbr{id: e.To(), via: re})
	}
	for k := range adj {
		ns := adj[k]
		sort.Slice(ns, func(i, j int) bool { return ns[i].via.ID < ns[j].via.ID })
		adj[k] = ns
	}

	// Target defaults to the source only when unset is NOT useful for chains; a
	// chain needs a distinct target. If target is empty, treat as "no chain"
	// (empty) rather than erroring — surfaces always pass a target for chains.
	target := p.Target

	// extend returns a new slice with edge appended, never aliasing the parent
	// path's backing array. This is essential: the BFS branches over siblings,
	// and a shared-capacity append would let one branch overwrite another's
	// recorded edges. Allocation-per-step is the cycle/determinism-safe choice.
	extend := func(path []query.ResultEdge, edge query.ResultEdge) []query.ResultEdge {
		next := make([]query.ResultEdge, len(path)+1)
		copy(next, path)
		next[len(path)] = edge
		return next
	}

	// Level-by-level BFS over simple paths. Processing levels in edge-length
	// order means complete paths are discovered SHORTEST FIRST, so a MaxPaths cap
	// returns the shortest paths even when it truncates. The per-path visited set
	// makes each path simple (no repeated node); source==target is handled by
	// recording when an edge leads INTO target (captures cycles, never a
	// zero-length path). DefaultMaxChainDepth bounds the number of levels.
	type pathState struct {
		cur     model.NodeId
		edges   []query.ResultEdge
		visited map[model.NodeId]struct{}
	}

	var found [][]query.ResultEdge
	if target != "" {
		frontier := []pathState{{
			cur:     p.Symbol,
			edges:   nil,
			visited: map[model.NodeId]struct{}{p.Symbol: {}},
		}}
		depth := 0
		for len(frontier) > 0 && depth < DefaultMaxChainDepth && len(found) < maxPaths {
			var nextLevel []pathState
			for _, ps := range frontier {
				if len(found) >= maxPaths {
					break
				}
				for _, nb := range adj[ps.cur] {
					if len(found) >= maxPaths {
						break
					}
					if nb.id == target {
						// Complete path into target (also captures source==target cycles).
						found = append(found, extend(ps.edges, nb.via))
						continue
					}
					if _, seen := ps.visited[nb.id]; seen {
						continue // keep each path simple (no repeated node)
					}
					nv := make(map[model.NodeId]struct{}, len(ps.visited)+1)
					for k := range ps.visited {
						nv[k] = struct{}{}
					}
					nv[nb.id] = struct{}{}
					nextLevel = append(nextLevel, pathState{
						cur:     nb.id,
						edges:   extend(ps.edges, nb.via),
						visited: nv,
					})
				}
			}
			frontier = nextLevel
			depth++
		}
	}

	sortPaths(found)
	outcome := query.OutcomeFound
	if len(found) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: op,
		Outcome:  outcome,
		Symbol:   p.Symbol,
		Paths:    found,
	}, nil
}
