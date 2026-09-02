package embed_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// Index.Search ranks by cosine descending with a deterministic NodeId tie-break,
// rebuilt from a slice of vectors.
func TestIndex_RebuildAndRank(t *testing.T) {
	ctx := context.Background()
	// Three nodes; node "b" aligns exactly with the query, "a" is orthogonal-ish,
	// "c" is the negative direction.
	rows := []embed.Vector{
		{NodeID: model.NodeId("a"), Values: []float32{0, 1}},
		{NodeID: model.NodeId("b"), Values: []float32{1, 0}},
		{NodeID: model.NodeId("c"), Values: []float32{-1, 0}},
	}
	ix := embed.NewIndex()
	if err := ix.Rebuild(ctx, rows); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if ix.Len() != 3 {
		t.Fatalf("Len = %d, want 3", ix.Len())
	}
	hits := ix.Search([]float32{1, 0}, 0)
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3", len(hits))
	}
	// b (cos=1) first, a (cos=0) second, c (cos=-1) last.
	if hits[0].NodeID != "b" || hits[2].NodeID != "c" {
		t.Fatalf("ranking = %v, want b first, c last", []model.NodeId{hits[0].NodeID, hits[1].NodeID, hits[2].NodeID})
	}
	// Determinism: repeated runs identical.
	hits2 := ix.Search([]float32{1, 0}, 0)
	for i := range hits {
		if hits[i].NodeID != hits2[i].NodeID || hits[i].Score != hits2[i].Score {
			t.Fatalf("non-deterministic ranking at %d", i)
		}
	}
}

// Tie-break: equal scores order by NodeId ascending.
func TestIndex_TieBreak(t *testing.T) {
	ix := embed.NewIndex()
	// Two identical vectors ⇒ identical cosine ⇒ NodeId tie-break.
	ix.Put(model.NodeId("zzz"), []float32{1, 0})
	ix.Put(model.NodeId("aaa"), []float32{1, 0})
	hits := ix.Search([]float32{1, 0}, 0)
	if len(hits) != 2 || hits[0].NodeID != "aaa" || hits[1].NodeID != "zzz" {
		t.Fatalf("tie-break = %v, want aaa before zzz", hits)
	}
}

// Limit truncates after ranking.
func TestIndex_Limit(t *testing.T) {
	ix := embed.NewIndex()
	ix.Put(model.NodeId("a"), []float32{1, 0})
	ix.Put(model.NodeId("b"), []float32{0, 1})
	if hits := ix.Search([]float32{1, 0}, 1); len(hits) != 1 {
		t.Fatalf("limit=1 returned %d hits", len(hits))
	}
}

// AC-3 / SW-263 review item 2: a quantised tie that straddles the rank-cutoff
// must be broken on canonical NodeId, NOT on the float cosine. The previous
// implementation sorted on float then truncated, so a tie that the contract
// says is a tie could survive or be dropped on a difference the contract
// forbids. The fix quantises BEFORE the ordering AND the truncation. This
// test pins the corrected behaviour: with limit=2 and three vectors whose
// pair (a, b) quantises to the same value but order by float as (b, a),
// the truncated top-2 must contain the NodeId-ascending first one (a).
func TestIndex_QuantisedTieBreaksAcrossTruncationBoundary(t *testing.T) {
	ix := embed.NewIndex()
	// Three vectors in 1-D. Their cosines with query [1,0] are exactly
	// the values themselves. Pick floats where (a, b) quantise equal at
	// factor 10000, and (a) ranks higher by NodeId (a < b), but the
	// float-derived order has b first (because b's float is larger).
	//
	//   a  -> cos=0.90001 -> quantised 9000  (NodeId "aaa", lowest)
	//   b  -> cos=0.90003 -> quantised 9000  (NodeId "bbb", tied with a)
	//   c  -> cos=0.95    -> quantised 9500  (NodeId "ccc", highest)
	//
	// Float order would be (b, a, c) — b's larger float puts it ahead of a.
	// The AC-3 contract ties them (both quantise to 9000) and breaks on
	// NodeId ascending: (a, b, c). With limit=2 the truncated top must
	// be (a, b), preserving a (the NodeId-ascending first) at the front.
	ix.Put(model.NodeId("bbb"), []float32{0.90003})
	ix.Put(model.NodeId("aaa"), []float32{0.90001})
	ix.Put(model.NodeId("ccc"), []float32{0.95})
	hits := ix.Search([]float32{1}, 2)
	if len(hits) != 2 {
		t.Fatalf("limit=2 returned %d hits", len(hits))
	}
	// AC-3: quantised order is (a, b, c) — a precedes b on NodeId tie-break.
	// After truncation to 2, we must have (a, b) NOT (b, a). The previous
	// (float-order-then-truncate) implementation would have returned (b, c)
	// — the float-derived order placed b ahead of a and then dropped a.
	if hits[0].NodeID != "aaa" {
		t.Errorf("rank 1 = %s, want aaa (AC-3: quantised tie broken on canonical NodeId, then truncated)",
			hits[0].NodeID)
	}
	if hits[1].NodeID != "bbb" {
		t.Errorf("rank 2 = %s, want bbb (AC-3: quantised tie preserves b in the second slot)",
			hits[1].NodeID)
	}
}

// DocumentID plumbs through Rebuild and Search (SW-263 review / AC-2 item 1):
// a row's DocumentID is the embedding-space document id the GenerationStore
// persisted alongside the vector. Search must surface it on each hit.
func TestIndex_DocumentIDSurfacesInSearch(t *testing.T) {
	ctx := context.Background()
	rows := []embed.Vector{
		{NodeID: model.NodeId("a"), DocumentID: "doc-a", Values: []float32{1, 0}},
		{NodeID: model.NodeId("b"), DocumentID: "doc-b-shared", Values: []float32{0, 1}},
		{NodeID: model.NodeId("c"), DocumentID: "doc-b-shared", Values: []float32{0, 1}},
	}
	ix := embed.NewIndex()
	if err := ix.Rebuild(ctx, rows); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	hits := ix.Search([]float32{1, 0}, 0)
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3", len(hits))
	}
	gotDocs := map[model.NodeId]string{}
	for _, h := range hits {
		gotDocs[h.NodeID] = h.DocumentID
	}
	want := map[model.NodeId]string{
		"a": "doc-a",
		"b": "doc-b-shared",
		"c": "doc-b-shared",
	}
	for id, d := range want {
		if gotDocs[id] != d {
			t.Errorf("hit %s DocumentID = %q, want %q", id, gotDocs[id], d)
		}
	}
	// b and c share the same DocumentID but are distinct rows — the v2
	// "multiple nodes share one document" case the retrieval module's
	// hierarchical dedupe key (AC-2) must preserve.
	if len(hits) != 3 {
		t.Errorf("shared-document nodes merged into one: %d hits, want 3", len(hits))
	}
}
