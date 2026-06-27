package embed

import (
	"container/heap"
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// VectorIndex is the backend-agnostic ranking contract shared by the brute-force
// cosine index and the HNSW approximate-nearest-neighbour index. Both satisfy it
// identically (Search returns []Hit ordered score-desc, NodeId-asc), so the search
// service and the generate path are oblivious to which backend is wired — this is
// the seam invariant that lets a caller opt into HNSW without code changes (AC6).
type VectorIndex interface {
	// Rebuild replaces the index contents from the durable VectorTable.
	Rebuild(ctx context.Context, table VectorTable) error
	// Put inserts/updates a single vector (incremental embed path).
	Put(id model.NodeId, values []float32)
	// Search returns up to limit hits ranked by cosine similarity, score
	// DESCENDING with a deterministic NodeId-ascending tie-break.
	Search(query []float32, limit int) []Hit
	// Len returns the number of indexed vectors.
	Len() int
}

// Compile-time proof that both backends satisfy the seam.
var (
	_ VectorIndex = (*Index)(nil)
	_ VectorIndex = (*HNSWIndex)(nil)
)

// Index kind selectors for VectorIndexConfig.Index.
const (
	IndexBruteforce = "bruteforce"
	IndexHNSW       = "hnsw"
)

// HNSWParams are the HNSW graph-construction and search knobs (the graphi.yaml
// `vector.hnsw.*` keys; the literal yaml wiring lands in SW-085). Defaults follow
// the canonical HNSW paper recommendations (M=16, efConstruction=200) with a
// recall-favouring efSearch=64.
type HNSWParams struct {
	// M is the max number of bidirectional links per node per layer (layer 0 uses
	// 2*M). Higher M ⇒ better recall, more memory. Default 16.
	M int `json:"m"`
	// EfConstruction is the build-time candidate-list size. Higher ⇒ better graph
	// quality, slower build. Default 200.
	EfConstruction int `json:"ef_construction"`
	// EfSearch is the query-time candidate-list size. Higher ⇒ better recall,
	// slower query. Must be >= the query limit for good recall. Default 64.
	EfSearch int `json:"ef_search"`
}

// VectorIndexConfig selects and tunes the vector index backend. The zero/default
// value selects the brute-force backend, preserving the FU-3 / SW-059 behaviour
// (OFF-by-default contract: no HNSW unless explicitly opted in). It follows the
// in-engine value-struct config pattern (pdg.DefaultConfig, DefaultCloneConfig);
// the surface-level graphi.yaml key parsing lands in SW-085.
type VectorIndexConfig struct {
	// Index is "bruteforce" (default) or "hnsw".
	Index string `json:"index"`
	// HNSW holds the HNSW knobs; ignored when Index != "hnsw".
	HNSW HNSWParams `json:"hnsw"`
}

// DefaultVectorIndexConfig returns the OFF-by-default configuration: the
// brute-force backend with documented HNSW defaults pre-filled (so a caller that
// only flips Index to "hnsw" gets sane params).
func DefaultVectorIndexConfig() VectorIndexConfig {
	return VectorIndexConfig{
		Index: IndexBruteforce,
		HNSW:  HNSWParams{M: 16, EfConstruction: 200, EfSearch: 64},
	}
}

// InvalidConfig is the typed error returned by Validate / NewVectorIndex when a
// vector-index configuration value is out of contract. It carries the offending
// field and reason so a surface can fail fast with a precise message — never a
// panic and never a generic failure (AC7: fail-fast on bad config).
type InvalidConfig struct {
	Field  string
	Value  string
	Reason string
}

func (e *InvalidConfig) Error() string {
	return fmt.Sprintf("invalid vector config: field %q value %q: %s", e.Field, e.Value, e.Reason)
}

// Validate checks the configuration WITHOUT constructing anything or touching any
// vectors, so a bad config is rejected before any indexing work begins. An unknown
// index kind, or a non-positive HNSW parameter when the HNSW backend is selected,
// is an *InvalidConfig.
func (c VectorIndexConfig) Validate() error {
	switch c.Index {
	case "", IndexBruteforce:
		return nil
	case IndexHNSW:
		if c.HNSW.M < 1 {
			return &InvalidConfig{Field: "vector.hnsw.m", Value: itoa(c.HNSW.M), Reason: "must be >= 1"}
		}
		if c.HNSW.EfConstruction < 1 {
			return &InvalidConfig{Field: "vector.hnsw.ef_construction", Value: itoa(c.HNSW.EfConstruction), Reason: "must be >= 1"}
		}
		if c.HNSW.EfSearch < 1 {
			return &InvalidConfig{Field: "vector.hnsw.ef_search", Value: itoa(c.HNSW.EfSearch), Reason: "must be >= 1"}
		}
		return nil
	default:
		return &InvalidConfig{Field: "vector.index", Value: c.Index, Reason: "must be \"bruteforce\" or \"hnsw\""}
	}
}

// NewVectorIndex validates cfg (fail-fast) and constructs the selected backend.
// The default / empty / "bruteforce" selector returns the brute-force *Index, so
// the default build path never constructs HNSW (OFF-by-default contract, AC4/AC5).
func NewVectorIndex(cfg VectorIndexConfig) (VectorIndex, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Index {
	case IndexHNSW:
		return NewHNSWIndex(cfg.HNSW), nil
	default:
		return NewIndex(), nil
	}
}

// ---------------------------------------------------------------------------
// HNSW index
// ---------------------------------------------------------------------------

// hnswNode is one indexed vector plus its per-layer adjacency. neighbors[l] holds
// the NodeIds linked at layer l; index 0 is the base layer present for every node.
type hnswNode struct {
	id        model.NodeId
	vec       []float32
	norm      float64
	level     int
	neighbors [][]model.NodeId
}

// HNSWIndex is a deterministic, pure-Go (CGo-free) Hierarchical Navigable Small
// World approximate-nearest-neighbour index. It satisfies the same VectorIndex
// contract as the brute-force Index, so it is a drop-in upgrade behind the seam.
//
// Determinism (the load-bearing invariant): textbook HNSW draws node levels from
// an RNG and is sensitive to insertion order. This implementation removes BOTH
// sources of nondeterminism:
//
//   - Level assignment is a pure function of the NodeId (FNV-1a hash → uniform →
//     floor(-ln(u) * mL)); the same node always lands on the same level.
//   - Insertion is always in canonical NodeId-ascending order, and every
//     candidate/neighbor ordering breaks ties by NodeId ascending.
//
// So a rebuild over the same vectors yields a byte-identical graph and byte-identical
// query results across runs, processes, and full-vs-incremental indexes.
type HNSWIndex struct {
	params HNSWParams
	mL     float64 // level-generation normalisation factor 1/ln(M)
	nodes  map[model.NodeId]*hnswNode
	order  []model.NodeId // canonical NodeId order
	entry  model.NodeId   // current entry point (highest layer; NodeId-asc tie)
	hasEnt bool
}

// NewHNSWIndex returns an empty HNSW index with the given (already-normalised by
// the factory) parameters. Callers normally go through NewVectorIndex.
func NewHNSWIndex(p HNSWParams) *HNSWIndex {
	if p.M < 1 {
		p.M = 16
	}
	if p.EfConstruction < 1 {
		p.EfConstruction = 200
	}
	if p.EfSearch < 1 {
		p.EfSearch = 64
	}
	return &HNSWIndex{
		params: p,
		mL:     1.0 / math.Log(float64(p.M)),
		nodes:  make(map[model.NodeId]*hnswNode),
	}
}

// Rebuild replaces the graph from the durable table. Vectors arrive in canonical
// NodeId order (VectorTable.Load guarantees it); inserting in that order keeps the
// build a pure function of the vector set.
func (h *HNSWIndex) Rebuild(ctx context.Context, table VectorTable) error {
	vecs, err := table.Load(ctx)
	if err != nil {
		return err
	}
	h.nodes = make(map[model.NodeId]*hnswNode, len(vecs))
	h.order = h.order[:0]
	h.entry, h.hasEnt = "", false
	for _, v := range vecs {
		h.insert(v.NodeID, v.Values)
	}
	return nil
}

// Put inserts/updates a single vector. On update the node is removed and
// re-inserted so its adjacency is rebuilt consistently.
func (h *HNSWIndex) Put(id model.NodeId, values []float32) {
	if _, exists := h.nodes[id]; exists {
		h.remove(id)
	}
	cp := make([]float32, len(values))
	copy(cp, values)
	h.insert(id, cp)
}

// Len returns the number of indexed vectors.
func (h *HNSWIndex) Len() int { return len(h.nodes) }

// levelFor derives a deterministic level for id: floor(-ln(u) * mL) where u is a
// stable uniform in (0,1] hashed from the NodeId. No RNG ⇒ reproducible topology.
func (h *HNSWIndex) levelFor(id model.NodeId) int {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(id))
	// Map the 64-bit hash to u in (0,1]; +1 avoids u==0 (ln(0) = -Inf).
	u := (float64(sum.Sum64()>>11) + 1.0) / float64(uint64(1)<<53)
	lvl := int(math.Floor(-math.Log(u) * h.mL))
	if lvl < 0 {
		lvl = 0
	}
	return lvl
}

