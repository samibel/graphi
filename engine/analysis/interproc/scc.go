package interproc

import "sort"

// CallGraph is the adjacency representation of procedure-level call edges.
// Keys are caller procedure IDs; values are sorted callee IDs.
type CallGraph map[string][]string

// SCC is a strongly connected component: a set of procedure IDs that are
// mutually reachable through the call graph (direct or mutual recursion).
// Within each SCC, procedure IDs are sorted for deterministic iteration.
type SCC []string

// TarjanSCC computes the strongly connected components of the call graph using
// Tarjan's algorithm. The returned SCCs are in reverse topological order
// (callees before callers), and each SCC's members are sorted lexicographically
// for deterministic processing.
func TarjanSCC(g CallGraph) []SCC {
	t := &tarjanState{
		graph:   g,
		index:   make(map[string]int),
		lowlink: make(map[string]int),
		onStack: make(map[string]bool),
	}

	// Collect all nodes (callers + callees) and sort for deterministic order.
	nodeSet := make(map[string]struct{})
	for caller, callees := range g {
		nodeSet[caller] = struct{}{}
		for _, callee := range callees {
			nodeSet[callee] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	for _, n := range nodes {
		if _, visited := t.index[n]; !visited {
			t.strongConnect(n)
		}
	}
	return t.result
}

// tarjanState holds the mutable state for a single Tarjan SCC computation.
type tarjanState struct {
	graph   CallGraph
	index   map[string]int
	lowlink map[string]int
	onStack map[string]bool
	stack   []string
	counter int
	result  []SCC
}

// strongConnect is the recursive core of Tarjan's algorithm.
func (t *tarjanState) strongConnect(v string) {
	t.index[v] = t.counter
	t.lowlink[v] = t.counter
	t.counter++
	t.stack = append(t.stack, v)
	t.onStack[v] = true

	// Iterate over successors in sorted order (determinism).
	successors := t.graph[v]
	for _, w := range successors {
		if _, visited := t.index[w]; !visited {
			t.strongConnect(w)
			if t.lowlink[w] < t.lowlink[v] {
				t.lowlink[v] = t.lowlink[w]
			}
		} else if t.onStack[w] {
			if t.index[w] < t.lowlink[v] {
				t.lowlink[v] = t.index[w]
			}
		}
	}

	// If v is a root node, pop the SCC.
	if t.lowlink[v] == t.index[v] {
		var component SCC
		for {
			w := t.stack[len(t.stack)-1]
			t.stack = t.stack[:len(t.stack)-1]
			t.onStack[w] = false
			component = append(component, w)
			if w == v {
				break
			}
		}
		sort.Strings(component)
		t.result = append(t.result, component)
	}
}
