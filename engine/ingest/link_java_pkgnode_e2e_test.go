package ingest_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// TestLink_JavaPackageNode_InterningLifecycle proves the WP-01 interned
// `package` node survives an incremental pass byte-identically to a full index in
// the two scenarios that could wrongly drop a shared node: (1) deleting one file
// of a multi-file package while a sibling still declares it, and (2) a file
// changing its package declaration while a sibling keeps the old one. The package
// node is minted by EVERY file in the package with the same NodeId, so the
// stale-node sweep must keep it alive as long as ≥1 committed file declares it.
func TestLink_JavaPackageNode_InterningLifecycle(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
		"x/A.java": "package com.x;\npublic class A { public void run() {} }\n",
		"x/B.java": "package com.x;\npublic class B { public void run() {} }\n",
		"app/C.java": "package com.app;\nimport com.x.A;\n" +
			"public class C { public void go() { new A().run(); } }\n",
	}

	t.Run("delete one file, sibling keeps package", func(t *testing.T) {
		storeInc := graphstore.NewMemStore()
		t.Cleanup(func() { _ = storeInc.Close() })
		iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
		repo := writeRepo(t, initial)
		if err := iInc.IngestAll(ctx, repo); err != nil {
			t.Fatalf("inc IngestAll: %v", err)
		}
		// Delete A.java; com.x is still declared by B.java.
		if err := os.Remove(filepath.Join(repo, "x", "A.java")); err != nil {
			t.Fatalf("rm A: %v", err)
		}
		if err := iInc.IngestChanged(ctx, repo, []string{"x/A.java"}); err != nil {
			t.Fatalf("incremental: %v", err)
		}
		incSnap := filepath.Join(t.TempDir(), "inc")
		if err := storeInc.Snapshot(ctx, incSnap); err != nil {
			t.Fatalf("inc snapshot: %v", err)
		}
		incBytes, _ := os.ReadFile(incSnap)

		full := map[string]string{
			"x/B.java":   initial["x/B.java"],
			"app/C.java": initial["app/C.java"],
		}
		fullBytes := fullSnapshotBytes(t, full)
		if !bytes.Equal(incBytes, fullBytes) {
			t.Fatalf("incremental != full after sibling-preserving delete (package node lifecycle):\ninc =%s\nfull=%s", incBytes, fullBytes)
		}
	})

	t.Run("file changes package, sibling keeps old package", func(t *testing.T) {
		storeInc := graphstore.NewMemStore()
		t.Cleanup(func() { _ = storeInc.Close() })
		iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
		repo := writeRepo(t, initial)
		if err := iInc.IngestAll(ctx, repo); err != nil {
			t.Fatalf("inc IngestAll: %v", err)
		}
		// Move A into a different package; com.x is still declared by B.java.
		movedA := "package com.z;\npublic class A { public void run() {} }\n"
		mustWrite(t, repo, "x/A.java", movedA)
		if err := iInc.IngestChanged(ctx, repo, []string{"x/A.java"}); err != nil {
			t.Fatalf("incremental: %v", err)
		}
		incSnap := filepath.Join(t.TempDir(), "inc")
		if err := storeInc.Snapshot(ctx, incSnap); err != nil {
			t.Fatalf("inc snapshot: %v", err)
		}
		incBytes, _ := os.ReadFile(incSnap)

		full := map[string]string{
			"x/A.java":   movedA,
			"x/B.java":   initial["x/B.java"],
			"app/C.java": initial["app/C.java"],
		}
		fullBytes := fullSnapshotBytes(t, full)
		if !bytes.Equal(incBytes, fullBytes) {
			t.Fatalf("incremental != full after package-change (interned node must persist for sibling):\ninc =%s\nfull=%s", incBytes, fullBytes)
		}
	})
}
