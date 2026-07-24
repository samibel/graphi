package ingest

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// TestWalk_DoesNotRetainSrc pins the full-pass memory contract for source
// bytes: walk() returns metadata-only units (path, relPath, content hash) and
// must NOT retain any file's bytes — holding units[].src kept the entire
// repo's source resident for the whole ingest. The bytes are instead read
// on demand inside parseUnit, bounded by the parse-pool width, and the graph
// a full pass commits from those on-demand reads must be identical.
func TestWalk_DoesNotRetainSrc(t *testing.T) {
	repo := writeRepoIngest(t, map[string]string{
		"app/util.py": "def util():\n    return 1\n",
		"shop/cart.go": `package shop
func checkout() int { return 1 }
`,
		"readme.md": "# demo\n",
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	units, err := i.walk(repo, nil)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(units) != 3 {
		t.Fatalf("walked %d units, want 3", len(units))
	}
	for _, u := range units {
		if u.src != nil {
			t.Errorf("unit %s retains %d src bytes; walk must return metadata-only units", u.relPath, len(u.src))
		}
		if u.hash == "" {
			t.Errorf("unit %s has no content hash", u.relPath)
		}
	}

	// The on-demand read path must still commit the full graph.
	if err := i.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	nodes, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	var haveGoFn, havePyFn bool
	for _, n := range nodes {
		switch n.SourcePath() {
		case "shop/cart.go":
			haveGoFn = true
		case "app/util.py":
			havePyFn = true
		}
	}
	if !haveGoFn || !havePyFn {
		t.Fatalf("full pass over metadata-only units lost nodes (go=%v py=%v, %d nodes total)", haveGoFn, havePyFn, len(nodes))
	}
}
