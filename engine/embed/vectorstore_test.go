package embed_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// Index.Search ranks by cosine descending with a deterministic NodeId tie-break,
// rebuilt from a durable table.
func TestIndex_RebuildAndRank(t *testing.T) {
	ctx := context.Background()
	table := embed.NewMemVectorTable()
	// Three nodes; node "b" aligns exactly with the query, "a" is orthogonal-ish,
	// "c" is the negative direction.
	rows := []embed.Vector{
		{NodeID: model.NodeId("a"), Values: []float32{0, 1}},
		{NodeID: model.NodeId("b"), Values: []float32{1, 0}},
		{NodeID: model.NodeId("c"), Values: []float32{-1, 0}},
	}
	for _, r := range rows {
		if err := table.Upsert(ctx, r); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	ix := embed.NewIndex()
	if err := ix.Rebuild(ctx, table); err != nil {
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
