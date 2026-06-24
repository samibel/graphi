package ingest_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/query"
)

// TestLink_Python_CrossModuleQueries proves the FU-5 outcome for Python: a
// multi-package repo, ingested through the production Python parser + the FU-5
// pyResolver, answers Callees/Callers across modules and resolves the from-import
// binding to a heuristic-tier cross-module edge with file:line evidence.
func TestLink_Python_CrossModuleQueries(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, map[string]string{
		"app/main.py": `from shop import price
import tax.rates as rates

def checkout():
    return price() + rates.compute()
`,
		"shop/api.py": `def price():
    return 10
`,
		"tax/rates/calc.py": `def compute():
    return 7
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	id := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		t.Fatalf("symbol %q not found among %d nodes", qn, len(nodes))
		return ""
	}

	svc := query.New(store)

	// from-import bare binding: checkout -> shop.price.
	callees, err := svc.Callees(ctx, id("app.checkout"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callees, id("shop.price")) {
		t.Errorf("Callees(checkout) missing cross-module price: %+v", callees.Nodes)
	}
	// import-alias selector: checkout -> rates.compute.
	if !containsNode(callees, id("rates.compute")) {
		t.Errorf("Callees(checkout) missing rates.compute: %+v", callees.Nodes)
	}
	callers, err := svc.Callers(ctx, id("shop.price"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callers, id("app.checkout")) {
		t.Errorf("Callers(price) missing checkout: %+v", callers.Nodes)
	}

	edges, err := store.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	if err != nil {
		t.Fatal(err)
	}
	var foundXmod bool
	for _, e := range edges {
		if e.From() == id("app.checkout") && e.To() == id("shop.price") {
			foundXmod = true
			if e.Tier() != model.TierHeuristic {
				t.Errorf("cross-module Python edge tier = %q, want heuristic", e.Tier())
			}
			if len(e.Evidence()) == 0 {
				t.Errorf("cross-module Python edge has no evidence")
			}
		}
		if _, err := store.GetNode(ctx, e.To()); err != nil {
			t.Errorf("calls edge to absent target %s", e.To())
		}
	}
	if !foundXmod {
		t.Error("no cross-module checkout->price calls edge emitted")
	}
}

// TestLink_Python_GoldenIncrementalVsFull drives the byte-identical invariant with
// the production Python parser across a rename-of-target (cross-module caller must
// re-link), an added sibling, and a deleted file.
func TestLink_Python_GoldenIncrementalVsFull(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
		"app/main.py": `from shop import price

def checkout():
    return price()
`,
		"shop/api.py": `def price():
    return 10
`,
		"util/util.py": `def helper():
    return 1
`,
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Rename price()->cost() (cross-module caller checkout must re-link), add a
	// same-package sibling, delete util.
	mustWrite(t, repo, "shop/api.py", `def cost():
    return 10
`)
	mustWrite(t, repo, "app/main.py", `from shop import cost

def checkout():
    return cost()
`)
	mustWrite(t, repo, "shop/extra.py", `from shop import cost

def extra():
    return cost()
`)
	if err := os.Remove(filepath.Join(repo, "util/util.py")); err != nil {
		t.Fatalf("rm util: %v", err)
	}
	if err := iInc.IngestChanged(ctx, repo, []string{"app/main.py", "shop/api.py", "shop/extra.py", "util/util.py"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{
		"app/main.py": `from shop import cost

def checkout():
    return cost()
`,
		"shop/api.py": `def cost():
    return 10
`,
		"shop/extra.py": `from shop import cost

def extra():
    return cost()
`,
	}
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("Python incremental != full (byte-level, incl. provenance):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}
}
