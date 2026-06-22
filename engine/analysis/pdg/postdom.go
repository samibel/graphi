package pdg

import (
	"sort"

	"github.com/samibel/graphi/core/model"
)

// controlDeps computes control-dependence edges via a post-dominance tree
// constructed using the Cooper-Harvey-Kennedy (CHK) algorithm.
//
// The algorithm:
//  1. Build the reverse CFG (reversed edges) and identify the exit node.
//     If no natural exit exists, a synthetic "exit" node is added.
//  2. Compute the post-dominator tree over the reverse CFG using iterative
//     dataflow (CHK): idom[n] is the immediate post-dominator of n.
//  3. For each CFG edge (a→b), walk up the post-dominator tree from b until
//     reaching idom[a]. Every node on that walk (excluding idom[a]) is
//     control-dependent on a.
//
// All output is sorted deterministically by (From, To, Kind).
func controlDeps(
	nodeIDs []model.NodeId,
	adj map[model.NodeId][]model.NodeId,
	entryNode model.NodeId,
) []DepEdge {
	if len(nodeIDs) == 0 {
		return nil
	}

	// Build reverse adjacency (for post-dominator computation, we treat
	// reverse CFG: edges point from successor to predecessor).
	revAdj := make(map[model.NodeId][]model.NodeId, len(nodeIDs))
	exitCandidates := make(map[model.NodeId]struct{}, len(nodeIDs))
	hasIncoming := make(map[model.NodeId]struct{}, len(nodeIDs))
	for _, nid := range nodeIDs {
		exitCandidates[nid] = struct{}{}
	}
	for src, succs := range adj {
		for _, dst := range succs {
			revAdj[dst] = append(revAdj[dst], src)
			hasIncoming[dst] = struct{}{}
		}
	}

	// Sort reverse adjacency lists for determinism.
	for k := range revAdj {
		sort.Slice(revAdj[k], func(i, j int) bool { return revAdj[k][i] < revAdj[k][j] })
	}

	// Identify exit node: a node with no successors. If multiple exist or
	// none exist, we pick the largest node ID as exit (deterministic choice),
	// or if all nodes have successors, use a synthetic approach.
	var exitNode model.NodeId
	var leafNodes []model.NodeId
	for _, nid := range nodeIDs {
		if len(adj[nid]) == 0 {
			leafNodes = append(leafNodes, nid)
		}
	}
	if len(leafNodes) > 0 {
		sort.Slice(leafNodes, func(i, j int) bool { return leafNodes[i] < leafNodes[j] })
		exitNode = leafNodes[0]
	} else {
		// All nodes have successors (cycles). Pick the last node ID as exit.
		sorted := make([]model.NodeId, len(nodeIDs))
		copy(sorted, nodeIDs)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		exitNode = sorted[len(sorted)-1]
	}

	// Build node index for O(1) lookup.
	nodeIdx := make(map[model.NodeId]int, len(nodeIDs))
	for i, nid := range nodeIDs {
		nodeIdx[nid] = i
	}

	// Compute reverse post-order (RPO) of the reverse CFG (rooted at exit).
	// In the reverse CFG, the exit node is the entry.
	rpo := reversePostOrder(exitNode, revAdj, nodeIDs)
	rpoIndex := make(map[model.NodeId]int, len(rpo))
	for i, nid := range rpo {
		rpoIndex[nid] = i
	}

	// Cooper-Harvey-Kennedy iterative dominator algorithm on the reverse CFG.
	// Here "dominator" in the reverse CFG = "post-dominator" in the original.
	idom := make(map[model.NodeId]model.NodeId, len(nodeIDs))
	idom[exitNode] = exitNode // exit post-dominates itself

	intersect := func(b1, b2 model.NodeId) model.NodeId {
		f1, f2 := b1, b2
		for f1 != f2 {
			for rpoIndex[f1] > rpoIndex[f2] {
				f1 = idom[f1]
			}
			for rpoIndex[f2] > rpoIndex[f1] {
				f2 = idom[f2]
			}
		}
		return f1
	}

	changed := true
	for changed {
		changed = false
		for _, b := range rpo {
			if b == exitNode {
				continue
			}
			// Predecessors in the reverse CFG = successors in adj (original CFG reversed).
			// Actually, in the reverse CFG, predecessors of b are nodes that b
			// has edges to, which means revAdj gives us "successors in reverse CFG"
			// but we need predecessors. Predecessors of b in reverse CFG = adj[b]
			// (the nodes that b points to in original CFG become predecessors in reverse).
			// Wait — let's be precise:
			// Original CFG: adj[a] contains b means a→b.
			// Reverse CFG: revAdj[b] contains a means b→a in reverse CFG (i.e., edge b←a).
			// For CHK on reverse CFG rooted at exit:
			//   predecessors of b in reverse CFG = nodes c such that c→b in reverse CFG
			//   = nodes c such that b→c in original CFG = adj[b].
			preds := adj[b]

			var newIdom model.NodeId
			first := true
			for _, p := range preds {
				if _, ok := idom[p]; !ok {
					continue // not yet processed
				}
				if first {
					newIdom = p
					first = true // processed at least one
					first = false
					continue
				}
				newIdom = intersect(newIdom, p)
			}

			if first {
				// No processed predecessor found; skip.
				continue
			}

			if old, ok := idom[b]; !ok || old != newIdom {
				idom[b] = newIdom
				changed = true
			}
		}
	}

	// Derive control-dependence edges from the post-dominator tree.
	// For each original CFG edge (a→b): walk up the post-dominator tree from
	// b to idom[a]. Every node on that path (including b, excluding idom[a])
	// is control-dependent on a.
	seen := make(map[[2]model.NodeId]struct{})
	var edges []DepEdge

	for a, succs := range adj {
		idomA, aHasIdom := idom[a]
		for _, b := range succs {
			cur := b
			for i := 0; i < len(nodeIDs)+1; i++ { // bounded walk
				if aHasIdom && cur == idomA {
					break
				}
				if cur == "" {
					break
				}
				key := [2]model.NodeId{a, cur}
				if _, dup := seen[key]; !dup && cur != a {
					seen[key] = struct{}{}
					edges = append(edges, DepEdge{
						From:           a,
						To:             cur,
						Kind:           EdgeKindControlDep,
						DerivationRule: "post_dominance_frontier",
					})
				}
				parent, ok := idom[cur]
				if !ok || parent == cur {
					break
				}
				cur = parent
			}
		}
	}

	// Sort output deterministically.
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})

	return edges
}

