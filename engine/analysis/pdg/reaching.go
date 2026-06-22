package pdg

import (
	"container/heap"
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// reachingDefs computes data-dependence edges via a worklist-based reaching-
// definitions analysis. For each node, it propagates the set of definitions
// (node IDs) that may reach it along the forward adjacency. When a node is
// both a definition and a use site, it produces a data_dep edge from the
// reaching definition to the use.
//
// The algorithm:
//  1. Every node that has outgoing edges is a potential definition site.
//  2. Initialize each definition's own reach set to {itself}.
//  3. Worklist propagation: for each node, propagate its reach set to all
//     successors. A successor's reach set is the union of incoming reach sets.
//     When a successor gains new definitions, it is re-enqueued.
//  4. After fixpoint, for every edge (u→v) in the original graph, each
//     definition d in reach[u] produces a data_dep edge d→v.
//
// The worklist uses container/heap with canonical ordering (node ID) for
// determinism.
func reachingDefs(
	ctx context.Context,
	nodeIDs []model.NodeId,
	adj map[model.NodeId][]model.NodeId,
	cfg PDGConfig,
) ([]DepEdge, []string) {
	var diagnostics []string

	// reach[n] is the set of definition node IDs that reach node n.
	// We represent sets as sorted slices for determinism.
	reach := make(map[model.NodeId]map[model.NodeId]struct{}, len(nodeIDs))

	// Initialize: every node that has outgoing edges (i.e., defines something
	// that flows forward) seeds its own reach set. We also seed every node
	// with itself so that even leaf nodes participate.
	for _, nid := range nodeIDs {
		reach[nid] = map[model.NodeId]struct{}{nid: {}}
	}

	// Build the worklist seeded with all nodes that have successors.
	wl := &rdWorkList{}
	heap.Init(wl)
	for _, nid := range nodeIDs {
		if len(adj[nid]) > 0 {
			heap.Push(wl, rdWorkItem{nodeID: nid})
		}
	}

	totalWork := 0
	for wl.Len() > 0 {
		select {
		case <-ctx.Done():
			return nil, append(diagnostics, fmt.Sprintf("reaching-defs: context cancelled after %d iterations", totalWork))
		default:
		}

		totalWork++
		if cfg.MaxWork > 0 && totalWork > cfg.MaxWork {
			diagnostics = append(diagnostics, fmt.Sprintf("reaching-defs: max_work cap exceeded (%d)", cfg.MaxWork))
			break
		}

		item := heap.Pop(wl).(rdWorkItem)
		curReach := reach[item.nodeID]

		for _, succ := range adj[item.nodeID] {
			succReach := reach[succ]
			if succReach == nil {
				succReach = make(map[model.NodeId]struct{})
				reach[succ] = succReach
			}

			changed := false
			for def := range curReach {
				if _, exists := succReach[def]; !exists {
					succReach[def] = struct{}{}
					changed = true
				}
			}

			if changed {
				heap.Push(wl, rdWorkItem{nodeID: succ})
			}
		}
	}

	// Emit data-dependence edges: for each node v, every definition d in
	// reach[v] (other than v itself) produces a data_dep edge d→v. This
	// captures "d's definition may reach (and thus influence) v".
	var edges []DepEdge
	for _, nid := range nodeIDs {
		defs := reach[nid]
		if len(defs) == 0 {
			continue
		}
		// Collect and sort definitions for determinism.
		sorted := make([]model.NodeId, 0, len(defs))
		for d := range defs {
			if d != nid { // no self-dep edges
				sorted = append(sorted, d)
			}
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for _, d := range sorted {
			edges = append(edges, DepEdge{
				From:           d,
				To:             nid,
				Kind:           EdgeKindDataDep,
				DerivationRule: "reaching_definitions",
			})
		}
	}

	// Sort output deterministically: by (From, To, Kind).
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})

	return edges, diagnostics
}

// rdWorkItem is a worklist entry for reaching-definitions.
type rdWorkItem struct {
	nodeID model.NodeId
	index  int // heap index
}

// rdWorkList is a min-heap priority queue ordered by nodeID (canonical
// ordering) for deterministic iteration.
type rdWorkList []rdWorkItem

func (w rdWorkList) Len() int { return len(w) }

func (w rdWorkList) Less(i, j int) bool {
	return w[i].nodeID < w[j].nodeID
}

func (w rdWorkList) Swap(i, j int) {
	w[i], w[j] = w[j], w[i]
	w[i].index = i
	w[j].index = j
}

func (w *rdWorkList) Push(x any) {
	item := x.(rdWorkItem)
	item.index = len(*w)
	*w = append(*w, item)
}

func (w *rdWorkList) Pop() any {
	old := *w
	n := len(old)
	item := old[n-1]
	old[n-1] = rdWorkItem{} // avoid memory leak
	item.index = -1
	*w = old[:n-1]
	return item
}
