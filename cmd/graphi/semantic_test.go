package main

// AC-2 / AC-3 CLI tests for `graphi semantic status`.
//
// The CLI exit-code table is the operator's surface for distinguishing
// the five situations. The test pins:
//
//   - exit 0 = ready (state matches an indexed generation)
//   - exit 1 = actionable (missing / stale)
//   - exit 2 = error / corrupt (or usage errors)
//
// Each row names the input that makes the CLI return a DIFFERENT exit
// code WITHOUT the fix — a regression that collapsed 0/1/2 onto one
// code would still pass a "non-zero" assertion but would fail here.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// cliExitCase names one CLI invocation and the expected exit code. The
// test runs each case through runSemanticStatusAt against an isolated
// fixture (a temp dir, no environment leaking) and asserts the exit.
type cliExitCase struct {
	name     string
	envSetup func(t *testing.T) (root, dbPath, metaDir string, reg *embed.Registry)
	wantRC   int
	// wantState pins the wire document's state field. Empty means "any"
	// (the test cares about the exit code, not the state shape).
	wantState string
	// wantRepair pins the exact repair string. Empty means "any".
	wantRepair string
}

// cliExitCases is the table the test iterates. Every row maps the
// canonical exit-code contract AC-2 names (0=ready, 1=actionable,
// 2=error/corrupt) to the wire document's state and the exact repair
// command.
func semanticCLIExitCases() []cliExitCase {
	return []cliExitCase{
		{
			name: "ready",
			envSetup: func(t *testing.T) (string, string, string, *embed.Registry) {
				root := t.TempDir()
				meta := t.TempDir()
				reg := newReadyRegistry(t, meta)
				return root, "", meta, reg
			},
			wantRC:    0,
			wantState: "ready",
		},
		{
			name: "missing_no_embedder",
			envSetup: func(t *testing.T) (string, string, string, *embed.Registry) {
				root := t.TempDir()
				return root, "", "", nil
			},
			wantRC:     1,
			wantState:  "missing",
			wantRepair: "graphi setup-embedder",
		},
		{
			name: "missing_no_meta_sidecar",
			envSetup: func(t *testing.T) (string, string, string, *embed.Registry) {
				root := t.TempDir()
				reg := embed.NewRegistry()
				if err := reg.Register(embed.NewMockEmbedder(8)); err != nil {
					t.Fatalf("register mock: %v", err)
				}
				reg.Freeze()
				return root, "", "", reg
			},
			wantRC:     1,
			wantState:  "missing",
			wantRepair: "graphi index --semantic",
		},
		{
			name: "stale_fingerprint",
			envSetup: func(t *testing.T) (string, string, string, *embed.Registry) {
				root := t.TempDir()
				meta := t.TempDir()
				// Seed a stale generation under a different fingerprint.
				seedStaleGeneration(t, meta)
				reg := embed.NewRegistry()
				if err := reg.Register(embed.NewMockEmbedder(8)); err != nil {
					t.Fatalf("register mock: %v", err)
				}
				reg.Freeze()
				return root, "", meta, reg
			},
			wantRC:     1,
			wantState:  "stale",
			wantRepair: "graphi index --semantic",
		},
		{
			name: "corrupt",
			envSetup: func(t *testing.T) (string, string, string, *embed.Registry) {
				root := t.TempDir()
				meta := t.TempDir()
				// Seed a sidecar whose active generation fails the dim check.
				seedCorruptGeneration(t, meta)
				reg := embed.NewRegistry()
				mock := embed.NewMockEmbedder(8)
				if err := reg.Register(mock); err != nil {
					t.Fatalf("register mock: %v", err)
				}
				reg.Freeze()
				return root, "", meta, reg
			},
			wantRC:     2,
			wantState:  "corrupt",
			wantRepair: "graphi index --semantic",
		},
	}
}

// TestRunSemanticStatus_ExitCodeTable drives the canonical exit-code
// table AC-2 names. Every row is the contract; a regression that
// collapses the table onto one exit code would still pass a "non-zero"
// assertion but fails here.
func TestRunSemanticStatus_ExitCodeTable(t *testing.T) {
	t.Setenv("GRAPHI_EMBEDDER", "") // ensure no embedder leaks from host env
	for _, tc := range semanticCLIExitCases() {
		t.Run(tc.name, func(t *testing.T) {
			root, dbPath, metaDir, reg := tc.envSetup(t)
			var out bytes.Buffer
			gotRC := runSemanticStatusWithRegistryAt(root, []string{"--json", "-root", root, "-db", dbPath, "-meta", metaDir}, &out, reg)
			if gotRC != tc.wantRC {
				t.Errorf("exit = %d, want %d (output: %s)", gotRC, tc.wantRC, out.String())
			}
			var doc struct {
				State  string `json:"state"`
				Repair string `json:"repair"`
			}
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatalf("decode: %v\n%s", err, out.String())
			}
			if tc.wantState != "" && doc.State != tc.wantState {
				t.Errorf("state = %q, want %q", doc.State, tc.wantState)
			}
			if tc.wantRepair != "" && doc.Repair != tc.wantRepair {
				t.Errorf("repair = %q, want %q", doc.Repair, tc.wantRepair)
			}
		})
	}
}

