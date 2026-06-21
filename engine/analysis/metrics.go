package analysis

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// DefaultMetricsTopN is the documented per-kind output cap for graph metrics. A
// request with MaxNodes <= 0 uses this cap. Each kind (hub/bridge/centrality) is
// independently capped to its top-N by score, keeping the result and token cost
// bounded on large graphs. All three computations are O(V+E) and cycle-safe.
const DefaultMetricsTopN = 50

// Metric kind labels emitted by the metrics analyzer.
const (
	MetricHub        = "hub"
	MetricBridge     = "bridge"
	MetricCentrality = "centrality"
)

// metricsAnalyzer computes graph-theory signals over the indexed graph: hub
// (degree), bridge (articulation points via Tarjan DFS — the AC-endorsed
// substitute for unbounded O(VE) betweenness), and centrality (degree
// centrality = degree/(N−1)). All three are O(V+E), exact, and cycle-safe. It
// is the fourth analyzer on the SW-022 registry.
type metricsAnalyzer struct{}

func (metricsAnalyzer) Name() string { return "metrics" }

// Analyze emits hub/bridge/centrality NodeScore entries over the whole graph.
// The articulation-point set is a graph invariant, so determinism is structural;
// output order is stabilized by sortMetrics (kind, score DESC, node id).
func (a metricsAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	const op = "metrics"

	topN := p.MaxNodes
	if topN <= 0 {
		topN = DefaultMetricsTopN
	}

	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return Analysis{}, err
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Analysis{}, err
	}

	nodeOf := make(map[model.NodeId]query.ResultNode, len(nodes))
	for _, n := range nodes {
		nodeOf[n.ID()] = nodeToResult(n)
	}
	n := len(nodes)

	// Undirected degree (each edge contributes to both endpoints; a self-loop
	// contributes once to its single endpoint) and undirected adjacency.
	degree := make(map[model.NodeId]int, n)
	adj := make(map[model.NodeId][]model.NodeId, n)
	for _, e := range edges {
		f, t := e.From(), e.To()
		degree[f]++
		if f != t {
			degree[t]++
		}
		adj[f] = append(adj[f], t)
		if f != t {
			adj[t] = append(adj[t], f)
		}
	}

	apScore := articulationPoints(nodeOf, adj)

	scores := make([]NodeScore, 0, 2*n+len(apScore))
	for id, node := range nodeOf {
		d := degree[id]
		scores = append(scores, NodeScore{Node: node, Kind: MetricHub, Score: float64(d), EdgeCount: d})
	}
	for id, children := range apScore {
		node := nodeOf[id]
		scores = append(scores, NodeScore{Node: node, Kind: MetricBridge, Score: float64(children), EdgeCount: children})
	}
	denom := 0.0
	if n > 1 {
		denom = float64(n - 1)
	}
	for id, node := range nodeOf {
		d := degree[id]
		score := 0.0
		if denom > 0 {
			score = float64(d) / denom
		}
		scores = append(scores, NodeScore{Node: node, Kind: MetricCentrality, Score: score, EdgeCount: d})
	}

	sortMetrics(scores)
	scores = capPerKind(scores, topN)

	outcome := query.OutcomeFound
	if len(scores) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: op,
		Outcome:  outcome,
		Symbol:   p.Symbol,
		Metrics:  scores,
	}, nil
}

// capPerKind keeps at most topN entries per Kind, preserving the sortMetrics
// order (kind, score DESC, node id). Scores beyond the cap per kind are dropped.
func capPerKind(scores []NodeScore, topN int) []NodeScore {
	seen := map[string]int{}
	out := make([]NodeScore, 0, len(scores))
	for _, s := range scores {
		if seen[s.Kind] >= topN {
			continue
		}
		seen[s.Kind]++
		out = append(out, s)
	}
	return out
}

// articulationPoints returns the set of cut vertices of the undirected graph
// described by adj, mapped to the count of DFS-tree children whose subtree has
// no back edge above the vertex (≥1 for articulation points). It uses the
// standard Tarjan disc/low DFS iteratively (bounded stack; safe on large
// graphs) with the root special case (root is an articulation point iff it has
// ≥2 DFS children). Nodes and adjacency are visited in sorted-id order so the
// computation is reproducible; the resulting SET is a graph invariant anyway.
func articulationPoints(nodes map[model.NodeId]query.ResultNode, adj map[model.NodeId][]model.NodeId) map[model.NodeId]int {
	ids := make([]model.NodeId, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for k := range adj {
		a := adj[k]
		sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
		adj[k] = a
	}

	disc := make(map[model.NodeId]int, len(nodes))
	low := make(map[model.NodeId]int, len(nodes))
	visited := make(map[model.NodeId]bool, len(nodes))
	parent := make(map[model.NodeId]model.NodeId, len(nodes))
	children := make(map[model.NodeId]int, len(nodes))
	timer := 0

	type frame struct {
		node model.NodeId
		par  model.NodeId
		idx  int
	}

	for _, root := range ids {
		if visited[root] {
			continue
		}
		visited[root] = true
		disc[root] = timer
		low[root] = timer
		timer++
		parent[root] = root // sentinel: a root is its own parent
		stack := []frame{{node: root, par: root, idx: 0}}
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			neighbors := adj[f.node]
			if f.idx < len(neighbors) {
				w := neighbors[f.idx]
				f.idx++
				if w == f.par {
					continue // skip the tree edge back to the parent
				}
				if !visited[w] {
					visited[w] = true
					parent[w] = f.node
					children[f.node]++
					disc[w] = timer
					low[w] = timer
					timer++
					stack = append(stack, frame{node: w, par: f.node, idx: 0})
				} else if disc[w] < low[f.node] {
					// Back edge: raise low via the neighbor's discovery time.
					low[f.node] = disc[w]
				}
			} else {
				// Finished f.node: propagate its low to the parent frame.
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					p := &stack[len(stack)-1]
					if low[f.node] < low[p.node] {
						low[p.node] = low[f.node]
					}
				}
			}
		}
	}

	// Classify articulation points from disc/low/children.
	ap := make(map[model.NodeId]int)
	dfsChildren := make(map[model.NodeId][]model.NodeId, len(nodes))
	for w, p := range parent {
		if p != w {
			dfsChildren[p] = append(dfsChildren[p], w)
		}
	}
	for _, v := range ids {
		if parent[v] == v {
			// DFS-tree root: articulation iff >=2 children.
			if children[v] >= 2 {
				ap[v] = children[v]
			}
			continue
		}
		count := 0
		for _, w := range dfsChildren[v] {
			if low[w] >= disc[v] {
				count++
			}
		}
		if count > 0 {
			ap[v] = count
		}
	}
	return ap
}
