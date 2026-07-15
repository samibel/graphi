package ingest_test

// SW-118 (ING-DEC): the cross-DB fault-injection matrix. The ingest pipeline
// commits to TWO stores — the graphstore (three batched sessions per full
// pass: write, link, typeresolve) and the meta sidecar (one transaction per
// pass) — so a crash between a durable graph commit and the meta commit leaves
// the two databases at different generations. These tests kill the pipeline at
// every such boundary and prove the system CONVERGES afterwards: the next
// pass/recovery ends byte-identical (portable snapshot bytes) to a store that
// never crashed. The disposition of every kill point is recorded in
// docs/adr/0004-ingest-recovery-disposition.md.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// batchFaultStore fails BeginBatch on the Nth call. IngestAll opens exactly
// three batched sessions (write → link → typeresolve), so failing call N
// simulates a process death AFTER batch N-1 committed durably and BEFORE
// phase N ran — with the meta transaction (cache, reverse-deps, semantics
// stamp) rolling back, which is exactly the cross-DB divergence window.
type batchFaultStore struct {
	graphstore.Graphstore
	batchCalls  int
	failAtBatch int // 1-based; 0 disarms
	injected    error
}

func (f *batchFaultStore) BeginBatch(ctx context.Context) (graphstore.Batch, error) {
	f.batchCalls++
	if f.failAtBatch != 0 && f.batchCalls == f.failAtBatch {
		return nil, f.injected
	}
	return graphstore.BeginBatch(ctx, f.Graphstore)
}

// writeFaultStore fails the Nth mutating write. Over a MemStore the batch
// layer is a passthrough (every write immediately durable, Rollback a no-op),
// so failing write N leaves writes 1..N-1 DURABLE mid-phase — the harshest
// partial-graph-write crash shape.
type writeFaultStore struct {
	graphstore.Graphstore
	writes      int
	failAtWrite int // 1-based; 0 disarms
	injected    error
}

func (f *writeFaultStore) bump() error {
	f.writes++
	if f.failAtWrite != 0 && f.writes == f.failAtWrite {
		return f.injected
	}
	return nil
}

func (f *writeFaultStore) PutNode(ctx context.Context, n model.Node) error {
	if err := f.bump(); err != nil {
		return err
	}
	return f.Graphstore.PutNode(ctx, n)
}

func faultSnapshotBytes(t *testing.T, st graphstore.Graphstore) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.snapshot")
	if err := st.Snapshot(context.Background(), p); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// mutateTree applies the identity-churning edit set the matrix replays on
