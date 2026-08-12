package taskctx

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// countingReader instruments the query-plan shape task_context executes:
// selective seed resolution, one bounded in+out read per seed, one batched
// hydration, one compact BriefStats aggregate — never a Nodes()/Edges()
// catalog scan (CORE-02).
type countingReader struct {
	*graphstore.MemStore

	nodeScans      int
	edgeScans      int
	boundedReads   int
	hydrations     int
	aggregateCalls int
}

func (c *countingReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	c.nodeScans++
	return c.MemStore.Nodes(ctx, q)
}

func (c *countingReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	c.edgeScans++
	return c.MemStore.Edges(ctx, q)
}

func (c *countingReader) IncomingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	c.boundedReads++
	return c.MemStore.IncomingBounded(ctx, id, limit, kinds...)
}

func (c *countingReader) OutgoingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	c.boundedReads++
	return c.MemStore.OutgoingBounded(ctx, id, limit, kinds...)
}

func (c *countingReader) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	c.hydrations++
	return c.MemStore.NodesByID(ctx, ids)
}

func (c *countingReader) BriefStats(ctx context.Context, topSymbols int) (graphstore.BriefStats, error) {
	c.aggregateCalls++
	return c.MemStore.BriefStats(ctx, topSymbols)
}

// TestSelectiveGate_TaskContext_NoCatalogScans pins the read shape. Reads
// scale with the seed count (2 bounded reads per seed), hydration is batched
// (resolve + one neighbor batch), and the config section uses exactly one
// compact aggregate. Switching any stage to a catalog scan turns this red.
func TestSelectiveGate_TaskContext_NoCatalogScans(t *testing.T) {
	deps := fixtureDeps(t)
	mem, ok := deps.Query.Reader().(*graphstore.MemStore)
	if !ok {
		t.Fatalf("fixture reader is %T, want *MemStore", deps.Query.Reader())
	}
	cr := &countingReader{MemStore: mem}
	counted := resolve.Deps{Query: query.New(cr), Search: search.New(cr)}

	res, err := Assemble(context.Background(), Params{Task: "util.Format", Deps: counted})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if res.Outcome == "error" || res.Outcome == "empty" {
		t.Fatalf("fixture task must resolve, got %s", res.Outcome)
	}

	if cr.nodeScans != 0 || cr.edgeScans != 0 {
		t.Fatalf("SELECTIVE-GATE RED: task_context issued %d Nodes()/%d Edges() catalog reads", cr.nodeScans, cr.edgeScans)
	}
	if cr.boundedReads == 0 || cr.boundedReads > 2*seedLimit {
		t.Fatalf("bounded reads = %d, want 1..%d (2 per seed)", cr.boundedReads, 2*seedLimit)
	}
	if cr.aggregateCalls != 1 {
		t.Fatalf("aggregate calls = %d, want exactly one BriefStats", cr.aggregateCalls)
	}
	if cr.hydrations > 3 {
		t.Fatalf("expected batched hydration (≤3 NodesByID calls), got %d", cr.hydrations)
	}
}
