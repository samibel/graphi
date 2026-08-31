package embed

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/samibel/graphi/core/model"
)

// Vector is a node embedding keyed by NodeId. It is the in-memory index's
// internal row shape — a NodeId, the embedding-space document id from
// SW-260's SemanticDocument.DocumentID, and its Dim-length vector. The
// GenerationStore handles durable persistence; the index is rebuilt from a
// Load-ed []Vector by Rebuild. DocumentID is the document identity the
// retrieval module's hierarchical dedupe key (AC-2) consumes; it is
// carried here so Search can return it without a second store lookup.
//
// Why a separate Vector type (rather than reusing Row): the in-memory index
// does not need a generation id, text hash, path, lines or span method to
// rank by cosine similarity. Carrying NodeId + DocumentID + vector keeps
// the index's working set small and matches what HNSW needs to construct
// its graph.
type Vector struct {
	NodeID     model.NodeId
	DocumentID string
	Values     []float32
}

// Index is a brute-force cosine-similarity index over an in-memory
// map[NodeId]indexedVector, where indexedVector carries the cosine source
// vector and the embedding-space document id the SW-260/SW-261 GenerationStore
// produced it from. It is rebuilt from a []Vector by Rebuild (a pure local
// operation: no I/O, no embedder, no network). HNSW (sub-linear ANN) is an
// explicit follow-up; brute force is intended and acceptable for FU-3.
//
// Concurrency: safe for concurrent reads (Search) and incremental writes
// (Put) — the underlying map is mutex-protected.
type Index struct {
	mu         sync.RWMutex
	byID       map[model.NodeId]indexedVector
	docByID    map[model.NodeId]string
	valuesByID map[model.NodeId][]float32 // separate handle for Put backward-compat
	order      []model.NodeId             // canonical NodeId order for deterministic ranking ties
}

// indexedVector is the per-node entry the index stores: the cosine source
// vector and the embedding-space document id the GenerationStore persisted
// with it. Two parallel maps exist so Put (the legacy increment path,
// which does not carry a DocumentID) can update the cosine source without
// erasing the document id from a prior rebuild.
type indexedVector struct {
	values     []float32
	documentID string
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{
		byID:       make(map[model.NodeId]indexedVector),
		docByID:    make(map[model.NodeId]string),
		valuesByID: make(map[model.NodeId][]float32),
	}
}

