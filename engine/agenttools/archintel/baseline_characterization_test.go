package archintel

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// countingReader pins the architecture tools' read shape: detection needs the
// whole graph by definition, so each call is the documented full-graph pass —
// exactly ONE Nodes() and ONE Edges() catalog read, and nothing selective
// beyond that.
type countingReader struct {
	*graphstore.MemStore

	nodeScans int
	edgeScans int
	byID      int
}

func (c *countingReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	c.nodeScans++
	return c.MemStore.Nodes(ctx, q)
}

func (c *countingReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	c.edgeScans++
	return c.MemStore.Edges(ctx, q)
}

func (c *countingReader) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	c.byID++
	return c.MemStore.NodesByID(ctx, ids)
}

func TestSelectiveGate_Architecture_OneCatalogPass(t *testing.T) {
	deps := layeredDeps(t)
	mem, ok := deps.Query.Reader().(*graphstore.MemStore)
	if !ok {
		t.Fatalf("fixture reader is %T, want *MemStore", deps.Query.Reader())
	}
	cr := &countingReader{MemStore: mem}
	counted := resolve.Deps{Query: query.New(cr), Search: search.New(cr)}

	if _, err := Assemble(context.Background(), Params{Deps: counted}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cr.nodeScans != 1 || cr.edgeScans != 1 || cr.byID != 0 {
		t.Fatalf("architecture reads = %d nodes/%d edges scans, %d NodesByID — want exactly 1/1/0", cr.nodeScans, cr.edgeScans, cr.byID)
	}

	cr.nodeScans, cr.edgeScans, cr.byID = 0, 0, 0
	if _, err := Violations(context.Background(), ViolationsParams{Deps: counted}); err != nil {
		t.Fatalf("Violations: %v", err)
	}
	if cr.nodeScans != 1 || cr.edgeScans != 1 || cr.byID != 0 {
		t.Fatalf("architecture_violations reads = %d nodes/%d edges scans, %d NodesByID — want exactly 1/1/0", cr.nodeScans, cr.edgeScans, cr.byID)
	}
}
