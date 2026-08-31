package runtime_test

// The build → reload → StateReady round trip (SW-261, review round 3).
//
// WHY THIS FILE EXISTS. Three review rounds verified fail-CLOSED behaviour and
// nothing ever asserted that the happy path survives a round trip: the runtime
// state tests cover missing / stale / corrupt, `semantic_search_test.go`
// asserts only non-ready responses, and `internal/canary/reload_test.go`
// hand-writes a fingerprint instead of building one. That gap is the direct
// reason two build/reload asymmetries shipped green — first the graph
// generation (round 1), then the embedding dimension (round 3). A state
// machine whose only tested transitions are the failing ones is not tested.
//
// WHAT IT PINS. One production-shaped pass: build a generation through
// BuildSemanticGeneration (the same helper `graphi index --semantic` calls),
// close everything, then ask the runtime's own state loader — with a FRESH
// embedder instance, exactly as NewSearchService constructs one — and require
// StateReady. Any field the build path fills in and the reload path does not
// (or fills differently) breaks canonical equality and shows up here as
// StateStale.
//
// THE ZERO-DIAL RULE. Reload must never dial the embedder: `internal/canary`
// pins that a configured reload performs no request. So the fake below does
// not merely report a dimension — it FAILS the test if Embed or ProbeDim is
// called after the build phase. A fix that made reload symmetric by probing
// would turn this test red, which is the point.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ingest"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
)

// lateDimEmbedder models the shape of the only real embedder graphi ships
// with: Ollama reports Dim() == 0 until its first request, and learns the
// true dimension by making one. Everything about the round trip that matters
// follows from that — the build path knows the dimension, a freshly
// constructed reload embedder does not.
type lateDimEmbedder struct {
	dim      int  // the dimension discovered by ProbeDim
	probed   bool // whether ProbeDim has run on THIS instance
	sealed   *atomic.Bool
	t        *testing.T
	probeErr error
}

func newLateDimEmbedder(t *testing.T, sealed *atomic.Bool, dim int) *lateDimEmbedder {
	t.Helper()
	return &lateDimEmbedder{dim: dim, sealed: sealed, t: t}
}

func (e *lateDimEmbedder) ID() string { return "late-dim:test" }

// Dim reports 0 until ProbeDim has run on this instance — the Ollama shape.
func (e *lateDimEmbedder) Dim() int {
	if !e.probed {
		return 0
	}
	return e.dim
}

func (e *lateDimEmbedder) ProbeDim(_ context.Context) error {
	if e.sealed.Load() {
		e.t.Errorf("ProbeDim called after the build phase: reload must not dial the embedder (internal/canary pins zero dials on reload)")
	}
	if e.probeErr != nil {
		return e.probeErr
	}
	e.probed = true
	return nil
}

func (e *lateDimEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.sealed.Load() {
		e.t.Errorf("Embed called after the build phase: reload must not dial the embedder")
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, e.dim)
		for j := range v {
			v[j] = float32(i+1) / float32(j+2)
		}
		out[i] = v
	}
	return out, nil
}

// staticDocs yields one deterministic document per node so the build has
// something to embed without needing source bytes on disk.
type staticDocs struct{}

func (staticDocs) Document(n model.Node) (embed.SemanticDocument, bool) {
	text := n.Kind() + " " + n.QualifiedName()
	return embed.SemanticDocument{
		DocumentID:     "doc-" + string(n.ID()),
		NodeID:         n.ID(),
		Kind:           n.Kind(),
		QualifiedName:  n.QualifiedName(),
		Path:           n.SourcePath(),
		SpanMethod:     "ast",
		TextHash:       "hash-" + string(n.ID()),
		DocumentSchema: embed.DocumentSchema,
		Text:           text,
	}, true
}

