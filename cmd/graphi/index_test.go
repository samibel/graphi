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

// `graphi index` requires -root.
func TestRunIndex_RequiresRoot(t *testing.T) {
	if code := runIndex([]string{"--semantic"}); code == 0 {
		t.Fatal("runIndex without -root exit = 0, want non-zero")
	}
}
