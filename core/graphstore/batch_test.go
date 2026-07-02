package graphstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	gs "github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// The batch contract runs against every backend via the package-level
// BeginBatch helper: SQLite exercises the native batched session, mem the
// passthrough fallback — the observable semantics must be identical.

func TestBatch_CommitVisibility(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t, b)
			n1 := mustNode(t, "function", "pkg.A", "a.go", 1, 1)
			n2 := mustNode(t, "function", "pkg.B", "b.go", 1, 1)
			e := mustEdge(t, n1.ID(), n2.ID(), "calls", model.TierConfirmed, 1, "call", []string{"a.go:1"})

			batch, err := gs.BeginBatch(ctx, s)
			if err != nil {
				t.Fatalf("BeginBatch: %v", err)
			}
			defer func() { _ = batch.Rollback() }()
			for _, put := range []func() error{
				func() error { return batch.PutNode(ctx, n1) },
				func() error { return batch.PutNode(ctx, n2) },
				func() error { return batch.PutEdge(ctx, e) },
			} {
				if err := put(); err != nil {
					t.Fatalf("batch write: %v", err)
				}
			}
			if err := batch.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}

			nodes, err := s.Nodes(ctx, gs.Query{})
			if err != nil {
				t.Fatalf("Nodes: %v", err)
			}
			if len(nodes) != 2 {
				t.Fatalf("committed nodes = %d, want 2", len(nodes))
			}
			if _, err := s.GetEdge(ctx, e.ID()); err != nil {
				t.Fatalf("committed edge missing: %v", err)
			}
		})
	}
}

func TestBatch_RollbackDiscards(t *testing.T) {
	// Only the SQLite backend buffers; the passthrough fallback documents that
	// writes are immediately durable, so rollback semantics are native-only.
	ctx := context.Background()
	s := newStore(t, backend{name: "sqlite", factory: gs.SQLiteFactory})
	n := mustNode(t, "function", "pkg.A", "a.go", 1, 1)

	batch, err := gs.BeginBatch(ctx, s)
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}
	if err := batch.PutNode(ctx, n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if err := batch.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := s.GetNode(ctx, n.ID()); !errors.Is(err, gs.ErrNotFound) {
		t.Fatalf("rolled-back node visible: err=%v", err)
	}
	// Rollback after Commit/Rollback is a no-op (defer-safe).
	if err := batch.Rollback(); err != nil {
		t.Fatalf("second Rollback: %v", err)
	}
	// Writes after the session ended fail typed.
	if err := batch.PutNode(ctx, n); !errors.Is(err, gs.ErrClosed) {
		t.Fatalf("write after rollback: err=%v, want ErrClosed", err)
	}
}

func TestBatch_EndpointSemantics(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t, b)
			committed := mustNode(t, "function", "pkg.Old", "old.go", 1, 1)
			if err := s.PutNode(ctx, committed); err != nil {
				t.Fatalf("seed node: %v", err)
			}

			batch, err := gs.BeginBatch(ctx, s)
			if err != nil {
				t.Fatalf("BeginBatch: %v", err)
			}
			defer func() { _ = batch.Rollback() }()

			local := mustNode(t, "function", "pkg.New", "new.go", 1, 1)
			if err := batch.PutNode(ctx, local); err != nil {
				t.Fatalf("PutNode: %v", err)
			}
			// Batch-local → committed endpoint resolves without a commit.
			e1 := mustEdge(t, local.ID(), committed.ID(), "calls", model.TierHeuristic, 0.9, "call", []string{"a.go:1"})
			if err := batch.PutEdge(ctx, e1); err != nil {
				t.Fatalf("edge to committed endpoint: %v", err)
			}
			// Unknown endpoint keeps the typed error.
			ghost := mustNode(t, "function", "pkg.Ghost", "ghost.go", 1, 1)
			e2 := mustEdge(t, local.ID(), ghost.ID(), "calls", model.TierHeuristic, 0.9, "call", []string{"a.go:1"})
			if err := batch.PutEdge(ctx, e2); !errors.Is(err, gs.ErrUnknownEdgeEndpoint) {
				t.Fatalf("edge to unknown endpoint: err=%v, want ErrUnknownEdgeEndpoint", err)
			}
			// A batch-local DELETE revokes endpoint eligibility immediately.
			if err := batch.DeleteNode(ctx, committed.ID()); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}
			e3 := mustEdge(t, local.ID(), committed.ID(), "references", model.TierHeuristic, 0.8, "ref", []string{"a.go:2"})
			if err := batch.PutEdge(ctx, e3); !errors.Is(err, gs.ErrUnknownEdgeEndpoint) {
				t.Fatalf("edge to batch-deleted endpoint: err=%v, want ErrUnknownEdgeEndpoint", err)
			}
			if err := batch.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
		})
	}
}

