package graphstore_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	gs "github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// backend pairs a human name with a Factory so the contract suite runs the
// IDENTICAL test bodies against every implementation (AC 5, AC 12).
type backend struct {
	name    string
	factory gs.Factory
}

// allBackends is the single source of backends the contract suite is
// parameterized over: the in-memory test double and the durable SQLite backend.
func allBackends() []backend {
	return []backend{
		{name: "mem", factory: gs.MemFactory},
		{name: "sqlite", factory: gs.SQLiteFactory},
	}
}

func mustNode(t *testing.T, kind, qn, path string, line, col int) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, qn, path, line, col)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return n
}

func mustEdge(t *testing.T, from, to model.NodeId, kind string, tier model.ConfidenceTier, conf float64, reason string, evidence []string) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, kind, tier, conf, reason, evidence)
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	return e
}

// newStore builds a fresh empty store for a backend in a temp dir.
func newStore(t *testing.T, b backend) gs.Graphstore {
	t.Helper()
	st, err := b.factory(t.TempDir())
	if err != nil {
		t.Fatalf("[%s] factory: %v", b.name, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// nodesEqual / edgesEqual compare by every public field so provenance is checked.
func nodesEqual(a, b model.Node) bool {
	return a.ID() == b.ID() && a.Kind() == b.Kind() && a.QualifiedName() == b.QualifiedName() &&
		a.SourcePath() == b.SourcePath() && a.Line() == b.Line() && a.Column() == b.Column()
}

func edgesEqual(a, b model.Edge) bool {
	return a.ID() == b.ID() && a.From() == b.From() && a.To() == b.To() && a.Kind() == b.Kind() &&
		a.Tier() == b.Tier() && a.Confidence() == b.Confidence() && a.Reason() == b.Reason() &&
		reflect.DeepEqual(a.Evidence(), b.Evidence())
}

// seed inserts a small deterministic graph and returns it.
func seed(t *testing.T, st gs.Graphstore) ([]model.Node, []model.Edge) {
	t.Helper()
	ctx := context.Background()
	n1 := mustNode(t, "function", "pkg/foo.Bar", "pkg/foo.go", 10, 2)
	n2 := mustNode(t, "function", "pkg/foo.Baz", "pkg/foo.go", 20, 2)
	n3 := mustNode(t, "type", "pkg/foo.Widget", "pkg/foo.go", 5, 1)
	for _, n := range []model.Node{n1, n2, n3} {
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
	e1 := mustEdge(t, n1.ID(), n2.ID(), "calls", model.TierDerived, 0.9, "resolved call site", []string{"foo.go:11"})
	e2 := mustEdge(t, n1.ID(), n3.ID(), "uses", model.TierHeuristic, 0.5, "mentions Widget", []string{"foo.go:12", "foo.go:13"})
	for _, e := range []model.Edge{e1, e2} {
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}
	return []model.Node{n1, n2, n3}, []model.Edge{e1, e2}
}

// TestContract_ReadAfterWrite covers basic CRUD + endpoint enforcement across all
// backends (AC 5, AC 12 substrate).
func TestContract_ReadAfterWrite(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			nodes, edges := seed(t, st)

			got, err := st.GetNode(ctx, nodes[0].ID())
			if err != nil {
				t.Fatalf("GetNode: %v", err)
			}
			if !nodesEqual(got, nodes[0]) {
				t.Fatalf("node mismatch: got %+v want %+v", got, nodes[0])
			}
			ge, err := st.GetEdge(ctx, edges[0].ID())
			if err != nil {
				t.Fatalf("GetEdge: %v", err)
			}
			if !edgesEqual(ge, edges[0]) {
				t.Fatalf("edge mismatch incl. provenance: got %+v want %+v", ge, edges[0])
			}

			if _, err := st.GetNode(ctx, model.NodeId("deadbeefdeadbeef")); !errors.Is(err, gs.ErrNotFound) {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}

			// Edge with unknown endpoint must be rejected.
			orphan := mustEdge(t, model.NodeId("0000000000000000"), nodes[0].ID(), "calls",
				model.TierDerived, 0.5, "dangling", []string{"x:1"})
			if err := st.PutEdge(ctx, orphan); !errors.Is(err, gs.ErrUnknownEdgeEndpoint) {
				t.Fatalf("expected ErrUnknownEdgeEndpoint, got %v", err)
			}
		})
	}
}

// TestContract_CanonicalOrdering asserts listings come back sorted by ID.
func TestContract_CanonicalOrdering(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			seed(t, st)

			ns, err := st.Nodes(ctx, gs.Query{})
			if err != nil {
				t.Fatalf("Nodes: %v", err)
			}
			for i := 1; i < len(ns); i++ {
				if ns[i-1].ID() >= ns[i].ID() {
					t.Fatalf("nodes not in canonical order: %q >= %q", ns[i-1].ID(), ns[i].ID())
				}
			}
			es, err := st.Edges(ctx, gs.Query{})
			if err != nil {
				t.Fatalf("Edges: %v", err)
			}
			for i := 1; i < len(es); i++ {
				if es[i-1].ID() >= es[i].ID() {
					t.Fatalf("edges not in canonical order")
				}
			}
		})
	}
}

