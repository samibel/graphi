package ingest_test

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/trust"
)

// TestTrustSnapshot_AggregatePreservingMutationReadsStale is the adversarial
// pin for the count cross-check: a graph mutation in the crash window between
// the graph's commit and the snapshot rebind that PRESERVES the node and edge
// totals but changes the kind distribution must still read STALE (contract
// §1.6: "the graph changed after the snapshot"). A totals-only cross-check
// certifies this state CURRENT with stale per-kind/per-tier facts.
func TestTrustSnapshot_AggregatePreservingMutationReadsStale(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	before, err := store.TrustStats(ctx, 0)
	if err != nil {
		t.Fatalf("TrustStats: %v", err)
	}

	// Simulate the crash window: mutate the committed graph directly (no
	// source change, so drift stays clean and the cache stays certified)
	// swapping one edge's kind while keeping totals identical.
	var victim model.Edge
	if err := graphstore.ForEachEdge(ctx, store, func(e model.Edge) error {
		victim = e
		return nil
	}); err != nil {
		t.Fatalf("ForEachEdge: %v", err)
	}
	if victim.ID() == "" {
		t.Fatal("fixture broken: no committed edge to mutate")
	}
	if err := store.DeleteEdge(ctx, victim.ID()); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	swapped, err := model.NewEdge(victim.From(), victim.To(), "trust-probe-kind",
		victim.Tier(), victim.Confidence(), "adversarial kind swap", []string{"test"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	if err := store.PutEdge(ctx, swapped); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}

	after, err := store.TrustStats(ctx, 0)
	if err != nil {
		t.Fatalf("TrustStats: %v", err)
	}
	if after.NodesTotal != before.NodesTotal || after.EdgesTotal != before.EdgesTotal {
		t.Fatalf("fixture broken: totals moved (%d/%d -> %d/%d), the attack needs them preserved",
			before.NodesTotal, before.EdgesTotal, after.NodesTotal, after.EdgesTotal)
	}

	f := trustFreshness(ctx, t, ing, root)
	if !f.Current || !f.Index.WarmStartable {
		t.Fatalf("fixture broken: the mutation must be invisible to drift: %+v", f)
	}
	_, state, err := trust.Evaluate(ctx, store, f, liveGeneration(ctx, t, store))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state == trust.StateCurrent {
		t.Fatalf("FALSE GREEN: totals-preserving graph mutation after publish reads CURRENT")
	}
	if state != trust.StateStale {
		t.Fatalf("state = %s, want STALE for a graph changed after the snapshot", state)
	}
}

// portlessStore hides every optional port (TrustAggregatePort, Batcher,
// GraphScanner) behind the plain Graphstore interface — the shape of a wrapper
// store that forwards the core interface only.
type portlessStore struct{ graphstore.Graphstore }

// TestTrustSnapshot_PortlessWriterClearsSnapshot pins the port-missing skip
// path decision: a pass whose store cannot serve the trust aggregate must not
// RETAIN an older published triple either. On the incremental path the
// generation binding never moves, so a leftover snapshot satisfies every
// equality and would read CURRENT through a port-implementing reader while the
// graph has moved on. The pass therefore clears the three keys; readers then
// derive UNAVAILABLE ("no answer" — ADR 0006 D4), never a wrong answer.
func TestTrustSnapshot_PortlessWriterClearsSnapshot(t *testing.T) {
	ctx := context.Background()
	mem := graphstore.NewMemStore()
	t.Cleanup(func() { _ = mem.Close() })
	ing := newIngester(t, portlessStore{mem}, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	gen := liveGeneration(ctx, t, mem)

	// Seed the triple a port-capable writer would have left behind, bound to
	// the live generation and matching the live aggregate — the exact shape
	// that satisfies every DeriveState equality.
	seed := func() {
		stats, err := mem.TrustStats(ctx, 16)
		if err != nil {
			t.Fatalf("TrustStats: %v", err)
		}
		snap := trust.Snapshot{
			SchemaVersion:   trust.SnapshotSchemaVersion,
			SnapshotVersion: trust.SnapshotVersion,
			Generation:      trust.GenerationRef{FullPassGeneration: gen},
			Graph:           trust.NewGraphFacts(stats),
			External:        trust.NewExternalFacts(stats),
		}
		b, err := trust.Encode(snap)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		for key, value := range map[string]string{
			trust.MetaSnapshot:           string(b),
			trust.MetaSnapshotDigest:     trust.Digest(b),
			trust.MetaSnapshotGeneration: gen,
		} {
			if err := mem.SetMetadata(ctx, key, value); err != nil {
				t.Fatalf("seed %s: %v", key, err)
			}
		}
	}
	seed()
	// Sanity: the seeded triple genuinely reads CURRENT before the attack —
	// otherwise clearing proves nothing.
	if _, state, err := trust.Evaluate(ctx, mem, trustFreshness(ctx, t, ing, root), gen); err != nil || state != trust.StateCurrent {
		t.Fatalf("seeded triple = (%s, %v), want CURRENT", state, err)
	}
	// A port-less READER fails closed too: without the live aggregate there
	// is no cross-check, so even a verifying triple reads UNAVAILABLE.
	if _, state, err := trust.Evaluate(ctx, portlessStore{mem}, trustFreshness(ctx, t, ing, root), gen); err != nil || state != trust.StateUnavailable {
		t.Fatalf("Evaluate(port-less reader) = (%s, %v), want UNAVAILABLE", state, err)
	}

	// An aggregate-invisible edit (same-length comment churn) through the
	// portless writer: the graph is recommitted, the generation cannot move,
	// and the seeded triple would keep verifying if it were left behind.
	rewrite(t, root, "util/util.go", "package util\n\n// x\nfunc Answer() int { return 42 }\n")
	if err := ing.IngestChanged(ctx, root, []string{"util/util.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	if _, found, _, _, err := trust.Load(ctx, mem); err != nil || found {
		t.Fatalf("Load after portless incremental = (found %v, err %v), want the seeded triple cleared", found, err)
	}
	_, state, err := trust.Evaluate(ctx, mem, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, mem))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state == trust.StateCurrent {
		t.Fatalf("FALSE GREEN: portless incremental pass left a CURRENT-reading snapshot behind")
	}
	if state != trust.StateUnavailable {
		t.Fatalf("state = %s, want UNAVAILABLE after the portless pass cleared the triple", state)
	}

	// The full-pass path clears leftovers the same way.
	seed()
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll (second): %v", err)
	}
	if _, found, _, _, err := trust.Load(ctx, mem); err != nil || found {
		t.Fatalf("Load after portless full pass = (found %v, err %v), want the seeded triple cleared", found, err)
	}
}

// TestTrustSnapshot_InnerGenerationBindingChecked pins the digest-protected
// binding: the generation INSIDE the snapshot document must also equal the
// live generation. A triple whose outer key was rebound to the live generation
// around unrefreshed bytes (a partial restore of kv_meta, or any key-level
// corruption the digest cannot see) must read STALE, never CURRENT.
func TestTrustSnapshot_InnerGenerationBindingChecked(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	// Forge a verifying triple whose FACTS match the live graph but whose
	// inner binding names another pass; rebind the outer key to the live
	// generation (the digest does not cover it).
	stats, err := store.TrustStats(ctx, 16)
	if err != nil {
		t.Fatalf("TrustStats: %v", err)
	}
	snap := trust.Snapshot{
		SchemaVersion:   trust.SnapshotSchemaVersion,
		SnapshotVersion: trust.SnapshotVersion,
		Generation:      trust.GenerationRef{FullPassGeneration: "another-pass"},
		Graph:           trust.NewGraphFacts(stats),
		External:        trust.NewExternalFacts(stats),
	}
	b, err := trust.Encode(snap)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	gen := liveGeneration(ctx, t, store)
	for key, value := range map[string]string{
		trust.MetaSnapshot:           string(b),
		trust.MetaSnapshotDigest:     trust.Digest(b),
		trust.MetaSnapshotGeneration: gen,
	} {
		if err := store.SetMetadata(ctx, key, value); err != nil {
			t.Fatalf("forge %s: %v", key, err)
		}
	}

	_, state, err := trust.Evaluate(ctx, store, trustFreshness(ctx, t, ing, root), gen)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state == trust.StateCurrent {
		t.Fatalf("FALSE GREEN: outer generation key rebound around unrefreshed bytes reads CURRENT")
	}
	if state != trust.StateStale {
		t.Fatalf("state = %s, want STALE for an inner/outer generation mismatch", state)
	}
}

// snapshotWriteFailStore embeds the concrete MemStore (so every optional port,
// TrustAggregatePort included, stays visible) and fails the FIRST trust
// snapshot key write — the publish window's operational-failure shape.
type snapshotWriteFailStore struct {
	*graphstore.MemStore
	fail bool
}

func (s *snapshotWriteFailStore) SetMetadata(ctx context.Context, key, value string) error {
	if s.fail && key == trust.MetaSnapshot {
		return errors.New("injected trust snapshot write failure")
	}
	return s.MemStore.SetMetadata(ctx, key, value)
}

// TestTrustSnapshot_WriteFailureFailsThePass pins the loud-failure contract on
// the full-pass path: a snapshot publish failure fails IngestAll and leaves
// the full-pass marker open, so readers derive INCOMPLETE — a certified graph
// with a silently skipped snapshot is the one outcome §14.4 variant 3 forbids.
func TestTrustSnapshot_WriteFailureFailsThePass(t *testing.T) {
	ctx := context.Background()
	mem := graphstore.NewMemStore()
	t.Cleanup(func() { _ = mem.Close() })
	store := &snapshotWriteFailStore{MemStore: mem, fail: true}
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, typeresolveFixture())

	if err := ing.IngestAll(ctx, root); err == nil {
		t.Fatal("IngestAll succeeded despite the snapshot publish failing — silent skip")
	}
	marker, err := ing.FullPassInProgress(ctx)
	if err != nil {
		t.Fatalf("FullPassInProgress: %v", err)
	}
	if !marker {
		t.Fatal("full-pass marker cleared although the pass failed inside the publish window")
	}
	// The next pass with a healthy store recovers to a certified CURRENT.
	store.fail = false
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll (recovery): %v", err)
	}
	_, state, err := trust.Evaluate(ctx, mem, trustFreshness(ctx, t, ing, root), liveGeneration(ctx, t, mem))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != trust.StateCurrent {
		t.Fatalf("state = %s, want CURRENT after the recovery pass", state)
	}
}

