package pdg

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

// mustNode creates a node (panics on error for test brevity).
func mustNode(t *testing.T, kind, name, path string, line, col int) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, name, path, line, col)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// mustEdge creates a def-use edge (panics on error).
func mustEdge(t *testing.T, from, to model.NodeId, kind string) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, kind,
		model.TierDerived, 0.9, "pdg test", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestSimpleDataDep verifies that a simple definition→use chain produces a
// data_dep edge.
func TestSimpleDataDep(t *testing.T) {
	// A defines → B references → C
	a := mustNode(t, "variable", "x_def", "main.go", 1, 1)
	b := mustNode(t, "variable", "x_use", "main.go", 2, 1)
	c := mustNode(t, "variable", "y_use", "main.go", 3, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "defines")
	e2 := mustEdge(t, b.ID(), c.ID(), "references")

	reader := &testReader{
		nodes: []model.Node{a, b, c},
		edges: []model.Edge{e1, e2},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.DataDepEdges) == 0 {
		t.Fatal("expected at least one data_dep edge, got none")
	}

	// There should be a data_dep edge from a→b (a's definition reaches b).
	found := false
	for _, edge := range result.DataDepEdges {
		if edge.From == a.ID() && edge.To == b.ID() && edge.Kind == EdgeKindDataDep {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected data_dep edge from %s to %s", a.ID(), b.ID())
	}

	// Verify all edges have the correct kind and derivation rule.
	for _, edge := range result.DataDepEdges {
		if edge.Kind != EdgeKindDataDep {
			t.Errorf("data dep edge has wrong kind: %s", edge.Kind)
		}
		if edge.DerivationRule != "reaching_definitions" {
			t.Errorf("data dep edge has wrong derivation rule: %s", edge.DerivationRule)
		}
	}
}

// TestControlDep verifies that control-dependence edges are computed from
// a branching CFG structure.
func TestControlDep(t *testing.T) {
	// Predicate → thenBranch (direct edge)
	// Predicate → elseBranch (direct edge)
	// thenBranch → merge
	// elseBranch → merge
	pred := mustNode(t, "predicate", "if_cond", "main.go", 1, 1)
	thenB := mustNode(t, "statement", "then_stmt", "main.go", 2, 1)
	elseB := mustNode(t, "statement", "else_stmt", "main.go", 3, 1)
	merge := mustNode(t, "statement", "merge_stmt", "main.go", 4, 1)

	e1 := mustEdge(t, pred.ID(), thenB.ID(), "references")
	e2 := mustEdge(t, pred.ID(), elseB.ID(), "references")
	e3 := mustEdge(t, thenB.ID(), merge.ID(), "defines")
	e4 := mustEdge(t, elseB.ID(), merge.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{pred, thenB, elseB, merge},
		edges: []model.Edge{e1, e2, e3, e4},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.ControlDepEdges) == 0 {
		t.Fatal("expected at least one control_dep edge, got none")
	}

	// Verify all control-dep edges have correct kind and derivation rule.
	for _, edge := range result.ControlDepEdges {
		if edge.Kind != EdgeKindControlDep {
			t.Errorf("control dep edge has wrong kind: %s", edge.Kind)
		}
		if edge.DerivationRule != "post_dominance_frontier" {
			t.Errorf("control dep edge has wrong derivation rule: %s", edge.DerivationRule)
		}
	}
}

// TestCycleHandling verifies that the analyzer terminates on a cyclic graph
// (loop) without infinite looping.
func TestCycleHandling(t *testing.T) {
	// a → b → c → a (cycle)
	a := mustNode(t, "statement", "loop_start", "main.go", 1, 1)
	b := mustNode(t, "statement", "loop_body", "main.go", 2, 1)
	c := mustNode(t, "statement", "loop_back", "main.go", 3, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "defines")
	e2 := mustEdge(t, b.ID(), c.ID(), "defines")
	e3 := mustEdge(t, c.ID(), a.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{a, b, c},
		edges: []model.Edge{e1, e2, e3},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// The analyzer must terminate. We don't assert exact edges here —
	// the key property is termination + non-nil result.
	if result.DataDepEdges == nil {
		t.Error("expected non-nil DataDepEdges slice")
	}
	if result.ControlDepEdges == nil {
		t.Error("expected non-nil ControlDepEdges slice")
	}
	if len(result.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(result.Nodes))
	}
}

// TestEmptyGraph verifies the analyzer returns an empty result on an empty graph.
func TestEmptyGraph(t *testing.T) {
	reader := &testReader{}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.DataDepEdges) != 0 {
		t.Errorf("expected 0 data dep edges on empty graph, got %d", len(result.DataDepEdges))
	}
	if len(result.ControlDepEdges) != 0 {
		t.Errorf("expected 0 control dep edges on empty graph, got %d", len(result.ControlDepEdges))
	}
	if len(result.Nodes) != 0 {
		t.Errorf("expected 0 nodes on empty graph, got %d", len(result.Nodes))
	}
}

