package ingest_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
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

// TestProfile_DistinctAndSuperset asserts the ladder that actually exists
// (ADR 0010): deep ⊇ balanced ⊇ fast, with FAST strictly smaller. It does NOT
// assert that balanced and deep differ — since the import aggregation was
// removed they are graph-identical, and the surviving strictness comes from
// Fast dropping every `imports` edge.
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
//
// THE FIXTURE CARRIES TWO IMPORTERS OF ONE TARGET ON PURPOSE. Until ADR 0010
// this test was green for the wrong reason: its fixture gave every target a
// single importer, so the balanced aggregation was a no-op on it
// (aggregateImportsByTarget passed singleton groups through verbatim) and the
// test could not observe the very difference it asserts is absent. The
// library-vs-CLI profile asymmetry it was supposed to guard is exactly what
// hid PARITY-003, so the fixture now contains the shape that differed.
func TestProfile_BalancedDefaultIsUnchanged(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod": "module example.com/repo\n\ngo 1.26\n",
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return price() + tax.Rate() }
`,
		"shop/price.go": `package shop
import "example.com/repo/tax"
func price() int { return tax.Rate() }
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

// TestProfile_BalancedKeepsEveryImporterEdge replaces
// TestProfile_BalancedAggregatesExternalImports, which asserted the OPPOSITE
// and was wrong on both halves — recorded here rather than deleted, because a
// retired assertion is evidence about how the defect survived.
//
// The retired test asserted `balancedImports < deepImports` ("aggregation did
// not reduce") over a fixture importing "example.com/repo/tax" — a package the
// SAME fixture declares, i.e. an INTRA-REPO import. It called that external
// because engine/ingest's isExternalImport split the path on "/" and found a
// dot in "example.com". So the test named its subject "ExternalImports" while
// pinning the aggregation of internal ones, and its green was the reason
// PARITY-003 looked intentional for two releases.
//
// What is true, and what this test pins instead (ADR 0010): under EVERY
// profile that keeps imports edges at all, EACH importer of a target keeps its
// OWN edge. The retired aggregation kept one edge per target from a
// representative source, so the second importer of a target had no imports
// edge — a dropped true edge in a GA operation, and the shape the conformance
// table's `change_colliding_package_dir` row now catches under the balanced
// axis on both stores.
func TestProfile_BalancedKeepsEveryImporterEdge(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod": "module example.com/repo\n\ngo 1.26\n",
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

	// Two importers, one target file: two edges, on both profiles. The
	// retired aggregation produced ONE here.
	if got := importEdgeCount(t, balanced); got != 2 {
		t.Errorf("balanced imports edges = %d, want 2 (one per importer of tax/tax.go); "+
			"a lower number means representative-collapsing is back and an importer lost its edge", got)
	}
	if bal, dp := importEdgeCount(t, balanced), importEdgeCount(t, deep); bal != dp {
		t.Errorf("balanced imports (%d) != deep imports (%d): the two profiles must agree on imports "+
			"edges — only fast drops them (ADR 0010)", bal, dp)
	}

	// Non-vacuity: each importer is really the source of its own edge, and no
	// edge carries another file's evidence (the aggregation merged evidence
	// across the group and published one importer citing the others' lines).
	edges, err := balanced.Edges(context.Background(), graphstore.Query{EdgeKind: "imports"})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	nodes, err := balanced.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	pathOf := map[model.NodeId]string{}
	for _, n := range nodes {
		pathOf[n.ID()] = filepath.ToSlash(n.SourcePath())
	}
	seen := map[string]bool{}
	for _, e := range edges {
		from := pathOf[e.From()]
		seen[from] = true
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, from+":") {
				t.Errorf("imports edge from %s cites evidence %q belonging to another file — "+
					"aggregated evidence is back", from, ev)
			}
		}
	}
	for _, want := range []string{"shop/cart.go", "shop/price.go"} {
		if !seen[want] {
			t.Errorf("no imports edge originates from %s; that importer lost its edge", want)
		}
	}
}

// TestProfile_DeepPreservesRelationshipsAfterCompaction asserts deep ⊇ balanced.
// Its name mentions compaction/aggregation, which no longer exists (ADR 0010);
// the assertion is kept because it is the cheapest guard against a future
// balanced-only reduction reappearing without the ladder being re-stated.
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