func TestBatch_DeleteNodeCascadesInsideBatch(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t, b)
			batch, err := gs.BeginBatch(ctx, s)
			if err != nil {
				t.Fatalf("BeginBatch: %v", err)
			}
			defer func() { _ = batch.Rollback() }()

			hub := mustNode(t, "function", "pkg.Hub", "hub.go", 1, 1)
			leaf := mustNode(t, "function", "pkg.Leaf", "leaf.go", 1, 1)
			edge := mustEdge(t, leaf.ID(), hub.ID(), "calls", model.TierConfirmed, 1, "call", []string{"a.go:1"})
			for _, op := range []func() error{
				func() error { return batch.PutNode(ctx, hub) },
				func() error { return batch.PutNode(ctx, leaf) },
				func() error { return batch.PutEdge(ctx, edge) },
				func() error { return batch.DeleteNode(ctx, hub.ID()) },
			} {
				if err := op(); err != nil {
					t.Fatalf("batch op: %v", err)
				}
			}
			if err := batch.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}

			if _, err := s.GetNode(ctx, hub.ID()); !errors.Is(err, gs.ErrNotFound) {
				t.Fatalf("deleted node visible: err=%v", err)
			}
			// The batch-local edge incident to the deleted node was cascaded.
			if _, err := s.GetEdge(ctx, edge.ID()); !errors.Is(err, gs.ErrNotFound) {
				t.Fatalf("cascaded edge visible: err=%v", err)
			}
			// The FTS index agrees: neither owner is searchable.
			hits, err := s.SearchNodes(ctx, "Hub", 10)
			if err != nil {
				t.Fatalf("SearchNodes: %v", err)
			}
			if len(hits) != 0 {
				t.Fatalf("deleted node still searchable: %v", hits)
			}
		})
	}
}

func TestBatch_FTSConsistencyAfterCommit(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t, b)
			// Pre-seed a committed node, then RE-PUT it with a changed qualified
			// name inside a batch: exactly one FTS row must survive, carrying the
			// new text (the rowid-keyed delete replaces the old row).
			old := mustNode(t, "function", "pkg.Before", "x.go", 1, 1)
			if err := s.PutNode(ctx, old); err != nil {
				t.Fatalf("seed: %v", err)
			}

			batch, err := gs.BeginBatch(ctx, s)
			if err != nil {
				t.Fatalf("BeginBatch: %v", err)
			}
			defer func() { _ = batch.Rollback() }()
			if err := batch.PutNode(ctx, old); err != nil { // idempotent re-put
				t.Fatalf("re-put: %v", err)
			}
			fresh := mustNode(t, "function", "pkg.After", "y.go", 2, 2)
			if err := batch.PutNode(ctx, fresh); err != nil {
				t.Fatalf("put fresh: %v", err)
			}
			if err := batch.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}

			for term, wantID := range map[string]model.NodeId{
				"Before": old.ID(),
				"After":  fresh.ID(),
			} {
				hits, err := s.SearchNodes(ctx, term, 10)
				if err != nil {
					t.Fatalf("SearchNodes(%q): %v", term, err)
				}
				if len(hits) != 1 || hits[0].Node.ID() != wantID {
					t.Fatalf("SearchNodes(%q) = %v, want exactly %s", term, hits, wantID)
				}
			}
		})
	}
}

func TestBatch_FailHookFiresOnCommit(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, backend{name: "sqlite", factory: gs.SQLiteFactory})
	sq, ok := s.(*gs.SQLiteStore)
	if !ok {
		t.Fatal("sqlite factory did not return *SQLiteStore")
	}
	n := mustNode(t, "function", "pkg.A", "a.go", 1, 1)

	injected := errors.New("post-commit fault")
	sq.SetFailAfterCommitHook(injected)

	batch, err := gs.BeginBatch(ctx, s)
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}
	if err := batch.PutNode(ctx, n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if err := batch.Commit(ctx); !errors.Is(err, injected) {
		t.Fatalf("Commit err = %v, want injected fault", err)
	}
	// Durable-first invariant: SQLite is authoritative, cache merely evicted.
	if _, err := s.GetNode(ctx, n.ID()); err != nil {
		t.Fatalf("node lost after post-commit fault: %v", err)
	}
}

func TestBatch_NonBatchWriteBlocksUntilCommit(t *testing.T) {
	// The single-writer discipline holds across a batch: a concurrent
	// one-transaction write waits for the open batch and then succeeds —
	// blocking, never deadlock, never interleaving into the batch tx.
	ctx := context.Background()
	s := newStore(t, backend{name: "sqlite", factory: gs.SQLiteFactory})
	inBatch := mustNode(t, "function", "pkg.InBatch", "a.go", 1, 1)
	outside := mustNode(t, "function", "pkg.Outside", "b.go", 1, 1)

	batch, err := gs.BeginBatch(ctx, s)
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}
	if err := batch.PutNode(ctx, inBatch); err != nil {
		t.Fatalf("PutNode: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- s.PutNode(ctx, outside) }()
	select {
	case err := <-done:
		t.Fatalf("non-batch write completed while batch open: %v", err)
	case <-time.After(100 * time.Millisecond):
		// expected: still blocked on the single-writer mutex
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("blocked write failed after commit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("non-batch write still blocked after batch commit (deadlock)")
	}
	if _, err := s.GetNode(ctx, outside.ID()); err != nil {
		t.Fatalf("post-batch write missing: %v", err)
	}
}