// insert adds id with its vector, wiring neighbors layer by layer following the
// standard HNSW greedy-descent + ef_construction beam search, with all tie-breaks
// resolved by NodeId so the result is order-independent.
func (h *HNSWIndex) insert(id model.NodeId, vec []float32) {
	cp := make([]float32, len(vec))
	copy(cp, vec)
	lvl := h.levelFor(id)
	n := &hnswNode{
		id:        id,
		vec:       cp,
		norm:      norm(cp),
		level:     lvl,
		neighbors: make([][]model.NodeId, lvl+1),
	}
	h.nodes[id] = n
	// maintain canonical order slice
	idx := sort.Search(len(h.order), func(i int) bool { return h.order[i] >= id })
	h.order = append(h.order, "")
	copy(h.order[idx+1:], h.order[idx:])
	h.order[idx] = id

	if !h.hasEnt {
		h.entry, h.hasEnt = id, true
		return
	}

	ep := h.entry
	epLevel := h.nodes[ep].level
	// Phase 1: greedy descent from entry to the layer just above the new node's top.
	for l := epLevel; l > lvl; l-- {
		ep = h.greedyClosest(vec, ep, l)
	}
	// Phase 2: at each layer from min(lvl, epLevel) down to 0, beam-search for
	// neighbors and connect bidirectionally.
	start := lvl
	if epLevel < start {
		start = epLevel
	}
	for l := start; l >= 0; l-- {
		cands := h.searchLayer(vec, []model.NodeId{ep}, l, h.params.EfConstruction)
		m := h.params.M
		if l == 0 {
			m = 2 * h.params.M
		}
		selected := h.selectNeighbors(cands, m)
		n.neighbors[l] = selected
		for _, nb := range selected {
			h.connect(nb, id, l)
		}
		if len(cands) > 0 {
			ep = cands[0].id // closest, for the next layer down
		}
	}

	// Promote entry point if the new node sits higher (NodeId-asc tie-break).
	if lvl > h.nodes[h.entry].level || (lvl == h.nodes[h.entry].level && id < h.entry) {
		h.entry = id
	}
}

