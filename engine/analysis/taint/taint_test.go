package taint

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// testReader implements query.Reader over in-memory slices for testing.
type testReader struct {
	nodes []model.Node
	edges []model.Edge
}

func (r *testReader) GetNode(_ context.Context, id model.NodeId) (model.Node, error) {
	for _, n := range r.nodes {
		if n.ID() == id {
			return n, nil
		}
	}
	return model.Node{}, graphstore.ErrNotFound
}

func (r *testReader) GetEdge(_ context.Context, id model.EdgeId) (model.Edge, error) {
	for _, e := range r.edges {
		if e.ID() == id {
			return e, nil
		}
	}
	return model.Edge{}, graphstore.ErrNotFound
}

func (r *testReader) Nodes(_ context.Context, _ graphstore.Query) ([]model.Node, error) {
	return r.nodes, nil
}

func (r *testReader) Edges(_ context.Context, _ graphstore.Query) ([]model.Edge, error) {
	return r.edges, nil
}

var _ query.Reader = (*testReader)(nil)

// helper to create a node (panics on error for test brevity).
func mustNode(t *testing.T, kind, name, path string, line, col int) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, name, path, line, col)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// helper to create a def-use edge (panics on error).
func mustEdge(t *testing.T, from, to model.NodeId, kind string) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, kind,
		model.TierDerived, 0.9, "taint test", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSimpleSourceToSink(t *testing.T) {
	// source → intermediate → sink
	src := mustNode(t, "call", "http.Request.FormValue", "handler.go", 10, 1)
	mid := mustNode(t, "variable", "userInput", "handler.go", 11, 1)
	sink := mustNode(t, "call", "database/sql.DB.Query", "handler.go", 12, 1)

	e1 := mustEdge(t, src.ID(), mid.ID(), "defines")
	e2 := mustEdge(t, mid.ID(), sink.ID(), "references")

	reader := &testReader{
		nodes: []model.Node{src, mid, sink},
		edges: []model.Edge{e1, e2},
	}

	cfg := DefaultConfig()
	cfg.ContentHash = "test-hash"
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Findings) == 0 {
		t.Fatal("expected at least one finding, got none")
	}

	f := result.Findings[0]
	if f.SourceID != src.ID() {
		t.Errorf("source: want %s, got %s", src.ID(), f.SourceID)
	}
	if f.SinkID != sink.ID() {
		t.Errorf("sink: want %s, got %s", sink.ID(), f.SinkID)
	}
	if f.SinkCategory != "sql_injection" {
		t.Errorf("category: want sql_injection, got %s", f.SinkCategory)
	}
	if len(f.Path) == 0 {
		t.Error("expected non-empty path provenance")
	}
	if f.ConfigHash != "test-hash" {
		t.Errorf("config hash: want test-hash, got %s", f.ConfigHash)
	}
}

func TestSanitizerKillsTaint(t *testing.T) {
	// source → sanitizer → sink
	src := mustNode(t, "call", "http.Request.FormValue", "handler.go", 10, 1)
	san := mustNode(t, "call", "html.EscapeString", "handler.go", 11, 1)
	sink := mustNode(t, "call", "database/sql.DB.Query", "handler.go", 12, 1)

	e1 := mustEdge(t, src.ID(), san.ID(), "defines")
	e2 := mustEdge(t, san.ID(), sink.ID(), "references")

	reader := &testReader{
		nodes: []model.Node{src, san, sink},
		edges: []model.Edge{e1, e2},
	}

	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings (sanitizer should kill taint), got %d", len(result.Findings))
	}
}

func TestReassignmentKillsTaint(t *testing.T) {
	// source → taintedVar → safeAssign → sink
	// The key: safeAssign does NOT have a taint label, and there's a new
	// def-use edge from safeAssign to sink (not from taintedVar).
	src := mustNode(t, "call", "os.Getenv", "main.go", 1, 1)
	tainted := mustNode(t, "variable", "x", "main.go", 2, 1)
	safe := mustNode(t, "literal", "safe_value", "main.go", 3, 1)
	sink := mustNode(t, "call", "os/exec.Command", "main.go", 4, 1)

	e1 := mustEdge(t, src.ID(), tainted.ID(), "defines")
	// safe overwrites x — the edge goes from safe to sink, NOT from tainted
	e2 := mustEdge(t, safe.ID(), sink.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{src, tainted, safe, sink},
		edges: []model.Edge{e1, e2},
	}

	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// No path from source→sink because safe_value broke the def-use chain.
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings (reassignment should kill taint), got %d", len(result.Findings))
	}
}

func TestEmptyGraph(t *testing.T) {
	reader := &testReader{}
	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings on empty graph, got %d", len(result.Findings))
	}
}

func TestNoSources(t *testing.T) {
	// Graph with a sink but no sources.
	sink := mustNode(t, "call", "database/sql.DB.Query", "handler.go", 12, 1)
	reader := &testReader{nodes: []model.Node{sink}}
	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings with no sources, got %d", len(result.Findings))
	}
}

func TestNoSinks(t *testing.T) {
	// Graph with a source but no sinks.
	src := mustNode(t, "call", "http.Request.FormValue", "handler.go", 10, 1)
	reader := &testReader{nodes: []model.Node{src}}
	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings with no sinks, got %d", len(result.Findings))
	}
}