// TestSemanticRoundTrip_BuildThenReloadIsReady is the missing witness: a
// generation built through the production helper must reload as READY through
// the production state loader, using a fresh embedder and without dialing.
func TestSemanticRoundTrip_BuildThenReloadIsReady(t *testing.T) {
	ctx := context.Background()
	metaDir := t.TempDir()
	gstore := graphstore.NewMemStore()
	t.Cleanup(func() { _ = gstore.Close() })

	// Two nodes are enough: the round trip is about fingerprint equality,
	// not about ranking.
	putNode(t, ctx, gstore, "pkg.Alpha", "a.go")
	putNode(t, ctx, gstore, "pkg.Beta", "b.go")

	// Seed a REAL graph identity. Without this the store has none, both sides
	// fall back to the documented placeholder, and they agree for a reason
	// that has nothing to do with the round trip — so a build/reload
	// disagreement on a non-placeholder identity would slip through. The
	// nodes are inserted directly here, so nothing else writes this key.
	if err := gstore.SetMetadata(ctx, "index.commit_generation", "generation-under-test"); err != nil {
		t.Fatalf("seed graph identity: %v", err)
	}

	ing, err := ingest.New(gstore, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}

	genStore, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}

	var sealed atomic.Bool
	buildEmb := newLateDimEmbedder(t, &sealed, 5)
	reg := embed.NewRegistry()
	if err := reg.Register(buildEmb); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reg.Freeze()

	res, err := rtime.BuildSemanticGeneration(ctx, ing, gstore, reg, genStore, nil, staticDocs{}, embed.NewIndex(), nil)
	if err != nil {
		t.Fatalf("BuildSemanticGeneration: %v", err)
	}
	if res.Embedded == 0 {
		t.Fatalf("build embedded nothing (%+v): the round trip needs a non-empty generation", res)
	}
	if got := buildEmb.Dim(); got != 5 {
		t.Fatalf("build embedder Dim() = %d after the pass, want 5 (ProbeDim should have run)", got)
	}
	if err := genStore.Close(); err != nil {
		t.Fatalf("close generation store: %v", err)
	}

	// From here on the embedder must not be touched. Everything below is the
	// reload path, which production reaches with a process that has only just
	// started and has never embedded anything.
	sealed.Store(true)

	// A FRESH embedder, as NewSearchService constructs from GRAPHI_EMBEDDER:
	// unprobed, so Dim() == 0 until something asks it to discover.
	reloadEmb := newLateDimEmbedder(t, &sealed, 5)
	if got := reloadEmb.Dim(); got != 0 {
		t.Fatalf("fresh reload embedder Dim() = %d, want 0 (the fixture must reproduce Ollama's shape)", got)
	}

	state := rtime.LoadSemanticStateForTest(ctx, gstore, metaDir, reloadEmb)
	if state.State != embed.StateReady {
		t.Fatalf("state after build → reload = %s (reason %q), want ready.\n"+
			"A generation this runtime just built must reload as usable; anything else means the "+
			"build and reload fingerprints disagree.\nrequested = %s",
			state.State, state.Reason, state.Requested.Canonical())
	}
}

// TestSemanticRoundTrip_GraphChangeMakesItStale is the other half of the same
// witness: ready must not be sticky. Once the graph moves, the generation the
// runtime built for the previous graph has to read stale — that is the whole
// point of carrying graph identity in the fingerprint, and a test that only
// asserted `ready` could be satisfied by an identity that never advances.
func TestSemanticRoundTrip_GraphChangeMakesItStale(t *testing.T) {
	ctx := context.Background()
	metaDir := t.TempDir()
	gstore := graphstore.NewMemStore()
	t.Cleanup(func() { _ = gstore.Close() })

	putNode(t, ctx, gstore, "pkg.Alpha", "a.go")
	ing, err := ingest.New(gstore, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	genStore, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}

	var sealed atomic.Bool
	reg := embed.NewRegistry()
	if err := reg.Register(newLateDimEmbedder(t, &sealed, 5)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	reg.Freeze()
	if _, err := rtime.BuildSemanticGeneration(ctx, ing, gstore, reg, genStore, nil, staticDocs{}, embed.NewIndex(), nil); err != nil {
		t.Fatalf("BuildSemanticGeneration: %v", err)
	}
	if err := genStore.Close(); err != nil {
		t.Fatalf("close generation store: %v", err)
	}

	// The graph moves. In production every committed mutation advances
	// index.commit_generation (engine/ingest writes it); the key is spelled
	// out here on purpose, so this test also pins the wire name the runtime
	// reads in graphGenerationFromStore.
	if err := gstore.SetMetadata(ctx, "index.commit_generation", "generation-after-the-mutation"); err != nil {
		t.Fatalf("advance graph generation: %v", err)
	}

	sealed.Store(true)
	state := rtime.LoadSemanticStateForTest(ctx, gstore, metaDir, newLateDimEmbedder(t, &sealed, 5))
	if state.State != embed.StateStale {
		t.Fatalf("state after a graph mutation = %s, want stale: vectors built for the previous graph must not read ready", state.State)
	}
}

// putNode stores one function node, failing the test rather than the pass if
// the node cannot be constructed.
func putNode(t *testing.T, ctx context.Context, gs graphstore.Graphstore, qualifiedName, path string) {
	t.Helper()
	n, err := model.NewNode("function", qualifiedName, path, 1, 1)
	if err != nil {
		t.Fatalf("NewNode(%s): %v", qualifiedName, err)
	}
	if err := gs.PutNode(ctx, n); err != nil {
		t.Fatalf("PutNode(%s): %v", qualifiedName, err)
	}
}
