package graphstore

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/samibel/graphi/core/model"
)

func TestEdgeIDTreeHighDegreeWritesStayBalanced(t *testing.T) {
	// Random insertion order is the regression shape that turns a sorted-slice
	// adjacency index into O(n²) copying. The deterministic treap must remain
	// shallow while still serving only the requested canonical prefix.
	const count = 100_000
	order := rand.New(rand.NewSource(1)).Perm(count) //nolint:gosec // deterministic structural test, not security
	tree := &edgeIDTree{}
	for _, value := range order {
		tree.insert(model.EdgeId(fmt.Sprintf("%016x", value)))
	}
	if tree.len() != count {
		t.Fatalf("tree len = %d, want %d", tree.len(), count)
	}
	if height := edgeIDTreeHeight(tree.root); height > 128 {
		t.Fatalf("high-degree tree height = %d for %d IDs; ordered write index is not logarithmic", height, count)
	}
	prefix := tree.first(32)
	if len(prefix) != 32 {
		t.Fatalf("prefix len = %d, want 32", len(prefix))
	}
	for i, id := range prefix {
		want := model.EdgeId(fmt.Sprintf("%016x", i))
		if id != want {
			t.Fatalf("prefix[%d] = %s, want %s", i, id, want)
		}
	}
	for i := 0; i < 1_000; i++ {
		tree.delete(model.EdgeId(fmt.Sprintf("%016x", i)))
	}
	if tree.len() != count-1_000 {
		t.Fatalf("tree len after delete = %d, want %d", tree.len(), count-1_000)
	}
}

func edgeIDTreeHeight(root *edgeIDTreeNode) int {
	if root == nil {
		return 0
	}
	left := edgeIDTreeHeight(root.left)
	right := edgeIDTreeHeight(root.right)
	if right > left {
		left = right
	}
	return left + 1
}

func TestMemBoundedUnfilteredHighKindCardinality(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	defer store.Close()
	hub, err := model.NewNode("function", "manykinds.Hub", "manykinds.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, hub); err != nil {
		t.Fatal(err)
	}

	const degree = 10_000
	var first model.Edge
	for i := 0; i < degree; i++ {
		leaf, err := model.NewNode("function", fmt.Sprintf("manykinds.Leaf%05d", i), "manykinds.go", i+2, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, leaf); err != nil {
			t.Fatal(err)
		}
		edge, err := model.NewEdge(hub.ID(), leaf.ID(), fmt.Sprintf("kind-%05d", i), model.TierDerived, 1, "many kinds", []string{"manykinds.go:1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, edge); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = edge
		}
	}

	edges, truncated, err := store.OutgoingBounded(ctx, hub.ID(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(edges) != 1 || edges[0].ID() != first.ID() {
		t.Fatalf("limit=1 over %d unique kinds = %v, truncated=%v; want first (kind,id) edge and true", degree, edges, truncated)
	}
	kinds := store.outKinds[hub.ID()]
	if kinds == nil || kinds.len() != degree {
		t.Fatalf("ordered distinct-kind index len = %d, want %d", kinds.len(), degree)
	}
	if height := kindTreeHeight(kinds.root); height > 128 {
		t.Fatalf("distinct-kind tree height = %d for %d kinds; limit=1 would not stay logarithmic", height, degree)
	}
}

func kindTreeHeight(root *kindTreeNode) int {
	if root == nil {
		return 0
	}
	left := kindTreeHeight(root.left)
	right := kindTreeHeight(root.right)
	if right > left {
		left = right
	}
	return left + 1
}
