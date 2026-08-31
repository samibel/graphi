package runtime_test

// Tests for the typed GenerationStore state plumbing the runtime does on
// every search-service construction (SW-261 AC-10). The point of these
// tests is the runtime non-ready matrix: every non-ready state
// (missing, stale, corrupt) must surface the typed unavailable response
// with the right Reason. A previous revision conflated zero with
// StateMissing, so an explicit `missing` was answered with available:true
// — exactly the fail-open AC-7 forbids.
//
// The tests are runnable against the existing NewSearchService through a
// thin helper that injects the test embedder. The production code path is
// the same: loadSemanticState is the one place the runtime reads the
// GenerationStore's Active and packages the result.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
)

// TestLoadSemanticState_MissingStoreReturnsMissing: an empty store must
// surface StateMissing with the missing reason. A previous revision
// confused zero with StateMissing, so a runtime that explicitly called
// Active and got back StateMissing was answered with available:true —
// fail-open against AC-7. The new StateUnset sentinel distinguishes
// "no state plumbed" from "Active reported missing".
func TestLoadSemanticState_MissingStoreReturnsMissing(t *testing.T) {
	ctx := context.Background()
	gstore := graphstore.NewMemStore()
	metaDir := t.TempDir()
	emb := embed.NewMockEmbedder(16)
	state := rtime.LoadSemanticStateForTest(ctx, gstore, metaDir, emb)
	if state.State != embed.StateMissing {
		t.Fatalf("state = %s, want missing (empty store, no generations)", state.State)
	}
	if state.Reason != search.ReasonUnavailable {
		t.Fatalf("reason = %q, want %q", state.Reason, search.ReasonUnavailable)
	}
}

// TestLoadSemanticState_StaleActiveReturnsStale simulates a stale
// generation by writing a v1 fingerprint into the GenerationStore and
// asking Active for a v2 fingerprint. Active must report StateStale
// (the fingerprints differ) and the runtime must surface the stale
// reason to the search service.
func TestLoadSemanticState_StaleActiveReturnsStale(t *testing.T) {
	ctx := context.Background()
	gstore := graphstore.NewMemStore()
	metaDir := t.TempDir()
	emb := embed.NewMockEmbedder(16)
	store, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	// Build and commit a v1 generation under the placeholder graph gen.
	v1fp := embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  "v1",
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	b1, err := store.Begin(ctx, v1fp)
	if err != nil {
		t.Fatalf("Begin v1: %v", err)
	}
	if err := b1.Upsert(ctx, embed.Row{
		DocumentID: "doc-a", NodeID: "a", TextHash: "h-a", Path: "p.go",
		StartLine: 1, EndLine: 1, SpanMethod: "ast",
		Vector: make([]float32, 16),
	}); err != nil {
		t.Fatalf("Upsert v1: %v", err)
	}
	if err := b1.Commit(ctx); err != nil {
		t.Fatalf("Commit v1: %v", err)
	}

	state := rtime.LoadSemanticStateForTest(ctx, gstore, metaDir, emb)
	if state.State != embed.StateStale {
		t.Fatalf("state = %s, want stale (v1 fingerprint active, v2 requested)", state.State)
	}
	if state.Reason != search.ReasonStale {
		t.Fatalf("reason = %q, want %q", state.Reason, search.ReasonStale)
	}
}

// TestLoadSemanticState_CorruptActiveReturnsCorrupt simulates a corrupt
// generation by inserting a row whose vector dim disagrees with the
// fingerprint at the SQL level (the schema enforces dim at Upsert time;
// the only way to land a corrupt state in the durable sidecar is a
// hand-tamper). The runtime must surface StateCorrupt with the corrupt
// reason. The SQLite-level single-row dim sample previously missed dim
// drift in any non-sampled row; the fix validates every row.
func TestLoadSemanticState_CorruptActiveReturnsCorrupt(t *testing.T) {
	ctx := context.Background()
	gstore := graphstore.NewMemStore()
	metaDir := t.TempDir()
	emb := embed.NewMockEmbedder(16)
	// Open the sidecar directly (we need to bypass the schema's dim
	// enforcement at Upsert time to land a corrupt state).
	db, err := openSidecarForTest(metaDir)
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer func() { _ = db.Close() }()
	// The schema must exist before we tamper. OpenSQLiteGenerationStore
	// would create it but also call MigrateFromLegacyVectors, which is
	// fine; we use the open-from-dir path then close it so the schema
	// exists.
	if _, err := embed.OpenSQLiteGenerationStore(ctx, metaDir); err != nil {
		t.Fatalf("priming schema: %v", err)
	}
	// Re-open the sidecar.
	db, err = openSidecarForTest(metaDir)
	if err != nil {
		t.Fatalf("reopen sidecar: %v", err)
	}
	defer func() { _ = db.Close() }()
	// Compute the runtime's expected fingerprint so the tampered row's
	// stored fingerprint matches it (otherwise the comparison would
	// report StateStale before reaching the dim check).
	fp := embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO generations (id, fingerprint, fingerprint_dim, document_schema, row_count, is_active, is_staging)
         VALUES (?, ?, ?, ?, 1, 1, 0)`,
		"g-tampered", fp.Canonical(), 16, embed.DocumentSchema); err != nil {
		t.Fatalf("insert tampered generation: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO generation_rows (generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"g-tampered", "doc-a", "a", "h-a", "p.go", 1, 1, "ast",
		[]byte{0, 0, 0, 0}); err != nil { // dim 1, not 16
		t.Fatalf("insert tampered row: %v", err)
	}

	state := rtime.LoadSemanticStateForTest(ctx, gstore, metaDir, emb)
	if state.State != embed.StateCorrupt {
		t.Fatalf("state = %s, want corrupt (one row's vector dim disagrees with the fingerprint)", state.State)
	}
	if !strings.Contains(state.Reason, search.ReasonCorrupt) && !strings.Contains(state.Reason, "corrupt") {
		t.Fatalf("reason = %q, want it to mention corrupt", state.Reason)
	}
}

// openSidecarForTest opens the ingest-meta sidecar at the supplied
// metaDir in test mode (WAL + busy_timeout), returning the raw handle.
func openSidecarForTest(metaDir string) (*sql.DB, error) {
	return sql.Open("sqlite", filepath.Join(metaDir, "ingest-meta.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
}
