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

// hierarchyRepo is a multi-file Go package exercising EP-011 G2 hierarchy:
//
//	shop/types.go declares:
//	  type Reader (interface)            — embedded by Collector (implements)
//	  type Base (struct)                 — embedded by Impl (inherits)
//	  type Collector interface { Reader }
//	  type Impl struct { Base }
//	shop/extra.go adds a same-package cross-file interface embed (Collector2).
//	tax/iface.go is a cross-package interface embedded via selector (tax.IFace).
//
// It produces implements/inherits edges that must (a) appear in the graph, (b)
// be byte-identical between a full re-index and an incremental edit.
func hierarchyRepo() map[string]string {
	return map[string]string{
		"shop/types.go": `package shop

// Reader is a same-package interface embedded by Collector.
type Reader interface {
	Read(p []byte) (int, error)
}

// Base is a same-package struct embedded by Impl.
type Base struct {
	id int
}

// Collector embeds Reader (implements, same-package).
type Collector interface {
	Reader
	Collect() error
}

// Impl embeds Base (inherits, same-package).
type Impl struct {
	Base
	name string
}
`,
		"shop/extra.go": `package shop

// Collector2 embeds Collector across files (same package, cross-file).
type Collector2 interface {
	Collector
}
`,
		"tax/iface.go": `package tax

// IFace is a cross-package interface embedded via selector in shop.
type IFace interface {
	Fax() error
}
`,
		"shop/xpkg.go": `package shop

import "example.com/repo/tax"

// Cross embeds tax.IFace via a selector (implements, cross-package).
type Cross interface {
	tax.IFace
}
`,
	}
}

// assertHasHierarchyEdge fails the test unless a directed edge (from,to,kind)
// exists in the store.
func assertHasHierarchyEdge(t *testing.T, store *graphstore.MemStore, fromQN, wantKind, toQN string) {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	qnid := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		t.Fatalf("node %q not found", qn)
		return ""
	}
	from, to := qnid(fromQN), qnid(toQN)
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	for _, e := range edges {
		if e.From() == from && e.To() == to && e.Kind() == wantKind {
			return // found
		}
	}
	t.Fatalf("hierarchy edge %s -[%s]-> %s not found among %d edges", fromQN, wantKind, toQN, len(edges))
}

// TestLink_HierarchyEdgesPresent proves the user-visible G2 outcome: ingesting a
// Go package with embedded interfaces/structs yields implements/inherits edges
// carrying full provenance.
func TestLink_HierarchyEdgesPresent(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, hierarchyRepo())
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	// Same-package interface embed → implements.
	assertHasHierarchyEdge(t, store, "shop.Collector", query.EdgeKindImplements, "shop.Reader")
	// Same-package struct embed → inherits.
	assertHasHierarchyEdge(t, store, "shop.Impl", query.EdgeKindInherits, "shop.Base")
	// Cross-file same-package interface embed → implements.
	assertHasHierarchyEdge(t, store, "shop.Collector2", query.EdgeKindImplements, "shop.Collector")
	// Cross-package selector interface embed → implements.
	assertHasHierarchyEdge(t, store, "shop.Cross", query.EdgeKindImplements, "tax.IFace")
}

// TestLink_HierarchyGoldenIncrementalVsFull is the sacred invariant for G2: an
// incremental edit that adds/removes embedded types must converge BYTE-IDENTICAL
// to a full re-index, including the new implements/inherits edges and their
// provenance. This would FALSE-GREEN on a stub parser; it uses the production Go
// parser+linker.
func TestLink_HierarchyGoldenIncrementalVsFull(t *testing.T) {
	ctx := context.Background()

	// Incremental store: ingest initial, then edit types.go to add a NEW embed.
	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, hierarchyRepo())
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Edit types.go: Collector now ALSO embeds an extra same-package interface
	// (Added), exercising add-embed incremental convergence.
	mustWrite(t, repo, "shop/types.go", `package shop

type Reader interface {
	Read(p []byte) (int, error)
}
type Base struct {
	id int
}
type Added interface {
	Extra() int
}
type Collector interface {
	Reader
	Added
	Collect() error
}
type Impl struct {
	Base
	name string
}
`)
	if err := iInc.IngestChanged(ctx, repo, []string{"shop/types.go"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, err := os.ReadFile(incSnap)
	if err != nil {
		t.Fatalf("read inc: %v", err)
	}

	// Full re-index of the SAME mutated repo.
	mutated := hierarchyRepo()
	mutated["shop/types.go"] = `package shop

type Reader interface {
	Read(p []byte) (int, error)
}
type Base struct {
	id int
}
type Added interface {
	Extra() int
}
type Collector interface {
	Reader
	Added
	Collect() error
}
type Impl struct {
	Base
	name string
}
`
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("hierarchy incremental != full (byte-level, incl. provenance):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}
}

// TestLink_HierarchyDeterminismAcrossRuns ingests the same repo twice into fresh
// stores and asserts the snapshots are byte-identical (no map-iteration leakage
// in the new edge kinds).
func TestLink_HierarchyDeterminismAcrossRuns(t *testing.T) {
	a := fullSnapshotBytes(t, hierarchyRepo())
	b := fullSnapshotBytes(t, hierarchyRepo())
	if !bytes.Equal(a, b) {
		t.Fatalf("two full ingests of the same repo differ:\na=%s\nb=%s", a, b)
	}
}