// TestDeterminism runs the same analysis multiple times and verifies identical
// results across runs.
func TestDeterminism(t *testing.T) {
	a := mustNode(t, "variable", "x_def", "main.go", 1, 1)
	b := mustNode(t, "variable", "x_use", "main.go", 2, 1)
	c := mustNode(t, "variable", "y_use", "main.go", 3, 1)
	d := mustNode(t, "variable", "z_use", "main.go", 4, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "defines")
	e2 := mustEdge(t, a.ID(), c.ID(), "defines")
	e3 := mustEdge(t, b.ID(), d.ID(), "references")
	e4 := mustEdge(t, c.ID(), d.ID(), "references")

	reader := &testReader{
		nodes: []model.Node{a, b, c, d},
		edges: []model.Edge{e1, e2, e3, e4},
	}

	analyzer := New(DefaultConfig())

	var firstResult PDGResult
	for i := 0; i < 5; i++ {
		result, err := analyzer.Run(context.Background(), reader)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstResult = result
			continue
		}

		// Data dep edges must be identical.
		if len(result.DataDepEdges) != len(firstResult.DataDepEdges) {
			t.Fatalf("run %d: data dep count differs: %d vs %d",
				i, len(result.DataDepEdges), len(firstResult.DataDepEdges))
		}
		for j := range result.DataDepEdges {
			if result.DataDepEdges[j] != firstResult.DataDepEdges[j] {
				t.Errorf("run %d data dep %d differs: %+v vs %+v",
					i, j, result.DataDepEdges[j], firstResult.DataDepEdges[j])
			}
		}

		// Control dep edges must be identical.
		if len(result.ControlDepEdges) != len(firstResult.ControlDepEdges) {
			t.Fatalf("run %d: control dep count differs: %d vs %d",
				i, len(result.ControlDepEdges), len(firstResult.ControlDepEdges))
		}
		for j := range result.ControlDepEdges {
			if result.ControlDepEdges[j] != firstResult.ControlDepEdges[j] {
				t.Errorf("run %d control dep %d differs: %+v vs %+v",
					i, j, result.ControlDepEdges[j], firstResult.ControlDepEdges[j])
			}
		}

		// Node list must be identical.
		if len(result.Nodes) != len(firstResult.Nodes) {
			t.Fatalf("run %d: node count differs: %d vs %d",
				i, len(result.Nodes), len(firstResult.Nodes))
		}
		for j := range result.Nodes {
			if result.Nodes[j].ID != firstResult.Nodes[j].ID {
				t.Errorf("run %d node %d differs: %s vs %s",
					i, j, result.Nodes[j].ID, firstResult.Nodes[j].ID)
			}
		}
	}
}

