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

// snapshotBytes ingests-full a repo into a fresh store and returns its snapshot.
func fullSnapshotBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	repo := writeRepo(t, files)
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	snap := filepath.Join(t.TempDir(), "s")
	if err := store.Snapshot(context.Background(), snap); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// TestLink_GoldenIncrementalVsFull_RealGo drives the sacred invariant with the
// PRODUCTION Go parser (which actually emits PendingRefs/Imports), covering
// same-package cross-file, cross-package, rename-of-target, add-file and
// delete-file. It would FALSE-GREEN on an empty-edge stub, so it is the real
// proof: incremental and full snapshots must be byte-identical INCLUDING
// confidence/tier/reason/evidence (Snapshot serializes full edge provenance).
func TestLink_GoldenIncrementalVsFull_RealGo(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
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
		"util/util.go": `package util
func Helper() int { return 1 }
`,
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Mutate: rename price()->cost() (cross-file caller checkout must re-link),
	// add a new same-package sibling file, delete util.
	mustWrite(t, repo, "shop/price.go", `package shop
func cost() int { return 10 }
`)
	mustWrite(t, repo, "shop/cart.go", `package shop
import "example.com/repo/tax"
func checkout() int { return cost() + tax.Rate() }
`)
	mustWrite(t, repo, "shop/extra.go", `package shop
func extra() int { return cost() }
`)
	if err := os.Remove(filepath.Join(repo, "util/util.go")); err != nil {
		t.Fatalf("rm util: %v", err)
	}

	if err := iInc.IngestChanged(ctx, repo, []string{"shop/cart.go", "shop/price.go", "shop/extra.go", "util/util.go"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return cost() + tax.Rate() }
`,
		"shop/price.go": `package shop
func cost() int { return 10 }
`,
		"shop/extra.go": `package shop
func extra() int { return cost() }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	}
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("incremental != full (byte-level, incl. provenance):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}
}

// mustWrite writes a file under repo (creating dirs).
func mustWrite(t *testing.T, repo, rel, content string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLink_CrossPackageAndCrossFileQueries proves the user-visible outcome: a
// multi-file / multi-package repo, ingested through production parsing + linking,
// answers Callees/Callers across files and resolves a pkg.Fn selector to the
// right symbol with a heuristic-tier edge.
func TestLink_CrossPackageAndCrossFileQueries(t *testing.T) {
	ctx := context.Background()
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

	// Same-package cross-file: checkout -> price.
	callees, err := svc.Callees(ctx, id("shop.checkout"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callees, id("shop.price")) {
		t.Errorf("Callees(checkout) missing price (cross-file): %+v", callees.Nodes)
	}
	callers, err := svc.Callers(ctx, id("shop.price"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callers, id("shop.checkout")) {
		t.Errorf("Callers(price) missing checkout: %+v", callers.Nodes)
	}

	// Cross-package: checkout -> tax.Rate, heuristic tier with file:line evidence.
	if !containsNode(callees, id("tax.Rate")) {
		t.Errorf("Callees(checkout) missing cross-package tax.Rate: %+v", callees.Nodes)
	}
	edges, err := store.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	if err != nil {
		t.Fatal(err)
	}
	var foundXpkg bool
	for _, e := range edges {
		if e.From() == id("shop.checkout") && e.To() == id("tax.Rate") {
			foundXpkg = true
			if e.Tier() != model.TierHeuristic {
				t.Errorf("cross-package edge tier = %q, want heuristic", e.Tier())
			}
			if len(e.Evidence()) == 0 {
				t.Errorf("cross-package edge has no evidence")
			}
		}
		// No edge may target a node that is not in the store.
		if _, err := store.GetNode(ctx, e.To()); err != nil {
			t.Errorf("calls edge to absent target %s", e.To())
		}
	}
	if !foundXpkg {
		t.Error("no cross-package checkout->tax.Rate calls edge emitted")
	}
}

// TestLink_RenameOfTarget_OldEdgeAbsent asserts that after a rename of the call
// target, the OLD cross-file edge is gone AND the new one is present.
func TestLink_RenameOfTarget_OldEdgeAbsent(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, map[string]string{
		"shop/cart.go":  "package shop\nfunc checkout() int { return price() }\n",
		"shop/price.go": "package shop\nfunc price() int { return 10 }\n",
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	nodes, _ := store.Nodes(ctx, graphstore.Query{})
	idOf := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		return ""
	}
	checkout := idOf("shop.checkout")
	oldPrice := idOf("shop.price")

	// Compute the old edge id (checkout -> price calls).
	oldEdge, err := model.NewEdge(checkout, oldPrice, "calls", model.TierDerived, 0.9, "x", []string{"shop/cart.go:2"})
	if err != nil {
		t.Fatal(err)
	}

	// Rename price -> cost; checkout now calls cost.
	mustWrite(t, repo, "shop/price.go", "package shop\nfunc cost() int { return 10 }\n")
	mustWrite(t, repo, "shop/cart.go", "package shop\nfunc checkout() int { return cost() }\n")
	if err := ing.IngestChanged(ctx, repo, []string{"shop/cart.go", "shop/price.go"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	// Old edge must be absent.
	if _, err := store.GetEdge(ctx, oldEdge.ID()); err == nil {
		t.Errorf("stale rename edge survived: %s", oldEdge.ID())
	}

	// New edge checkout -> cost must be present.
	nodes2, _ := store.Nodes(ctx, graphstore.Query{})
	var newCheckout, cost model.NodeId
	for _, n := range nodes2 {
		switch n.QualifiedName() {
		case "shop.checkout":
			newCheckout = n.ID()
		case "shop.cost":
			cost = n.ID()
		}
	}
	edges, _ := store.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	var found bool
	for _, e := range edges {
		if e.From() == newCheckout && e.To() == cost {
			found = true
		}
	}
	if !found {
		t.Error("new checkout->cost edge not present after rename")
	}
}

// TestLink_SameClauseDifferentDir asserts no cross-directory phantom: two
// `package util` directories each with Helper(); a caller resolves to its own.
func TestLink_SameClauseDifferentDir(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, map[string]string{
		"a/util.go": "package util\nfunc Helper() int { return 1 }\nfunc Caller() int { return Helper() }\n",
		"b/util.go": "package util\nfunc Helper() int { return 2 }\n",
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	nodes, _ := store.Nodes(ctx, graphstore.Query{})
	var caller, aHelper, bHelper model.NodeId
	for _, n := range nodes {
		if n.QualifiedName() == "util.Caller" {
			caller = n.ID()
		}
		if n.QualifiedName() == "util.Helper" {
			if n.SourcePath() == "a/util.go" {
				aHelper = n.ID()
			} else {
				bHelper = n.ID()
			}
		}
	}
	edges, _ := store.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	for _, e := range edges {
		if e.From() == caller {
			if e.To() == bHelper {
				t.Error("cross-directory phantom: Caller resolved to b/util Helper")
			}
			if e.To() != aHelper {
				t.Errorf("Caller resolved to unexpected %s", e.To())
			}
		}
	}
}

// TestLink_DotAndBlankImportsNoPhantom asserts dot/blank imports yield no
// phantom selector edges and ingest succeeds.
func TestLink_DotAndBlankImportsNoPhantom(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, map[string]string{
		"app/main.go": `package app
import (
	. "example.com/repo/lib"
	_ "example.com/repo/side"
)
func run() { Do() }
`,
		"lib/lib.go":   "package lib\nfunc Do() {}\n",
		"side/side.go": "package side\nfunc init() {}\n",
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	// No edge may target an absent node (no phantom).
	edges, _ := store.Edges(ctx, graphstore.Query{})
	for _, e := range edges {
		if _, err := store.GetNode(ctx, e.To()); err != nil {
			t.Errorf("phantom edge to absent target %s (kind %s)", e.To(), e.Kind())
		}
	}
}

// TestLink_CyclicImportsTerminate asserts a cyclic import graph terminates and
// produces a byte-identical incremental-vs-full result.
func TestLink_CyclicImportsTerminate(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"a/a.go": "package a\nimport \"example.com/repo/b\"\nfunc A() int { return b.B() }\n",
		"b/b.go": "package b\nimport \"example.com/repo/a\"\nfunc B() int { return a.A() }\n",
	}
	repo := writeRepo(t, files)
	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	mustWrite(t, repo, "a/a.go", "package a\nimport \"example.com/repo/b\"\nfunc A() int { return b.B() + 1 }\n")
	if err := iInc.IngestChanged(ctx, repo, []string{"a/a.go"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatal(err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{
		"a/a.go": "package a\nimport \"example.com/repo/b\"\nfunc A() int { return b.B() + 1 }\n",
		"b/b.go": "package b\nimport \"example.com/repo/a\"\nfunc B() int { return a.A() }\n",
	}
	fullBytes := fullSnapshotBytes(t, mutated)
	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("cyclic incremental != full:\ninc =%s\nfull=%s", incBytes, fullBytes)
	}
}
