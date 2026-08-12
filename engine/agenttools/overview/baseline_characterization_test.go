package overview

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// countingReader instruments the query-plan shape repo_overview executes. The
// DEFAULT call must stay on the compact aggregates (one BriefStats, one
// TrustStats, at most two selective lookups) and must never issue a
// Nodes()/Edges() catalog scan; only the documented Communities opt-in may.
type countingReader struct {
	*graphstore.MemStore

	nodeScans      int
	edgeScans      int
	aggregateCalls int
	lookups        int
}

func (c *countingReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	c.nodeScans++
	return c.MemStore.Nodes(ctx, q)
}

func (c *countingReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	c.edgeScans++
	return c.MemStore.Edges(ctx, q)
}

func (c *countingReader) BriefStats(ctx context.Context, topSymbols int) (graphstore.BriefStats, error) {
	c.aggregateCalls++
	return c.MemStore.BriefStats(ctx, topSymbols)
}

func (c *countingReader) TrustStats(ctx context.Context, topN int) (graphstore.TrustStats, error) {
	c.aggregateCalls++
	return c.MemStore.TrustStats(ctx, topN)
}

func (c *countingReader) QualifiedName(ctx context.Context, qn string) ([]model.Node, error) {
	c.lookups++
	return c.MemStore.QualifiedName(ctx, qn)
}

// TestSelectiveGate_RepoOverview_DefaultUsesAggregatesOnly pins the default
// call's read shape; switching any section to a catalog scan turns this red.
func TestSelectiveGate_RepoOverview_DefaultUsesAggregatesOnly(t *testing.T) {
	deps := fixtureDeps(t)
	mem, ok := deps.Query.Reader().(*graphstore.MemStore)
	if !ok {
		t.Fatalf("fixture reader is %T, want *MemStore", deps.Query.Reader())
	}
	cr := &countingReader{MemStore: mem}
	counted := resolve.Deps{Query: query.New(cr), Search: search.New(cr)}

	res, err := Assemble(context.Background(), Params{Deps: counted})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if res.Outcome == "error" || res.Outcome == "empty" {
		t.Fatalf("fixture graph must produce an overview, got %s", res.Outcome)
	}

	if cr.nodeScans != 0 || cr.edgeScans != 0 {
		t.Fatalf("SELECTIVE-GATE RED: default repo_overview issued %d Nodes()/%d Edges() catalog reads", cr.nodeScans, cr.edgeScans)
	}
	if cr.aggregateCalls != 2 {
		t.Fatalf("aggregate calls = %d, want exactly BriefStats + TrustStats", cr.aggregateCalls)
	}
	if cr.lookups > 2 {
		t.Fatalf("selective lookups = %d, want ≤2", cr.lookups)
	}

	// The opt-in community pass IS the documented full-graph read: exactly one
	// node scan and one edge scan, nothing more.
	cr.nodeScans, cr.edgeScans = 0, 0
	if _, err := Assemble(context.Background(), Params{Deps: counted, Communities: true}); err != nil {
		t.Fatalf("Assemble(communities): %v", err)
	}
	if cr.nodeScans != 1 || cr.edgeScans != 1 {
		t.Fatalf("community pass reads = %d nodes/%d edges scans, want exactly 1/1", cr.nodeScans, cr.edgeScans)
	}
}