// TestTrustSnapshot_NoAbsolutePathsPersisted pins data minimization on the
// persisted bytes: a full pass over a repo with a parse-skipped file publishes
// skip facts, and neither the repo root nor any absolute path appears in the
// canonical snapshot document (contract doc §2.3 rules 8–9).
func TestTrustSnapshot_NoAbsolutePathsPersisted(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	files := typeresolveFixture()
	files["assets/broken.json"] = "{not valid json"
	root := writeRepo(t, files)
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	snap, found, digestOK, _, err := trust.Load(ctx, store)
	if err != nil || !found || !digestOK {
		t.Fatalf("Load = (found %v, digestOK %v, err %v), want a verified snapshot", found, digestOK, err)
	}
	if snap.Parse.Skipped == 0 || len(snap.Parse.Paths) == 0 {
		t.Fatalf("fixture broken: no skip facts collected: %+v", snap.Parse)
	}
	raw, err := store.Metadata(ctx, trust.MetaSnapshot)
	if err != nil {
		t.Fatalf("read snapshot bytes: %v", err)
	}
	if strings.Contains(raw, root) {
		t.Errorf("persisted snapshot leaks the repo root %q:\n%s", root, raw)
	}
	for _, p := range snap.Parse.Paths {
		if path.IsAbs(p) || filepath.IsAbs(filepath.FromSlash(p)) {
			t.Errorf("persisted skip path %q is absolute", p)
		}
	}
	for _, u := range snap.TypeResolution.DegradedUnits {
		if path.IsAbs(u.Dir) {
			t.Errorf("persisted degraded unit dir %q is absolute", u.Dir)
		}
	}
}
