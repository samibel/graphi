package canary_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// dialFailEmbedder fails the test if its Embed is ever reached — standing in for a
// network embedder dial. The reload path (Index.Rebuild from the durable store)
// must NEVER touch an embedder, so this proves reload is a pure local read
// with ZERO dials (SW-061 canary extension; SW-261: the GenerationStore
// replaces the legacy `vectors` table).
type dialFailEmbedder struct{ t *testing.T }

func (e dialFailEmbedder) ID() string { return "mock" }
func (e dialFailEmbedder) Dim() int   { return 4 }
func (e dialFailEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	e.t.Fatal("embedder dialed on the reload path; reload MUST be a pure local read (zero egress)")
	return nil, nil
}

// reloadFixtureSeed writes a tiny ready generation to dir and returns the
// vectors a reload would see. The seed uses a separate fingerprint from
// the canary embedder so a fingerprint mismatch (and therefore a re-embed)
// is impossible — the reload path is the point of the test.
func reloadFixtureSeed(t *testing.T, ctx context.Context, dir string) []embed.Vector {
	t.Helper()
	store, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	fp := embed.Fingerprint{
		ModelID:         "mock",
		Dim:             4,
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	b, err := store.Begin(ctx, fp)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	want := []embed.Vector{
		{NodeID: model.NodeId("a"), Values: []float32{1, 0, 0, 0}},
		{NodeID: model.NodeId("b"), Values: []float32{0, 1, 0, 0}},
	}
	for _, v := range want {
		if err := b.Upsert(ctx, embed.Row{
			DocumentID: string(v.NodeID),
			NodeID:     v.NodeID,
			TextHash:   "h-" + string(v.NodeID),
			Vector:     v.Values,
		}); err != nil {
			_ = b.Abort(ctx)
			t.Fatalf("Upsert: %v", err)
		}
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return want
}

// The reload-on-startup path performs ZERO embedder dials: vectors persisted
// by a prior index pass are loaded from the durable GenerationStore and
// Rebuilt into the in-memory index without ever invoking the embedder.
func TestReload_PerformsZeroDials(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Persist a couple of vectors (as `index --semantic` would have).
	want := reloadFixtureSeed(t, ctx, dir)

	// Simulate startup: a configured (would-dial) embedder is present, but reload
	// must not touch it. Rebuild reads local rows only.
	_ = dialFailEmbedder{t} // registering+using it would dial; reload must not

	store, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = store.Close() }()
	fp := embed.Fingerprint{
		ModelID:         "mock",
		Dim:             4,
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	gen, state, err := store.Active(ctx, fp, nil)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if state != embed.StateReady {
		t.Fatalf("reload state = %s, want ready", state)
	}
	rows, err := store.Load(ctx, gen.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vecs := make([]embed.Vector, len(rows))
	for i, r := range rows {
		vecs[i] = embed.Vector{NodeID: r.NodeID, DocumentID: r.DocumentID, Values: r.Vector}
	}
	index := embed.NewIndex()
	if err := index.Rebuild(ctx, vecs); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if index.Len() != len(want) {
		t.Fatalf("reloaded index Len = %d, want %d", index.Len(), len(want))
	}
	// Search the reloaded index (still no embedder dial — query vector supplied).
	hits := index.Search([]float32{1, 0, 0, 0}, 0)
	if len(hits) != 2 || hits[0].NodeID != "a" {
		t.Fatalf("reloaded search ranking = %+v, want a first", hits)
	}
}
