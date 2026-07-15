package resolve

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// countingReader instruments the query-plan shape resolve executes: legacy
// full listings (Nodes/Edges) versus selective symbol lookups
// (QualifiedName/SourcePath/NodesByID), and how many rows each returns. It was
// the instrument behind the SW-110 (TEST-01) AC5 full-scan baseline and is now
// the instrument behind the flipped SW-116 (CORE-02) must-be-selective gate.
type countingReader struct {
	inner *graphstore.MemStore

	nodeCalls int // legacy full listings via Nodes()
	nodeRows  int
	edgeCalls int // legacy listings via Edges()

	selLookups  int // selective QualifiedName/SourcePath/NodesByID calls
	selNodeRows int // rows returned by those selective lookups
}

func (c *countingReader) GetNode(ctx context.Context, id model.NodeId) (model.Node, error) {
	return c.inner.GetNode(ctx, id)
}
func (c *countingReader) GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error) {
	return c.inner.GetEdge(ctx, id)
}
func (c *countingReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	c.nodeCalls++
	ns, err := c.inner.Nodes(ctx, q)
	c.nodeRows += len(ns)
	return ns, err
}
func (c *countingReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	c.edgeCalls++
	return c.inner.Edges(ctx, q)
}

// Selective port delegation (graphstore.GraphLookup + SymbolLookupPort),
// instrumented.
func (c *countingReader) Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return c.inner.Incoming(ctx, id, kinds...)
}
func (c *countingReader) Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	return c.inner.Outgoing(ctx, id, kinds...)
}
func (c *countingReader) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	c.selLookups++
	ns, err := c.inner.NodesByID(ctx, ids)
	c.selNodeRows += len(ns)
	return ns, err
}
func (c *countingReader) QualifiedName(ctx context.Context, qn string) ([]model.Node, error) {
	c.selLookups++
	ns, err := c.inner.QualifiedName(ctx, qn)
	c.selNodeRows += len(ns)
	return ns, err
}
func (c *countingReader) SourcePath(ctx context.Context, path string) ([]model.Node, error) {
	c.selLookups++
	ns, err := c.inner.SourcePath(ctx, path)
	c.selNodeRows += len(ns)
	return ns, err
}
func (c *countingReader) Search(ctx context.Context, text string, limit int) ([]graphstore.RankedNode, error) {
	return c.inner.Search(ctx, text, limit)
}

func seedNodes(t *testing.T, n int) (*graphstore.MemStore, string) {
	t.Helper()
	ctx := context.Background()
	st := graphstore.NewMemStore()
	var wantQN string
	for i := 0; i < n; i++ {
		name := "pkg.Sym" + string(rune('A'+i%26)) + itoa(i)
		nd, err := model.NewNode("function", name, "pkg/f.go", i+1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := st.PutNode(ctx, nd); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
		if i == n/2 {
			wantQN = name
		}
	}
	return st, wantQN
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestSelectiveGate_ResolveExact_NoFullNodeScan is the FLIPPED SW-110 AC5
// baseline (flipped by SW-116 / CORE-02, exactly as the old drift message
// instructed): resolving a qualified-name reference no longer loads the whole
// node set — it is served by the SymbolLookupPort exact-equality lookups, and
// the rows returned are exactly the matched set.
func TestSelectiveGate_ResolveExact_NoFullNodeScan(t *testing.T) {
	const total = 20
	st, wantQN := seedNodes(t, total)

	cr := &countingReader{inner: st}
	d := Deps{Query: query.New(cr)}

	res, err := Strict(context.Background(), d, wantQN)
	if err != nil {
		t.Fatalf("Strict: %v", err)
	}
	if !res.Resolved() || res.Method != MethodExactName {
		t.Fatalf("expected exact-name resolution, got resolved=%v method=%q", res.Resolved(), res.Method)
	}
	if cr.nodeCalls != 0 || cr.edgeCalls != 0 {
		t.Fatalf("SELECTIVE-GATE RED: resolve issued %d Nodes()/%d Edges() legacy listings — the full node scan crept back in (ADR 0003 D7)",
			cr.nodeCalls, cr.edgeCalls)
	}
	if cr.selLookups == 0 {
		t.Fatalf("SELECTIVE-GATE RED: resolve resolved without any selective lookup — where did the answer come from?")
	}
	if cr.selNodeRows != 1 {
		t.Fatalf("SELECTIVE-GATE RED: selective lookups returned %d node rows, want exactly the 1 match (over-scan)", cr.selNodeRows)
	}
	t.Logf("resolveExact selective plan: %d lookup(s), %d node row(s) for one exact name (old baseline scanned all %d nodes)",
		cr.selLookups, cr.selNodeRows, total)
}
