package ingest_test

// WP-01 follow-up: on-demand (wildcard) imports must link to the imported
// package itself, never to its parent package. Regression guard for the
// adversarial-verify finding that `import com.a.b.*;` wrongly targeted `com.a`.

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

func TestLinkJavaWildcardImport_TargetsExactPackage(t *testing.T) {
	ctx := context.Background()

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	// com.a.b is the imported package; com.a is a real declared package too, so a
	// wrong (parent) edge would resolve to a committed node and be observable.
	repo := writeRepo(t, map[string]string{
		"src/com/a/Parent.java":     "package com.a;\n\npublic class Parent {\n    public void run() {}\n}\n",
		"src/com/a/b/Widget.java":   "package com.a.b;\n\npublic class Widget {\n    public void run() {}\n}\n",
		"src/com/app/Consumer.java": "package com.app;\n\nimport com.a.b.*;\n\npublic class Consumer {\n    public void run() { new Widget().run(); }\n}\n",
	})
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	// Resolve the package node ids by qualified name.
	pkgID := map[string]string{}
	var consumerFileID string
	for _, n := range nodes {
		if n.Kind() == "package" {
			pkgID[n.QualifiedName()] = string(n.ID())
		}
		if n.Kind() == "file" && n.SourcePath() == "src/com/app/Consumer.java" {
			consumerFileID = string(n.ID())
		}
	}
	if pkgID["com.a.b"] == "" || pkgID["com.a"] == "" || consumerFileID == "" {
		t.Fatalf("missing expected nodes: pkgs=%v consumer=%q", pkgID, consumerFileID)
	}

	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	toExactPkg, toParentPkg := false, false
	for _, e := range edges {
		if e.Kind() != "imports" || string(e.From()) != consumerFileID {
			continue
		}
		switch string(e.To()) {
		case pkgID["com.a.b"]:
			toExactPkg = true
		case pkgID["com.a"]:
			toParentPkg = true
		}
	}
	if !toExactPkg {
		t.Errorf("wildcard import missing file→package edge to com.a.b (the imported package)")
	}
	if toParentPkg {
		t.Errorf("wildcard import fabricated a file→package edge to com.a (the PARENT package) — the off-by-one-segment defect")
	}
}
