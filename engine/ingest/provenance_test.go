package ingest_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"

	_ "modernc.org/sqlite"
)

// edgeParser emits one node per file (identity derived from path) and, for each
// "edge:other.go" directive, one CALLS edge from this file's node to the target
// file's node. It also records the target as a forward reference so the reverse
// dependency cascade re-derives this file when the target changes. This lets the
// SW-037 provenance tests assert that BOTH node and edge ids are recorded in the
// edit_provenance side-channel, including across the cascade.
type edgeParser struct{}

func nodeForPath(path string) (model.Node, error) {
	return model.NewNode("function", "pkg/fn"+filepath.Base(path), path, 1, 1)
}

func (edgeParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	n, err := nodeForPath(path)
	if err != nil {
		return nil, err
	}
	var edges []model.Edge
	var refs []string
	for _, line := range splitLines(string(src)) {
		if rest, ok := cutPrefix(line, "edge:"); ok {
			target := rest
			tn, err := nodeForPath(target)
			if err != nil {
				return nil, err
			}
			e, err := model.NewEdge(n.ID(), tn.ID(), "calls", model.TierDerived, 0.9, "edge directive", []string{"directive"})
			if err != nil {
				return nil, err
			}
			edges = append(edges, e)
			refs = append(refs, target)
		}
		if rest, ok := cutPrefix(line, "use:"); ok {
			refs = append(refs, rest)
		}
	}
	return &parse.ParseResult{
		Meta:       parse.SourceMeta{Path: path, Language: "stub", Size: len(src)},
		Nodes:      []model.Node{n},
		Edges:      edges,
		References: refs,
	}, nil
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, trimSpace(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, trimSpace(cur))
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return trimSpace(s[len(prefix):]), true
	}
	return "", false
}

// --- AC-1: per-element provenance, nodes + edges, cascade -------------------

func TestProvenance_RecordedForNodesAndEdgesAcrossCascade(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	i, err := ingest.New(store, edgeParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i.Close()

	// b.go depends on a.go (via use:) AND has an edge to a.go. Editing a.go
	// cascades to b.go (b is a dependent of a), so the incremental pass touches
	// both files' nodes and b's edge.
	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go\nedge:a.go\n",
	})
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n//changed\n"), 0o600); err != nil {
		t.Fatalf("edit a.go: %v", err)
	}

	prov := ingest.EditProvenance{EditID: "edit-1", OpType: ingest.EditOpMove, Timestamp: 1234}
	if err := i.IngestChangedWithProvenance(ctx, repo, []string{"a.go"}, prov); err != nil {
		t.Fatalf("IngestChangedWithProvenance: %v", err)
	}

	recs, err := i.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("EditProvenance: %v", err)
	}

	// Expected affected elements: a.go's node, b.go's node (cascade), b.go's edge
	// (re-derived during the cascade pass). Compute their ids the same way the
	// parser does.
	aNode, _ := nodeForPath("a.go")
	bNode, _ := nodeForPath("b.go")
	bEdge, _ := model.NewEdge(bNode.ID(), aNode.ID(), "calls", model.TierDerived, 0.9, "edge directive", []string{"directive"})

	want := map[string]string{ // element id -> kind
		string(aNode.ID()): "node",
		string(bNode.ID()): "node",
		string(bEdge.ID()): "edge",
	}
	got := make(map[string]string)
	for _, r := range recs {
		if r.EditID != "edit-1" {
			t.Fatalf("unexpected edit id %q", r.EditID)
		}
		if r.OpType != ingest.EditOpMove {
			t.Fatalf("element %s op_type = %q, want move", r.ElementID, r.OpType)
		}
		if r.RecordedAt != 1234 {
			t.Fatalf("element %s recorded_at = %d, want 1234 (single timestamp per edit)", r.ElementID, r.RecordedAt)
		}
		got[r.ElementID] = string(r.ElementKind)
	}
	for id, kind := range want {
		k, ok := got[id]
		if !ok {
			t.Fatalf("missing provenance for %s element %s; got rows: %+v", kind, id, recs)
		}
		if k != kind {
			t.Fatalf("element %s kind = %q, want %q", id, k, kind)
		}
	}
}

