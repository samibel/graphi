package ingest_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/ingest"
)

// indexWithProfile ingests a fixture under a specific profile and returns the store.
func indexWithProfile(t *testing.T, repo string, p profile.Profile) graphstore.Graphstore {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	i.WithProfile(p)
	if err := i.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll %s: %v", p, err)
	}
	return store
}

// edgeSet returns the set of edge IDs in the store.
func edgeSet(t *testing.T, store graphstore.Graphstore) map[string]struct{} {
	t.Helper()
	edges, err := store.Edges(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	set := make(map[string]struct{}, len(edges))
	for _, e := range edges {
		set[string(e.ID())] = struct{}{}
	}
	return set
}

// TestProfile_DistinctAndSuperset asserts that fast/balanced/deep produce
// distinct edge sets and that deep is a superset of balanced and fast.
func TestProfile_DistinctAndSuperset(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return price() + tax.Rate() }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	})

	fast := indexWithProfile(t, repo, profile.Fast)
	balanced := indexWithProfile(t, repo, profile.Balanced)
	deep := indexWithProfile(t, repo, profile.Deep)

	fastSet := edgeSet(t, fast)
	balancedSet := edgeSet(t, balanced)
	deepSet := edgeSet(t, deep)

	// Superset invariants: deep ⊇ balanced ⊇ fast.
	for id := range fastSet {
		if _, ok := balancedSet[id]; !ok {
			t.Fatalf("balanced missing edge %s present in fast", id)
		}
	}
	for id := range balancedSet {
		if _, ok := deepSet[id]; !ok {
			t.Fatalf("deep missing edge %s present in balanced", id)
		}
	}

	// Distinctness: fast should have strictly fewer edges than balanced
	// because it drops import-fanout edges and skips typeresolve.
	if len(fastSet) >= len(balancedSet) {
		t.Fatalf("fast edge count (%d) not less than balanced (%d)", len(fastSet), len(balancedSet))
	}
	if len(balancedSet) > len(deepSet) {
		t.Fatalf("balanced edge count (%d) greater than deep (%d)", len(balancedSet), len(deepSet))
	}
}

// TestProfile_FastKeepsCoreNavigableEdges asserts that fast mode retains
// definition/direct-reference edges even though it drops import fanout.
func TestProfile_FastKeepsCoreNavigableEdges(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return price() + tax.Rate() }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	})

	store := indexWithProfile(t, repo, profile.Fast)
	edges, err := store.Edges(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}

	var hasCall bool
	for _, e := range edges {
		if e.Kind() == "calls" {
			hasCall = true
			break
		}
	}
	if !hasCall {
		t.Fatal("fast mode produced no 'calls' edges")
	}
}

// TestProfile_BalancedDefaultIsUnchanged asserts that an ingester without an
// explicit profile behaves like balanced (runs typeresolve and keeps imports).
func TestProfile_BalancedDefaultIsUnchanged(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return price() + tax.Rate() }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	})

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	if err := i.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll default: %v", err)
	}

	defaultSet := edgeSet(t, store)
	balancedSet := edgeSet(t, indexWithProfile(t, repo, profile.Balanced))

	if len(defaultSet) != len(balancedSet) {
		t.Fatalf("default edge count (%d) != balanced (%d)", len(defaultSet), len(balancedSet))
	}
	for id := range defaultSet {
		if _, ok := balancedSet[id]; !ok {
			t.Fatalf("balanced missing edge %s present in default", id)
		}
	}
}

// importEdgeCount returns the number of "imports" edges in the store.
func importEdgeCount(t *testing.T, store graphstore.Graphstore) int {
	t.Helper()
	edges, err := store.Edges(context.Background(), graphstore.Query{EdgeKind: "imports"})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	return len(edges)
}

// TestProfile_BalancedAggregatesExternalImports asserts that balanced mode
// aggregates external import edges by target package while deep mode keeps
// individual imports.
func TestProfile_BalancedAggregatesExternalImports(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return tax.Rate() }
`,
		"shop/price.go": `package shop
import "example.com/repo/tax"
func price() int { return tax.Rate() }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	})

	balanced := indexWithProfile(t, repo, profile.Balanced)
	deep := indexWithProfile(t, repo, profile.Deep)

	balancedImports := importEdgeCount(t, balanced)
	deepImports := importEdgeCount(t, deep)

	if balancedImports >= deepImports {
		t.Fatalf("balanced imports (%d) not fewer than deep imports (%d); aggregation did not reduce", balancedImports, deepImports)
	}
}

// TestProfile_DeepPreservesRelationshipsAfterCompaction asserts that the deep
// edge set is still a superset of balanced after compaction/aggregation.
func TestProfile_DeepPreservesRelationshipsAfterCompaction(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return price() + tax.Rate() }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	})

	balanced := indexWithProfile(t, repo, profile.Balanced)
	deep := indexWithProfile(t, repo, profile.Deep)

	balancedSet := edgeSet(t, balanced)
	deepSet := edgeSet(t, deep)

	for id := range balancedSet {
		if _, ok := deepSet[id]; !ok {
			t.Fatalf("deep missing edge %s present in balanced", id)
		}
	}
}
