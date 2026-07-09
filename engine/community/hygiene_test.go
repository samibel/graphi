package community

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// putKindNode inserts a node of an arbitrary kind (the shared mustNode helper is
// function-only) so the hygiene test can plant external / package / file nodes.
func putKindNode(t *testing.T, store graphstore.Graphstore, kind, qn, src string) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, qn, src, 1, 1)
	if err != nil {
		t.Fatalf("NewNode(%s, %s): %v", kind, qn, err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	return n
}

// TestDetect_ExcludesArtifactNodes is the WP-14 follow-up-E gate: a graph with a
// real symbol plus interned external / package / file artifact nodes partitions
// into symbol-only communities — no artifact node is ever a member — for BOTH the
// package-prefix baseline and the default Louvain detector.
func TestDetect_ExcludesArtifactNodes(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()

	sym := mustNode(t, store, "app.Handler")                  // real symbol
	ext := putKindNode(t, store, "external", "os.Getenv", "") // interned external
	pkg := putKindNode(t, store, "package", "com.app", "")    // interned package
	file := putKindNode(t, store, "file", "app/main.go", "app/main.go")
	// A calls edge from the symbol to the external target (the WP-14 shape).
	mustEdge(t, store, sym.ID(), ext.ID())

	artifacts := map[model.NodeId]string{ext.ID(): "external", pkg.ID(): "package", file.ID(): "file"}

	for _, tc := range []struct {
		name string
		det  Detector
	}{
		{"package-prefix", PackagePrefixDetector{}},
		{"louvain", LouvainDetector{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comms, err := tc.det.Detect(ctx, store)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			sawSym := false
			for _, c := range comms {
				for _, m := range c.Members {
					if kind, isArtifact := artifacts[m]; isArtifact {
						t.Errorf("%s community %d includes artifact node %s (kind %s)", tc.name, c.ID, m, kind)
					}
					if m == sym.ID() {
						sawSym = true
					}
				}
			}
			if !sawSym {
				t.Errorf("%s: the real symbol app.Handler is missing from every community", tc.name)
			}
		})
	}
}

// TestClusterExcludesArtifacts pins that resolving the community of an artifact
// node returns not-found (they are not navigable members).
func TestClusterExcludesArtifacts(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	mustNode(t, store, "app.Handler")
	ext := putKindNode(t, store, "external", "os.Getenv", "")

	_, ok, err := Cluster(ctx, store, DefaultDetector(), ext.ID())
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}
	if ok {
		t.Errorf("Cluster resolved an external artifact node to a community; want not-found")
	}
}

// TestWikiExcludesArtifacts is the end-to-end proof that generated wiki pages
// carry no external/package/file member lines, since the wiki renders straight
// from community membership.
func TestWikiExcludesArtifacts(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	sym := mustNode(t, store, "app.Handler")
	ext := putKindNode(t, store, "external", "os.Getenv", "")
	mustEdge(t, store, sym.ID(), ext.ID())

	comms, err := DefaultDetector().Detect(ctx, store)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	for _, c := range comms {
		for _, m := range c.Members {
			if m == ext.ID() {
				t.Fatalf("external node is a community member; wiki would render it")
			}
		}
	}
	// Defensive: the external QN must not surface as a member bullet anywhere in a
	// serialized community index (a coarse but direct end-to-end check).
	blob, err := SerializeCommunities(comms)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if strings.Contains(string(blob), string(ext.ID())) {
		t.Errorf("serialized communities reference the external artifact node id")
	}
}