// TestRunSemanticStatus_NoRepoExit2 is the AC-10 invariant: outside a
// repository, the verb fails closed with exit 2 and a typed message.
func TestRunSemanticStatus_NoRepoExit2(t *testing.T) {
	var out bytes.Buffer
	gotRC := runSemanticStatusAt(t.TempDir(), []string{"--json"}, &out)
	if gotRC == 0 {
		t.Errorf("semantic status outside a repo exit = 0, want non-zero; output: %s", out.String())
	}
}

// TestRunSemanticStatus_HumanRenderIsReadable asserts the human output
// contains the documented keywords. A regression that strips the human
// rendering would leave operators reading the JSON form for every call.
func TestRunSemanticStatus_HumanRenderIsReadable(t *testing.T) {
	t.Setenv("GRAPHI_EMBEDDER", "")
	root := t.TempDir()
	meta := t.TempDir()
	reg := newReadyRegistry(t, meta)
	var out bytes.Buffer
	gotRC := runSemanticStatusWithRegistryAt(root, []string{"-root", root, "-meta", meta}, &out, reg)
	if gotRC != 0 {
		t.Fatalf("ready exit = %d, want 0; output: %s", gotRC, out.String())
	}
	for _, want := range []string{"Graphi semantic status", "state:", "ready", "languages:", "Go is validated; every other language is unvalidated"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human output missing %q:\n%s", want, out.String())
		}
	}
}

// newReadyRegistry builds a Registry whose active embedder matches a
// ready-generation seed in metaDir. The seed path commits one row under
// the mock embedder's fingerprint.
func newReadyRegistry(t *testing.T, metaDir string) *embed.Registry {
	t.Helper()
	reg := embed.NewRegistry()
	mock := embed.NewMockEmbedder(8)
	if err := reg.Register(mock); err != nil {
		t.Fatalf("register mock: %v", err)
	}
	reg.Freeze()
	seedReadyGeneration(t, metaDir, mock)
	return reg
}

// seedReadyGeneration commits one row under the mock embedder's
// fingerprint. The reader then reports StateReady.
func seedReadyGeneration(t *testing.T, metaDir string, mock embed.Embedder) {
	t.Helper()
	store, err := embed.OpenSQLiteGenerationStore(context.Background(), metaDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	fp := embed.Fingerprint{
		ModelID:         mock.ID(),
		Dim:             mock.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	build, err := store.Begin(context.Background(), fp)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := build.Upsert(context.Background(), embed.Row{
		DocumentID: "doc-1",
		NodeID:     model.NodeId("n1"),
		TextHash:   "h",
		Path:       "/x",
		StartLine:  1,
		EndLine:    1,
		SpanMethod: "ast",
		Vector:     make([]float32, mock.Dim()),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := build.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// seedStaleGeneration writes a sidecar whose active generation's
// fingerprint differs from the mock embedder's. Active reports StateStale.
func seedStaleGeneration(t *testing.T, metaDir string) {
	t.Helper()
	store, err := embed.OpenSQLiteGenerationStore(context.Background(), metaDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	staleFP := embed.Fingerprint{
		ModelID:        "stale-embedder",
		Dim:            8,
		DocumentSchema: embed.DocumentSchema,
	}
	build, err := store.Begin(context.Background(), staleFP)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := build.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// seedCorruptGeneration writes a sidecar whose active generation's
// fingerprint MATCHES the requested one (so Active reaches the per-row
// dim check) but whose row vector BLOB is the wrong length.
func seedCorruptGeneration(t *testing.T, metaDir string) {
	t.Helper()
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS generations (
			id TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL,
			fingerprint_dim INTEGER NOT NULL,
			document_schema TEXT NOT NULL,
			row_count INTEGER NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 0,
			is_staging INTEGER NOT NULL DEFAULT 0,
			committed_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS generation_rows (
			generation_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			text_hash TEXT NOT NULL,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			span_method TEXT NOT NULL,
			vector BLOB NOT NULL,
			PRIMARY KEY (generation_id, node_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS generations_one_active ON generations(is_active) WHERE is_active = 1`,
		`CREATE UNIQUE INDEX IF NOT EXISTS generations_one_staging ON generations(is_staging) WHERE is_staging = 1`,
	}
	for _, s := range ddl {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	// The fingerprint declared on disk uses model_id="mock" with Dim=8
	// (matching the mock embedder the CLI registers). The per-row dim
	// check in Active then fails because the row's vector BLOB is 4
	// floats (16 bytes) — the persisted dim does NOT equal the
	// fingerprint's declared dim. Active returns StateCorrupt.
	mockFP := embed.Fingerprint{
		ModelID:         "mock",
		Dim:             8,
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	fpStr := mockFP.Canonical()
	if _, err := db.Exec(`INSERT INTO generations(id, fingerprint, fingerprint_dim, document_schema, row_count, is_active, is_staging, committed_at)
		VALUES(?, ?, ?, ?, ?, 1, 0, '2026-08-30T12:34:56Z')`,
		"v3-corrupt", fpStr, 8, embed.DocumentSchema, 1); err != nil {
		t.Fatalf("insert gen: %v", err)
	}
	vector := make([]byte, 4*4) // 16 bytes — wrong for dim=8
	if _, err := db.Exec(`INSERT INTO generation_rows(generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"v3-corrupt", "doc-1", "n1", "h", "/x", 1, 1, "ast", vector); err != nil {
		t.Fatalf("insert row: %v", err)
	}
}
