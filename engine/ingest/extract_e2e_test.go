package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/query"
)

// TestEndToEnd_RealGoExtractionPowersQueries is the proof that closes the
// foundational gap: indexing a real Go file with the production parser populates
// the graph with symbol nodes and intra-file edges, and the structural query
// service answers callers/callees/references over them — no synthetic fixtures.
func TestEndToEnd_RealGoExtractionPowersQueries(t *testing.T) {
	dir := t.TempDir()
	src := `package shop

const TaxRate = 7

func price(n int) int { return n * TaxRate }

func checkout() int { return price(3) }
`
	if err := os.WriteFile(filepath.Join(dir, "cart.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(context.Background(), dir); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	// The graph is no longer empty on real input.
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("graph is empty after ingesting real Go source — extraction did not run")
	}

	id := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		t.Fatalf("symbol %q not extracted (have %d nodes)", qn, len(nodes))
		return ""
	}

	svc := query.New(store)
	ctx := context.Background()

	// checkout calls price.
	callees, err := svc.Callees(ctx, id("shop.checkout"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callees, id("shop.price")) {
		t.Errorf("Callees(checkout) did not include price: %+v", callees.Nodes)
	}

	// price is called by checkout (reverse direction).
	callers, err := svc.Callers(ctx, id("shop.price"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callers, id("shop.checkout")) {
		t.Errorf("Callers(price) did not include checkout: %+v", callers.Nodes)
	}

	// price references TaxRate.
	refs, err := svc.References(ctx, id("shop.TaxRate"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(refs, id("shop.price")) {
		t.Errorf("References(TaxRate) did not include price: %+v", refs.Nodes)
	}
}

func containsNode(r query.Result, want model.NodeId) bool {
	for _, n := range r.Nodes {
		if n.ID == want {
			return true
		}
	}
	return false
}