// AC-1: op_type closed-enum rejection.
func TestProvenance_RejectsUnknownOpType(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	i, err := ingest.New(store, &stubParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i.Close()
	repo := writeRepo(t, map[string]string{"a.go": "package a\n"})
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	bad := ingest.EditProvenance{EditID: "e", OpType: ingest.EditOpType("frobnicate"), Timestamp: 1}
	if err := i.IngestChangedWithProvenance(ctx, repo, []string{"a.go"}, bad); err == nil {
		t.Fatal("expected rejection of unknown op_type, got nil")
	}
	// An empty edit id is also rejected.
	if err := i.IngestChangedWithProvenance(ctx, repo, []string{"a.go"}, ingest.EditProvenance{OpType: ingest.EditOpApply}); err == nil {
		t.Fatal("expected rejection of empty edit id, got nil")
	}
}

// AC-1: single timestamp shared across the whole touched set + ≤2s freshness
// over the IngestChanged window.
func TestProvenance_SingleTimestampAndFreshnessBudget(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	i, err := ingest.New(store, edgeParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer i.Close()

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go\nedge:a.go\n",
	})
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n//x\n"), 0o600); err != nil {
		t.Fatalf("edit: %v", err)
	}

	prov := ingest.EditProvenance{EditID: "e2", OpType: ingest.EditOpApply, Timestamp: time.Now().UnixNano()}
	start := time.Now()
	if err := i.IngestChangedWithProvenance(ctx, repo, []string{"a.go"}, prov); err != nil {
		t.Fatalf("IngestChangedWithProvenance: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("incremental freshness %v exceeds 2s budget", elapsed)
	}

	recs, err := i.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("EditProvenance: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected provenance rows")
	}
	ts := recs[0].RecordedAt
	for _, r := range recs {
		if r.RecordedAt != ts {
			t.Fatalf("timestamps diverge across one edit: %d vs %d", r.RecordedAt, ts)
		}
	}
}

// --- AC-2: crash recovery is provenance-idempotent --------------------------

func TestProvenance_CrashRecoveryIsIdempotent(t *testing.T) {
	ctx := context.Background()

	// Reference run: an uninterrupted edit, to capture the expected side-channel.
	refStore := graphstore.NewMemStore()
	defer refStore.Close()
	ref, err := ingest.New(refStore, edgeParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New ref: %v", err)
	}
	defer ref.Close()

	crashStore := graphstore.NewMemStore()
	defer crashStore.Close()
	crash, err := ingest.New(crashStore, edgeParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New crash: %v", err)
	}
	defer crash.Close()

	files := map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go\nedge:a.go\n",
	}
	refRepo := writeRepo(t, files)
	crashRepo := writeRepo(t, files)
	if err := ref.IngestAll(ctx, refRepo); err != nil {
		t.Fatalf("ref IngestAll: %v", err)
	}
	if err := crash.IngestAll(ctx, crashRepo); err != nil {
		t.Fatalf("crash IngestAll: %v", err)
	}

	prov := ingest.EditProvenance{EditID: "edit-X", OpType: ingest.EditOpRename, Timestamp: 9999}

	// Uninterrupted edit on the reference.
	if err := os.WriteFile(filepath.Join(refRepo, "a.go"), []byte("package a\n//e\n"), 0o600); err != nil {
		t.Fatalf("ref edit: %v", err)
	}
	if err := ref.IngestChangedWithProvenance(ctx, refRepo, []string{"a.go"}, prov); err != nil {
		t.Fatalf("ref ingest: %v", err)
	}
	refRecs, err := ref.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("ref provenance: %v", err)
	}

	// Crash run: same edit, but fault injected after Phase-1 dirty-mark.
	if err := os.WriteFile(filepath.Join(crashRepo, "a.go"), []byte("package a\n//e\n"), 0o600); err != nil {
		t.Fatalf("crash edit: %v", err)
	}
	injected := errors.New("simulated crash after dirty-mark")
	crash.SetFailAfterDirtyMarkHook(injected)
	if err := crash.IngestChangedWithProvenance(ctx, crashRepo, []string{"a.go"}, prov); !errors.Is(err, injected) {
		t.Fatalf("expected injected crash, got %v", err)
	}

	// After the crash: the file is still dirty AND no partial provenance recorded.
	partial, err := crash.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("crash provenance (pre-recover): %v", err)
	}
	if len(partial) != 0 {
		t.Fatalf("expected NO provenance before recovery, got %d rows: %+v", len(partial), partial)
	}

	// Recover: re-ingests and reproduces the identical side-channel state.
	if err := crash.RecoverWithRoot(ctx, crashRepo); err != nil {
		t.Fatalf("RecoverWithRoot: %v", err)
	}
	crashRecs, err := crash.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("crash provenance (post-recover): %v", err)
	}

	if !sameProvenance(refRecs, crashRecs) {
		t.Fatalf("recovery side-channel diverges from uninterrupted edit:\nref=%+v\ncrash=%+v", refRecs, crashRecs)
	}
	if len(crashRecs) == 0 {
		t.Fatal("expected recovered provenance rows")
	}
}

// --- Schema migration: existing pre-SW-037 sidecar (Finding 1, AC-2) --------

