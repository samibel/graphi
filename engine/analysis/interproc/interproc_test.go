package interproc

import (
	"context"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

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

// helper to create an edge (panics on error).
func mustEdge(t *testing.T, from, to model.NodeId, kind string) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, kind, model.TierDerived, 0.9, "test", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// 1. Simple call chain: A → B → C produces summaries for all three procedures.
func TestSimpleCallChainSummary(t *testing.T) {
	a := mustNode(t, "function", "pkg.A", "a.go", 1, 1)
	b := mustNode(t, "function", "pkg.B", "b.go", 1, 1)
	c := mustNode(t, "function", "pkg.C", "c.go", 1, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "calls")
	e2 := mustEdge(t, b.ID(), c.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{a, b, c},
		edges: []model.Edge{e1, e2},
	}

	analyzer := New(DefaultCaps(), 0)
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// All three procedures should have summaries.
	if len(result.Summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(result.Summaries))
	}

	// Verify each procedure has a summary.
	for _, nid := range []model.NodeId{a.ID(), b.ID(), c.ID()} {
		if _, ok := result.Summaries[string(nid)]; !ok {
			t.Errorf("missing summary for %s", nid)
		}
	}
}

// 2. Recursive procedure: A → A (self-recursive) triggers widening.
func TestRecursiveProcedureWidening(t *testing.T) {
	a := mustNode(t, "function", "pkg.Rec", "rec.go", 1, 1)
	e := mustEdge(t, a.ID(), a.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{a},
		edges: []model.Edge{e},
	}

	analyzer := New(DefaultCaps(), 2) // widening threshold = 2
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// Should produce a summary (possibly approximate via widening).
	s, ok := result.Summaries[string(a.ID())]
	if !ok {
		t.Fatal("missing summary for recursive procedure")
	}

	// After widening, the summary should exist and have output labels.
	if len(s.OutputLabels) == 0 {
		t.Error("expected non-empty output labels after fixpoint")
	}
}

// 3. SCC detection: A → B → A forms a 2-node SCC.
func TestSCCDetection(t *testing.T) {
	g := CallGraph{
		"A": {"B"},
		"B": {"A"},
	}
	sccs := TarjanSCC(g)

	// Should have exactly 1 SCC containing both A and B.
	found := false
	for _, scc := range sccs {
		if len(scc) == 2 {
			sorted := make([]string, len(scc))
			copy(sorted, scc)
			sort.Strings(sorted)
			if sorted[0] == "A" && sorted[1] == "B" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected SCC {A, B}, got %v", sccs)
	}
}

// 4. Cache hit/miss: running twice should produce cache hits on the second run.
func TestCacheHitMiss(t *testing.T) {
	a := mustNode(t, "function", "pkg.Leaf", "leaf.go", 1, 1)
	b := mustNode(t, "function", "pkg.Caller", "caller.go", 1, 1)
	e := mustEdge(t, b.ID(), a.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{a, b},
		edges: []model.Edge{e},
	}

	analyzer := New(DefaultCaps(), 0)

	// First run — should be all misses.
	result1, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	stats1 := result1.CacheStats
	if stats1.Misses == 0 {
		t.Error("expected cache misses on first run")
	}

	// Second run — should get cache hits.
	result2, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	stats2 := result2.CacheStats
	if stats2.Hits == 0 {
		t.Error("expected cache hits on second run")
	}
}

// 5. Cap enforcement: MaxProcedures=1 triggers a diagnostic.
func TestCapEnforcement(t *testing.T) {
	a := mustNode(t, "function", "pkg.A", "a.go", 1, 1)
	b := mustNode(t, "function", "pkg.B", "b.go", 1, 1)
	c := mustNode(t, "function", "pkg.C", "c.go", 1, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "calls")
	e2 := mustEdge(t, b.ID(), c.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{a, b, c},
		edges: []model.Edge{e1, e2},
	}

	caps := Caps{
		MaxProcedures:     1, // 3 procs exceed this
		MaxIterations:     50,
		MaxSCCSize:        100,
		MaxSummaryEntries: 10000,
		MaxTotalWork:      500000,
	}

	analyzer := New(caps, 0)
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Diagnostics) == 0 {
		t.Error("expected cap-exceeded diagnostic for MaxProcedures")
	}
}

// 6. Empty graph: returns empty result with no errors.
func TestEmptyGraph(t *testing.T) {
	reader := &testReader{}
	analyzer := New(DefaultCaps(), 0)

	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Summaries) != 0 {
		t.Errorf("expected 0 summaries on empty graph, got %d", len(result.Summaries))
	}
	if len(result.SCCs) != 0 {
		t.Errorf("expected 0 SCCs on empty graph, got %d", len(result.SCCs))
	}
}