// connect adds `to` to `from`'s neighbor list at layer l, then prunes `from`'s
// list back to its M budget by keeping the closest (NodeId-asc tie-break) so the
// graph stays bounded and deterministic.
func (h *HNSWIndex) connect(from, to model.NodeId, l int) {
	fn := h.nodes[from]
	if l >= len(fn.neighbors) {
		return
	}
	for _, e := range fn.neighbors[l] {
		if e == to {
			return
		}
	}
	fn.neighbors[l] = append(fn.neighbors[l], to)
	m := h.params.M
	if l == 0 {
		m = 2 * h.params.M
	}
	if len(fn.neighbors[l]) <= m {
		return
	}
	// Prune: keep the m closest neighbors to `from`.
	cand := make([]scored, 0, len(fn.neighbors[l]))
	for _, e := range fn.neighbors[l] {
		cand = append(cand, scored{id: e, score: h.sim(fn.vec, fn.norm, e)})
	}
	sortScored(cand)
	kept := make([]model.NodeId, 0, m)
	for i := 0; i < m && i < len(cand); i++ {
		kept = append(kept, cand[i].id)
	}
	fn.neighbors[l] = kept
}

// selectNeighbors keeps the m highest-similarity candidates (already ordered by
// searchLayer: score desc, NodeId asc).
func (h *HNSWIndex) selectNeighbors(cands []scored, m int) []model.NodeId {
	out := make([]model.NodeId, 0, m)
	for i := 0; i < len(cands) && i < m; i++ {
		out = append(out, cands[i].id)
	}
	return out
}

