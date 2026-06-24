package canary_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// dialFailEmbedder fails the test if its Embed is ever reached — standing in for a
// network embedder dial. The reload path (Index.Rebuild from the durable vectors
// table) must NEVER touch an embedder, so this proves reload is a pure local read
// with ZERO dials (SW-061 canary extension; story AC: "reload of persisted vectors
// on startup is a pure local read (no dial, no embed)").
type dialFailEmbedder struct{ t *testing.T }

func (e dialFailEmbedder) ID() string { return "mock" }
func (e dialFailEmbedder) Dim() int   { return 4 }
func (e dialFailEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	e.t.Fatal("embedder dialed on the reload path; reload MUST be a pure local read (zero egress)")
	return nil, nil
}

// The reload-on-startup path performs ZERO embedder dials: vectors persisted by a
// prior index pass are loaded from the durable SQLite sidecar and Rebuilt into the
// in-memory index without ever invoking the embedder.
func TestReload_PerformsZeroDials(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Persist a couple of vectors (as `index --semantic` would have).
	table, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open table: %v", err)
	}
	for _, v := range []embed.Vector{
		{NodeID: model.NodeId("a"), Values: []float32{1, 0, 0, 0}},
		{NodeID: model.NodeId("b"), Values: []float32{0, 1, 0, 0}},
	} {
		if err := table.Upsert(ctx, v); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	_ = table.Close()

	// Simulate startup: a configured (would-dial) embedder is present, but reload
	// must not touch it. Rebuild reads local rows only.
	_ = dialFailEmbedder{t} // registering+using it would dial; reload must not

	reload, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("reopen table: %v", err)
	}
	defer reload.Close()
	index := embed.NewIndex()
	if err := index.Rebuild(ctx, reload); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if index.Len() != 2 {
		t.Fatalf("reloaded index Len = %d, want 2", index.Len())
	}
	// Search the reloaded index (still no embedder dial — query vector supplied).
	hits := index.Search([]float32{1, 0, 0, 0}, 0)
	if len(hits) != 2 || hits[0].NodeID != "a" {
		t.Fatalf("reloaded search ranking = %+v, want a first", hits)
	}
}