// TestDiamondCFG verifies correct analysis of a diamond-shaped CFG:
//
//	    entry
//	   /     \
//	left    right
//	   \     /
//	    merge
func TestDiamondCFG(t *testing.T) {
	entry := mustNode(t, "predicate", "cond", "main.go", 1, 1)
	left := mustNode(t, "statement", "left_branch", "main.go", 2, 1)
	right := mustNode(t, "statement", "right_branch", "main.go", 3, 1)
	merge := mustNode(t, "statement", "merge_point", "main.go", 4, 1)

	e1 := mustEdge(t, entry.ID(), left.ID(), "references")
	e2 := mustEdge(t, entry.ID(), right.ID(), "references")
	e3 := mustEdge(t, left.ID(), merge.ID(), "defines")
	e4 := mustEdge(t, right.ID(), merge.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{entry, left, right, merge},
		edges: []model.Edge{e1, e2, e3, e4},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// entry's definition should reach left, right, and merge via data dep.
	if len(result.DataDepEdges) == 0 {
		t.Fatal("expected data dep edges in diamond CFG")
	}

	// merge should have data deps from both left and right.
	leftToMerge := false
	rightToMerge := false
	for _, edge := range result.DataDepEdges {
		if edge.To == merge.ID() {
			if edge.From == left.ID() {
				leftToMerge = true
			}
			if edge.From == right.ID() {
				rightToMerge = true
			}
		}
	}
	if !leftToMerge {
		t.Error("expected data_dep edge from left to merge")
	}
	if !rightToMerge {
		t.Error("expected data_dep edge from right to merge")
	}

	// Control-dep: thenBranch and elseBranch should be control-dependent on entry.
	if len(result.ControlDepEdges) == 0 {
		t.Fatal("expected control dep edges in diamond CFG")
	}
}

// TestUnreachableNodes verifies that nodes with no edges are included in the
// result node list but produce no dependence edges.
func TestUnreachableNodes(t *testing.T) {
	a := mustNode(t, "variable", "connected_def", "main.go", 1, 1)
	b := mustNode(t, "variable", "connected_use", "main.go", 2, 1)
	island := mustNode(t, "variable", "unreachable_island", "main.go", 10, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{a, b, island},
		edges: []model.Edge{e1},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// All 3 nodes should be in the result.
	if len(result.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(result.Nodes))
	}

	// The island node should not appear in any data-dep edge.
	for _, edge := range result.DataDepEdges {
		if edge.From == island.ID() || edge.To == island.ID() {
			t.Errorf("unreachable node %s should not appear in data dep edges", island.ID())
		}
	}
}

// TestRegistryIntegration verifies the analyzer's Name() matches the expected
// dispatch key and that it can be constructed with default config.
func TestRegistryIntegration(t *testing.T) {
	cfg := DefaultConfig()
	analyzer := New(cfg)

	if analyzer.Name() != "pdg" {
		t.Errorf("name: want pdg, got %s", analyzer.Name())
	}

	// Verify default config is reasonable.
	if cfg.MaxNodes <= 0 {
		t.Error("default MaxNodes should be positive")
	}
	if cfg.MaxWork <= 0 {
		t.Error("default MaxWork should be positive")
	}
	if cfg.MaxDepth <= 0 {
		t.Error("default MaxDepth should be positive")
	}
	if len(cfg.EdgeKinds) == 0 {
		t.Error("default EdgeKinds should be non-empty")
	}
}

// TestSingleNode verifies that a graph with a single node produces no
// dependence edges.
func TestSingleNode(t *testing.T) {
	a := mustNode(t, "variable", "lonely", "main.go", 1, 1)

	reader := &testReader{
		nodes: []model.Node{a},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.DataDepEdges) != 0 {
		t.Errorf("expected 0 data dep edges for single node, got %d", len(result.DataDepEdges))
	}
	if len(result.ControlDepEdges) != 0 {
		t.Errorf("expected 0 control dep edges for single node, got %d", len(result.ControlDepEdges))
	}
	if len(result.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(result.Nodes))
	}
}

// TestLinearChainTransitivity verifies that reaching-definitions computes
// transitive data dependencies through a linear chain: a→b→c→d means a's
// definition reaches d.
func TestLinearChainTransitivity(t *testing.T) {
	a := mustNode(t, "variable", "chain_a", "main.go", 1, 1)
	b := mustNode(t, "variable", "chain_b", "main.go", 2, 1)
	c := mustNode(t, "variable", "chain_c", "main.go", 3, 1)
	d := mustNode(t, "variable", "chain_d", "main.go", 4, 1)

	e1 := mustEdge(t, a.ID(), b.ID(), "defines")
	e2 := mustEdge(t, b.ID(), c.ID(), "defines")
	e3 := mustEdge(t, c.ID(), d.ID(), "defines")

	reader := &testReader{
		nodes: []model.Node{a, b, c, d},
		edges: []model.Edge{e1, e2, e3},
	}

	analyzer := New(DefaultConfig())
	result, err := analyzer.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	// a's definition should reach d transitively.
	foundAtoD := false
	for _, edge := range result.DataDepEdges {
		if edge.From == a.ID() && edge.To == d.ID() {
			foundAtoD = true
			break
		}
	}
	if !foundAtoD {
		t.Error("expected transitive data_dep edge from a to d in linear chain")
	}
}
