// Package community implements deterministic Louvain modularity-maximizing
// community detection (SW-103) as a PURE function over graphi's canonical
// entity/edge model. It lives in the `core` layer and depends ONLY on
// `core/model` — it never imports `engine`, `surfaces`, `cmd`, or `graphstore`.
// The `engine/community` adapter is responsible for projecting a
// `graphstore.Graphstore` into the node/edge view this package consumes.
//
// # Why determinism is the contract
//
// Canonical Louvain is randomized and order-sensitive: results depend on node
// visitation order and on how ties in the modularity gain (ΔQ) are broken.
// graphi requires byte-identical output across runs, process restarts, and
// platforms. This package therefore pins EVERY order-sensitive decision to the
// canonical entity key (model.NodeId, a fixed-width xxhash64 hex string):
//
//   - Node visitation order is the canonical NodeId ascending order — never Go
//     map iteration order.
//   - ΔQ ties (gains equal within a fixed compile-time epsilon) are broken by
//     choosing the candidate community whose representative member has the
//     smallest NodeId.
//   - All weight and ΔQ accumulation happens in a fixed canonical order (sorted
//     edges / sorted neighbours), so floating-point non-associativity cannot
//     leak run-to-run variation.
//   - Community IDs are assigned from the canonical ordering of each community's
//     representative member (its smallest NodeId), not from discovery order.
//   - Convergence is a fixed pass cap plus a deterministic ΔQ-improvement
//     threshold. There is no wall-clock, no math/rand, and no RNG seeding.
//
// The algorithm is consequently a pure function of (sorted nodes, sorted edges):
// the same resulting graph state always yields identical community IDs and
// membership, which is what makes full-vs-incremental parity hold.
package community

import (
	"sort"

	"github.com/samibel/graphi/core/model"
)

// epsilon is the fixed modularity-gain tie tolerance. It is a compile-time
// constant (never data- or time-derived): two candidate communities whose gains
// differ by at most epsilon are treated as tied, and the tie is resolved by the
// smallest representative NodeId. It is small enough not to over-merge yet large
// enough to absorb benign floating-point rounding.
const epsilon = 1e-12

// maxLocalPasses bounds the local-moving sweeps within a single level so a
// pathological graph can never drive an unbounded "until stable" loop.
const maxLocalPasses = 100

// maxLevels bounds the number of aggregation levels for the same reason.
const maxLevels = 100

// Community is a detected community: a stable 1-based ID assigned from the
// canonical ordering of community representatives (smallest member NodeId), and
// its member node ids sorted by NodeId ascending.
type Community struct {
	ID      int
	Members []model.NodeId
}

// Result is the deterministic outcome of detection: communities ordered by ID
// (equivalently, by representative NodeId ascending).
type Result struct {
	Communities []Community
}

// CommunityOf returns a map from NodeId to its 1-based community ID. It is a
// convenience derived from Communities; iterating the map is unordered, so
// callers that need stable output must use Communities directly.
func (r Result) CommunityOf() map[model.NodeId]int {
	out := make(map[model.NodeId]int)
	for _, c := range r.Communities {
		for _, m := range c.Members {
			out[m] = c.ID
		}
	}
	return out
}

// edgeRef is one weighted undirected adjacency entry (to-node index + weight).
type edgeRef struct {
	to int
	w  float64
}

// weightedGraph is the internal undirected weighted graph for one Louvain level.
// Node 0..n-1 are dense indices. For level 0 the index order is the canonical
// NodeId ascending order, so smaller index == smaller canonical key.
type weightedGraph struct {
	n        int
	adj      [][]edgeRef // adj[i] sorted by .to ascending
	selfLoop []float64   // self-loop weight per node (counted once in m)
	degree   []float64   // weighted degree k_i = Σ adj.w + 2*selfLoop
	m        float64     // total edge weight = 0.5 * Σ degree
}

