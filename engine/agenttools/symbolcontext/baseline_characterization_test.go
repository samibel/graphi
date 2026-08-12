package symbolcontext

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// countingReader instruments the query-plan shape symbol_context executes. The
// tool must stay on the selective ports (CORE-02): exact lookups, bounded
// incoming reads, batched hydration — never a Nodes()/Edges() catalog scan.
type countingReader struct {
	*graphstore.MemStore

	nodeScans    int
	edgeScans    int
	boundedReads int
	hydrations   int
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

func (c *countingReader) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	c.hydrations++
	return c.MemStore.NodesByID(ctx, ids)
}

// TestSelectiveGate_SymbolContext_NoCatalogScans pins the walk's read shape:
// zero catalog scans, bounded inbound reads only, one hydration batch per hop.
// Removing the bounds (e.g. switching the walk to Reader.Edges) turns this red.
func TestSelectiveGate_SymbolContext_NoCatalogScans(t *testing.T) {
	deps := fixtureDeps(t)
	mem, ok := deps.Query.Reader().(*graphstore.MemStore)
	if !ok {
		t.Fatalf("fixture reader is %T, want *MemStore", deps.Query.Reader())
	}
	cr := &countingReader{MemStore: mem}
	counted := resolve.Deps{Query: query.New(cr), Search: search.New(cr)}

	res, err := Context(context.Background(), Params{Ref: "util.Format", Deps: counted})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if res.Outcome == "error" || res.Outcome == "empty" {
		t.Fatalf("fixture symbol must resolve, got %s", res.Outcome)
	}

	if cr.nodeScans != 0 || cr.edgeScans != 0 {
		t.Fatalf("SELECTIVE-GATE RED: symbol_context issued %d Nodes()/%d Edges() catalog reads", cr.nodeScans, cr.edgeScans)
	}
	if cr.boundedReads == 0 {
		t.Fatal("expected the test walk to use IncomingBounded")
	}
	// Hydration batches scale with the FIXED number of service calls (resolve
	// + 8 relations + one per walk hop), never with the edge count. The pinned
	// ceiling is that structural count; a per-edge N+1 pattern would add one
	// call per inbound edge and blow past it on any non-trivial graph.
	const structuralHydrationCeiling = 12
	if cr.hydrations > structuralHydrationCeiling {
		t.Fatalf("expected structurally-bounded hydration (≤%d NodesByID calls), got %d", structuralHydrationCeiling, cr.hydrations)
	}
}
