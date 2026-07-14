package main

// SW-118 (ING-DEC): proves the session-open path replays crash-recovery state.
// warmOrFullIngest now calls RecoverWithRoot BEFORE trusting the store — this
// test constructs the one divergence the drift pass CANNOT see and asserts the
// wiring heals it:
//
//  1. full-ingest a repo (symbol Old);
//  2. edit the file (symbol New) and crash the incremental pass AFTER a
//     durable graph delete but BEFORE any put (the old node is gone from the
//     graph; the meta phase-2 tx — cache update + clear-dirty — rolled back);
//  3. REVERT the file on disk. Now disk == meta cache, so DriftSet reports no
//     change and a drift-only warm start would serve the divergent graph
//     (missing node) forever. Only the durable dirty rows know better.
//
// warmOrFullIngest must replay the dirty units and converge the store to the
// reference bytes of an uninterrupted index of the (reverted) tree.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// putFaultStore fails the Nth PutNode; deletes and every other operation pass
// through. Over the passthrough MemStore batch this leaves earlier writes of
// the same "batch" durable — the mid-phase crash shape.
type putFaultStore struct {
	graphstore.Graphstore
	puts      int
	failAtPut int // 1-based; 0 disarms
	injected  error
}

func (f *putFaultStore) PutNode(ctx context.Context, n model.Node) error {
	f.puts++
	if f.failAtPut != 0 && f.puts == f.failAtPut {
		return f.injected
	}
	return f.Graphstore.PutNode(ctx, n)
}

func TestWarmOrFullIngest_ReplaysDirtyUnitsBeforeTrustingTheStore(t *testing.T) {
	ctx := context.Background()
	const original = "package p\n\nfunc Old() {}\n"
	const edited = "package p\n\nfunc New() {}\n"

	repo := t.TempDir()
	file := filepath.Join(repo, "a.go")
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatalf("write a.go: %v", err)
	}

	fs := &putFaultStore{Graphstore: graphstore.NewMemStore()}
	defer fs.Close()
	ing, err := ingest.New(fs, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer ing.Close()

	if err := warmOrFullIngest(ctx, ing, repo, nil); err != nil {
		t.Fatalf("initial warmOrFullIngest: %v", err)
	}

	// Edit, then crash the incremental pass on its FIRST re-put: the delete of
	// the old node is already durable, the replacement never lands, and the
	// meta phase-2 transaction rolls back (dirty rows stay set).
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatalf("edit a.go: %v", err)
	}
	injected := errors.New("simulated crash after durable delete")
	fs.failAtPut, fs.injected = fs.puts+1, injected
	if err := ing.IngestChanged(ctx, repo, []string{"a.go"}); !errors.Is(err, injected) {
		t.Fatalf("expected injected crash, got %v", err)
	}
	fs.failAtPut = 0 // disarm

	// Revert the file: disk now matches the (stale-but-rolled-back-to) meta
	// cache, so the drift pass alone would see NOTHING to do.
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatalf("revert a.go: %v", err)
	}

	var dirty int
	if err := ing.MetaDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM dirty_units").Scan(&dirty); err != nil {
		t.Fatalf("count dirty: %v", err)
	}
	if dirty == 0 {
		t.Fatalf("crash left no dirty units — the scenario no longer reproduces the divergence window")
	}

	// Session open: must recover (replay dirty) before the warm/drift decision.
	if err := warmOrFullIngest(ctx, ing, repo, nil); err != nil {
		t.Fatalf("session-open warmOrFullIngest: %v", err)
	}

	if err := ing.MetaDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM dirty_units").Scan(&dirty); err != nil {
		t.Fatalf("count dirty after recovery: %v", err)
	}
	if dirty != 0 {
		t.Fatalf("session open did not clear the dirty set (%d rows) — recovery is not wired", dirty)
	}

	// Convergence oracle: byte-identical to an uninterrupted index of the tree.
	ref := graphstore.NewMemStore()
	defer ref.Close()
	ringest, err := ingest.New(ref, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("reference ingest.New: %v", err)
	}
	defer ringest.Close()
	if err := ringest.IngestAll(ctx, repo); err != nil {
		t.Fatalf("reference IngestAll: %v", err)
	}

	snap := func(st graphstore.Graphstore) []byte {
		p := filepath.Join(t.TempDir(), "s.snapshot")
		if err := st.Snapshot(ctx, p); err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		return b
	}
	got, want := snap(fs.Graphstore), snap(ref)
	if !bytes.Equal(got, want) {
		t.Fatalf("recovered store diverges from uninterrupted reference:\n got=%s\nwant=%s", got, want)
	}
}