// greedyClosest walks the graph at layer l from ep, always stepping to the
// strictly-more-similar neighbor, until no neighbor improves. Deterministic: ties
// never trigger a move, and neighbor lists are stable.
func (h *HNSWIndex) greedyClosest(q []float32, ep model.NodeId, l int) model.NodeId {
	qn := norm(q)
	cur := ep
	curSim := h.simQ(q, qn, cur)
	for {
		improved := false
		cn := h.nodes[cur]
		if l < len(cn.neighbors) {
			for _, nb := range cn.neighbors[l] {
				if s := h.simQ(q, qn, nb); s > curSim || (s == curSim && nb < cur) {
					cur, curSim, improved = nb, s, true
				}
			}
		}
		if !improved {
			return cur
		}
	}
}

// searchLayer is the HNSW beam search at one layer: it expands the ef best
// candidates from the entry set and returns them ordered (score desc, NodeId asc).
func (h *HNSWIndex) searchLayer(q []float32, entries []model.NodeId, l, ef int) []scored {
	qn := norm(q)
	visited := make(map[model.NodeId]struct{}, ef*2)
	// candidate max-heap by similarity (best to expand next); result min-heap by
	// similarity (worst easily evictable).
	cand := &maxHeap{}
	res := &minHeap{}
	heap.Init(cand)
	heap.Init(res)
	for _, e := range entries {
		s := h.simQ(q, qn, e)
		visited[e] = struct{}{}
		heap.Push(cand, scored{id: e, score: s})
		heap.Push(res, scored{id: e, score: s})
	}
	for cand.Len() > 0 {
		c := heap.Pop(cand).(scored)
		worst := (*res)[0]
		if res.Len() >= ef && c.score < worst.score {
			break
		}
		cn := h.nodes[c.id]
		if l < len(cn.neighbors) {
			for _, nb := range cn.neighbors[l] {
				if _, seen := visited[nb]; seen {
					continue
				}
				visited[nb] = struct{}{}
				s := h.simQ(q, qn, nb)
				worst = (*res)[0]
				if res.Len() < ef || s > worst.score || (s == worst.score && nb < worst.id) {
					heap.Push(cand, scored{id: nb, score: s})
					heap.Push(res, scored{id: nb, score: s})
					if res.Len() > ef {
						heap.Pop(res)
					}
				}
			}
		}
	}
	out := make([]scored, 0, res.Len())
	for res.Len() > 0 {
		out = append(out, heap.Pop(res).(scored))
	}
	sortScored(out)
	return out
}