// TestContract_Filters covers kind + text filtering across all backends.
func TestContract_Filters(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			seed(t, st)

			funcs, err := st.Nodes(ctx, gs.Query{NodeKind: "function"})
			if err != nil {
				t.Fatalf("Nodes(kind): %v", err)
			}
			if len(funcs) != 2 {
				t.Fatalf("expected 2 function nodes, got %d", len(funcs))
			}

			calls, err := st.Edges(ctx, gs.Query{EdgeKind: "calls"})
			if err != nil {
				t.Fatalf("Edges(kind): %v", err)
			}
			if len(calls) != 1 {
				t.Fatalf("expected 1 calls edge, got %d", len(calls))
			}

			// Full-text: "Widget" appears in node n3's qualified name.
			hits, err := st.Nodes(ctx, gs.Query{Text: "Widget"})
			if err != nil {
				t.Fatalf("Nodes(text): %v", err)
			}
			if len(hits) != 1 || hits[0].QualifiedName() != "pkg/foo.Widget" {
				t.Fatalf("text search expected 1 Widget node, got %+v", hits)
			}

			// Full-text over edge reason.
			ehits, err := st.Edges(ctx, gs.Query{Text: "Widget"})
			if err != nil {
				t.Fatalf("Edges(text): %v", err)
			}
			if len(ehits) != 1 || ehits[0].Reason() != "mentions Widget" {
				t.Fatalf("edge text search expected 1 hit, got %+v", ehits)
			}
		})
	}
}

// TestContract_CacheTransparency proves eviction is transparent: re-querying after
// a full evict yields byte-identical results (AC 2, AC 11).
func TestContract_CacheTransparency(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			seed(t, st)

			before := serializeListing(t, ctx, st)

			if err := st.EvictCache(ctx); err != nil {
				t.Fatalf("EvictCache: %v", err)
			}
			after := serializeListing(t, ctx, st)

			if !bytes.Equal(before, after) {
				t.Fatalf("results differ pre/post eviction:\nbefore=%s\nafter =%s", before, after)
			}
		})
	}
}

// serializeListing renders the full store contents to canonical model bytes for
// byte-for-byte comparison under the defined ordering.
func serializeListing(t *testing.T, ctx context.Context, st gs.Graphstore) []byte {
	t.Helper()
	ns, err := st.Nodes(ctx, gs.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	es, err := st.Edges(ctx, gs.Query{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	g := model.NewGraph(ns, es)
	out, err := g.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return out
}

// TestContract_SnapshotLoadRoundTrip proves snapshot→load into a fresh store
// yields exactly the same nodes/edges/metadata, deterministically (AC 3, AC 8).
func TestContract_SnapshotLoadRoundTrip(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			src := newStore(t, b)
			seed(t, src)

			dir := t.TempDir()
			snapPath := filepath.Join(dir, "graph.snapshot")
			if err := src.Snapshot(ctx, snapPath); err != nil {
				t.Fatalf("Snapshot: %v", err)
			}

			// Fresh empty store of the SAME backend.
			dst := newStore(t, b)
			if err := dst.Load(ctx, snapPath); err != nil {
				t.Fatalf("Load: %v", err)
			}

			srcBytes := serializeListing(t, ctx, src)
			dstBytes := serializeListing(t, ctx, dst)
			if !bytes.Equal(srcBytes, dstBytes) {
				t.Fatalf("round-trip mismatch:\nsrc=%s\ndst=%s", srcBytes, dstBytes)
			}

			// Re-snapshot the rehydrated store: must be byte-identical to original.
			snapPath2 := filepath.Join(dir, "graph2.snapshot")
			if err := dst.Snapshot(ctx, snapPath2); err != nil {
				t.Fatalf("re-Snapshot: %v", err)
			}
			b1 := readFile(t, snapPath)
			b2 := readFile(t, snapPath2)
			if !bytes.Equal(b1, b2) {
				t.Fatalf("re-snapshot not byte-identical")
			}
		})
	}
}

// TestContract_LoadFailClosed proves bad snapshots are rejected and leave the
// target store unmodified (AC 13).
func TestContract_LoadFailClosed(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			nodes, _ := seed(t, st)
			before := serializeListing(t, ctx, st)

			dir := t.TempDir()
			cases := map[string][]byte{
				"garbage":        []byte("not json at all"),
				"truncated":      []byte(`{"magic":"graphi.graphstore.snapshot","format_versi`),
				"bad-magic":      []byte(`{"magic":"nope","format_version":1,"model_schema_version":1,"graph":{}}`),
				"bad-version":    []byte(`{"magic":"graphi.graphstore.snapshot","format_version":999,"model_schema_version":1,"graph":{}}`),
				"bad-modelver":   []byte(`{"magic":"graphi.graphstore.snapshot","format_version":1,"model_schema_version":999,"graph":{}}`),
			}
			for name, content := range cases {
				p := filepath.Join(dir, name+".snapshot")
				writeRaw(t, p, content)
				if err := st.Load(ctx, p); err == nil {
					t.Fatalf("[%s] expected load to fail closed", name)
				}
				after := serializeListing(t, ctx, st)
				if !bytes.Equal(before, after) {
					t.Fatalf("[%s] store mutated by failed load", name)
				}
			}
			// Sanity: original data still there.
			if _, err := st.GetNode(ctx, nodes[0].ID()); err != nil {
				t.Fatalf("original node lost after failed loads: %v", err)
			}
		})
	}
}

