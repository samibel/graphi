package brief

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
)

// countingReader instruments the query-plan shape agent_brief executes. The
// stable path must use one compact aggregate and must never fall back to the
// legacy Nodes/Edges catalog reads.
type countingReader struct {
	inner query.Reader

	nodeScans      int // Nodes() calls
	edgeScans      int // Edges() calls
	maxNodeRows    int
	maxEdgeRows    int
	sawSelective   bool
	aggregateCalls int
	aggregateRows  int
}

func (c *countingReader) GetNode(ctx context.Context, id model.NodeId) (model.Node, error) {
	return c.inner.GetNode(ctx, id)
}
func (c *countingReader) GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error) {
	return c.inner.GetEdge(ctx, id)
}
func (c *countingReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	c.nodeScans++
	if q != (graphstore.Query{}) {
		c.sawSelective = true
	}
	ns, err := c.inner.Nodes(ctx, q)
	if len(ns) > c.maxNodeRows {
		c.maxNodeRows = len(ns)
	}
	return ns, err
}
func (c *countingReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	c.edgeScans++
	if q != (graphstore.Query{}) {
		c.sawSelective = true
	}
	es, err := c.inner.Edges(ctx, q)
	if len(es) > c.maxEdgeRows {
		c.maxEdgeRows = len(es)
	}
	return es, err
}

func (c *countingReader) BriefStats(ctx context.Context, topSymbols int) (graphstore.BriefStats, error) {
	c.aggregateCalls++
	stats, err := c.inner.(graphstore.BriefAggregatePort).BriefStats(ctx, topSymbols)
	c.aggregateRows += len(stats.Files) + len(stats.TopInbound)
	return stats, err
}

// seedBriefGraph builds a small deterministic graph: a chain of functions with
// call edges between them, enough for agent_brief to digest.
func seedBriefGraph(t *testing.T) (*graphstore.MemStore, int, int) {
	t.Helper()
	ctx := context.Background()
	st := graphstore.NewMemStore()
	const n = 8
	nodes := make([]model.Node, n)
	for i := 0; i < n; i++ {
		nd, err := model.NewNode("function", "pkg.F"+string(rune('A'+i)), "pkg/f.go", i+1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := st.PutNode(ctx, nd); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
		nodes[i] = nd
	}
	edges := 0
	for i := 0; i+1 < n; i++ {
		e, err := model.NewEdge(nodes[i].ID(), nodes[i+1].ID(), "calls", model.TierDerived, 0.9, "call", []string{"x:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
		edges++
	}
	return st, n, edges
}

// TestSelectiveGate_Brief_UsesCompactAggregate flips the old full-scan
// characterization: the backend may scan internally to aggregate, but the
// engine receives only O(files + top symbols) rows and never materializes the
// entire graph through Nodes/Edges.
func TestSelectiveGate_Brief_UsesCompactAggregate(t *testing.T) {
	st, totalNodes, totalEdges := seedBriefGraph(t)

	cr := &countingReader{inner: st}
	deps := resolve.Deps{Query: query.New(cr)}

	if _, err := Assemble(context.Background(), Params{Topic: "", Deps: deps}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if cr.nodeScans != 0 || cr.edgeScans != 0 {
		t.Fatalf("SELECTIVE-GATE RED: brief issued %d Nodes()/%d Edges() catalog reads", cr.nodeScans, cr.edgeScans)
	}
	if cr.aggregateCalls != 1 {
		t.Fatalf("brief aggregate calls = %d, want exactly one", cr.aggregateCalls)
	}
	if cr.aggregateRows >= totalNodes+totalEdges {
		t.Fatalf("brief returned %d aggregate rows for %d nodes + %d edges; result is not compact", cr.aggregateRows, totalNodes, totalEdges)
	}
	t.Logf("agent_brief compact aggregate: %d returned rows for %d nodes + %d edges", cr.aggregateRows, totalNodes, totalEdges)
}
