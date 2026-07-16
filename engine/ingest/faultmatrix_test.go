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
	"github.com/samibel/graphi/engine/ingest"
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

// commitFaultStore returns an error only AFTER the selected native SQLite
// batch committed. Closing both databases immediately afterwards models a
// process death at a real durable boundary, not merely a failed BeginBatch.
type commitFaultStore struct {
	graphstore.Graphstore
	commits         int
	failAfterCommit int // absolute 1-based commit count; 0 disarms
	injected        error
}

func (f *commitFaultStore) BeginBatch(ctx context.Context) (graphstore.Batch, error) {
	b, err := graphstore.BeginBatch(ctx, f.Graphstore)
	if err != nil {
		return nil, err
	}
	return &commitFaultBatch{Batch: b, parent: f}, nil
}

type commitFaultBatch struct {
	graphstore.Batch
	parent *commitFaultStore
}

func (b *commitFaultBatch) Commit(ctx context.Context) error {
	if err := b.Batch.Commit(ctx); err != nil {
		return err
	}
	b.parent.commits++
	if b.parent.failAfterCommit != 0 && b.parent.commits == b.parent.failAfterCommit {
		return b.parent.injected
	}
	return nil
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

// revertTree restores mutateTree's source changes exactly. This is the
// adversarial crash shape for a hash-only warm start: the old sidecar cache and
// the reverted source agree, while the graph may contain the interrupted pass's
// new generation, so drift alone reports no work.
func revertTree(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("name:Original\n"), 0o600); err != nil {
		t.Fatalf("revert a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "c.go"), []byte("name:Doomed\n"), 0o600); err != nil {
		t.Fatalf("restore c.go: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "d.go")); err != nil {
		t.Fatalf("remove added d.go: %v", err)
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

// TestFaultMatrix_FullPass_SQLiteCloseReopen starts from an already certified
// warm store, mutates the tree, and then dies immediately after each durable
// graph batch of the replacement full pass. This includes the third (last)
// graph commit just before the sidecar transaction would commit. After genuine
// Close/Reopen, every partial generation must be cold and a forced full rebuild
// must converge to a fresh index.
func TestFaultMatrix_FullPass_SQLiteCloseReopen(t *testing.T) {
	ctx := context.Background()
	initial := map[string]string{
		"a.go": "name:Original\n",
		"b.go": "name:Keeper\n",
		"c.go": "name:Doomed\n",
	}

	for kill := 1; kill <= 3; kill++ {
		t.Run(fmt.Sprintf("crash-after-durable-batch-%d", kill), func(t *testing.T) {
			stateDir := t.TempDir()
			graphPath := filepath.Join(stateDir, "graphi.db")
			metaDir := filepath.Join(stateDir, "meta")
			repo := writeRepo(t, initial)

			sqlStore, err := graphstore.OpenSQLite(graphPath)
			if err != nil {
				t.Fatalf("OpenSQLite seed: %v", err)
			}
			faultStore := &commitFaultStore{Graphstore: sqlStore}
			ing, err := ingest.New(faultStore, renamingParser{}, metaDir)
			if err != nil {
				_ = sqlStore.Close()
				t.Fatalf("ingest.New seed: %v", err)
			}
			if err := ing.IngestAll(ctx, repo); err != nil {
				_ = ing.Close()
				_ = sqlStore.Close()
				t.Fatalf("seed IngestAll: %v", err)
			}
			if faultStore.commits != 3 {
				_ = ing.Close()
				_ = sqlStore.Close()
				t.Fatalf("seed full pass committed %d graph batches, want 3", faultStore.commits)
			}
			if _, ok, err := ing.CanWarmStart(ctx, repo); err != nil || !ok {
				_ = ing.Close()
				_ = sqlStore.Close()
				t.Fatalf("seed store not warm: ok=%v err=%v", ok, err)
			}

			mutateTree(t, repo)
			injected := fmt.Errorf("simulated process death after durable batch %d", kill)
			faultStore.failAfterCommit = faultStore.commits + kill
			faultStore.injected = injected
			if err := ing.IngestAll(ctx, repo); !errors.Is(err, injected) {
				_ = ing.Close()
				_ = sqlStore.Close()
				t.Fatalf("expected injected crash, got %v", err)
			}
			// Restore the source to exactly what the still-old sidecar cache
			// records. A hash-only restart would now see zero drift and serve the
			// interrupted graph generation indefinitely.
			revertTree(t, repo)
			// A process death drops both handles. The next checks intentionally use
			// fresh pools/connections against only the durable on-disk state.
			if err := ing.Close(); err != nil {
				_ = sqlStore.Close()
				t.Fatalf("close sidecar after crash: %v", err)
			}
			if err := sqlStore.Close(); err != nil {
				t.Fatalf("close graph after crash: %v", err)
			}

			reopenedStore, err := graphstore.OpenSQLite(graphPath)
			if err != nil {
				t.Fatalf("OpenSQLite after crash: %v", err)
			}
			t.Cleanup(func() { _ = reopenedStore.Close() })
			reopenedIng, err := ingest.New(reopenedStore, renamingParser{}, metaDir)
			if err != nil {
				t.Fatalf("ingest.New after crash: %v", err)
			}
			t.Cleanup(func() { _ = reopenedIng.Close() })

			var openGeneration string
			if err := reopenedIng.MetaDB().QueryRowContext(ctx,
				"SELECT value FROM ingest_semantics WHERE key = 'full_pass_in_progress'").Scan(&openGeneration); err != nil {
				t.Fatalf("read durable full-pass marker: %v", err)
			}
			graphGeneration, err := reopenedStore.Metadata(ctx, "index.full_ingest_generation")
			if err != nil {
				t.Fatalf("read durable graph generation: %v", err)
			}
			if openGeneration == "" || openGeneration != graphGeneration {
				t.Fatalf("recovery intent mismatch: marker=%q graph=%q", openGeneration, graphGeneration)
			}
			changed, deleted, err := reopenedIng.DriftSet(ctx, repo)
			if err != nil {
				t.Fatalf("drift after source revert: %v", err)
			}
			if len(changed) != 0 || len(deleted) != 0 {
				t.Fatalf("source-revert fixture has drift: changed=%v deleted=%v", changed, deleted)
			}
			if files, ok, err := reopenedIng.CanWarmStart(ctx, repo); err != nil || ok || files == 0 {
				t.Fatalf("partial generation trusted after reopen: files=%d ok=%v err=%v", files, ok, err)
			}

			// Session disposition for ok=false is a complete rebuild. It must both
			// converge and close/align the generation marker again.
			if err := reopenedIng.IngestAll(ctx, repo); err != nil {
				t.Fatalf("forced recovery full pass: %v", err)
			}
			if _, ok, err := reopenedIng.CanWarmStart(ctx, repo); err != nil || !ok {
				t.Fatalf("recovered store not warm: ok=%v err=%v", ok, err)
			}

			ref := graphstore.NewMemStore()
			defer ref.Close()
			refIng := newIngester(t, ref, renamingParser{})
			if err := refIng.IngestAll(ctx, repo); err != nil {
				t.Fatalf("reference IngestAll: %v", err)
			}
			got, want := faultSnapshotBytes(t, reopenedStore), faultSnapshotBytes(t, ref)
			if !bytes.Equal(got, want) {
				t.Fatalf("full pass after durable-batch-%d crash did not converge:\n got=%s\nwant=%s", kill, got, want)
			}
		})
	}
}

// TestFullPassGeneration_GraphFileRevert restores only the graph database to a
// prior, internally valid warm generation while leaving the sidecar at the
// latest generation. Content hashes and semantics still look valid on both
// files in isolation; the cross-store generation mismatch must nevertheless
// force a full rebuild after Close/Reopen.
func TestFullPassGeneration_GraphFileRevert(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	graphPath := filepath.Join(stateDir, "graphi.db")
	metaDir := filepath.Join(stateDir, "meta")
	repo := writeRepo(t, map[string]string{
		"a.go": "name:Original\n",
		"b.go": "name:Keeper\n",
		"c.go": "name:Doomed\n",
	})

	store, err := graphstore.OpenSQLite(graphPath)
	if err != nil {
		t.Fatalf("OpenSQLite generation one: %v", err)
	}
	ing, err := ingest.New(store, renamingParser{}, metaDir)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ingest.New generation one: %v", err)
	}
	if err := ing.IngestAll(ctx, repo); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatalf("generation-one IngestAll: %v", err)
	}
	firstGeneration, err := store.Metadata(ctx, "index.full_ingest_generation")
	if err != nil {
		t.Fatalf("read generation one: %v", err)
	}
	if err := ing.Close(); err != nil {
		_ = store.Close()
		t.Fatalf("close generation-one sidecar: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close generation-one graph: %v", err)
	}
	oldGraphFile, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatalf("backup generation-one graph file: %v", err)
	}

	mutateTree(t, repo)
	store, err = graphstore.OpenSQLite(graphPath)
	if err != nil {
		t.Fatalf("OpenSQLite generation two: %v", err)
	}
	ing, err = ingest.New(store, renamingParser{}, metaDir)
	if err != nil {
		_ = store.Close()
		t.Fatalf("ingest.New generation two: %v", err)
	}
	if err := ing.IngestAll(ctx, repo); err != nil {
		_ = ing.Close()
		_ = store.Close()
		t.Fatalf("generation-two IngestAll: %v", err)
	}
	secondGeneration, err := store.Metadata(ctx, "index.full_ingest_generation")
	if err != nil {
		t.Fatalf("read generation two: %v", err)
	}
	if firstGeneration == secondGeneration {
		t.Fatalf("two full passes reused generation %q", firstGeneration)
	}
	if err := ing.Close(); err != nil {
		_ = store.Close()
		t.Fatalf("close generation-two sidecar: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close generation-two graph: %v", err)
	}

	// Revert only the graph DB. Sidecars are removed in case the platform kept
	// empty WAL artifacts after the last close; no generation-two WAL may be
	// replayed over the restored generation-one main file.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(graphPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove graph SQLite sidecar %s: %v", suffix, err)
		}
	}
	if err := os.WriteFile(graphPath, oldGraphFile, 0o600); err != nil {
		t.Fatalf("restore generation-one graph file: %v", err)
	}

	revertedStore, err := graphstore.OpenSQLite(graphPath)
	if err != nil {
		t.Fatalf("OpenSQLite reverted graph: %v", err)
	}
	t.Cleanup(func() { _ = revertedStore.Close() })
	revertedIng, err := ingest.New(revertedStore, renamingParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New reverted graph: %v", err)
	}
	t.Cleanup(func() { _ = revertedIng.Close() })
	graphGeneration, err := revertedStore.Metadata(ctx, "index.full_ingest_generation")
	if err != nil {
		t.Fatalf("read reverted graph generation: %v", err)
	}
	var metaGeneration string
	if err := revertedIng.MetaDB().QueryRowContext(ctx,
		"SELECT value FROM ingest_semantics WHERE key = 'full_pass_generation'").Scan(&metaGeneration); err != nil {
		t.Fatalf("read latest sidecar generation: %v", err)
	}
	if graphGeneration != firstGeneration || metaGeneration != secondGeneration {
		t.Fatalf("revert fixture generations: graph=%q (want %q), meta=%q (want %q)", graphGeneration, firstGeneration, metaGeneration, secondGeneration)
	}
	if files, ok, err := revertedIng.CanWarmStart(ctx, repo); err != nil || ok || files == 0 {
		t.Fatalf("reverted graph trusted: files=%d ok=%v err=%v", files, ok, err)
	}

	if err := revertedIng.IngestAll(ctx, repo); err != nil {
		t.Fatalf("full rebuild after graph revert: %v", err)
	}
	if _, ok, err := revertedIng.CanWarmStart(ctx, repo); err != nil || !ok {
		t.Fatalf("rebuilt reverted store not warm: ok=%v err=%v", ok, err)
	}
	ref := graphstore.NewMemStore()
	defer ref.Close()
	refIng := newIngester(t, ref, renamingParser{})
	if err := refIng.IngestAll(ctx, repo); err != nil {
		t.Fatalf("reference IngestAll: %v", err)
	}
	got, want := faultSnapshotBytes(t, revertedStore), faultSnapshotBytes(t, ref)
	if !bytes.Equal(got, want) {
		t.Fatalf("rebuild after graph-file revert did not converge:\n got=%s\nwant=%s", got, want)
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