// 7. Determinism: five identical runs produce identical results.
func TestDeterminism(t *testing.T) {
	a := mustNode(t, "function", "pkg.A", "a.go", 1, 1)
	b := mustNode(t, "function", "pkg.B", "b.go", 1, 1)
	c := mustNode(t, "function", "pkg.C", "c.go", 1, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "calls")
	e2 := mustEdge(t, b.ID(), c.ID(), "calls")
	e3 := mustEdge(t, a.ID(), c.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{a, b, c},
		edges: []model.Edge{e1, e2, e3},
	}

	var firstSummaryKeys []string
	for i := 0; i < 5; i++ {
		analyzer := New(DefaultCaps(), 0)
		result, err := analyzer.Run(context.Background(), reader)
		if err != nil {
			t.Fatal(err)
		}

		// Collect sorted summary keys and output labels.
		keys := make([]string, 0, len(result.Summaries))
		for k := range result.Summaries {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var sig []string
		for _, k := range keys {
			s := result.Summaries[k]
			sig = append(sig, s.ProcID+":"+s.InputHash)
		}

		if i == 0 {
			firstSummaryKeys = sig
			continue
		}
		if len(sig) != len(firstSummaryKeys) {
			t.Fatalf("run %d: summary count differs: %d vs %d", i, len(sig), len(firstSummaryKeys))
		}
		for j := range sig {
			if sig[j] != firstSummaryKeys[j] {
				t.Errorf("run %d: summary %d differs: %q vs %q", i, j, sig[j], firstSummaryKeys[j])
			}
		}
	}
}

// 8. Registry integration: Name() returns the expected analyzer name.
func TestRegistryIntegration(t *testing.T) {
	analyzer := New(DefaultCaps(), 0)
	if analyzer.Name() != "interproc" {
		t.Errorf("name: want interproc, got %s", analyzer.Name())
	}
}

// 9. Content-addressed key determinism: same inputs produce same hash.
func TestContentKeyDeterminism(t *testing.T) {
	k1 := ContentKey("proc-A", []string{"x", "y", "z"})
	k2 := ContentKey("proc-A", []string{"z", "y", "x"})
	if k1 != k2 {
		t.Errorf("expected identical content keys for same labels in different order: %s != %s", k1, k2)
	}

	k3 := ContentKey("proc-B", []string{"x", "y", "z"})
	if k1 == k3 {
		t.Error("expected different content keys for different proc IDs")
	}
}

// 10. Tarjan SCC on a DAG: each node is its own SCC.
func TestSCCOnDAG(t *testing.T) {
	g := CallGraph{
		"A": {"B"},
		"B": {"C"},
		"C": {},
	}
	sccs := TarjanSCC(g)

	// A DAG has no non-trivial SCCs; each node is a singleton SCC.
	for _, scc := range sccs {
		if len(scc) != 1 {
			t.Errorf("expected singleton SCCs for DAG, got SCC of size %d: %v", len(scc), scc)
		}
	}
	// Should have 3 SCCs total.
	if len(sccs) != 3 {
		t.Errorf("expected 3 SCCs for 3-node DAG, got %d", len(sccs))
	}
}

// 11. MaxSCCSize cap: SCC larger than cap triggers diagnostic.
func TestMaxSCCSizeCap(t *testing.T) {
	a := mustNode(t, "function", "pkg.A", "a.go", 1, 1)
	b := mustNode(t, "function", "pkg.B", "b.go", 1, 1)
	c := mustNode(t, "function", "pkg.C", "c.go", 1, 1)

	// Create a 3-node SCC: A→B→C→A.
	e1 := mustEdge(t, a.ID(), b.ID(), "calls")
	e2 := mustEdge(t, b.ID(), c.ID(), "calls")
	e3 := mustEdge(t, c.ID(), a.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{a, b, c},
		edges: []model.Edge{e1, e2, e3},
	}

	caps := Caps{
		MaxProcedures:     5000,
		MaxIterations:     50,
		MaxSCCSize:        1, // 3-node SCC exceeds this
		MaxSummaryEntries: 10000,
		MaxTotalWork:      500000,
	}

	analyzer := New(caps, 0)
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Diagnostics) == 0 {
		t.Error("expected cap-exceeded diagnostic for MaxSCCSize")
	}

	// All summaries from the oversized SCC should be marked approximate.
	for _, s := range result.Summaries {
		if !s.Approximate {
			t.Errorf("expected approximate summary for %s in oversized SCC", s.ProcID)
		}
	}
}

// 12. Summary cache eviction: MaxSummaryEntries triggers eviction.
func TestSummaryCacheEviction(t *testing.T) {
	cache := NewSummaryCache(2)
	s1 := Summary{ProcID: "A", InputHash: "h1"}
	s2 := Summary{ProcID: "B", InputHash: "h2"}
	s3 := Summary{ProcID: "C", InputHash: "h3"}

	cache.Put("k1", s1)
	cache.Put("k2", s2)
	cache.Put("k3", s3) // should evict k1

	if _, ok := cache.Get("k1"); ok {
		t.Error("expected k1 to be evicted")
	}
	if _, ok := cache.Get("k2"); !ok {
		t.Error("expected k2 to still be present")
	}
	if _, ok := cache.Get("k3"); !ok {
		t.Error("expected k3 to still be present")
	}
	stats := cache.Stats()
	if stats.Evictions != 1 {
		t.Errorf("expected 1 eviction, got %d", stats.Evictions)
	}
}
