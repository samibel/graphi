package query

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// countingReader wraps a Reader and records the query-plan shape the structural
// service actually executes: how many times it hits Edges/Nodes and how many
// ROWS each of those calls scans. It is the instrument behind the SW-110
// (TEST-01) AC5 rows-scanned baseline.
type countingReader struct {
	inner Reader

	edgeCalls int
	edgeRows  int // total edge rows returned across all Edges() calls
	nodeCalls int
	nodeRows  int
	lastEdgeQ graphstore.Query
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
	c.lastEdgeQ = q
	es, err := c.inner.Edges(ctx, q)
	c.edgeRows += len(es)
	return es, err
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
	// but not its endpoint, so a selective (endpoint-indexed) read would skip them
	// while today's full-kind scan must still walk them.
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

// TestCharacterization_DirectedLookup_ScansAllEdgesOfKind is AC5: it pins the
// current read HOTPATH plan of callers/callees/references/definition. Because
// graphstore.Query has no endpoint filter, Service.directedLookup issues a single
// Edges(Query{EdgeKind}) that returns EVERY edge of that kind and filters to the
// symbol's endpoint in Go (service.go directedLookup). This baseline records that
// the rows SCANNED equals the total "calls" edges in the store — NOT the far
// smaller matched set — so SP-11/CORE-02's selective-read work has a measured
// "before" number to beat.
func TestCharacterization_DirectedLookup_ScansAllEdgesOfKind(t *testing.T) {
	const fanCallers = 3
	const decoyCalls = 7
	st, hub := seedCallsGraph(t, fanCallers, decoyCalls)
	totalCallsEdges := fanCallers + decoyCalls // every "calls" edge in the store

	cr := &countingReader{inner: st}
	svc := New(cr)

	res, err := svc.Callers(context.Background(), hub)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}

	// Result is the SMALL matched set …
	if len(res.Nodes) != fanCallers {
		t.Fatalf("expected %d callers in the result, got %d", fanCallers, len(res.Nodes))
	}
	// … but the plan SCANNED every "calls" edge (the characterized inefficiency).
	if cr.lastEdgeQ.EdgeKind != "calls" {
		t.Fatalf("expected an Edges(Query{EdgeKind:calls}) scan, got EdgeKind=%q", cr.lastEdgeQ.EdgeKind)
	}
	if cr.edgeRows != totalCallsEdges {
		t.Fatalf("BASELINE DRIFT: directedLookup scanned %d edge rows, expected the full %d calls edges "+
			"(loads all edges of a kind, filters in Go). If this dropped, a selective read landed — record the new baseline.",
			cr.edgeRows, totalCallsEdges)
	}
	// Baseline ratio, recorded for the SP-11 "before": rows scanned per matched row.
	t.Logf("directedLookup(callers) baseline: scanned %d calls edges to return %d matches (%.1fx over-scan)",
		cr.edgeRows, len(res.Nodes), float64(cr.edgeRows)/float64(len(res.Nodes)))
}

// TestCharacterization_Neighborhood_ScansAllEdges is AC5 for the neighborhood
// hotpath: it loads the ENTIRE edge set once (Edges(Query{}), no kind or endpoint
// filter) to build undirected adjacency. This pins that whole-graph read.
func TestCharacterization_Neighborhood_ScansAllEdges(t *testing.T) {
	const fanCallers = 2
	const decoyCalls = 5
	st, hub := seedCallsGraph(t, fanCallers, decoyCalls)
	totalEdges := fanCallers + decoyCalls + 1 // calls edges + the one references edge

	cr := &countingReader{inner: st}
	svc := New(cr)

	if _, err := svc.Neighborhood(context.Background(), hub, 1); err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	if cr.lastEdgeQ.EdgeKind != "" {
		t.Fatalf("expected an unfiltered Edges(Query{}) full scan, got EdgeKind=%q", cr.lastEdgeQ.EdgeKind)
	}
	if cr.edgeRows != totalEdges {
		t.Fatalf("BASELINE DRIFT: neighborhood scanned %d edge rows, expected the full %d edges", cr.edgeRows, totalEdges)
	}
	t.Logf("neighborhood baseline: loads the full edge set of %d rows (no endpoint index)", cr.edgeRows)
}
