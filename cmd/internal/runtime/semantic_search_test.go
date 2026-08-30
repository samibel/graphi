package runtime_test

// SW-261 review round 2 fix: the typed-state tests now actually drive
// SemanticSearch and assert the typed unavailable response for each
// non-ready state (missing, stale, corrupt, no-meta-dir). The previous
// revision stopped at SemanticState.State and never reproduced the
// `available: true` symptom AC-7 forbids. Each test here is paired with
// the production code path it guards:
//   - metaDir == ""  → CRITICAL 1: NewSearchService synthesises StateMissing
//   - empty store    → runtime.loadSemanticState reports StateMissing
//   - stale fp       → runtime.loadSemanticState reports StateStale
//   - corrupt row    → runtime.loadSemanticState reports StateCorrupt

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
)

// searchServiceWithState builds a Service that mirrors the production
// wiring: NewSearchService's output is a search.Service with the given
// SemanticState. The test then asserts what SemanticSearch returns —
// not the state itself, so the assertion reproduces the original
// fail-open symptom (available: true over an empty index) instead of
// just inspecting the field that holds the answer.
func searchServiceWithState(t *testing.T, st graphstore.Graphstore, emb embed.Embedder, state search.SemanticState) *search.Service {
	t.Helper()
	// Same composition NewSearchService uses, reduced to the parts the
	// state-driven unavailable envelope needs.
	reg := embed.NewRegistry()
	if err := reg.Register(emb); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.Freeze()
	svc := search.New(st).WithSemantic(reg, embed.NewIndex(), st).WithSemanticState(state)
	return svc
}

// TestSemanticSearch_NoMetaDirReturnsUnavailable pins CRITICAL 1: a
// configured embedder with metaDir == "" must surface the typed
// unavailable response. The previous revision left the runtime state
// at StateUnset, which the search service treated as "no state
// plumbed" and answered available:true over an empty index.
func TestSemanticSearch_NoMetaDirReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer func() { _ = st.Close() }()
	emb := embed.NewMockEmbedder(16)

	// NewSearchServiceWithEmbedder with metaDir == "" — the production
	// path this fix targets. A configured embedder but no meta sidecar
	// must answer unavailable, NOT available:true.
	svc := rtime.NewSearchServiceWithEmbedder(st, "", emb)
	res, err := svc.SemanticSearch(ctx, "q", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if res.Available {
		t.Fatalf("available=true with metaDir==\"\"; want false (CRITICAL 1)")
	}
	if res.Reason != search.ReasonUnavailable {
		t.Fatalf("reason=%q, want %q", res.Reason, search.ReasonUnavailable)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("hits=%d, want 0", len(res.Hits))
	}
}

// TestSemanticSearch_MissingStoreReturnsUnavailable pins the empty-store
// path: the runtime reads StateMissing from Active, the service answers
// unavailable with the missing reason. The pre-fix test stopped after
// asserting the state field; this one drives the public API.
func TestSemanticSearch_MissingStoreReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer func() { _ = st.Close() }()
	emb := embed.NewMockEmbedder(16)
	metaDir := t.TempDir()

	// Drive the production path: NewSearchService over an empty store
	// with a meta dir. The runtime reports StateMissing, the service
	// surfaces it.
	svc := rtime.NewSearchServiceWithEmbedder(st, metaDir, emb)
	res, err := svc.SemanticSearch(ctx, "q", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if res.Available {
		t.Fatalf("available=true over an empty store; want false")
	}
	if res.Reason != search.ReasonUnavailable {
		t.Fatalf("reason=%q, want %q", res.Reason, search.ReasonUnavailable)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("hits=%d, want 0", len(res.Hits))
	}
}

// TestSemanticSearch_StaleReturnsUnavailable pins the stale path: a
// generation built under a v1 fingerprint against a v2-requested
// fingerprint must surface the stale reason. The pre-fix test stopped
// after asserting the state field.
func TestSemanticSearch_StaleReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer func() { _ = st.Close() }()
	emb := embed.NewMockEmbedder(16)
	metaDir := t.TempDir()

	store, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	// Build and commit a v1 generation (stale relative to v2).
	v1fp := embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  "v1",
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	b, err := store.Begin(ctx, v1fp)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := b.Upsert(ctx, embed.Row{
		DocumentID: "doc-a", NodeID: "a", TextHash: "h-a", Path: "p/a.go",
		StartLine: 1, EndLine: 1, SpanMethod: "ast",
		Vector: make([]float32, emb.Dim()),
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	svc := rtime.NewSearchServiceWithEmbedder(st, metaDir, emb)
	res, err := svc.SemanticSearch(ctx, "q", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if res.Available {
		t.Fatalf("available=true over a stale generation; want false")
	}
	if res.Reason != search.ReasonStale {
		t.Fatalf("reason=%q, want %q", res.Reason, search.ReasonStale)
	}
}

// TestSemanticSearch_CorruptReturnsUnavailable pins the corrupt path:
// a hand-tampered row whose vector dim disagrees with the fingerprint
// must surface the corrupt reason. The pre-fix test stopped after
// asserting the state field; this one drives the public API.
func TestSemanticSearch_CorruptReturnsUnavailable(t *testing.T) {
	ctx := context.Background()
	st := graphstore.NewMemStore()
	defer func() { _ = st.Close() }()
	emb := embed.NewMockEmbedder(16)
	metaDir := t.TempDir()
	// Prime the schema.
	if _, err := embed.OpenSQLiteGenerationStore(ctx, metaDir); err != nil {
		t.Fatalf("priming: %v", err)
	}
	db, err := openSidecarForTest(metaDir)
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer func() { _ = db.Close() }()
	// Tamper: insert a row whose vector dim disagrees with the
	// fingerprint at the SQL level (Upsert-time validation would
	// reject this, so the corruption is hand-injected).
	fp := embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO generations (id, fingerprint, fingerprint_dim, document_schema, row_count, is_active, is_staging)
         VALUES (?, ?, ?, ?, 1, 1, 0)`,
		"g-tampered", fp.Canonical(), emb.Dim(), embed.DocumentSchema); err != nil {
		t.Fatalf("insert tampered generation: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO generation_rows (generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"g-tampered", "doc-a", "a", "h-a", "p.go", 1, 1, "ast",
		[]byte{0, 0, 0, 0}); err != nil { // dim 1, not 16
		t.Fatalf("insert tampered row: %v", err)
	}

	svc := rtime.NewSearchServiceWithEmbedder(st, metaDir, emb)
	res, err := svc.SemanticSearch(ctx, "q", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if res.Available {
		t.Fatalf("available=true over a corrupt generation; want false")
	}
	// The runtime substitutes the precise validation error for the
	// generic ReasonCorrupt prefix when Active returns one; either
	// shape is a corrupt answer (the typed unavailable envelope is
	// identical). The pre-fix shape was available=true, so any
	// corrupt reason satisfies the test.
	if res.Reason != search.ReasonCorrupt &&
		!strings.Contains(res.Reason, "corrupt") {
		t.Fatalf("reason=%q, want it to name corrupt", res.Reason)
	}
}