// TestContract_PathSafety proves traversal/symlink paths are rejected (AC 14).
func TestContract_PathSafety(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			seed(t, st)

			// Build the path by raw concatenation so the ".." segment survives into
			// the store (filepath.Join would pre-clean it away).
			bad := t.TempDir() + string(filepath.Separator) + ".." + string(filepath.Separator) + "escape.snapshot"
			if err := st.Snapshot(ctx, bad); !errors.Is(err, gs.ErrUnsafePath) {
				t.Fatalf("expected ErrUnsafePath on traversal snapshot, got %v", err)
			}
			if err := st.Load(ctx, bad); !errors.Is(err, gs.ErrUnsafePath) {
				t.Fatalf("expected ErrUnsafePath on traversal load, got %v", err)
			}
		})
	}
}

// TestContract_SymlinkEscape proves a symlink that escapes the intended directory
// is rejected on load (AC 14).
func TestContract_SymlinkEscape(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			src := newStore(t, b)
			seed(t, src)

			outside := t.TempDir()
			realSnap := filepath.Join(outside, "real.snapshot")
			if err := src.Snapshot(ctx, realSnap); err != nil {
				t.Fatalf("Snapshot: %v", err)
			}

			linkDir := t.TempDir()
			link := filepath.Join(linkDir, "link.snapshot")
			if err := makeSymlink(realSnap, link); err != nil {
				t.Skipf("symlink unsupported: %v", err)
			}
			dst := newStore(t, b)
			if err := dst.Load(ctx, link); !errors.Is(err, gs.ErrUnsafePath) {
				t.Fatalf("expected ErrUnsafePath on escaping symlink load, got %v", err)
			}
		})
	}
}

// TestContract_SearchNodes asserts full-text search returns ranked, ordered,
// limited results with no error on empty/no-match queries.
func TestContract_SearchNodes(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t, b)
			seed(t, st)

			// Empty query returns no results and no error.
			empty, err := st.SearchNodes(ctx, "", 10)
			if err != nil {
				t.Fatalf("SearchNodes empty: %v", err)
			}
			if len(empty) != 0 {
				t.Fatalf("expected 0 results for empty query, got %d", len(empty))
			}

			// No-match query returns no results and no error.
			none, err := st.SearchNodes(ctx, "DefinitelyMissing", 10)
			if err != nil {
				t.Fatalf("SearchNodes none: %v", err)
			}
			if len(none) != 0 {
				t.Fatalf("expected 0 results for no-match query, got %d", len(none))
			}

			// "Widget" matches exactly one node.
			hits, err := st.SearchNodes(ctx, "Widget", 10)
			if err != nil {
				t.Fatalf("SearchNodes Widget: %v", err)
			}
			if len(hits) != 1 || hits[0].Node.QualifiedName() != "pkg/foo.Widget" {
				t.Fatalf("expected 1 Widget hit, got %+v", hits)
			}

			// Limit works: "pkg" matches all three nodes, limit=2.
			limited, err := st.SearchNodes(ctx, "pkg", 2)
			if err != nil {
				t.Fatalf("SearchNodes limited: %v", err)
			}
			if len(limited) != 2 {
				t.Fatalf("expected 2 limited results, got %d", len(limited))
			}

			// Deterministic ordering: re-query yields same order.
			again, err := st.SearchNodes(ctx, "pkg", 2)
			if err != nil {
				t.Fatalf("SearchNodes again: %v", err)
			}
			for i := range limited {
				if limited[i].Node.ID() != again[i].Node.ID() {
					t.Fatalf("ordering non-deterministic at %d", i)
				}
			}
		})
	}
}

// TestContract_EmptySnapshotRoundTrip ensures an empty store snapshots/loads
// cleanly across backends.
func TestContract_EmptySnapshotRoundTrip(t *testing.T) {
	for _, b := range allBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			src := newStore(t, b)
			p := filepath.Join(t.TempDir(), "empty.snapshot")
			if err := src.Snapshot(ctx, p); err != nil {
				t.Fatalf("Snapshot empty: %v", err)
			}
			dst := newStore(t, b)
			if err := dst.Load(ctx, p); err != nil {
				t.Fatalf("Load empty: %v", err)
			}
			ns, _ := dst.Nodes(ctx, gs.Query{})
			es, _ := dst.Edges(ctx, gs.Query{})
			if len(ns) != 0 || len(es) != 0 {
				t.Fatalf("expected empty store, got %d nodes %d edges", len(ns), len(es))
			}
		})
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	data, err := readFileHelper(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return data
}