// buildGraph projects the node/edge view into the canonical level-0 weighted
// undirected graph. Edge weight is model.Edge.Confidence(); parallel and
// opposite call/import/reference edges between the same node pair are folded
// into a single undirected weight via fixed (sorted) accumulation order. Edges
// whose endpoints are not both present in nodes are skipped. It also returns the
// sorted NodeId slice (index == node index).
func buildGraph(nodes []model.NodeId, edges []model.Edge) (*weightedGraph, []model.NodeId) {
	// Canonical node ordering + dedupe.
	sorted := make([]model.NodeId, 0, len(nodes))
	seen := make(map[model.NodeId]struct{}, len(nodes))
	for _, id := range nodes {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := make(map[model.NodeId]int, len(sorted))
	for i, id := range sorted {
		idx[id] = i
	}
	n := len(sorted)

	// Fold edges deterministically: sort by (From,To,Kind) so accumulation order
	// is fixed and FP-stable regardless of arrival order.
	ce := make([]model.Edge, len(edges))
	copy(ce, edges)
	sort.Slice(ce, func(i, j int) bool {
		if ce[i].From() != ce[j].From() {
			return ce[i].From() < ce[j].From()
		}
		if ce[i].To() != ce[j].To() {
			return ce[i].To() < ce[j].To()
		}
		return ce[i].Kind() < ce[j].Kind()
	})

	self := make([]float64, n)
	type pair struct{ a, b int }
	pairW := make(map[pair]float64)
	pairOrder := make([]pair, 0) // preserves first-seen (sorted) order for stable build
	for _, e := range ce {
		a, ok1 := idx[e.From()]
		b, ok2 := idx[e.To()]
		if !ok1 || !ok2 {
			continue // endpoint not in node set
		}
		w := e.Confidence()
		if a == b {
			self[a] += w
			continue
		}
		if a > b {
			a, b = b, a
		}
		p := pair{a, b}
		if _, exists := pairW[p]; !exists {
			pairOrder = append(pairOrder, p)
		}
		pairW[p] += w
	}

	adj := make([][]edgeRef, n)
	for _, p := range pairOrder {
		w := pairW[p]
		adj[p.a] = append(adj[p.a], edgeRef{to: p.b, w: w})
		adj[p.b] = append(adj[p.b], edgeRef{to: p.a, w: w})
	}
	// Sort each adjacency list by neighbour index so neighbour iteration (and
	// thus ΔQ accumulation) is in a fixed canonical order.
	for i := range adj {
		sort.Slice(adj[i], func(x, y int) bool { return adj[i][x].to < adj[i][y].to })
	}

	degree := make([]float64, n)
	var twoM float64
	for i := 0; i < n; i++ {
		var d float64
		for _, e := range adj[i] {
			d += e.w
		}
		d += 2 * self[i]
		degree[i] = d
		twoM += d
	}

	return &weightedGraph{
		n:        n,
		adj:      adj,
		selfLoop: self,
		degree:   degree,
		m:        twoM / 2,
	}, sorted
}

// gain returns the modularity gain of moving an extracted node with degree ki
// (and weight kiin to community C) into community C of total degree ctot:
//
//	ΔQ = kiin/m - (ctot*ki)/(2*m^2)
//
// Derived from the standard Louvain incremental formula with the node first
// removed from its community (so ctot/ki already exclude it).
func gain(kiin, ctot, ki, m float64) float64 {
	return kiin/m - (ctot*ki)/(2*m*m)
}

// localMoving runs deterministic local-moving sweeps over g until no node moves
// or the pass cap is hit. repr[i] is the representative (smallest original node
// index, == smallest canonical key) of level-node i, used for ΔQ tie-breaking.
// It returns the community label of each node (a label is one of the node
// indices that anchors the community).
func localMoving(g *weightedGraph, repr []int) []int {
	n := g.n
	comm := make([]int, n)
	commTot := make([]float64, n)
	commRepr := make([]int, n)
	members := make([][]int, n)
	for i := 0; i < n; i++ {
		comm[i] = i
		commTot[i] = g.degree[i]
		commRepr[i] = repr[i]
		members[i] = []int{i}
	}
	if g.m == 0 {
		return comm // no edges → every node is its own singleton
	}

	for pass := 0; pass < maxLocalPasses; pass++ {
		moved := false
		for i := 0; i < n; i++ { // canonical node order
			ci := comm[i]
			ki := g.degree[i]

			// Weight from i to each neighbouring community, accumulated in sorted
			// neighbour order (fixed → FP-stable).
			neigh := make(map[int]float64)
			for _, e := range g.adj[i] {
				neigh[comm[e.to]] += e.w
			}

			// Extract i from ci for the gain math.
			commTot[ci] -= ki

			bestComm := ci
			bestRepr := commRepr[ci]
			bestGain := gain(neigh[ci], commTot[ci], ki, g.m)

			// Evaluate candidate communities in deterministic (sorted-label) order.
			labels := make([]int, 0, len(neigh))
			for c := range neigh {
				labels = append(labels, c)
			}
			sort.Ints(labels)
			for _, c := range labels {
				if c == ci {
					continue
				}
				gC := gain(neigh[c], commTot[c], ki, g.m)
				switch {
				case gC > bestGain+epsilon:
					bestGain, bestComm, bestRepr = gC, c, commRepr[c]
				case gC >= bestGain-epsilon:
					// Tie within epsilon → smallest representative key wins.
					if commRepr[c] < bestRepr {
						bestComm, bestRepr = c, commRepr[c]
					}
				}
			}

			// Re-insert i into the chosen community.
			commTot[bestComm] += ki
			if bestComm != ci {
				comm[i] = bestComm
				// Remove i from ci's member list; recompute repr if i was the min.
				members[ci] = removeInt(members[ci], i)
				if repr[i] == commRepr[ci] {
					commRepr[ci] = minRepr(members[ci], repr)
				}
				// Add i to bestComm.
				members[bestComm] = append(members[bestComm], i)
				if repr[i] < commRepr[bestComm] {
					commRepr[bestComm] = repr[i]
				}
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	return comm
}

// removeInt removes the first occurrence of v from s (order not preserved).
func removeInt(s []int, v int) []int {
	for i, x := range s {
		if x == v {
			s[i] = s[len(s)-1]
			return s[:len(s)-1]
		}
	}
	return s
}

// minRepr returns the smallest repr[m] over members m, or a large sentinel when
// empty.
func minRepr(members []int, repr []int) int {
	best := int(^uint(0) >> 1) // max int sentinel
	for _, m := range members {
		if repr[m] < best {
			best = repr[m]
		}
	}
	return best
}

// Detect runs deterministic multi-level Louvain (local-moving + aggregation)
// over the given nodes and edges and returns communities with stable, canonical
// IDs. It is a pure function of (sorted nodes, sorted edges): no map iteration
// order, no wall-clock, no randomness. Degenerate inputs (empty / single-node /
// fully-disconnected) yield deterministic singleton communities without panics.
func Detect(nodes []model.NodeId, edges []model.Edge) Result {
	g0, sortedIDs := buildGraph(nodes, edges)
	n := g0.n
	if n == 0 {
		return Result{Communities: []Community{}}
	}

	// origToCur[o] = current level-node index that original node o belongs to.
	origToCur := make([]int, n)
	for i := range origToCur {
		origToCur[i] = i
	}

	cur := g0
	reprCur := make([]int, n) // repr of each current level-node (orig index)
	for i := range reprCur {
		reprCur[i] = i
	}

	for level := 0; level < maxLevels; level++ {
		if cur.m == 0 {
			break // no edges left → nothing to merge
		}
		labels := localMoving(cur, reprCur)

		// Relabel communities to dense indices ordered by representative
		// (smallest reprCur over members) ascending → canonical ID ordering.
		newIdx, count, reprNew := relabelByRepr(labels, reprCur)
		if count == cur.n {
			break // no merging happened at this level → converged
		}

		// Propagate the merge to original nodes.
		for o := 0; o < n; o++ {
			origToCur[o] = newIdx[labels[origToCur[o]]]
		}

		cur = aggregate(cur, labels, newIdx, count)
		reprCur = reprNew
	}

	return assemble(origToCur, sortedIDs)
}

// relabelByRepr maps each distinct community label to a dense index 0..count-1
// ordered by the label's representative (the smallest reprCur among its
// members) ascending. Distinct communities have disjoint members, so their
// representatives are distinct — the ordering is total and deterministic. It
// returns the label→dense map, the community count, and the representative of
// each dense community.
func relabelByRepr(labels []int, reprCur []int) (map[int]int, int, []int) {
	// Smallest repr per label.
	labelRepr := make(map[int]int)
	for node, lab := range labels {
		r := reprCur[node]
		if cur, ok := labelRepr[lab]; !ok || r < cur {
			labelRepr[lab] = r
		}
	}
	// Order labels by representative ascending.
	type lr struct {
		label int
		repr  int
	}
	ordered := make([]lr, 0, len(labelRepr))
	for lab, r := range labelRepr {
		ordered = append(ordered, lr{lab, r})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].repr < ordered[j].repr })

	newIdx := make(map[int]int, len(ordered))
	reprNew := make([]int, len(ordered))
	for dense, e := range ordered {
		newIdx[e.label] = dense
		reprNew[dense] = e.repr
	}
	return newIdx, len(ordered), reprNew
}

// aggregate builds the next-level weighted graph where each community becomes a
// super-node (dense index via newIdx∘labels). Intra-community weight folds into
// the super-node's self-loop; inter-community weight folds into super-edges.
// Accumulation iterates nodes and neighbours in index order (fixed → FP-stable).
func aggregate(g *weightedGraph, labels []int, newIdx map[int]int, count int) *weightedGraph {
	self := make([]float64, count)
	type pair struct{ a, b int }
	pairW := make(map[pair]float64)
	pairOrder := make([]pair, 0)

	superOf := func(node int) int { return newIdx[labels[node]] }

	for i := 0; i < g.n; i++ {
		si := superOf(i)
		// Existing self-loop carries over into the super-node self-loop.
		self[si] += g.selfLoop[i]
		for _, e := range g.adj[i] {
			j := e.to
			if j < i {
				continue // count each undirected pair once (i < j)
			}
			sj := superOf(j)
			if si == sj {
				self[si] += e.w
				continue
			}
			a, b := si, sj
			if a > b {
				a, b = b, a
			}
			p := pair{a, b}
			if _, exists := pairW[p]; !exists {
				pairOrder = append(pairOrder, p)
			}
			pairW[p] += e.w
		}
	}

	adj := make([][]edgeRef, count)
	for _, p := range pairOrder {
		w := pairW[p]
		adj[p.a] = append(adj[p.a], edgeRef{to: p.b, w: w})
		adj[p.b] = append(adj[p.b], edgeRef{to: p.a, w: w})
	}
	for i := range adj {
		sort.Slice(adj[i], func(x, y int) bool { return adj[i][x].to < adj[i][y].to })
	}

	degree := make([]float64, count)
	var twoM float64
	for i := 0; i < count; i++ {
		var d float64
		for _, e := range adj[i] {
			d += e.w
		}
		d += 2 * self[i]
		degree[i] = d
		twoM += d
	}

	return &weightedGraph{n: count, adj: adj, selfLoop: self, degree: degree, m: twoM / 2}
}

// assemble groups original nodes by their final community and assigns 1-based
// IDs ordered by representative (smallest member NodeId) ascending. Members are
// sorted by NodeId ascending.
func assemble(origToCur []int, sortedIDs []model.NodeId) Result {
	// Group original node indices by final community index.
	groups := make(map[int][]int)
	for o, c := range origToCur {
		groups[c] = append(groups[c], o)
	}
	// Each group's representative = smallest original index (== smallest NodeId,
	// since sortedIDs is canonical order).
	type grp struct {
		repr    int
		members []int
	}
	ordered := make([]grp, 0, len(groups))
	for _, members := range groups {
		// members are appended in ascending o order already (loop is 0..n-1),
		// so members[0] is the smallest index; keep it explicit for safety.
		minIdx := members[0]
		for _, o := range members {
			if o < minIdx {
				minIdx = o
			}
		}
		ordered = append(ordered, grp{repr: minIdx, members: members})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].repr < ordered[j].repr })

	comms := make([]Community, 0, len(ordered))
	for i, g := range ordered {
		sort.Ints(g.members)
		mem := make([]model.NodeId, len(g.members))
		for k, o := range g.members {
			mem[k] = sortedIDs[o]
		}
		comms = append(comms, Community{ID: i + 1, Members: mem})
	}
	return Result{Communities: comms}
}

