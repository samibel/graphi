package trust_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/freshness"
)

// publish writes the three snapshot keys the way the ingest wiring does, so
// the Load tests exercise the exact persisted triple.
func publish(ctx context.Context, t *testing.T, store graphstore.Graphstore, s trust.Snapshot, generation string) []byte {
	t.Helper()
	b := mustEncode(t, s)
	if err := store.SetMetadata(ctx, trust.MetaSnapshot, string(b)); err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	if err := store.SetMetadata(ctx, trust.MetaSnapshotDigest, trust.Digest(b)); err != nil {
		t.Fatalf("publish digest: %v", err)
	}
	if err := store.SetMetadata(ctx, trust.MetaSnapshotGeneration, generation); err != nil {
		t.Fatalf("publish generation: %v", err)
	}
	return b
}

func TestLoad_MissingSnapshot(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	_, rawFound, digestOK, generation, err := trust.Load(ctx, store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rawFound || digestOK || generation != "" {
		t.Errorf("Load(empty store) = (found %v, digestOK %v, gen %q), want (false, false, \"\")", rawFound, digestOK, generation)
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	want := buildSnapshot(t, false)
	publish(ctx, t, store, want, "gen-1")

	got, rawFound, digestOK, generation, err := trust.Load(ctx, store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !rawFound || !digestOK || generation != "gen-1" {
		t.Fatalf("Load = (found %v, digestOK %v, gen %q), want (true, true, gen-1)", rawFound, digestOK, generation)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestLoad_FailsClosed pins every broken-triple shape onto (rawFound=true,
// digestOK=false, err=nil): the corruption is reported through digestOK,
// never masked behind an error and never partially interpreted.
func TestLoad_FailsClosed(t *testing.T) {
	ctx := context.Background()
	snap := buildSnapshot(t, false)
	unsupported := snap
	unsupported.SchemaVersion = trust.SnapshotSchemaVersion + 1

	cases := []struct {
		name    string
		corrupt func(t *testing.T, store graphstore.Graphstore)
	}{
		{"garbage bytes", func(t *testing.T, store graphstore.Graphstore) {
			publish(ctx, t, store, snap, "gen-1")
			if err := store.SetMetadata(ctx, trust.MetaSnapshot, "{corrupt"); err != nil {
				t.Fatalf("corrupt: %v", err)
			}
		}},
		{"digest mismatch", func(t *testing.T, store graphstore.Graphstore) {
			b := publish(ctx, t, store, snap, "gen-1")
			flipped := append([]byte{}, b...)
			flipped[len(flipped)-2] ^= 0x01
			if err := store.SetMetadata(ctx, trust.MetaSnapshot, string(flipped)); err != nil {
				t.Fatalf("flip: %v", err)
			}
		}},
		{"digest key missing", func(t *testing.T, store graphstore.Graphstore) {
			b := mustEncode(t, snap)
			if err := store.SetMetadata(ctx, trust.MetaSnapshot, string(b)); err != nil {
				t.Fatalf("bytes only: %v", err)
			}
		}},
		{"unsupported schema with a valid digest", func(t *testing.T, store graphstore.Graphstore) {
			publish(ctx, t, store, unsupported, "gen-1")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := graphstore.NewMemStore()
			t.Cleanup(func() { _ = store.Close() })
			tc.corrupt(t, store)
			got, rawFound, digestOK, _, err := trust.Load(ctx, store)
			if err != nil {
				t.Fatalf("Load: %v (corruption must not surface as an error)", err)
			}
			if !rawFound || digestOK {
				t.Errorf("Load = (found %v, digestOK %v), want (true, false)", rawFound, digestOK)
			}
			if !reflect.DeepEqual(got, trust.Snapshot{}) {
				t.Errorf("Load returned a partially interpreted snapshot: %+v", got)
			}
		})
	}
}

// TestLoad_ClearedTombstoneReadsAbsent pins the tombstone contract: a
// port-less pass clears a stale publish by emptying the keys (bytes first),
// and Load must read that as absent — including the mid-clear crash shape
// where only the bytes were emptied and digest/generation still verify
// against nothing.
func TestLoad_ClearedTombstoneReadsAbsent(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	publish(ctx, t, store, buildSnapshot(t, false), "gen-1")
	if err := store.SetMetadata(ctx, trust.MetaSnapshot, ""); err != nil {
		t.Fatalf("clear bytes: %v", err)
	}

	_, rawFound, digestOK, generation, err := trust.Load(ctx, store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rawFound || digestOK || generation != "" {
		t.Errorf("Load(cleared bytes) = (found %v, digestOK %v, gen %q), want (false, false, \"\")", rawFound, digestOK, generation)
	}
}

func TestEvaluate_GenerationBinding(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	stats, err := store.TrustStats(ctx, 16)
	if err != nil {
		t.Fatalf("TrustStats: %v", err)
	}
	snap := trust.Snapshot{
		SchemaVersion:   trust.SnapshotSchemaVersion,
		SnapshotVersion: trust.SnapshotVersion,
		Generation:      trust.GenerationRef{FullPassGeneration: "gen-live"},
		Graph:           trust.NewGraphFacts(stats),
		External:        trust.NewExternalFacts(stats),
	}
	publish(ctx, t, store, snap, "gen-live")
	f := freshness.Report{Current: true, Index: freshness.IndexState{Exists: true, WarmStartable: true}}

	if _, state, err := trust.Evaluate(ctx, store, f, "gen-live"); err != nil || state != trust.StateCurrent {
		t.Errorf("Evaluate(matching generation) = (%s, %v), want CURRENT", state, err)
	}
	if _, state, err := trust.Evaluate(ctx, store, f, "gen-other"); err != nil || state != trust.StateStale {
		t.Errorf("Evaluate(different live generation) = (%s, %v), want STALE", state, err)
	}
}

func TestFactDigest_IgnoresGenerationBinding(t *testing.T) {
	base := buildSnapshot(t, false)
	rebound := base
	rebound.Generation = trust.GenerationRef{FullPassGeneration: "another-pass", SourceCommit: "0000", Branch: "wip", IndexProfile: "fast"}

	fdBase, err := trust.FactDigest(base)
	if err != nil {
		t.Fatalf("FactDigest: %v", err)
	}
	fdRebound, err := trust.FactDigest(rebound)
	if err != nil {
		t.Fatalf("FactDigest: %v", err)
	}
	if fdBase != fdRebound {
		t.Errorf("fact digest differs across generation bindings: %s vs %s", fdBase, fdRebound)
	}
	if bytes.Equal(mustEncode(t, base), mustEncode(t, rebound)) {
		t.Error("fixture broken: the two bindings encode identically, so the test proves nothing")
	}

	changed := base
	changed.Graph.NodesTotal++
	fdChanged, err := trust.FactDigest(changed)
	if err != nil {
		t.Fatalf("FactDigest: %v", err)
	}
	if fdChanged == fdBase {
		t.Error("fact digest is insensitive to a fact change")
	}
}