// reversePostOrder computes a reverse post-order traversal of the graph
// rooted at root, using the given adjacency. Nodes unreachable from root
// are appended at the end in sorted order for completeness.
func reversePostOrder(
	root model.NodeId,
	adj map[model.NodeId][]model.NodeId,
	allNodes []model.NodeId,
) []model.NodeId {
	visited := make(map[model.NodeId]struct{}, len(allNodes))
	var postOrder []model.NodeId

	var dfs func(n model.NodeId)
	dfs = func(n model.NodeId) {
		if _, seen := visited[n]; seen {
			return
		}
		visited[n] = struct{}{}
		// Visit successors in sorted order for determinism.
		succs := adj[n]
		sorted := make([]model.NodeId, len(succs))
		copy(sorted, succs)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for _, s := range sorted {
			dfs(s)
		}
		postOrder = append(postOrder, n)
	}

	dfs(root)

	// Reverse to get reverse post-order.
	rpo := make([]model.NodeId, len(postOrder))
	for i, n := range postOrder {
		rpo[len(postOrder)-1-i] = n
	}

	// Append unreachable nodes in sorted order.
	var unreachable []model.NodeId
	for _, nid := range allNodes {
		if _, seen := visited[nid]; !seen {
			unreachable = append(unreachable, nid)
		}
	}
	sort.Slice(unreachable, func(i, j int) bool { return unreachable[i] < unreachable[j] })
	rpo = append(rpo, unreachable...)

	return rpo
}
