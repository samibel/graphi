package embed_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// A SQLite-backed VectorTable round-trips vectors durably: vectors written by one
// table handle are read back identically by a fresh handle opened from the SAME
// meta dir — the reload-after-restart contract, a pure local read with no
// re-embedding.
func TestSQLiteVectorTable_DurableRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	write, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open write table: %v", err)
	}
	want := []embed.Vector{
		{NodeID: model.NodeId("a"), Values: []float32{0, 1, 0, 0}},
		{NodeID: model.NodeId("b"), Values: []float32{1, 0, 0, 0}},
		{NodeID: model.NodeId("c"), Values: []float32{-1, 0, 0, 0}},
	}
	for _, v := range want {
		if err := write.Upsert(ctx, v); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	_ = write.Close()

	// Fresh process simulation: a brand-new handle on the same dir.
	read, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open read table: %v", err)
	}
	defer read.Close()
	got, err := read.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d vectors, want %d", len(got), len(want))
	}
	// Load returns canonical NodeId order (a, b, c).
	for i, w := range want {
		if got[i].NodeID != w.NodeID {
			t.Fatalf("row %d NodeID = %q, want %q", i, got[i].NodeID, w.NodeID)
		}
		if len(got[i].Values) != len(w.Values) {
			t.Fatalf("row %d dim = %d, want %d", i, len(got[i].Values), len(w.Values))
		}
		for j := range w.Values {
			if got[i].Values[j] != w.Values[j] {
				t.Fatalf("row %d comp %d = %v, want %v", i, j, got[i].Values[j], w.Values[j])
			}
		}
	}
}

