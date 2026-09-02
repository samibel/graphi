package runtime

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
)

// TestComposeRetrieval_PreservesProductionIdentityAndExplain exercises the
// actual composition adapter, not an engine-only substitute. It catches the
// two wiring defects from the first SW-263 review: losing the persisted
// document id and omitting model/index fingerprints before retrieval reaches
// resolve.Deps for SW-264.
func TestComposeRetrieval_PreservesProductionIdentityAndExplain(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	node, err := model.NewNode("function", "pkg.Target", "pkg/target.go", 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}

	emb := embed.NewMockEmbedder(8)
	reg := embed.NewRegistry()
	if err := reg.Register(emb); err != nil {
		t.Fatal(err)
	}
	vectors, err := emb.Embed(ctx, []string{"target behaviour"})
	if err != nil {
		t.Fatal(err)
	}
	index := embed.NewIndex()
	if err := index.Rebuild(ctx, []embed.Vector{{NodeID: node.ID(), DocumentID: "doc-target-v2", Values: vectors[0]}}); err != nil {
		t.Fatal(err)
	}
	fingerprint := embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	svc := search.New(store).
		WithSemantic(reg, index, store).
		WithSemanticState(search.SemanticState{State: embed.StateReady, Requested: fingerprint})

	composition := &Composition{store: store, graphQuery: query.New(store)}
	retriever := composition.composeRetrieval(svc)
	if retriever == nil {
		t.Fatal("composeRetrieval returned nil for a graph-backed composition")
	}
	got, err := retriever.Retrieve(ctx, resolve.RetrieverRequest{Query: "target behaviour", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if got.Degradation != "ready" {
		t.Fatalf("Degradation = %q, want ready", got.Degradation)
	}
	if len(got.Rows) == 0 {
		t.Fatal("production-composed retrieval returned no rows")
	}
	if got.Rows[0].DocumentID != "doc-target-v2" {
		t.Errorf("DocumentID = %q, want persisted semantic identity", got.Rows[0].DocumentID)
	}
	if got.Rows[0].Region != "semantic_prefix" || got.Summary.Strategy != "semantic_first" {
		t.Errorf("semantic-first provenance was not preserved through the production adapter: row=%+v summary=%+v", got.Rows[0], got.Summary)
	}
	if got.Rows[0].Explain.SemanticRank == 0 || got.Rows[0].Explain.Final != got.Rows[0].Final {
		t.Errorf("explain was not preserved through the composition adapter: %+v", got.Rows[0])
	}
	if got.Summary.ModelFingerprint != emb.ID() {
		t.Errorf("ModelFingerprint = %q, want %q", got.Summary.ModelFingerprint, emb.ID())
	}
	if got.Summary.IndexFingerprint != fingerprint.Canonical() {
		t.Errorf("IndexFingerprint = %q, want %q", got.Summary.IndexFingerprint, fingerprint.Canonical())
	}
	if got.Summary.WeightsHash == "" || got.Summary.CandidateK != 50 || got.Summary.RRFk != 60 {
		t.Errorf("summary was not preserved through the composition adapter: %+v", got.Summary)
	}
}

func TestProductionRetrievalMode_ExperimentalFusionIsUnreachable(t *testing.T) {
	for _, mode := range []retrieval.Mode{retrieval.ModeFusionNoGraph, retrieval.ModeFusionGraph} {
		if got := productionRetrievalMode(int(mode)); got != retrieval.ModeAuto {
			t.Errorf("productionRetrievalMode(%d) = %d, want ModeAuto", mode, got)
		}
	}
}
