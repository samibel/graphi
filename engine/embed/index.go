package embed

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/samibel/graphi/core/model"
)

// Vector is a node embedding keyed by NodeId. It is the in-memory index's
// internal row shape — a NodeId plus its Dim-length vector. The
// GenerationStore handles durable persistence; the index is rebuilt from a
// Load-ed []Vector by Rebuild.
//
// Why a separate Vector type (rather than reusing Row): the in-memory index
// does not need a generation id, document id, text hash, path, lines or
// span method to rank by cosine similarity. Carrying only the NodeId +
// vector keeps the index's working set small and matches what HNSW needs
// to construct its graph.
type Vector struct {
	NodeID model.NodeId
	Values []float32
}

// Index is a brute-force cosine-similarity index over an in-memory
// map[NodeId][]float32. It is rebuilt from a []Vector by Rebuild (a pure
// local operation: no I/O, no embedder, no network). HNSW (sub-linear ANN)
// is an explicit follow-up; brute force is intended and acceptable for FU-3.
//
// Concurrency: safe for concurrent reads (Search) and incremental writes
// (Put) — the underlying map is mutex-protected.
type Index struct {
	mu    sync.RWMutex
	byID  map[model.NodeId][]float32
	order []model.NodeId // canonical NodeId order for deterministic ranking ties
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{byID: make(map[model.NodeId][]float32)}
}

// Rebuild replaces the index contents from the given rows. The rows MUST
// be in canonical NodeId order (GenerationStore.Load guarantees this), but
// the function does not depend on it — it just inserts every row. Callers
// load rows from the store, optionally filter / merge for carry-forward,
// and pass the result here.
func (ix *Index) Rebuild(_ context.Context, rows []Vector) error {
	byID := make(map[model.NodeId][]float32, len(rows))
	order := make([]model.NodeId, 0, len(rows))
	for _, v := range rows {
		cp := make([]float32, len(v.Values))
		copy(cp, v.Values)
		byID[v.NodeID] = cp
		order = append(order, v.NodeID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	ix.mu.Lock()
	ix.byID = byID
	ix.order = order
	ix.mu.Unlock()
	return nil
}

// Put inserts/updates a single vector in the in-memory index (used after an
// incremental embed). It does NOT persist; callers Upsert to the durable
// store separately.
func (ix *Index) Put(id model.NodeId, values []float32) {
	cp := make([]float32, len(values))
	copy(cp, values)
	ix.mu.Lock()
	if _, exists := ix.byID[id]; !exists {
		// ix.order is kept sorted, so insert at the binary-search position in
		// O(N) rather than re-sorting the whole slice on every Put.
		idx := sort.Search(len(ix.order), func(i int) bool { return ix.order[i] >= id })
		ix.order = append(ix.order, "")
		copy(ix.order[idx+1:], ix.order[idx:])
		ix.order[idx] = id
	}
	ix.byID[id] = cp
	ix.mu.Unlock()
}

// Len returns the number of indexed vectors.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.byID)
}

// Hit is one ranked semantic-search result: a NodeId and its cosine score.
type Hit struct {
	NodeID model.NodeId
	Score  float64
}

// Search ranks indexed vectors by cosine similarity to query and returns up to
// limit hits, ordered by score DESCENDING with a deterministic NodeId-ascending
// tie-break. A non-positive limit returns all hits. It performs no I/O and no
// network.
func (ix *Index) Search(query []float32, limit int) []Hit {
	ix.mu.RLock()
	order := make([]model.NodeId, len(ix.order))
	copy(order, ix.order)
	hits := make([]Hit, 0, len(order))
	qn := norm(query)
	for _, id := range order {
		v := ix.byID[id]
		hits = append(hits, Hit{NodeID: id, Score: cosine(query, v, qn)})
	}
	ix.mu.RUnlock()

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score // higher cosine first
		}
		return hits[i].NodeID < hits[j].NodeID // deterministic tie-break
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// norm returns the L2 norm of v.
func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

// cosine returns the cosine similarity of a and b, given a's precomputed norm.
// Mismatched lengths or a zero-norm operand yield 0 (no match), never a panic.
func cosine(a, b []float32, aNorm float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, bSq float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	for _, x := range b {
		bSq += float64(x) * float64(x)
	}
	bNorm := math.Sqrt(bSq)
	if aNorm == 0 || bNorm == 0 {
		return 0
	}
	return dot / (aNorm * bNorm)
}
