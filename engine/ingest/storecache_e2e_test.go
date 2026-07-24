package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// TestIngest_DoesNotBuildStoreCache pins the ingest-path memory contract for
// the SQLite backend: neither a full pass nor an incremental pass may
// materialize the store's whole-graph memGraph hot cache. Every ingest-side
// read streams through the GraphScanner ports, so CacheRebuilds — the
// observable cache-miss/rebuild counter — must stay at zero across both
// passes. Query surfaces still rebuild the cache lazily on their first read;
// that first rebuild simply must not be paid (repeatedly, across the
// per-phase batch evictions) during indexing itself.
func TestIngest_DoesNotBuildStoreCache(t *testing.T) {
	repo := writeRepo(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.21\n",
		"pkg/a/a.go": `package a

func Helper() int { return 1 }
`,
		"pkg/b/b.go": `package b

import "demo/pkg/a"

func Use() int { return a.Helper() }
`,
		"app/util.py": "def util():\n    return 1\n",
	})
	store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	ctx := context.Background()

	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if got := store.CacheRebuilds(); got != 0 {
		t.Errorf("full pass rebuilt the whole-graph hot cache %d time(s); ingest reads must stream via the scan ports", got)
	}

	// Incremental pass over the now-populated store: the warm path's reads
	// (reverse-dep index, linker, typeresolve, orphan sweep) must stream too.
	changed := filepath.Join(repo, "pkg", "b", "b.go")
	if err := os.WriteFile(changed, []byte(`package b

import "demo/pkg/a"

func Use() int { return a.Helper() + 1 }
`), 0o600); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if err := ing.IngestChanged(ctx, repo, []string{"pkg/b/b.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	if got := store.CacheRebuilds(); got != 0 {
		t.Errorf("incremental pass rebuilt the whole-graph hot cache %d time(s); ingest reads must stream via the scan ports", got)
	}
}
