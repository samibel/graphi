package ingest_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/freshness"
)

// trustFreshness derives a freshness.Report from the ingester's own read-only
// probes — the same facts internal/freshness/probe reads — so the Evaluate
// inputs stay real instead of hand-picked.
func trustFreshness(ctx context.Context, t *testing.T, ing *ingest.Ingester, root string) freshness.Report {
	t.Helper()
	var f freshness.Report
	f.Index.Exists = true
	files, warm, err := ing.CanWarmStart(ctx, root)
	if err != nil {
		t.Fatalf("CanWarmStart: %v", err)
	}
	f.Index.FilesCached = files
	f.Index.WarmStartable = warm
	marker, err := ing.FullPassInProgress(ctx)
	if err != nil {
		t.Fatalf("FullPassInProgress: %v", err)
	}
	f.Index.FullPassInProgress = marker
	if warm {
		d, err := ing.DriftDetail(ctx, root, nil)
		if err != nil {
			t.Fatalf("DriftDetail: %v", err)
		}
		f.Drift = freshness.Drift{Added: len(d.Added), Changed: len(d.Modified), Removed: len(d.Deleted)}
		f.Current = d.Total() == 0
	}
	return f
}

func liveGeneration(ctx context.Context, t *testing.T, store graphstore.Graphstore) string {
	t.Helper()
	gen, err := store.Metadata(ctx, "index.full_ingest_generation")
	if err != nil {
		t.Fatalf("read live generation: %v", err)
	}
	return gen
}

func loadFactDigest(ctx context.Context, t *testing.T, store graphstore.Graphstore) string {
	t.Helper()
	snap, found, digestOK, _, err := trust.Load(ctx, store)
	if err != nil || !found || !digestOK {
		t.Fatalf("Load = (found %v, digestOK %v, err %v), want a verified snapshot", found, digestOK, err)
	}
	fd, err := trust.FactDigest(snap)
	if err != nil {
		t.Fatalf("FactDigest: %v", err)
	}
	return fd
}

// TestTrustSnapshot_FullPassPublishesCurrent pins the happy path: a completed
// full pass publishes the verifying key triple bound to the live generation,
// the snapshot's counts equal the store's, and Evaluate derives CURRENT.
func TestTrustSnapshot_FullPassPublishesCurrent(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	raw, err := store.Metadata(ctx, trust.MetaSnapshot)
	if err != nil {
		t.Fatalf("snapshot bytes missing after full pass: %v", err)
	}
	digest, err := store.Metadata(ctx, trust.MetaSnapshotDigest)
	if err != nil {
		t.Fatalf("snapshot digest missing after full pass: %v", err)
	}
	if got := trust.Digest([]byte(raw)); got != digest {
		t.Errorf("stored digest %s does not verify against the stored bytes (%s)", digest, got)
	}
	gen, err := store.Metadata(ctx, trust.MetaSnapshotGeneration)
	if err != nil {
		t.Fatalf("snapshot generation missing after full pass: %v", err)
	}
	if live := liveGeneration(ctx, t, store); gen == "" || gen != live {
		t.Errorf("snapshot generation %q, want the live generation %q (non-empty)", gen, live)
	}

	snap, state, err := trust.Evaluate(ctx, store, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, store))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != trust.StateCurrent {
		t.Fatalf("state = %s, want CURRENT after an uninterrupted full pass", state)
	}

	stats, err := store.TrustStats(ctx, 0)
	if err != nil {
		t.Fatalf("TrustStats: %v", err)
	}
	if snap.Graph.NodesTotal != stats.NodesTotal || snap.Graph.EdgesTotal != stats.EdgesTotal {
		t.Errorf("snapshot counts (%d nodes, %d edges) != store counts (%d, %d)",
			snap.Graph.NodesTotal, snap.Graph.EdgesTotal, stats.NodesTotal, stats.EdgesTotal)
	}
	brief, err := store.BriefStats(ctx, 0)
	if err != nil {
		t.Fatalf("BriefStats: %v", err)
	}
	wantTiers := trust.TierCounts{
		Confirmed: brief.TierCounts[model.TierConfirmed],
		Derived:   brief.TierCounts[model.TierDerived],
		Heuristic: brief.TierCounts[model.TierHeuristic],
	}
	if snap.Graph.EdgesByTier != wantTiers {
		t.Errorf("EdgesByTier = %+v, want the BriefStats tier counts %+v", snap.Graph.EdgesByTier, wantTiers)
	}
	if snap.Graph.EdgesByTier.Confirmed == 0 {
		t.Error("fixture's typeresolve-confirmed edge is missing from the snapshot tier counts")
	}
	if snap.TypeResolution.UnitsTotal == 0 || snap.TypeResolution.ConfirmedEdges == 0 {
		t.Errorf("typeresolve summary was not collected: %+v", snap.TypeResolution)
	}
}