// A changed/absent embedder identity invalidates stale vectors: Load scoped to a
// DIFFERENT embedder_id reads zero rows rather than mixing embedding spaces.
func TestSQLiteVectorTable_EmbedderInvalidatesStale(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	write, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := write.Upsert(ctx, embed.Vector{NodeID: model.NodeId("a"), Values: []float32{1, 0, 0, 0}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	_ = write.Close()

	// A different embedder identity reads zero rows (stale vectors invalidated).
	other, err := embed.OpenSQLiteVectorTable(ctx, dir, "ollama:nomic-embed-text", 4)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	defer other.Close()
	got, err := other.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("changed embedder loaded %d stale vectors, want 0", len(got))
	}
}

// Determinism: indexing the same nodes twice with the deterministic mock embedder
// produces IDENTICAL durable vectors AND identical ranked hits — the double-index
// equality contract (story AC: "determinism").
func TestGenerateAndPersist_DoubleIndexEquality(t *testing.T) {
	ctx := context.Background()
	nodes := mustNodes(t)
	mock := embed.NewMockEmbedder(16)

	index := func() (*embed.Index, *embed.MemVectorTable) {
		reg := embed.NewRegistry()
		reg.Register(mock)
		ix := embed.NewIndex()
		table := embed.NewMemVectorTable()
		res, err := embed.GenerateAndPersist(ctx, reg, nodes, ix, table)
		if err != nil {
			t.Fatalf("GenerateAndPersist: %v", err)
		}
		if !res.Configured || res.Embedded != len(nodes) {
			t.Fatalf("unexpected result: %+v", res)
		}
		return ix, table
	}

	ix1, t1 := index()
	ix2, t2 := index()

	// Durable vectors are byte-identical across the two passes.
	v1, _ := t1.Load(ctx)
	v2, _ := t2.Load(ctx)
	if len(v1) != len(v2) || len(v1) != len(nodes) {
		t.Fatalf("vector counts differ: %d vs %d (want %d)", len(v1), len(v2), len(nodes))
	}
	for i := range v1 {
		if v1[i].NodeID != v2[i].NodeID {
			t.Fatalf("vector %d NodeID differs: %q vs %q", i, v1[i].NodeID, v2[i].NodeID)
		}
		for j := range v1[i].Values {
			if v1[i].Values[j] != v2[i].Values[j] {
				t.Fatalf("vector %d comp %d differs", i, j)
			}
		}
	}

	// Ranked hits are identical for the same query vector.
	q, _ := mock.Embed(ctx, []string{embed.NodeText(nodes[0])})
	h1 := ix1.Search(q[0], 0)
	h2 := ix2.Search(q[0], 0)
	if len(h1) != len(h2) {
		t.Fatalf("hit counts differ: %d vs %d", len(h1), len(h2))
	}
	for i := range h1 {
		if h1[i].NodeID != h2[i].NodeID || h1[i].Score != h2[i].Score {
			t.Fatalf("hit %d differs: %+v vs %+v", i, h1[i], h2[i])
		}
	}
	// The exact-match node ranks first (cosine 1.0).
	if h1[0].NodeID != nodes[0].ID() {
		t.Fatalf("top hit = %q, want %q", h1[0].NodeID, nodes[0].ID())
	}
}

// Reload reproduces the SAME ranking as the in-memory index it was persisted from,
// WITHOUT re-embedding: Rebuild from the durable table reads local rows only.
func TestGenerateAndPersist_ReloadMatchesInMemory(t *testing.T) {
	ctx := context.Background()
	nodes := mustNodes(t)
	dir := t.TempDir()
	mock := embed.NewMockEmbedder(16)
	reg := embed.NewRegistry()
	reg.Register(mock)

	// Generate into an in-memory index + durable SQLite table.
	liveIndex := embed.NewIndex()
	table, err := embed.OpenSQLiteVectorTable(ctx, dir, mock.ID(), mock.Dim())
	if err != nil {
		t.Fatalf("open table: %v", err)
	}
	if _, err := embed.GenerateAndPersist(ctx, reg, nodes, liveIndex, table); err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}
	_ = table.Close()

	// Simulate a restart: a fresh table handle + a fresh index Rebuilt from durable
	// storage. A failEmbedder would fail if Rebuild re-embedded; Rebuild never
	// touches an embedder, so reload is a pure local read by construction.
	reloadTable, err := embed.OpenSQLiteVectorTable(ctx, dir, mock.ID(), mock.Dim())
	if err != nil {
		t.Fatalf("reopen table: %v", err)
	}
	defer reloadTable.Close()
	reloaded := embed.NewIndex()
	if err := reloaded.Rebuild(ctx, reloadTable); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if reloaded.Len() != liveIndex.Len() {
		t.Fatalf("reloaded Len = %d, want %d", reloaded.Len(), liveIndex.Len())
	}

	q, _ := mock.Embed(ctx, []string{embed.NodeText(nodes[1])})
	live := liveIndex.Search(q[0], 0)
	rl := reloaded.Search(q[0], 0)
	if len(live) != len(rl) {
		t.Fatalf("hit counts differ after reload: %d vs %d", len(live), len(rl))
	}
	for i := range live {
		if live[i].NodeID != rl[i].NodeID || live[i].Score != rl[i].Score {
			t.Fatalf("reload ranking diverged at %d: %+v vs %+v", i, live[i], rl[i])
		}
	}
}

// Graceful skip: with an unconfigured registry, GenerateAndPersist embeds nothing,
// persists nothing, and reports Configured=false — no error, no dial.
func TestGenerateAndPersist_GracefulSkip(t *testing.T) {
	ctx := context.Background()
	nodes := mustNodes(t)
	table := embed.NewMemVectorTable()
	res, err := embed.GenerateAndPersist(ctx, embed.NewRegistry(), nodes, embed.NewIndex(), table)
	if err != nil {
		t.Fatalf("GenerateAndPersist error on graceful-skip path: %v", err)
	}
	if res.Configured {
		t.Fatal("Configured = true with no embedder, want false")
	}
	if res.Embedded != 0 {
		t.Fatalf("Embedded = %d on graceful-skip path, want 0", res.Embedded)
	}
	if got, _ := table.Load(ctx); len(got) != 0 {
		t.Fatalf("graceful skip persisted %d vectors, want 0", len(got))
	}
}

func mustNodes(t *testing.T) []model.Node {
	t.Helper()
	specs := []struct {
		kind, qn, path string
	}{
		{"function", "pkg/foo.ParseGraph", "pkg/foo.go"},
		{"function", "pkg/foo.ParseGraphLite", "pkg/foo.go"},
		{"type", "pkg/bar.Graph", "pkg/bar.go"},
	}
	out := make([]model.Node, 0, len(specs))
	for i, s := range specs {
		n, err := model.NewNode(s.kind, s.qn, s.path, i+1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		out = append(out, n)
	}
	return out
}
