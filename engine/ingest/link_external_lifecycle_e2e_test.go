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

// TestLink_GoExternalNode_InterningLifecycle proves the WP-03 interned `external`
// node converges byte-identically between an incremental pass and a full re-index
// in the two lifecycle scenarios that could diverge:
//
//   - SHARING: two files both call os.ReadFile, minting the SAME interned external
//     node id. Deleting one file must NOT drop the shared node while the sibling
//     still references it (the incident edge from the surviving file keeps it in
//     the referenced set).
//   - ORPHANING: the SOLE referencer of an external symbol is edited to stop
//     referencing it. A full re-index would never mint that external node, so the
//     incremental pass must SWEEP it (sweepOrphanExternalNodes) to converge.
//
// External nodes are linker-minted (not owned by any file's node_ids), so they are
// invisible to the per-file stale-node deletion — this test guards the dedicated
// orphan sweep that keeps incremental == full.
func TestLink_GoExternalNode_InterningLifecycle(t *testing.T) {
	ctx := context.Background()

	const goMod = "module ext\n\ngo 1.21\n"
	// a.go and b.go both call os.ReadFile → one shared interned external node.
	aReadFile := "package app\n\nimport \"os\"\n\nfunc UseA() { _, _ = os.ReadFile(\"/a\") }\n"
	bReadFile := "package app\n\nimport \"os\"\n\nfunc UseB() { _, _ = os.ReadFile(\"/b\") }\n"

	t.Run("shared external survives sibling delete", func(t *testing.T) {
		initial := map[string]string{
			"go.mod":   goMod,
			"pkg/a.go": aReadFile,
			"pkg/b.go": bReadFile,
		}
		storeInc := graphstore.NewMemStore()
		t.Cleanup(func() { _ = storeInc.Close() })
		iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
		repo := writeRepo(t, initial)
		if err := iInc.IngestAll(ctx, repo); err != nil {
			t.Fatalf("inc IngestAll: %v", err)
		}
		// Delete a.go; os.ReadFile is still referenced by b.go and must survive.
		if err := os.Remove(filepath.Join(repo, "pkg", "a.go")); err != nil {
			t.Fatalf("rm a.go: %v", err)
		}
		if err := iInc.IngestChanged(ctx, repo, []string{"pkg/a.go"}); err != nil {
			t.Fatalf("incremental: %v", err)
		}
		incSnap := filepath.Join(t.TempDir(), "inc")
		if err := storeInc.Snapshot(ctx, incSnap); err != nil {
			t.Fatalf("inc snapshot: %v", err)
		}
		incBytes, _ := os.ReadFile(incSnap)

		full := map[string]string{"go.mod": goMod, "pkg/b.go": bReadFile}
		fullBytes := fullSnapshotBytes(t, full)
		if !bytes.Equal(incBytes, fullBytes) {
			t.Fatalf("incremental != full after sibling-preserving delete (external node lifecycle)")
		}
	})

	t.Run("orphan external swept when sole referencer stops", func(t *testing.T) {
		// a.go is the SOLE referencer of os.ReadFile; b.go references strconv.Atoi.
		bAtoi := "package app\n\nimport \"strconv\"\n\nfunc UseB() { _, _ = strconv.Atoi(\"1\") }\n"
		initial := map[string]string{
			"go.mod":   goMod,
			"pkg/a.go": aReadFile,
			"pkg/b.go": bAtoi,
		}
		storeInc := graphstore.NewMemStore()
		t.Cleanup(func() { _ = storeInc.Close() })
		iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
		repo := writeRepo(t, initial)
		if err := iInc.IngestAll(ctx, repo); err != nil {
			t.Fatalf("inc IngestAll: %v", err)
		}
		// Edit a.go to reference os.Getenv instead of os.ReadFile. os.ReadFile now
		// has no referencer and must be swept to match a full re-index.
		aGetenv := "package app\n\nimport \"os\"\n\nfunc UseA() { _ = os.Getenv(\"X\") }\n"
		mustWrite(t, repo, "pkg/a.go", aGetenv)
		if err := iInc.IngestChanged(ctx, repo, []string{"pkg/a.go"}); err != nil {
			t.Fatalf("incremental: %v", err)
		}
		incSnap := filepath.Join(t.TempDir(), "inc")
		if err := storeInc.Snapshot(ctx, incSnap); err != nil {
			t.Fatalf("inc snapshot: %v", err)
		}
		incBytes, _ := os.ReadFile(incSnap)

		full := map[string]string{"go.mod": goMod, "pkg/a.go": aGetenv, "pkg/b.go": bAtoi}
		fullBytes := fullSnapshotBytes(t, full)
		if !bytes.Equal(incBytes, fullBytes) {
			t.Fatalf("incremental != full after sole-referencer edit (orphan external node not swept)")
		}
	})
}
