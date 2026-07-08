package ingest_test

// WP-07: build/dependency output directories (node_modules, target, build,
// .gradle, dist) are pruned by DEFAULT so a monorepo that checks in generator
// output does not bloat the graph with non-source files. GRAPHI_INDEX_ALL opts
// back in.

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

var denylistRepo = map[string]string{
	"src/app.go":                "package app\n\nfunc Real() {}\n",
	"node_modules/dep/index.js": "function dep() {}\n",
	"target/gen/Generated.java": "package gen;\npublic class Generated {}\n",
	"build/out/Built.java":      "package out;\npublic class Built {}\n",
	".gradle/cache/Cached.java": "package cache;\npublic class Cached {}\n",
	"dist/bundle.js":            "function bundled() {}\n",
	"pkg/util.go":               "package pkg\n\nfunc Util() {}\n",
}

func indexedPaths(t *testing.T, indexAll bool) map[string]bool {
	t.Helper()
	if indexAll {
		t.Setenv("GRAPHI_INDEX_ALL", "1")
	} else {
		t.Setenv("GRAPHI_INDEX_ALL", "")
	}
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	repo := writeRepo(t, denylistRepo)
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	paths := map[string]bool{}
	for _, n := range nodes {
		paths[n.SourcePath()] = true
	}
	return paths
}

func anyUnder(paths map[string]bool, dir string) bool {
	for p := range paths {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

func TestDefaultDenylist_PrunesBuildOutputByDefault(t *testing.T) {
	paths := indexedPaths(t, false)
	// Real source is indexed.
	if !anyUnder(paths, "src") || !anyUnder(paths, "pkg") {
		t.Errorf("real source under src/ and pkg/ must be indexed; got paths=%v", keysOf(paths))
	}
	// Build/dependency output is pruned.
	for _, d := range []string{"node_modules", "target", "build", ".gradle", "dist"} {
		if anyUnder(paths, d) {
			t.Errorf("%s/ must be pruned by default but was indexed", d)
		}
	}
}

func TestDefaultDenylist_IndexAllOptsBackIn(t *testing.T) {
	paths := indexedPaths(t, true)
	// With GRAPHI_INDEX_ALL, at least one previously-pruned dir is now indexed.
	if !anyUnder(paths, "node_modules") && !anyUnder(paths, "target") && !anyUnder(paths, "build") {
		t.Errorf("GRAPHI_INDEX_ALL must index build-output dirs; got paths=%v", keysOf(paths))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