// seedOldSchemaSidecar writes an ingest-meta.db with the PRE-SW-037 schema:
// dirty_units carries ONLY (path), exactly as SW-036/EP-001 created it, and
// user_version is left at the SQLite default (0). It also seeds a cache row so
// the table is populated, mirroring a real already-indexed repo (ADD COLUMN with
// a NOT NULL DEFAULT must remain safe on a populated table). The file is closed
// before ingest.New re-opens it through the migration ladder.
func seedOldSchemaSidecar(t *testing.T, metaDir string) {
	t.Helper()
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatalf("mkdir meta: %v", err)
	}
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open old sidecar: %v", err)
	}
	defer db.Close()
	const oldDDL = `
CREATE TABLE IF NOT EXISTS file_content_cache (
	path TEXT PRIMARY KEY,
	content_hash TEXT NOT NULL,
	node_ids TEXT NOT NULL,
	last_ingested_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS reverse_deps (
	path TEXT PRIMARY KEY,
	dependents TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dirty_units (
	path TEXT PRIMARY KEY
);`
	if _, err := db.Exec(oldDDL); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	// Populate dirty_units so the ADD COLUMN migration runs against a non-empty
	// table (the case that would break a naive ALTER without a DEFAULT).
	if _, err := db.Exec("INSERT INTO dirty_units (path) VALUES (?)", "stale.go"); err != nil {
		t.Fatalf("seed dirty row: %v", err)
	}
}

// TestMigration_OldDirtyUnitsSchemaUpgradesAndAcceptsProvenance reproduces
// Finding 1: a sidecar created before SW-037 has dirty_units(path) only, so
// CREATE TABLE IF NOT EXISTS cannot add the edit-context columns and the first
// provenance-bearing markDirtyTx INSERT fails with "no such column: edit_id".
// Against the unfixed code this test FAILS at IngestChangedWithProvenance; with
// the migration ladder it passes end-to-end and records correct provenance, with
// crash recovery still provenance-idempotent on the upgraded DB.
func TestMigration_OldDirtyUnitsSchemaUpgradesAndAcceptsProvenance(t *testing.T) {
	ctx := context.Background()
	metaDir := t.TempDir()

	// 1. Lay down the OLD dirty_units(path) schema, then close it.
	seedOldSchemaSidecar(t, metaDir)

	// 2. Re-open through the current schema init + migration ladder.
	store := graphstore.NewMemStore()
	defer store.Close()
	i, err := ingest.New(store, edgeParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New on old sidecar: %v", err)
	}
	defer i.Close()

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go\nedge:a.go\n",
	})
	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n//changed\n"), 0o600); err != nil {
		t.Fatalf("edit a.go: %v", err)
	}

	// 3. A provenance-bearing edit must succeed end-to-end with NO
	//    "no such column" error against the migrated dirty_units table.
	prov := ingest.EditProvenance{EditID: "edit-mig", OpType: ingest.EditOpMove, Timestamp: 4242}
	if err := i.IngestChangedWithProvenance(ctx, repo, []string{"a.go"}, prov); err != nil {
		t.Fatalf("IngestChangedWithProvenance on migrated sidecar: %v", err)
	}

	recs, err := i.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("EditProvenance: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("expected provenance rows after edit on migrated sidecar")
	}
	for _, r := range recs {
		if r.EditID != "edit-mig" || r.OpType != ingest.EditOpMove || r.RecordedAt != 4242 {
			t.Fatalf("bad provenance on migrated sidecar: %+v", r)
		}
	}

	// Crash recovery must remain provenance-idempotent on the upgraded DB:
	// the dirty-row edit context (the new columns) must be replayable.
	if err := os.WriteFile(filepath.Join(repo, "b.go"), []byte("package b\nuse:a.go\nedge:a.go\n//touch\n"), 0o600); err != nil {
		t.Fatalf("edit b.go: %v", err)
	}
	prov2 := ingest.EditProvenance{EditID: "edit-mig-2", OpType: ingest.EditOpRename, Timestamp: 5555}
	injected := errors.New("simulated crash after dirty-mark")
	i.SetFailAfterDirtyMarkHook(injected)
	if err := i.IngestChangedWithProvenance(ctx, repo, []string{"b.go"}, prov2); !errors.Is(err, injected) {
		t.Fatalf("expected injected crash, got %v", err)
	}
	i.SetFailAfterDirtyMarkHook(nil)
	if err := i.RecoverWithRoot(ctx, repo); err != nil {
		t.Fatalf("RecoverWithRoot on migrated sidecar: %v", err)
	}
	recs2, err := i.EditProvenance(ctx)
	if err != nil {
		t.Fatalf("EditProvenance post-recover: %v", err)
	}
	var found bool
	for _, r := range recs2 {
		if r.EditID == "edit-mig-2" && r.OpType == ingest.EditOpRename && r.RecordedAt == 5555 {
			found = true
		}
	}
	if !found {
		t.Fatalf("recovery did not replay original edit context on migrated sidecar: %+v", recs2)
	}
}

func sameProvenance(a, b []ingest.EditProvenanceRecord) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(r ingest.EditProvenanceRecord) string {
		return fmt.Sprintf("%s|%s|%s|%s|%d", r.ElementID, r.ElementKind, r.EditID, r.OpType, r.RecordedAt)
	}
	seen := make(map[string]int)
	for _, r := range a {
		seen[key(r)]++
	}
	for _, r := range b {
		seen[key(r)]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}
