package analysis_test

import (
	"bytes"
	"context"
	"math"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// seedMetricsGraph builds two triangles joined at a single cut vertex C:
//
//	A-B, B-C, C-A   (triangle 1: A,B,C)
//	C-D, D-E, E-C   (triangle 2: C,D,E)
//
// C is the sole articulation point (removing C disconnects {A,B} from {D,E}).
// Degrees: C=4, others=2. Hub/centrality top = C; bridge = {C}.
func seedMetricsGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	names := []string{"A", "B", "C", "D", "E"}
	ids := make(map[string]model.NodeId, len(names))
	nodes := make(map[string]model.Node, len(names))
	for _, nm := range names {
		n, err := model.NewNode("function", "m."+nm, "m/"+nm+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", nm, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", nm, err)
		}
		ids[nm] = n.ID()
		nodes[nm] = n
	}
	mk := func(from, to string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), string(query.EdgeKindCalls), model.TierConfirmed, 0.9, from+"->"+to, []string{"m/" + from + ".go:1"})
		if err != nil {
			t.Fatalf("NewEdge(%s->%s): %v", from, to, err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge(%s->%s): %v", from, to, err)
		}
	}
	mk("A", "B")
	mk("B", "C")
	mk("C", "A")
	mk("C", "D")
	mk("D", "E")
	mk("E", "C")
	return store, ids
}

func metricsByKind(res analysis.Analysis, kind string) []analysis.NodeScore {
	var out []analysis.NodeScore
	for _, s := range res.Metrics {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func TestMetricsHubRanking(t *testing.T) {
	store, ids := seedMetricsGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "metrics", analysis.Params{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	hubs := metricsByKind(res, analysis.MetricHub)
	if len(hubs) == 0 {
		t.Fatal("no hub metrics emitted")
	}
	// C has degree 4 (top); others degree 2.
	if hubs[0].Node.ID != ids["C"] {
		t.Errorf("top hub = %s, want C", hubs[0].Node.QualifiedName)
	}
	if hubs[0].Score != 4 || hubs[0].EdgeCount != 4 {
		t.Errorf("top hub score/edgecount = %v/%d, want 4/4", hubs[0].Score, hubs[0].EdgeCount)
	}
	// Ranking is score DESC.
	for i := 1; i < len(hubs); i++ {
		if hubs[i].Score > hubs[i-1].Score {
			t.Errorf("hubs not ranked DESC at %d", i)
		}
	}
}

func TestMetricsBridgeArticulation(t *testing.T) {
	store, ids := seedMetricsGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "metrics", analysis.Params{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	bridges := metricsByKind(res, analysis.MetricBridge)
	// C is the sole articulation point.
	if len(bridges) != 1 {
		t.Fatalf("bridges = %d, want 1 (only C is a cut vertex): %+v", len(bridges), bridges)
	}
	if bridges[0].Node.ID != ids["C"] {
		t.Errorf("bridge = %s, want C", bridges[0].Node.QualifiedName)
	}
}

func TestMetricsCentrality(t *testing.T) {
	store, ids := seedMetricsGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "metrics", analysis.Params{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	cent := metricsByKind(res, analysis.MetricCentrality)
	if len(cent) == 0 {
		t.Fatal("no centrality metrics emitted")
	}
	if cent[0].Node.ID != ids["C"] {
		t.Errorf("top centrality = %s, want C", cent[0].Node.QualifiedName)
	}
	// C degree 4 / (5-1) = 1.0
	if math.Abs(cent[0].Score-1.0) > 1e-9 {
		t.Errorf("C centrality = %v, want 1.0", cent[0].Score)
	}
	// Top centrality node == top hub node (same degree ranking).
	hubs := metricsByKind(res, analysis.MetricHub)
	if hubs[0].Node.ID != cent[0].Node.ID {
		t.Error("top centrality node != top hub node")
	}
}

func TestMetricsCycleNoBridges(t *testing.T) {
	// A pure triangle (3-cycle) has no articulation points.
	ctx := context.Background()
	store := graphstore.NewMemStore()
	mk := func(name string) model.Node {
		n, _ := model.NewNode("function", "cyc."+name, "cyc/"+name+".go", 1, 1)
		_ = store.PutNode(ctx, n)
		return n
	}
	a, b, c := mk("A"), mk("B"), mk("C")
	mke := func(from, to model.Node) {
		e, _ := model.NewEdge(from.ID(), to.ID(), string(query.EdgeKindCalls), model.TierConfirmed, 0.9, "e", []string{"x"})
		_ = store.PutEdge(ctx, e)
	}
	mke(a, b)
	mke(b, c)
	mke(c, a)

	svc := analysis.NewDefaultService(store)
	res, err := svc.Dispatch(ctx, "metrics", analysis.Params{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if bridges := metricsByKind(res, analysis.MetricBridge); len(bridges) != 0 {
		t.Fatalf("cycle graph should have 0 bridges, got %d", len(bridges))
	}
}

func TestMetricsBoundTopN(t *testing.T) {
	store, _ := seedMetricsGraph(t)
	svc := analysis.NewDefaultService(store)

	// MaxNodes=1 -> exactly 1 hub, 1 centrality (top each); bridges unaffected (only 1).
	res, err := svc.Dispatch(context.Background(), "metrics", analysis.Params{MaxNodes: 1})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if h := metricsByKind(res, analysis.MetricHub); len(h) != 1 {
		t.Errorf("MaxNodes=1 -> hubs = %d, want 1", len(h))
	}
	if c := metricsByKind(res, analysis.MetricCentrality); len(c) != 1 {
		t.Errorf("MaxNodes=1 -> centrality = %d, want 1", len(c))
	}
}

func TestMetricsDeterminism(t *testing.T) {
	ctx := context.Background()
	s1, _ := seedMetricsGraph(t)
	svc1 := analysis.NewDefaultService(s1)
	first, err := svc1.Dispatch(ctx, "metrics", analysis.Params{})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	firstBytes, _ := analysis.Marshal(first)
	for i := 0; i < 30; i++ {
		res, err := svc1.Dispatch(ctx, "metrics", analysis.Params{})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b, _ := analysis.Marshal(res)
		if !bytes.Equal(firstBytes, b) {
			t.Fatalf("iteration %d non-byte-identical (determinism violated)", i)
		}
	}
	s2, _ := seedMetricsGraph(t)
	svc2 := analysis.NewDefaultService(s2)
	r2, err := svc2.Dispatch(ctx, "metrics", analysis.Params{})
	if err != nil {
		t.Fatalf("svc2: %v", err)
	}
	b2, _ := analysis.Marshal(r2)
	if !bytes.Equal(firstBytes, b2) {
		t.Fatal("two independent services produced non-identical metrics output")
	}
}

func TestMetricsRegistered(t *testing.T) {
	store, _ := seedMetricsGraph(t)
	svc := analysis.NewDefaultService(store)
	names := svc.Names()
	sort.Strings(names)
	for _, want := range []string{"impact", "call-chain", "metrics"} {
		if !containsAnalyzer(names, want) {
			t.Errorf("analyzer %q not registered; names = %v", want, names)
		}
	}
}
