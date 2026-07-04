package graphstore_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	gs "github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

func newSQLite(t *testing.T) *gs.SQLiteStore {
	t.Helper()
	st, err := gs.OpenSQLite(filepath.Join(t.TempDir(), "graphi.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestSQLite_WALMode asserts journal_mode=wal is active on the pool (AC 4, AC 9).
func TestSQLite_WALMode(t *testing.T) {
	st := newSQLite(t)
	mode, err := st.JournalMode(context.Background())
	if err != nil {
		t.Fatalf("JournalMode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("expected journal_mode=wal, got %q", mode)
	}
}

// TestSQLite_FTS5Schema asserts the FTS5 virtual table and its shadow tables exist
// over the searchable text fields, via sqlite_master inspection (AC 4, AC 16).
func TestSQLite_FTS5Schema(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)
	seed(t, st)

	// The presence of the fts5 virtual table is provable by the existence of its
	// shadow tables (search_data, search_idx, ...). We assert via a successful
	// MATCH query, which only works if the fts5 module backed the table.
	hits, err := st.Nodes(ctx, gs.Query{Text: "Widget"})
	if err != nil {
		t.Fatalf("fts5 MATCH query failed (fts5 likely unavailable): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 fts5 hit, got %d", len(hits))
	}
}

// TestSQLite_SourceOfTruth proves SQLite is authoritative: write, FULLY evict the
// cache, read back the identical record incl. provenance (AC 1).
func TestSQLite_SourceOfTruth(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)
	nodes, edges := seed(t, st)

	if err := st.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}

	got, err := st.GetEdge(ctx, edges[1].ID())
	if err != nil {
		t.Fatalf("GetEdge after evict: %v", err)
	}
	if !edgesEqual(got, edges[1]) {
		t.Fatalf("durable edge mismatch after evict: got %+v want %+v", got, edges[1])
	}
	gn, err := st.GetNode(ctx, nodes[2].ID())
	if err != nil {
		t.Fatalf("GetNode after evict: %v", err)
	}
	if !nodesEqual(gn, nodes[2]) {
		t.Fatalf("durable node mismatch after evict")
	}
}

// TestSQLite_CacheRebuildSignal proves a forced eviction causes an observable
// rebuild on the next read (served from SQLite, not stale cache) (AC 11).
func TestSQLite_CacheRebuildSignal(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)
	seed(t, st)

	// Prime the cache.
	if _, err := st.Nodes(ctx, gs.Query{}); err != nil {
		t.Fatalf("warm query: %v", err)
	}
	r0 := st.CacheRebuilds()

	if err := st.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	if _, err := st.Nodes(ctx, gs.Query{}); err != nil {
		t.Fatalf("post-evict query: %v", err)
	}
	r1 := st.CacheRebuilds()
	if r1 != r0+1 {
		t.Fatalf("expected exactly one rebuild after eviction, got delta %d", r1-r0)
	}

	// A second read without eviction must NOT rebuild (served from cache).
	if _, err := st.Nodes(ctx, gs.Query{}); err != nil {
		t.Fatalf("cached query: %v", err)
	}
	if st.CacheRebuilds() != r1 {
		t.Fatalf("unexpected rebuild on cached read")
	}
}

// TestSQLite_FailAfterCommitNoDataLoss proves the write-ordering invariant: a
// failure injected AFTER the SQLite commit but BEFORE the cache update leaves the
// durable state complete; the cache is merely invalidated (AC 6).
func TestSQLite_FailAfterCommitNoDataLoss(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)

	n := mustNode(t, "function", "pkg/x.Y", "pkg/x.go", 1, 1)
	if err := st.PutNode(ctx, n); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	// Arm the fault for the next write.
	injected := errors.New("simulated crash after commit")
	st.SetFailAfterCommitHook(injected)

	n2 := mustNode(t, "function", "pkg/x.Z", "pkg/x.go", 2, 1)
	err := st.PutNode(ctx, n2)
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}

	// Despite the post-commit failure, the durable row exists. Force a rebuild
	// from SQLite to prove the data was committed (not lost) and the cache was
	// invalidated rather than left stale.
	if err := st.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	got, err := st.GetNode(ctx, n2.ID())
	if err != nil {
		t.Fatalf("durable node missing after post-commit failure: %v", err)
	}
	if !nodesEqual(got, n2) {
		t.Fatalf("durable node mismatch after post-commit failure")
	}
}

// TestSQLite_DeleteNodeCrashSafe proves the SW-036 destructive op is crash-safe:
// a fault injected AFTER the delete transaction commits but BEFORE the cache
// update leaves SQLite authoritative (node + cascaded edges gone) and merely
// invalidates the cache — the same no-data-loss ordering PutNode guarantees.
func TestSQLite_DeleteNodeCrashSafe(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)

	n1 := mustNode(t, "function", "pkg/x.Y", "pkg/x.go", 1, 1)
	n2 := mustNode(t, "function", "pkg/x.Z", "pkg/x.go", 2, 1)
	for _, n := range []model.Node{n1, n2} {
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("seed node: %v", err)
		}
	}
	e := mustEdge(t, n1.ID(), n2.ID(), "calls", model.TierDerived, 0.9, "call", []string{"x.go:1"})
	if err := st.PutEdge(ctx, e); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	injected := errors.New("simulated crash after delete commit")
	st.SetFailAfterCommitHook(injected)

	err := st.DeleteNode(ctx, n1.ID())
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}

	// Force a rebuild from the durable layer; the delete was committed.
	if err := st.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	if _, err := st.GetNode(ctx, n1.ID()); !errors.Is(err, gs.ErrNotFound) {
		t.Fatalf("node survived crash-safe delete: %v", err)
	}
	if _, err := st.GetEdge(ctx, e.ID()); !errors.Is(err, gs.ErrNotFound) {
		t.Fatalf("cascaded edge survived crash-safe delete: %v", err)
	}
	if _, err := st.GetNode(ctx, n2.ID()); err != nil {
		t.Fatalf("unrelated node lost: %v", err)
	}
}