// every kill point: a renamed symbol (renamingParser derives NodeId from the
// name: directive, so this MINTS a new node id), a deleted file, and a new
// file — the three shapes that can orphan state across an interrupted pass.
func mutateTree(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("name:Renamed\n"), 0o600); err != nil {
		t.Fatalf("edit a.go: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "c.go")); err != nil {
		t.Fatalf("delete c.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "d.go"), []byte("name:Added\n"), 0o600); err != nil {
		t.Fatalf("add d.go: %v", err)
	}
}

// TestFaultMatrix_FullPass_KillAtEveryBatchBoundary kills IngestAll at each of
// its three graph-batch boundaries on a FRESH store, then edits the tree and
// re-runs the full pass. The re-run must be byte-identical to a fresh index of
// the edited tree: nodes committed by the interrupted pass for since-renamed /
// since-deleted files must be purged even though the (rolled-back) meta cache
// never recorded them — the store-derived purge this story added to IngestAll.
func TestFaultMatrix_FullPass_KillAtEveryBatchBoundary(t *testing.T) {
	ctx := context.Background()
	tree := map[string]string{
		"a.go": "name:Original\n",
		"b.go": "name:Keeper\n",
		"c.go": "name:Doomed\n",
	}

	// Coverage guard: the matrix below kills at boundaries 1..3, so a clean
	// pass must open EXACTLY 3 batches — if IngestAll ever grows a fourth,
	// this fails loudly instead of the matrix silently under-covering it.
	t.Run("matrix-covers-every-boundary", func(t *testing.T) {
		fs := &batchFaultStore{Graphstore: graphstore.NewMemStore()}
		defer fs.Close()
		i := newIngester(t, fs, renamingParser{})
		if err := i.IngestAll(ctx, writeRepo(t, tree)); err != nil {
			t.Fatalf("clean IngestAll: %v", err)
		}
		if fs.batchCalls != 3 {
			t.Fatalf("IngestAll opened %d graph batches; the kill matrix covers exactly 3 — extend the matrix", fs.batchCalls)
		}
	})

	for kill := 1; kill <= 3; kill++ {
		t.Run(fmt.Sprintf("kill-before-batch-%d", kill), func(t *testing.T) {
			injected := fmt.Errorf("simulated crash before batch %d", kill)
			fs := &batchFaultStore{Graphstore: graphstore.NewMemStore(), failAtBatch: kill, injected: injected}
			defer fs.Close()
			i := newIngester(t, fs, renamingParser{})

			repo := writeRepo(t, tree)
			if err := i.IngestAll(ctx, repo); !isError(err, injected) {
				t.Fatalf("expected injected crash, got %v", err)
			}

			// The tree changes between the crash and the retry — the shape that
			// orphans interrupted-pass state.
			mutateTree(t, repo)

			fs.failAtBatch = 0 // disarm: the retry runs to completion
			if err := i.IngestAll(ctx, repo); err != nil {
				t.Fatalf("recovery full pass: %v", err)
			}

			// Reference: a store that never crashed, fresh full index of the
			// edited tree.
			ref := graphstore.NewMemStore()
			defer ref.Close()
			ri := newIngester(t, ref, renamingParser{})
			if err := ri.IngestAll(ctx, repo); err != nil {
				t.Fatalf("reference IngestAll: %v", err)
			}

			got, want := faultSnapshotBytes(t, fs.Graphstore), faultSnapshotBytes(t, ref)
			if !bytes.Equal(got, want) {
				t.Fatalf("full pass after crash-before-batch-%d did not converge to fresh-index bytes:\n got=%s\nwant=%s", kill, got, want)
			}
			// The healed store is warm-startable again (the retry stamped it).
			if _, ok, err := i.CanWarmStart(ctx, repo); err != nil || !ok {
				t.Fatalf("healed store not warm-startable: ok=%v err=%v", ok, err)
			}
		})
	}
}

// TestFaultMatrix_Incremental_KillAfterDurableGraphWrite kills the incremental
// pass AFTER one graph write is already durable (phase 2, mid-batch on the
// passthrough MemStore) — so the graph is ahead of the meta sidecar, whose
// phase-2 transaction (cache update, provenance, clear-dirty) rolled back.
// The durable phase-1 dirty rows must let RecoverWithRoot replay the units and
// converge byte-identically to an uninterrupted store.
func TestFaultMatrix_Incremental_KillAfterDurableGraphWrite(t *testing.T) {
	ctx := context.Background()
	tree := map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go",
	}

	fs := &writeFaultStore{Graphstore: graphstore.NewMemStore()}
	defer fs.Close()
	i := newIngester(t, fs, &stubParser{})
	repo := writeRepo(t, tree)
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	// Reference: an identical store whose incremental pass never crashes.
	ref := graphstore.NewMemStore()
	defer ref.Close()
	ri := newIngester(t, ref, &stubParser{})
	refRepo := writeRepo(t, tree)
	if err := ri.IngestAll(ctx, refRepo); err != nil {
		t.Fatalf("reference IngestAll: %v", err)
	}

	edit := []byte("package a\n//changed\n")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), edit, 0o600); err != nil {
		t.Fatalf("edit a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refRepo, "a.go"), edit, 0o600); err != nil {
		t.Fatalf("edit reference a.go: %v", err)
	}

	// Crash on the SECOND mutating write of the incremental pass: write 1 is
	// durable, the rest never happen, the meta phase-2 tx rolls back.
	injected := fmt.Errorf("simulated crash after a durable graph write")
	fs.failAtWrite, fs.injected = fs.writes+2, injected
	// The pipeline wraps store errors (%w), so match by errors.Is.
	if err := i.IngestChanged(ctx, repo, []string{"a.go"}); !errors.Is(err, injected) {
		t.Fatalf("expected injected crash, got %v", err)
	}
	fs.failAtWrite = 0 // disarm

	// The durable dirty rows drive recovery to convergence.
	if err := i.RecoverWithRoot(ctx, repo); err != nil {
		t.Fatalf("RecoverWithRoot: %v", err)
	}
	if err := ri.IngestChanged(ctx, refRepo, []string{"a.go"}); err != nil {
		t.Fatalf("reference IngestChanged: %v", err)
	}

	got, want := faultSnapshotBytes(t, fs.Graphstore), faultSnapshotBytes(t, ref)
	if !bytes.Equal(got, want) {
		t.Fatalf("incremental crash did not converge after recovery:\n got=%s\nwant=%s", got, want)
	}

	// Recovery cleared the dirty set: a second recovery is a no-op.
	if err := i.RecoverWithRoot(ctx, repo); err != nil {
		t.Fatalf("idempotent RecoverWithRoot: %v", err)
	}
}
