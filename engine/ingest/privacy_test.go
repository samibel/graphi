package ingest_test

// SW-119 (PRIV-01) gates: the privacy defaults, provable end-to-end.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/ingest"
)

// TestPrivacy_SecretInIgnoredFileNeverEntersGraphOrSearch is the PRIV-01 exit
// gate ("Secret-Fixture nicht im Graph"): a credentials file listed in the
// repo's .gitignore must — under the DEFAULT configuration, over the REAL
// SQLite backend — appear neither as graph nodes nor in the full-text search
// index.
func TestPrivacy_SecretInIgnoredFileNeverEntersGraphOrSearch(t *testing.T) {
	t.Setenv(ingest.EnvRespectGitignore, "")
	t.Setenv(ingest.EnvIgnoreDirs, "")
	t.Setenv(ingest.EnvIndexAll, "")
	ctx := context.Background()

	root := writeRepo(t, map[string]string{
		".gitignore": "secrets.env\n",
		"main.go":    "package main\n",
		// stubParser derives node names from file basenames, so this node's
		// searchable qualified name would contain "secrets.env" if indexed.
		"secrets.env": "AWS_KEY=AKIAXXXXXXXXXXXXXXXX\n",
	})

	store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer store.Close()
	i := newIngester(t, store, &stubParser{})
	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("fixture produced no nodes at all — the gate proves nothing")
	}
	for _, n := range nodes {
		if n.SourcePath() == "secrets.env" {
			t.Fatalf("gitignored secret file entered the graph: %s", n.QualifiedName())
		}
	}
	hits, err := store.SearchNodes(ctx, "secrets", 10)
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("gitignored secret file is full-text searchable: %d hit(s), first %q", len(hits), hits[0].Node.QualifiedName())
	}
}

// TestPrivacy_MetaSidecarOwnerOnly: the ingest meta sidecar (file hashes,
// reverse-deps, provenance) is 0600 on creation AND migrated to 0600 when a
// pre-existing sidecar is wider.
func TestPrivacy_MetaSidecarOwnerOnly(t *testing.T) {
	metaDir := t.TempDir()
	dbPath := filepath.Join(metaDir, "ingest-meta.db")

	i := newIngesterAt(t, metaDir)
	_ = i
	assertMode(t, dbPath, 0o600)

	// Widen, reopen, expect migration.
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_ = newIngesterAt(t, metaDir)
	assertMode(t, dbPath, 0o600)
}

func newIngesterAt(t *testing.T, metaDir string) *ingest.Ingester {
	t.Helper()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := ingest.New(store, &stubParser{}, metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = i.Close() })
	return i
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