// TestTrustSnapshot_InterruptedPassReadsIncomplete simulates the crash window:
// with the full-pass recovery marker open, the snapshot state must read
// INCOMPLETE — never CURRENT — regardless of the stored triple.
func TestTrustSnapshot_InterruptedPassReadsIncomplete(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	// Re-open the marker exactly as beginFullPass persists it (the same
	// tamper the status lock-state tests use for an interrupted pass).
	if _, err := ing.MetaDB().ExecContext(ctx,
		"INSERT INTO ingest_semantics(key, value) VALUES('full_pass_in_progress', 'deadbeef') ON CONFLICT(key) DO UPDATE SET value = excluded.value"); err != nil {
		t.Fatalf("reopen full-pass marker: %v", err)
	}
	f := trustFreshness(ctx, t, ing, root)
	if !f.Index.FullPassInProgress || f.Index.WarmStartable {
		t.Fatalf("fixture broken: marker not visible in the freshness facts: %+v", f.Index)
	}

	_, state, err := trust.Evaluate(ctx, store, f, liveGeneration(ctx, t, store))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != trust.StateIncomplete {
		t.Fatalf("state = %s, want INCOMPLETE while the full-pass marker is open (never CURRENT)", state)
	}
}

// TestTrustSnapshot_CorruptBytesReadUnavailable overwrites the stored snapshot
// with garbage: the digest no longer verifies, so the reader fails closed to
// UNAVAILABLE without surfacing an error.
func TestTrustSnapshot_CorruptBytesReadUnavailable(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := store.SetMetadata(ctx, trust.MetaSnapshot, "{corrupt"); err != nil {
		t.Fatalf("corrupt snapshot: %v", err)
	}

	_, state, err := trust.Evaluate(ctx, store, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, store))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != trust.StateUnavailable {
		t.Fatalf("state = %s, want UNAVAILABLE for a corrupt snapshot", state)
	}
}

// TestTrustSnapshot_GenerationMismatchReadsStale rebinds the stored generation
// stamp to a different value: the snapshot describes another pass, so the
// reader derives STALE.
func TestTrustSnapshot_GenerationMismatchReadsStale(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := store.SetMetadata(ctx, trust.MetaSnapshotGeneration, "not-the-live-generation"); err != nil {
		t.Fatalf("rebind generation: %v", err)
	}

	_, state, err := trust.Evaluate(ctx, store, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, store))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != trust.StateStale {
		t.Fatalf("state = %s, want STALE for a generation mismatch", state)
	}
}

// editedTrustFixture is typeresolveFixture with one edit that changes the
// committed fact surface (an added function ⇒ new node and counts), so the
// parity test can also prove the fact digest is sensitive to the edit.
func editedTrustFixture() map[string]string {
	files := typeresolveFixture()
	files["util/util.go"] = "package util\n\nfunc Answer() int { return 43 }\n\nfunc Extra() int { return 1 }\n"
	return files
}

// TestTrustSnapshot_FullIncrementalFactParity is the ADR 0006 D3 parity pin
// for the snapshot facts: an incremental pass over an edit publishes the same
// FACT digest as a full pass over the final source state. The full canonical
// digests differ by construction (FullPassGeneration is a per-pass nonce), so
// the comparison uses trust.FactDigest — the decision recorded in
// engine/trust/serialize.go.
func TestTrustSnapshot_FullIncrementalFactParity(t *testing.T) {
	ctx := context.Background()

	// Baseline: full pass over the INITIAL state, proving below that the
	// fact digest actually moves with the edit.
	storeA := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeA.Close() })
	ingA := newIngester(t, storeA, parse.NewDefaultRegistry())
	rootA := writeRepo(t, typeresolveFixture())
	if err := ingA.IngestAll(ctx, rootA); err != nil {
		t.Fatalf("IngestAll (initial): %v", err)
	}
	digestInitial := loadFactDigest(ctx, t, storeA)

	// Incremental: full pass on the initial state, then the edit through
	// IngestChanged.
	storeB := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeB.Close() })
	ingB := newIngester(t, storeB, parse.NewDefaultRegistry())
	rootB := writeRepo(t, typeresolveFixture())
	if err := ingB.IngestAll(ctx, rootB); err != nil {
		t.Fatalf("IngestAll (pre-edit): %v", err)
	}
	rewrite(t, rootB, "util/util.go", editedTrustFixture()["util/util.go"])
	if err := ingB.IngestChanged(ctx, rootB, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	digestIncremental := loadFactDigest(ctx, t, storeB)

	// Reference: full pass directly over the EDITED state.
	storeC := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeC.Close() })
	ingC := newIngester(t, storeC, parse.NewDefaultRegistry())
	rootC := writeRepo(t, editedTrustFixture())
	if err := ingC.IngestAll(ctx, rootC); err != nil {
		t.Fatalf("IngestAll (edited): %v", err)
	}
	digestFull := loadFactDigest(ctx, t, storeC)

	if digestIncremental != digestFull {
		t.Errorf("incremental fact digest %s != full-pass-over-final-state fact digest %s", digestIncremental, digestFull)
	}
	if digestInitial == digestIncremental {
		t.Error("fact digest did not change across the edit — an insensitive digest proves nothing")
	}

	// The incremental rewrite also rebinds the triple: the snapshot stays
	// generation-bound and evaluates CURRENT on the incrementally updated
	// store.
	_, state, err := trust.Evaluate(ctx, storeB, trustFreshness(ctx, t, ingB, rootB), liveGeneration(ctx, t, storeB))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != trust.StateCurrent {
		t.Errorf("state = %s, want CURRENT after the incremental snapshot rewrite", state)
	}
}
