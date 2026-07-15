package brief

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
)

// countingReader instruments the query-plan shape agent_brief executes: it
// records whether any read was selective (non-empty Query) and how many full
// node/edge scans it performs. It backs the SW-110 (TEST-01) AC5 full-scan
// baseline for the agent-brief graph read.
type countingReader struct {
	inner query.Reader

	nodeScans    int // Nodes() calls
	edgeScans    int // Edges() calls
	maxNodeRows  int
	maxEdgeRows  int
	sawSelective bool
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

// TestCharacterization_Brief_FullGraphScan is AC5 for the agent_brief full-graph
// read (engine/agenttools/brief): buildView loads the ENTIRE node set and the
// ENTIRE edge set with unfiltered Nodes(Query{}) / Edges(Query{}) (brief.go
// buildView) to compute file degrees and hotspots.
//
// DELIBERATELY NOT flipped by SW-116 (CORE-02): the digest is an AGGREGATE
// (file degrees, hotspots), not a traversal — replacing it with per-node port
// reads would be an N+1 regression. Per ADR 0003 D6/U2 the catalog read stays
// until the EVAL-01 repos measure whether SQL aggregates are needed. This pin
// keeps that decision visible: if a selective read lands in buildView, U2 was
// resolved and this baseline must be re-recorded with the chosen strategy.
func TestCharacterization_Brief_FullGraphScan(t *testing.T) {
	st, totalNodes, totalEdges := seedBriefGraph(t)

	cr := &countingReader{inner: st}
	deps := resolve.Deps{Query: query.New(cr)}

	if _, err := Assemble(context.Background(), Params{Topic: "", Deps: deps}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if cr.sawSelective {
		t.Fatalf("BASELINE DRIFT: agent_brief issued a selective (non-empty) query — a selective read landed; record the new baseline")
	}
	if cr.maxNodeRows < totalNodes {
		t.Fatalf("BASELINE DRIFT: brief's largest node scan was %d rows, expected a full scan of all %d nodes", cr.maxNodeRows, totalNodes)
	}
	if cr.maxEdgeRows < totalEdges {
		t.Fatalf("BASELINE DRIFT: brief's largest edge scan was %d rows, expected a full scan of all %d edges", cr.maxEdgeRows, totalEdges)
	}
	t.Logf("agent_brief baseline: full-graph digest — %d node scan(s) up to %d rows, %d edge scan(s) up to %d rows (no endpoint/symbol index)",
		cr.nodeScans, cr.maxNodeRows, cr.edgeScans, cr.maxEdgeRows)
}