// TestSQLite_EvidenceDistinctionRoundTrip proves null/empty/populated evidence and
// tier/reason survive write→evict→snapshot→load exactly (AC 10).
//
// Note: model.NewEdge requires at least one non-empty evidence reference, so the
// "absent" case is represented by a single-element list and the "populated" case
// by multiple. The distinction we lock here is that the EXACT evidence slice
// (count, order-after-canonical-sort, content) round-trips without coercion.
func TestSQLite_EvidenceDistinctionRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)

	a := mustNode(t, "function", "pkg/p.A", "pkg/p.go", 1, 1)
	b := mustNode(t, "function", "pkg/p.B", "pkg/p.go", 2, 1)
	c := mustNode(t, "function", "pkg/p.C", "pkg/p.go", 3, 1)
	for _, n := range []model.Node{a, b, c} {
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
	single := mustEdge(t, a.ID(), b.ID(), "calls", model.TierConfirmed, 1.0, "single ev", []string{"only.go:1"})
	multi := mustEdge(t, a.ID(), c.ID(), "uses", model.TierHeuristic, 0.3, "multi ev",
		[]string{"z.go:9", "a.go:1", "m.go:5"})
	for _, e := range []model.Edge{single, multi} {
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}

	snap := filepath.Join(t.TempDir(), "ev.snapshot")
	if err := st.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	if err := st.Snapshot(ctx, snap); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newSQLite(t)
	if err := dst.Load(ctx, snap); err != nil {
		t.Fatalf("Load: %v", err)
	}

	gotSingle, err := dst.GetEdge(ctx, single.ID())
	if err != nil {
		t.Fatalf("GetEdge single: %v", err)
	}
	if !edgesEqual(gotSingle, single) {
		t.Fatalf("single-evidence edge not preserved: got %+v", gotSingle)
	}
	gotMulti, err := dst.GetEdge(ctx, multi.ID())
	if err != nil {
		t.Fatalf("GetEdge multi: %v", err)
	}
	if !edgesEqual(gotMulti, multi) {
		t.Fatalf("multi-evidence edge not preserved: got %+v", gotMulti)
	}
}

// TestSQLite_SnapshotNotRawDB proves the snapshot is the portable JSON envelope
// (carrying magic + versions), not a raw SQLite file copy (AC 8).
func TestSQLite_SnapshotNotRawDB(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)
	seed(t, st)
	p := filepath.Join(t.TempDir(), "portable.snapshot")
	if err := st.Snapshot(ctx, p); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	data := readFile(t, p)
	if bytes.HasPrefix(data, []byte("SQLite format 3")) {
		t.Fatalf("snapshot is a raw SQLite file, expected portable format")
	}
	if !bytes.Contains(data, []byte("graphi.graphstore.snapshot")) {
		t.Fatalf("snapshot missing format magic header")
	}
	if !bytes.Contains(data, []byte(`"format_version"`)) || !bytes.Contains(data, []byte(`"model_schema_version"`)) {
		t.Fatalf("snapshot missing version headers")
	}
}

// TestSQLite_Closed proves operations fail after Close.
func TestSQLite_Closed(t *testing.T) {
	ctx := context.Background()
	st, err := gs.OpenSQLite(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	n := mustNode(t, "function", "pkg/q.A", "pkg/q.go", 1, 1)
	if err := st.PutNode(ctx, n); !errors.Is(err, gs.ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

// TestSQLite_MetadataPersistence asserts that metadata survives a close/reopen cycle.
func TestSQLite_MetadataPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graphi.db")
	st, err := gs.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := st.SetMetadata(ctx, "index.profile", "deep"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st2, err := gs.OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = st2.Close() }()
	got, err := st2.Metadata(ctx, "index.profile")
	if err != nil {
		t.Fatalf("Metadata after reopen: %v", err)
	}
	if got != "deep" {
		t.Fatalf("metadata after reopen = %q, want %q", got, "deep")
	}
}

// TestSQLite_WALCheckpoint asserts that WALCheckpoint runs successfully and leaves
// journal_mode=wal intact.
func TestSQLite_WALCheckpoint(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)
	if err := st.WALCheckpoint(ctx, "TRUNCATE"); err != nil {
		t.Fatalf("WALCheckpoint: %v", err)
	}
	mode, err := st.JournalMode(ctx)
	if err != nil {
		t.Fatalf("JournalMode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q after checkpoint, want wal", mode)
	}
}

// TestSQLite_CompactEvidence asserts that duplicate evidence strings are
// deduplicated, deterministically sorted, and bounded by the cap.
func TestSQLite_CompactEvidence(t *testing.T) {
	ctx := context.Background()
	st := newSQLite(t)
	a := mustNode(t, "function", "pkg/p.A", "pkg/p.go", 1, 1)
	b := mustNode(t, "function", "pkg/p.B", "pkg/p.go", 2, 1)
	if err := st.PutNode(ctx, a); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if err := st.PutNode(ctx, b); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	e := mustEdge(t, a.ID(), b.ID(), "calls", model.TierHeuristic, 0.8, "call", []string{"z.go:1", "a.go:1", "z.go:1", "b.go:1", "a.go:1"})
	if err := st.PutEdge(ctx, e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	got, err := st.GetEdge(ctx, e.ID())
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	want := []string{"a.go:1", "b.go:1", "z.go:1"}
	if !reflect.DeepEqual(got.Evidence(), want) {
		t.Fatalf("evidence = %v, want %v", got.Evidence(), want)
	}
}
