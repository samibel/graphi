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
)

// These cascade-only regression tests (SW-050 review round 1, ADV-1) report ONLY
// the directly-edited file to IngestChanged, forcing dependentsOf to compute the
// real reverse-dependency closure. The shipped golden test pre-lists every
// affected file, so it never exercises the cascade and false-greened the two
// Critical byte-identity blockers (BLOCK-1, BLOCK-2). Each test here is red on
// the pre-fix code path and green after the fix.

// TestLink_CascadeOnly_CrossPackageCalleeRename covers BLOCK-1: reverse_deps was
// keyed by import-path string but looked up by file path, so an import-dependent
// was never cascaded. The cross-package callee tax.Rate is renamed to tax.Levy
// (callee file AND the matching caller call site change on disk), but ONLY the
// callee file tax/tax.go is reported as changed. The importer shop/cart.go must
// be cascaded in via dependentsOf and re-linked so the incremental snapshot emits
// checkout->tax.Levy, matching a full re-index byte-for-byte. On the pre-fix path
// cart.go is never re-linked and inc != full.
func TestLink_CascadeOnly_CrossPackageCalleeRename(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
		"shop/cart.go": `package shop
import "example.com/repo/tax"
func checkout() int { return tax.Rate() }
`,
		"tax/tax.go": `package tax
func Rate() int { return 7 }
`,
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Rename the cross-package callee Rate->Levy and update the caller's call site
	// on disk, but report ONLY the callee file as changed. dependentsOf must pull
	// the importer cart.go into the reprocessed set (BLOCK-1).
	mustWrite(t, repo, "tax/tax.go", `package tax
func Levy() int { return 7 }
`)
	mustWrite(t, repo, "shop/cart.go", `package shop
import "example.com/repo/tax"
func checkout() int { return tax.Levy() }
`)
	if err := iInc.IngestChanged(ctx, repo, []string{"tax/tax.go"}); err != nil {
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
func checkout() int { return tax.Levy() }
`,
		"tax/tax.go": `package tax
func Levy() int { return 7 }
`,
	}
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("cascade-only cross-package rename: incremental != full (import dependent not cascaded):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}

	// Positive assertion: the new cross-package edge actually exists incrementally.
	nodes, _ := storeInc.Nodes(ctx, graphstore.Query{})
	var checkout, levy model.NodeId
	for _, n := range nodes {
		switch n.QualifiedName() {
		case "shop.checkout":
			checkout = n.ID()
		case "tax.Levy":
			levy = n.ID()
		}
	}
	edges, _ := storeInc.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	var found bool
	for _, e := range edges {
		if e.From() == checkout && e.To() == levy {
			found = true
		}
	}
	if !found {
		t.Error("incremental missing cross-package checkout->tax.Levy edge after callee rename")
	}
}

// TestLink_CascadeOnly_IdentityPreservingCallerDrop covers BLOCK-2: the
// stale-edge sweep skipped edges whose To was also owned by the reprocessed set,
// so an identity-preserving caller edit left a stale cross-file calls edge.
// checkout (shop/cart.go) stops calling price (shop/price.go, same package,
// cross-file); checkout keeps its NodeId (name/path unchanged), so DeleteNode
// never cascades the edge. ONLY shop/cart.go is reported; the same-package
// sibling cascade pulls in price.go (so price's node is also owned, triggering
// the bug). A full re-index emits no checkout->price edge, so the stale edge must
// be deleted for byte-identity. On the pre-fix path it survives and inc != full.
func TestLink_CascadeOnly_IdentityPreservingCallerDrop(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
		"shop/cart.go": `package shop
func checkout() int { return price() }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// checkout drops the call to price but keeps its identity (same QN + path).
	// Report ONLY cart.go; price.go is reprocessed via the same-package sibling
	// cascade, making the stale checkout->price edge's To owned (BLOCK-2 trigger).
	mustWrite(t, repo, "shop/cart.go", `package shop
func checkout() int { return 0 }
`)
	if err := iInc.IngestChanged(ctx, repo, []string{"shop/cart.go"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{
		"shop/cart.go": `package shop
func checkout() int { return 0 }
`,
		"shop/price.go": `package shop
func price() int { return 10 }
`,
	}
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("cascade-only identity-preserving drop: incremental != full (stale cross-file edge survived):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}

	// Positive assertion: no stale checkout->price calls edge survives.
	nodes, _ := storeInc.Nodes(ctx, graphstore.Query{})
	var checkout, price model.NodeId
	for _, n := range nodes {
		switch n.QualifiedName() {
		case "shop.checkout":
			checkout = n.ID()
		case "shop.price":
			price = n.ID()
		}
	}
	edges, _ := storeInc.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	for _, e := range edges {
		if e.From() == checkout && e.To() == price {
			t.Error("stale cross-file checkout->price edge survived identity-preserving drop")
		}
	}
}