// Search ranks indexed vectors by cosine similarity to query, returning up to
// limit hits (score desc, NodeId asc). It runs greedy descent to layer 0 then a
// beam search with ef = max(EfSearch, limit), so recall is bounded by ef. Empty
// index returns no hits; a non-positive limit returns all beam results.
func (h *HNSWIndex) Search(query []float32, limit int) []Hit {
	if !h.hasEnt || len(h.nodes) == 0 {
		return nil
	}
	ep := h.entry
	epLevel := h.nodes[ep].level
	for l := epLevel; l > 0; l-- {
		ep = h.greedyClosest(query, ep, l)
	}
	ef := h.params.EfSearch
	if limit > ef {
		ef = limit
	}
	cands := h.searchLayer(query, []model.NodeId{ep}, 0, ef)
	hits := make([]Hit, 0, len(cands))
	for _, c := range cands {
		hits = append(hits, Hit{NodeID: c.id, Score: c.score})
	}
	// cands already (score desc, NodeId asc); enforce defensively at the boundary.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].NodeID < hits[j].NodeID
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// remove deletes id from the graph and scrubs it from every neighbor list. Used by
// Put on update; rare, so an O(N) scrub is acceptable and keeps determinism simple.
func (h *HNSWIndex) remove(id model.NodeId) {
	delete(h.nodes, id)
	if idx := sort.Search(len(h.order), func(i int) bool { return h.order[i] >= id }); idx < len(h.order) && h.order[idx] == id {
		h.order = append(h.order[:idx], h.order[idx+1:]...)
	}
	for _, n := range h.nodes {
		for l := range n.neighbors {
			scrubbed := n.neighbors[l][:0]
			for _, nb := range n.neighbors[l] {
				if nb != id {
					scrubbed = append(scrubbed, nb)
				}
			}
			n.neighbors[l] = scrubbed
		}
	}
	if h.entry == id {
		h.entry, h.hasEnt = "", false
		// recompute entry: highest level, NodeId-asc tie
		for _, nid := range h.order {
			if !h.hasEnt {
				h.entry, h.hasEnt = nid, true
				continue
			}
			if h.nodes[nid].level > h.nodes[h.entry].level {
				h.entry = nid
			}
		}
	}
}

// sim returns cosine similarity between the vector of node `to` and a source
// vector whose values+norm are supplied (used during construction/pruning).
func (h *HNSWIndex) sim(srcVec []float32, srcNorm float64, to model.NodeId) float64 {
	tn := h.nodes[to]
	if tn == nil {
		return 0
	}
	return cosine(srcVec, tn.vec, srcNorm)
}

// simQ returns cosine similarity between query (norm qn) and node id's vector.
func (h *HNSWIndex) simQ(q []float32, qn float64, id model.NodeId) float64 {
	tn := h.nodes[id]
	if tn == nil {
		return 0
	}
	return cosine(q, tn.vec, qn)
}

// scored is a (NodeId, similarity) pair used throughout construction and search.
type scored struct {
	id    model.NodeId
	score float64
}

// sortScored orders by score DESC, then NodeId ASC — the canonical deterministic
// ranking used everywhere in this file.
func sortScored(s []scored) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].score != s[j].score {
			return s[i].score > s[j].score
		}
		return s[i].id < s[j].id
	})
}

// maxHeap pops the highest-similarity candidate first (NodeId-asc tie-break),
// driving beam expansion toward the best frontier.
type maxHeap []scored

func (h maxHeap) Len() int { return len(h) }
func (h maxHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score > h[j].score
	}
	return h[i].id < h[j].id
}
func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x any)   { *h = append(*h, x.(scored)) }
func (h *maxHeap) Pop() any     { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

// minHeap pops the lowest-similarity result first (NodeId-desc tie-break so the
// element evicted on overflow is the worst AND highest-NodeId), keeping the kept
// set deterministic.
type minHeap []scored

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score < h[j].score
	}
	return h[i].id > h[j].id
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)   { *h = append(*h, x.(scored)) }
func (h *minHeap) Pop() any     { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

// itoa is a tiny dependency-free int→string for InvalidConfig messages.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