func TestCapEnforcement(t *testing.T) {
	// Build a chain: source → n1 → n2 → ... → nN → sink
	// Set MaxDepth=2 so it triggers cap before reaching sink.
	src := mustNode(t, "call", "http.Request.FormValue", "handler.go", 1, 1)
	n1 := mustNode(t, "variable", "v1", "handler.go", 2, 1)
	n2 := mustNode(t, "variable", "v2", "handler.go", 3, 1)
	n3 := mustNode(t, "variable", "v3", "handler.go", 4, 1)
	sink := mustNode(t, "call", "database/sql.DB.Query", "handler.go", 5, 1)

	e1 := mustEdge(t, src.ID(), n1.ID(), "defines")
	e2 := mustEdge(t, n1.ID(), n2.ID(), "defines")
	e3 := mustEdge(t, n2.ID(), n3.ID(), "defines")
	e4 := mustEdge(t, n3.ID(), sink.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{src, n1, n2, n3, sink},
		edges: []model.Edge{e1, e2, e3, e4},
	}

	cfg := DefaultConfig()
	caps := Caps{MaxDepth: 2, MaxNodes: 100, MaxWork: 100}
	analyzer := New(cfg, caps, nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Diagnostics) == 0 {
		t.Error("expected cap-exceeded diagnostic")
	}
}

func TestDeterminism(t *testing.T) {
	// Run the same analysis 5 times and verify identical results.
	src := mustNode(t, "call", "http.Request.FormValue", "handler.go", 10, 1)
	mid := mustNode(t, "variable", "userInput", "handler.go", 11, 1)
	sink := mustNode(t, "call", "database/sql.DB.Query", "handler.go", 12, 1)

	e1 := mustEdge(t, src.ID(), mid.ID(), "defines")
	e2 := mustEdge(t, mid.ID(), sink.ID(), "references")

	reader := &testReader{
		nodes: []model.Node{src, mid, sink},
		edges: []model.Edge{e1, e2},
	}

	cfg := DefaultConfig()
	cfg.ContentHash = "determinism-test"
	analyzer := New(cfg, DefaultCaps(), nil)

	var firstResult TaintResult
	for i := 0; i < 5; i++ {
		result, err := analyzer.Run(context.Background(), reader)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstResult = result
			continue
		}
		if len(result.Findings) != len(firstResult.Findings) {
			t.Fatalf("run %d: finding count differs: %d vs %d", i, len(result.Findings), len(firstResult.Findings))
		}
		for j := range result.Findings {
			if result.Findings[j].SourceID != firstResult.Findings[j].SourceID {
				t.Errorf("run %d finding %d: source differs", i, j)
			}
			if result.Findings[j].SinkID != firstResult.Findings[j].SinkID {
				t.Errorf("run %d finding %d: sink differs", i, j)
			}
			if result.Findings[j].PathLength != firstResult.Findings[j].PathLength {
				t.Errorf("run %d finding %d: path length differs", i, j)
			}
		}
	}
}

func TestConditionalPaths(t *testing.T) {
	// source → condTrue → sink  (tainted path)
	// source → condFalse → sink (safe path, no edge from source to condFalse)
	src := mustNode(t, "call", "http.Request.FormValue", "handler.go", 1, 1)
	condTrue := mustNode(t, "variable", "trueVal", "handler.go", 2, 1)
	condFalse := mustNode(t, "literal", "safe_literal", "handler.go", 3, 1)
	sink := mustNode(t, "call", "database/sql.DB.Query", "handler.go", 4, 1)

	e1 := mustEdge(t, src.ID(), condTrue.ID(), "defines")
	e2 := mustEdge(t, condTrue.ID(), sink.ID(), "references")
	// condFalse → sink exists but is NOT connected to the source
	e3 := mustEdge(t, condFalse.ID(), sink.ID(), "references")

	reader := &testReader{
		nodes: []model.Node{src, condTrue, condFalse, sink},
		edges: []model.Edge{e1, e2, e3},
	}

	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// Should find exactly 1 finding: source → condTrue → sink.
	// The condFalse → sink path should NOT be reported because condFalse is
	// not tainted (no def-use chain from source).
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding (tainted path only), got %d", len(result.Findings))
	}
}

// TestLabelSet tests the label set operations.
func TestLabelSet(t *testing.T) {
	ls := NewLabelSet("b", "a", "c", "a")
	if got := ls.String(); got != "{a,b,c}" {
		t.Errorf("want {a,b,c}, got %s", got)
	}
	if !ls.Contains("a") {
		t.Error("should contain a")
	}
	if ls.Contains("d") {
		t.Error("should not contain d")
	}

	ls2 := NewLabelSet("d", "a")
	union := ls.Union(ls2)
	if got := union.String(); got != "{a,b,c,d}" {
		t.Errorf("union: want {a,b,c,d}, got %s", got)
	}

	removed := ls.Remove([]string{"a", "c"})
	if got := removed.String(); got != "{b}" {
		t.Errorf("remove: want {b}, got %s", got)
	}

	// Universal sanitizer (empty toRemove).
	allGone := ls.Remove(nil)
	if !allGone.Empty() {
		t.Error("universal sanitizer should produce empty set")
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}

	bad := Config{Version: "1.0.0"}
	if err := bad.Validate(); err == nil {
		t.Error("config with no sources should fail validation")
	}
}

func TestRegistryIntegration(t *testing.T) {
	cfg := DefaultConfig()
	analyzer := New(cfg, DefaultCaps(), nil)

	if analyzer.Name() != "taint" {
		t.Errorf("name: want taint, got %s", analyzer.Name())
	}
}