// Rebuild replaces the index contents from the given rows. The rows MUST
// be in canonical NodeId order (GenerationStore.Load guarantees this), but
// the function does not depend on it — it just inserts every row. Callers
// load rows from the store, optionally filter / merge for carry-forward,
// and pass the result here. The DocumentID on each Vector is the v2
// embedding-space identity the GenerationStore persisted alongside the
// vector; the retrieval module consumes it for the AC-2 hierarchical
// dedupe key.
func (ix *Index) Rebuild(_ context.Context, rows []Vector) error {
	byID := make(map[model.NodeId]indexedVector, len(rows))
	docByID := make(map[model.NodeId]string, len(rows))
	valuesByID := make(map[model.NodeId][]float32, len(rows))
	order := make([]model.NodeId, 0, len(rows))
	for _, v := range rows {
		cp := make([]float32, len(v.Values))
		copy(cp, v.Values)
		byID[v.NodeID] = indexedVector{values: cp, documentID: v.DocumentID}
		docByID[v.NodeID] = v.DocumentID
		valuesByID[v.NodeID] = cp
		order = append(order, v.NodeID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	ix.mu.Lock()
	ix.byID = byID
	ix.docByID = docByID
	ix.valuesByID = valuesByID
	ix.order = order
	ix.mu.Unlock()
	return nil
}

// Put inserts/updates a single vector in the in-memory index (used after an
// incremental embed). It does NOT persist; callers Upsert to the durable
// store separately. Put does not take a DocumentID: the incremental path
// carries it through Upsert to the durable store and the next Rebuild
// reads it back. A subsequent Put on a node that already has a DocumentID
// (from a prior Rebuild) preserves that id.
func (ix *Index) Put(id model.NodeId, values []float32) {
	cp := make([]float32, len(values))
	copy(cp, values)
	ix.mu.Lock()
	prev, hadPrev := ix.byID[id]
	if _, exists := ix.byID[id]; !exists {
		// ix.order is kept sorted, so insert at the binary-search position in
		// O(N) rather than re-sorting the whole slice on every Put.
		idx := sort.Search(len(ix.order), func(i int) bool { return ix.order[i] >= id })
		ix.order = append(ix.order, "")
		copy(ix.order[idx+1:], ix.order[idx:])
		ix.order[idx] = id
	}
	docID := ""
	if hadPrev {
		docID = prev.documentID
	}
	ix.byID[id] = indexedVector{values: cp, documentID: docID}
	ix.valuesByID[id] = cp
	if docID != "" {
		ix.docByID[id] = docID
	}
	ix.mu.Unlock()
}

// Len returns the number of indexed vectors.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.byID)
}

// Hit is one ranked semantic-search result: a NodeId, the embedding-space
// document id the GenerationStore persisted with its vector (the SW-260
// SemanticDocument.DocumentID), and the cosine score the index computed.
// DocumentID is empty when the indexed row carried no document id (e.g. a
// legacy test fixture built without a GenerationStore round-trip).
type Hit struct {
	NodeID     model.NodeId
	DocumentID string
	Score      float64
}

// Search ranks indexed vectors by QUANTISED cosine similarity (AC-3) and
// returns up to limit hits, ordered by quantised score DESCENDING with a
// deterministic NodeId-ascending tie-break. A non-positive limit returns
// all hits. The quantised order is the AC-3 contract: cosine values are
// rounded to int(cos*10000) BEFORE the ordering key fires and BEFORE the
// limit truncates the list, so two floats within 5e-5 (one quantisation
// unit) are ties that resolve on canonical NodeId — the byte-stable
// ordering the retrieval module's hierarchical dedupe depends on.
//
// The cosine float is still carried on the returned Hit so downstream
// callers that need the unbounded value (the harness, debug paths) can
// read it; what changes here is the order, not the value.
//
// Search performs no I/O and no network.
func (ix *Index) Search(query []float32, limit int) []Hit {
	ix.mu.RLock()
	order := make([]model.NodeId, len(ix.order))
	copy(order, ix.order)
	byID := ix.byID
	hits := make([]Hit, 0, len(order))
	qn := norm(query)
	for _, id := range order {
		entry := byID[id]
		hits = append(hits, Hit{NodeID: id, DocumentID: entry.documentID, Score: cosine(query, entry.values, qn)})
	}
	ix.mu.RUnlock()

	// AC-3: order by quantised cosine (int(round(cos*10000))) desc, then
	// canonical NodeId asc. The truncation to `limit` happens AFTER this
	// order so a quantised tie that straddles the rank-50 cutoff selects
	// by NodeId rather than by a difference the contract says is not
	// there. The 5e-5 epsilon from the AC-3 spec corresponds to one full
	// unit at the 10000x scale, so two floats quantise equal iff their
	// difference is strictly less than 5e-5.
	sort.SliceStable(hits, func(i, j int) bool {
		qi, qj := quantiseCosine(hits[i].Score), quantiseCosine(hits[j].Score)
		if qi != qj {
			return qi > qj
		}
		return hits[i].NodeID < hits[j].NodeID
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// quantiseCosine is the AC-3 quantisation factor: int(round(cos*10000)),
// with NaN/+Inf folded to 0 and the cosine clamped to [-1, 1] before scaling.
// Kept here (the canonical site) and in engine/retrieval.QuantiseScore
// (the cross-package reusable form). They MUST agree: the retrieval
// module's union stage reads the canonical value the index produced, so
// any drift would silently re-rank rows the AC-3 contract tied.
func quantiseCosine(cos float64) int {
	if math.IsNaN(cos) || math.IsInf(cos, 0) {
		return 0
	}
	if cos > 1.0 {
		cos = 1.0
	} else if cos < -1.0 {
		cos = -1.0
	}
	scaled := cos * 10000
	if scaled >= 0 {
		return int(scaled + 0.5)
	}
	return -int(-scaled + 0.5)
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
