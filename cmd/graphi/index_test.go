package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// writeGoRepo creates a tiny Go repo the default parser extracts nodes from.
func writeGoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package shop

const TaxRate = 7

func price(n int) int { return n * TaxRate }

func checkout() int { return price(3) }
`
	if err := os.WriteFile(filepath.Join(dir, "cart.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// countVectorRows reports how many rows the durable `vectors` sidecar holds. A
// missing table/DB counts as zero (the default ingest path never creates rows).
func countVectorRows(t *testing.T, metaDir string) int {
	t.Helper()
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	if _, err := os.Stat(dbPath); err != nil {
		return 0
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open meta db: %v", err)
	}
	defer db.Close()
	var n int
	row := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM vectors")
	if err := row.Scan(&n); err != nil {
		// No vectors table at all ⇒ zero rows.
		return 0
	}
	return n
}

// The default `graphi index` path (no --semantic) ingests successfully and writes
// ZERO vectors and dials nothing — the trust posture for the default path.
func TestRunIndex_DefaultPathNoVectors(t *testing.T) {
	repo := writeGoRepo(t)
	meta := t.TempDir()
	db := filepath.Join(t.TempDir(), "graph.db")
	t.Setenv("GRAPHI_EMBEDDER", "") // ensure no embedder leaks from host env

	if code := runIndex([]string{"-root", repo, "-db", db, "-meta", meta}); code != 0 {
		t.Fatalf("runIndex default path exit = %d, want 0", code)
	}
	if n := countVectorRows(t, meta); n != 0 {
		t.Fatalf("default index wrote %d vectors, want 0 (must never embed)", n)
	}
}

// `graphi index --semantic` with NO embedder configured gracefully skips embedding
// — exit 0 (no error), lexical index still committed, and zero vectors persisted.
func TestRunIndex_SemanticGracefulSkipWhenUnconfigured(t *testing.T) {
	repo := writeGoRepo(t)
	meta := t.TempDir()
	db := filepath.Join(t.TempDir(), "graph.db")
	t.Setenv("GRAPHI_EMBEDDER", "") // unconfigured ⇒ graceful skip

	if code := runIndex([]string{"--semantic", "-root", repo, "-db", db, "-meta", meta}); code != 0 {
		t.Fatalf("runIndex --semantic (unconfigured) exit = %d, want 0 (graceful)", code)
	}
	if n := countVectorRows(t, meta); n != 0 {
		t.Fatalf("graceful-skip index wrote %d vectors, want 0", n)
	}
}

// `graphi index` without -root still errors when cwd is not a repository.
func TestRunIndex_RequiresRoot(t *testing.T) {
	if code := runIndexAt(t.TempDir(), []string{"--semantic"}); code == 0 {
		t.Fatal("runIndex without -root outside a repo exit = 0, want non-zero")
	}
}

// `graphi index` without -root inside a repo now behaves like `graphi sync`:
// it detects the repo and targets the auto-managed per-repo state store.
func TestRunIndex_NoRootAutoSyncsInsideRepo(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if code := runIndexAt(repo, nil); code != 0 {
		t.Fatalf("runIndex (no -root, inside repo) exit = %d, want 0", code)
	}
	matches, err := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "db.sqlite"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("auto-managed state DB matches = %v (err %v), want exactly one", matches, err)
	}
}

// TestRunIndex_ProfilePersists asserts that the resolved profile is stored in
// graphstore metadata and survives reopening the store.
func TestRunIndex_ProfilePersists(t *testing.T) {
	repo := writeGoRepo(t)
	meta := t.TempDir()
	db := filepath.Join(t.TempDir(), "graph.db")
	t.Setenv("GRAPHI_EMBEDDER", "")

	if code := runIndex([]string{"-profile", "deep", "-root", repo, "-db", db, "-meta", meta}); code != 0 {
		t.Fatalf("runIndex deep exit = %d, want 0", code)
	}

	st, err := openStore(db)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = st.Close() }()
	got, err := st.Metadata(context.Background(), "index.profile")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if got != "deep" {
		t.Fatalf("index.profile = %q, want deep", got)
	}
}

// TestRunIndex_ProfileEnvFlagPrecedence asserts CLI flag overrides env.
func TestRunIndex_ProfileEnvFlagPrecedence(t *testing.T) {
	repo := writeGoRepo(t)
	meta := t.TempDir()
	db := filepath.Join(t.TempDir(), "graph.db")
	t.Setenv("GRAPHI_EMBEDDER", "")
	t.Setenv("GRAPHI_INDEX_PROFILE", "deep")

	if code := runIndex([]string{"-profile", "fast", "-root", repo, "-db", db, "-meta", meta}); code != 0 {
		t.Fatalf("runIndex fast exit = %d, want 0", code)
	}

	st, err := openStore(db)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = st.Close() }()
	got, err := st.Metadata(context.Background(), "index.profile")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if got != "fast" {
		t.Fatalf("index.profile = %q, want fast (flag overrides env)", got)
	}
}

// TestRunIndex_ProfileInvalidRejects asserts that an invalid profile exits before indexing.
func TestRunIndex_ProfileInvalidRejects(t *testing.T) {
	repo := writeGoRepo(t)
	meta := t.TempDir()
	db := filepath.Join(t.TempDir(), "graph.db")
	t.Setenv("GRAPHI_EMBEDDER", "")

	if code := runIndex([]string{"-profile", "turbo", "-root", repo, "-db", db, "-meta", meta}); code == 0 {
		t.Fatal("runIndex invalid profile exit = 0, want non-zero")
	}
}

// TestRunIndex_ProfileDefaultBalanced asserts default profile is balanced.
func TestRunIndex_ProfileDefaultBalanced(t *testing.T) {
	repo := writeGoRepo(t)
	meta := t.TempDir()
	db := filepath.Join(t.TempDir(), "graph.db")
	t.Setenv("GRAPHI_EMBEDDER", "")
	t.Setenv("GRAPHI_INDEX_PROFILE", "")

	if code := runIndex([]string{"-root", repo, "-db", db, "-meta", meta}); code != 0 {
		t.Fatalf("runIndex default exit = %d, want 0", code)
	}

	st, err := openStore(db)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = st.Close() }()
	got, err := st.Metadata(context.Background(), "index.profile")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if got != "balanced" {
		t.Fatalf("index.profile = %q, want balanced", got)
	}
}
