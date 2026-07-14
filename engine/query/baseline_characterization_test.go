package query

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// countingReader wraps a MemStore and records the query-plan shape the
// structural service actually executes: legacy full listings (Edges/Nodes)
// versus selective port reads (Incoming/Outgoing/NodesByID), and how many rows
// each returns. It was the instrument behind the SW-110 (TEST-01) AC5
// rows-scanned baseline and is now the instrument behind the flipped SW-116
// (CORE-02) must-be-selective gates.
type countingReader struct {
	inner *graphstore.MemStore

	edgeCalls int // legacy full/kind listings via Edges()
	edgeRows  int
	nodeCalls int // legacy full listings via Nodes()
	nodeRows  int

	selEdgeCalls int // selective Incoming/Outgoing reads
	selEdgeRows  int // rows RETURNED by selective edge reads (== matched set)
	selNodeRows  int // rows returned by NodesByID
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
	es, err := c.inner.Edges(ctx, q)
	c.edgeRows += len(es)
	return es, err
}

// Selective port delegation (graphstore.GraphLookup), instrumented.
func (c *countingReader) Incoming(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	c.selEdgeCalls++
	es, err := c.inner.Incoming(ctx, id, kinds...)
	c.selEdgeRows += len(es)
	return es, err
}
func (c *countingReader) Outgoing(ctx context.Context, id model.NodeId, kinds ...model.EdgeKind) ([]model.Edge, error) {
	c.selEdgeCalls++
	es, err := c.inner.Outgoing(ctx, id, kinds...)
	c.selEdgeRows += len(es)
	return es, err
}
func (c *countingReader) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	ns, err := c.inner.NodesByID(ctx, ids)
	c.selNodeRows += len(ns)
	return ns, err
}

// seedCallsGraph builds a deterministic graph with fanCallers functions all
// calling one hub, plus decoyCalls unrelated call edges and one references edge.
// It returns the store and the hub's node id.
func seedCallsGraph(t *testing.T, fanCallers, decoyCalls int) (*graphstore.MemStore, model.NodeId) {
	t.Helper()
	ctx := context.Background()
	st := graphstore.NewMemStore()

	mkFunc := func(name string) model.Node {
		n, err := model.NewNode("function", "pkg."+name, "pkg/"+name+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
		return n
	}
	mkEdge := func(from, to model.NodeId, kind string) {
		e, err := model.NewEdge(from, to, kind, model.TierDerived, 0.9, "call", []string{"x:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}

	hub := mkFunc("Hub")
	for i := 0; i < fanCallers; i++ {
		caller := mkFunc(callerName(i))
		mkEdge(caller.ID(), hub.ID(), "calls")
	}
	// Decoy "calls" edges between unrelated nodes: they share the hub's edge KIND
	// but not its endpoint. The old full-kind scan had to walk them; the selective
	// read must never see them.
	for i := 0; i < decoyCalls; i++ {
		a := mkFunc(decoyName(i) + "A")
		b := mkFunc(decoyName(i) + "B")
		mkEdge(a.ID(), b.ID(), "calls")
	}
	// One references edge to prove the kind filter still matters.
	other := mkFunc("Ref")
	mkEdge(other.ID(), hub.ID(), "references")

	return st, hub.ID()
}

func callerName(i int) string { return "Caller" + itoa(i) }
func decoyName(i int) string  { return "Decoy" + itoa(i) }
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

// TestSelectiveGate_DirectedLookup_ScansOnlyIncidentEdges is the FLIPPED SW-110
// AC5 baseline (flipped by SW-116 / CORE-02, exactly as the old drift message
// instructed): callers/callees/references/definition no longer issue an
// Edges(Query{EdgeKind}) whole-class scan — they read ONLY the symbol's
// incident edges through graphstore.GraphLookup. The decoy edges of the same
// kind are never touched: rows scanned == matched set, not the edge class.
func TestSelectiveGate_DirectedLookup_ScansOnlyIncidentEdges(t *testing.T) {
	const fanCallers = 3
	const decoyCalls = 7
	st, hub := seedCallsGraph(t, fanCallers, decoyCalls)

	cr := &countingReader{inner: st}
	svc := New(cr)

	res, err := svc.Callers(context.Background(), hub)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}

	// Result is the matched set …
	if len(res.Nodes) != fanCallers {
		t.Fatalf("expected %d callers in the result, got %d", fanCallers, len(res.Nodes))
	}
	// … and the plan touched NOTHING else: no legacy listing, and the selective
	// reads returned exactly the matched edges (the decoys stayed unread).
	if cr.edgeCalls != 0 || cr.nodeCalls != 0 {
		t.Fatalf("SELECTIVE-GATE RED: callers issued %d Edges()/%d Nodes() legacy listings — a full scan crept back in (ADR 0003 D7)",
			cr.edgeCalls, cr.nodeCalls)
	}
	if cr.selEdgeRows != fanCallers {
		t.Fatalf("SELECTIVE-GATE RED: callers' selective reads returned %d edge rows, want exactly the %d matched (over-scan)",
			cr.selEdgeRows, fanCallers)
	}
	t.Logf("directedLookup(callers) selective plan: %d port read(s), %d edge rows for %d matches (1.0x scan ratio; old baseline scanned all %d calls edges)",
		cr.selEdgeCalls, cr.selEdgeRows, len(res.Nodes), fanCallers+decoyCalls)
}

// TestSelectiveGate_Neighborhood_ScansOnlyComponent is the FLIPPED SW-110 AC5
// neighborhood baseline: the traversal no longer loads the entire edge set —
// each hop reads only the frontier's incident edges, so the decoy component is
// never touched.
func TestSelectiveGate_Neighborhood_ScansOnlyComponent(t *testing.T) {
	const fanCallers = 2
	const decoyCalls = 5
	st, hub := seedCallsGraph(t, fanCallers, decoyCalls)
	// Depth 1 expands only the hub: its incident edges are the fanCallers calls
	// edges plus the one references edge.
	hubIncident := fanCallers + 1

	cr := &countingReader{inner: st}
	svc := New(cr)

	res, err := svc.Neighborhood(context.Background(), hub, 1)
	if err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	if len(res.Edges) != hubIncident {
		t.Fatalf("expected %d neighborhood edges, got %d", hubIncident, len(res.Edges))
	}
	if cr.edgeCalls != 0 || cr.nodeCalls != 0 {
		t.Fatalf("SELECTIVE-GATE RED: neighborhood issued %d Edges()/%d Nodes() legacy listings — the whole-graph read crept back in (ADR 0003 D7)",
			cr.edgeCalls, cr.nodeCalls)
	}
	if cr.selEdgeRows != hubIncident {
		t.Fatalf("SELECTIVE-GATE RED: neighborhood's selective reads returned %d edge rows, want exactly the hub's %d incident edges (decoys must stay unread)",
			cr.selEdgeRows, hubIncident)
	}
	t.Logf("neighborhood selective plan: %d port read(s), %d edge rows (old baseline loaded all %d edges)",
		cr.selEdgeCalls, cr.selEdgeRows, fanCallers+decoyCalls+1)
}
