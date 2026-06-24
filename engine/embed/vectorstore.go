package embed

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/samibel/graphi/core/model"
)

// Vector is a node embedding keyed by NodeId. It is the durable-table row shape:
// a NodeId plus its Dim-length vector. The durable layer stores exactly these
// rows; the in-memory index is rebuilt from them.
type Vector struct {
	NodeID model.NodeId `json:"node_id"`
	Values []float32    `json:"values"`
}

// VectorTable is the durable persistence seam for node vectors (the "durable
// table" of the plan). It is deliberately minimal: an Upsert/Load pair keyed by
// NodeId. graphstore is NOT extended with a vectors column (architect A1); a
// dedicated sidecar implementation satisfies this interface. MemVectorTable is the
// pure-Go in-memory reference used by tests and the default path.
//
// Embedding is gated behind explicit opt-in + `graphi index --semantic`; nothing
// here is touched on the default ingest path.
type VectorTable interface {
	// Upsert durably stores (or replaces) the vector for v.NodeID.
	Upsert(ctx context.Context, v Vector) error
	// Load returns every stored vector, in canonical NodeId order.
	Load(ctx context.Context) ([]Vector, error)
}

// MemVectorTable is an in-memory VectorTable (pure Go, no heavy deps). It is the
// reference durable seam used in tests and as the rebuild source for the
// in-memory index. Safe for concurrent use.
type MemVectorTable struct {
	mu sync.RWMutex
	m  map[model.NodeId][]float32
}

// NewMemVectorTable returns an empty in-memory vector table.
func NewMemVectorTable() *MemVectorTable {
	return &MemVectorTable{m: make(map[model.NodeId][]float32)}
}

// Upsert implements VectorTable.
func (t *MemVectorTable) Upsert(_ context.Context, v Vector) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]float32, len(v.Values))
	copy(cp, v.Values)
	t.m[v.NodeID] = cp
	return nil
}

// Load implements VectorTable, returning rows in canonical NodeId order.
func (t *MemVectorTable) Load(_ context.Context) ([]Vector, error) {
	t.mu.RLock()
	out := make([]Vector, 0, len(t.m))
	for id, vals := range t.m {
		cp := make([]float32, len(vals))
		copy(cp, vals)
		out = append(out, Vector{NodeID: id, Values: cp})
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

// Index is a brute-force cosine-similarity index over an in-memory
// map[NodeId][]float32, rebuilt from a durable VectorTable (the plan's "sidecar
// brute-force cosine index"). It is pure Go with no heavy deps. HNSW (sub-linear
// ANN) is an explicit follow-up; brute force is intended and acceptable for FU-3.
type Index struct {
	mu    sync.RWMutex
	byID  map[model.NodeId][]float32
	order []model.NodeId // canonical NodeId order for deterministic ranking ties
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{byID: make(map[model.NodeId][]float32)}
}

// Rebuild loads every vector from the durable table and replaces the in-memory
// index. It is the rebuild-from-durable path; the cache is never authoritative.
func (ix *Index) Rebuild(ctx context.Context, table VectorTable) error {
	vecs, err := table.Load(ctx)
	if err != nil {
		return err
	}
	byID := make(map[model.NodeId][]float32, len(vecs))
	order := make([]model.NodeId, 0, len(vecs))
	for _, v := range vecs {
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
// incremental embed). It does NOT persist; callers Upsert to the durable table
// separately.
func (ix *Index) Put(id model.NodeId, values []float32) {
	cp := make([]float32, len(values))
	copy(cp, values)
	ix.mu.Lock()
	if _, exists := ix.byID[id]; !exists {
		ix.order = append(ix.order, id)
		sort.Slice(ix.order, func(i, j int) bool { return ix.order[i] < ix.order[j] })
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