// Modularity computes the modularity Q of the partition commOf over the
// undirected weighted graph built from nodes and edges (edge weight =
// Edge.Confidence()), using the same canonical graph construction as Detect:
//
//	Q = Σ_c [ L_c/m - (D_c/(2m))^2 ]
//
// where L_c is the internal weight of community c (each intra-community
// undirected edge and self-loop counted once) and D_c is the total degree of
// community c. It returns 0 for an empty/edgeless graph. Nodes absent from
// commOf are treated as their own singleton community. The accumulation order is
// fixed (sorted nodes/edges) so the result is reproducible across runs and
// platforms. It is the metric used to compare Louvain against the package-prefix
// baseline (AC-1).
func Modularity(nodes []model.NodeId, edges []model.Edge, commOf map[model.NodeId]int) float64 {
	g, sortedIDs := buildGraph(nodes, edges)
	if g.m == 0 {
		return 0
	}
	// Community label per node index (distinct sentinel for nodes missing a
	// mapping so they stay singletons).
	const missingBase = -1
	label := make([]int, g.n)
	nextSingleton := missingBase
	for i, id := range sortedIDs {
		if c, ok := commOf[id]; ok {
			label[i] = c
		} else {
			label[i] = nextSingleton
			nextSingleton--
		}
	}

	// L_c and D_c accumulated in fixed index order.
	lc := make(map[int]float64)
	dc := make(map[int]float64)
	for i := 0; i < g.n; i++ {
		dc[label[i]] += g.degree[i]
		// self-loop is internal to its own community.
		lc[label[i]] += g.selfLoop[i]
		for _, e := range g.adj[i] {
			j := e.to
			if j < i {
				continue // each undirected pair once
			}
			if label[i] == label[j] {
				lc[label[i]] += e.w
			}
		}
	}

	// Sum contributions in sorted community-label order (fixed → FP-stable).
	labelsSet := make(map[int]struct{})
	for c := range dc {
		labelsSet[c] = struct{}{}
	}
	cs := make([]int, 0, len(labelsSet))
	for c := range labelsSet {
		cs = append(cs, c)
	}
	sort.Ints(cs)

	m := g.m
	var q float64
	for _, c := range cs {
		l := lc[c]
		d := dc[c]
		q += l/m - (d/(2*m))*(d/(2*m))
	}
	return q
}
