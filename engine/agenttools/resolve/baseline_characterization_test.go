package resolve

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// countingReader instruments the query-plan shape resolve executes: how many
// Nodes()/Edges() calls it issues, how many rows each scans, and whether any of
// them carried a non-empty (selective) query. It backs the SW-110 (TEST-01) AC5
// full-scan baseline for the agent-tool resolution seam.
type countingReader struct {
	inner query.Reader

	nodeCalls    int
	nodeRows     int
	sawSelective bool // any Nodes/Edges call with a non-empty Query
}

func (c *countingReader) GetNode(ctx context.Context, id model.NodeId) (model.Node, error) {
	return c.inner.GetNode(ctx, id)
}
func (c *countingReader) GetEdge(ctx context.Context, id model.EdgeId) (model.Edge, error) {
	return c.inner.GetEdge(ctx, id)
}
func (c *countingReader) Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error) {
	c.nodeCalls++
	if q != (graphstore.Query{}) {
		c.sawSelective = true
	}
	ns, err := c.inner.Nodes(ctx, q)
	c.nodeRows += len(ns)
	return ns, err
}
func (c *countingReader) Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error) {
	if q != (graphstore.Query{}) {
		c.sawSelective = true
	}
	return c.inner.Edges(ctx, q)
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

// TestCharacterization_ResolveExact_FullNodeScan is AC5 for the agent-tool
// resolution reads (engine/agenttools/resolve): resolving a qualified-name (or
// path) reference issues an UNFILTERED Nodes(Query{}) that loads EVERY node and
// filters in Go (resolve.go resolveExact), because graphstore.Query cannot filter
// by name/path. This baseline pins that whole-node-set scan as the measured
// "before" the selective-read work must improve.
func TestCharacterization_ResolveExact_FullNodeScan(t *testing.T) {
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
	if cr.sawSelective {
		t.Fatalf("BASELINE DRIFT: resolve issued a selective (non-empty) query — a selective read landed; record the new baseline")
	}
	if cr.nodeRows < total {
		t.Fatalf("BASELINE DRIFT: resolve scanned %d node rows, expected a full scan of all %d nodes", cr.nodeRows, total)
	}
	t.Logf("resolveExact baseline: full node-set scan of %d rows across %d Nodes() call(s) to resolve one exact name",
		cr.nodeRows, cr.nodeCalls)
}
