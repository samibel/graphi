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

// TestLink_Python_MixedTreeWithGoMod_IncrementalParity pins the ADR 0009
// review round 2 BLOCKER (independent reviewer, finding 1): in a POLYGLOT tree
// containing a go.mod, the reverse-dep translation (reverseDepKeys →
// DirsForImport) used to consult ONLY the module map, which owns no Python
// import target — so a Python caller's dependency record was stored verbatim
// ("shop") instead of as the target's directory ("src/shop"), the cascade
// never re-linked the caller, and the full pass's cross-module edge was
// PERMANENTLY missing from the incremental graph. A new frozen divergence of
// exactly the class ADR 0009 exists to close, introduced by its own fix.
//
// Three load-bearing choices, so a refactor does not quietly devalue the row:
//   - the Python package lives at src/shop while its import clause is "shop" —
//     if directory and clause coincide, the verbatim key accidentally matches
//     the directory key space and the divergence hides;
//   - the change set names ONLY the changed target file, never the importer —
//     convergence must come from the reverse-dep cascade, which is the thing
//     under test (the sibling golden test above names every file);
//   - a real Go package sits in the tree so the module map is genuinely live.
func TestLink_Python_MixedTreeWithGoMod_IncrementalParity(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
		"go.mod":     "module example.com/m\n\ngo 1.26\n",
		"gopkg/g.go": "package gopkg\n\nfunc G() int { return 1 }\n",
		"app/main.py": `from shop import price

def checkout():
    return price()
`,
		"src/shop/api.py": `def cost():
    return 10
`,
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// The target module gains the symbol the caller has been importing all
	// along; app/main.py is deliberately NOT in the change set.
	changedShop := `def cost():
    return 10

def price():
    return 12
`
	mustWrite(t, repo, "src/shop/api.py", changedShop)
	if err := iInc.IngestChanged(ctx, repo, []string{"src/shop/api.py"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{}
	for k, v := range initial {
		mutated[k] = v
	}
	mutated["src/shop/api.py"] = changedShop
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("mixed-tree Python incremental != full — the go.mod-gated reverse-dep translation froze the caller:\ninc =%s\nfull=%s", incBytes, fullBytes)
	}

	// Non-vacuity: the edge whose freezing this row exists to detect really is
	// in the converged graph.
	edges, err := storeInc.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := storeInc.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	byQN := map[string]model.NodeId{}
	for _, n := range nodes {
		byQN[n.QualifiedName()] = n.ID()
	}
	found := false
	for _, e := range edges {
		if e.From() == byQN["app.checkout"] && e.To() == byQN["shop.price"] {
			found = true
		}
	}
	if !found {
		t.Error("app.checkout --calls--> shop.price missing from the incremental graph (row would be vacuous)")
	}
}
