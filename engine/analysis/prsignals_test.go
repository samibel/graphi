package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/analysis/pdg"
	"github.com/samibel/graphi/engine/query"
)

// --- test fixtures -----------------------------------------------------------

// mkEdge builds an edge and stores it.
func mkEdge(t *testing.T, store *graphstore.MemStore, from, to model.NodeId, kind string) {
	t.Helper()
	e, err := model.NewEdge(from, to, kind, model.TierConfirmed, 1.0, "test", []string{"test-ref"})
	if err != nil {
		t.Fatalf("NewEdge(%s->%s): %v", from, to, err)
	}
	if err := store.PutEdge(context.Background(), e); err != nil {
		t.Fatalf("PutEdge(%s->%s): %v", from, to, err)
	}
}

// stubSignalSource feeds FIXED metric / PDG / churn signals to the detector so
// each signal is exercised in isolation without re-running EP-004/EP-005.
type stubSignalSource struct {
	metrics []NodeScore
	pdgRes  pdg.PDGResult
	churn   []githistory.ChurnScore
}

func (s *stubSignalSource) Metrics(_ context.Context, _ query.Reader) ([]NodeScore, error) {
	return s.metrics, nil
}
func (s *stubSignalSource) PDG(_ context.Context, _ query.Reader) (pdg.PDGResult, error) {
	return s.pdgRes, nil
}
func (s *stubSignalSource) Churn(_ context.Context, _ query.Reader) ([]githistory.ChurnScore, error) {
	return s.churn, nil
}

// signalsFor returns the SignalRecord for a region id (or fails the test).
func signalsFor(t *testing.T, rep SignalReport, id model.NodeId) SignalRecord {
	t.Helper()
	for _, rec := range rep.Regions {
		if rec.Region == id {
			return rec
		}
	}
	t.Fatalf("no signal record for region %s", id)
	return SignalRecord{}
}

// hasSignal reports whether a record carries a signal of the given kind, and
// returns the first matching flag.
func hasSignal(rec SignalRecord, kind string) (SignalFlag, bool) {
	for _, f := range rec.Signals {
		if f.Kind == kind {
			return f, true
		}
	}
	return SignalFlag{}, false
}

// --- AC1: hub classification over a configurable threshold -------------------

func TestPrSignalsHubOverThreshold(t *testing.T) {
	store := graphstore.NewMemStore()
	idHub := mkNode(t, store, "function", "pkg.Hub", "pkg/hub.go", 10)
	idLeaf := mkNode(t, store, "function", "pkg.Leaf", "pkg/leaf.go", 10)

	// Fixed metrics: Hub has degree 5 (high fan-in/out), Leaf has degree 1.
	src := &stubSignalSource{
		metrics: []NodeScore{
			{Node: query.ResultNode{ID: idHub}, Kind: MetricHub, Score: 5},
			{Node: query.ResultNode{ID: idLeaf}, Kind: MetricHub, Score: 1},
		},
	}
	// Threshold 3: Hub (5) classifies, Leaf (1) does not.
	a := prSignalsAnalyzer{source: src, config: signalConfig{HubDegreeThreshold: 3, SurpriseChurnMax: 1}}

	diff := "pkg/hub.go:Hub\npkg/leaf.go:Leaf"
	res, err := a.Analyze(context.Background(), store, Params{Diff: diff})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.SignalReport

	if _, ok := hasSignal(signalsFor(t, rep, idHub), SignalHub); !ok {
		t.Fatalf("expected Hub node to be classified hub")
	}
	if _, ok := hasSignal(signalsFor(t, rep, idLeaf), SignalHub); ok {
		t.Fatalf("expected Leaf node NOT to be classified hub")
	}
}

func TestPrSignalsHubThresholdIsConfigurable(t *testing.T) {
	store := graphstore.NewMemStore()
	idHub := mkNode(t, store, "function", "pkg.Hub", "pkg/hub.go", 10)
	src := &stubSignalSource{
		metrics: []NodeScore{{Node: query.ResultNode{ID: idHub}, Kind: MetricHub, Score: 5}},
	}
	// Raise the threshold ABOVE the node's degree → it must NOT be a hub. This
	// proves the threshold is genuinely configurable, not hard-coded.
	a := prSignalsAnalyzer{source: src, config: signalConfig{HubDegreeThreshold: 6, SurpriseChurnMax: 1}}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/hub.go:Hub"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, ok := hasSignal(signalsFor(t, *res.SignalReport, idHub), SignalHub); ok {
		t.Fatalf("expected NO hub at threshold 6 (degree 5); threshold not honored")
	}
}

// --- AC2: bridge detection on a disconnecting fixture (real metrics) ---------

// TestPrSignalsBridgeRealMetrics builds a graph with a single articulation point
// B joining two modules, runs the detector against the REAL metrics analyzer
// (no stub), and asserts B is annotated bridge. It also independently verifies
// the cut-vertex property: removing B disconnects the two modules.
func TestPrSignalsBridgeRealMetrics(t *testing.T) {
	store := graphstore.NewMemStore()
	// Module 1: A1-A2-A3 triangle. Module 2: C1-C2-C3 triangle. Bridge node B
	// is the ONLY connection between the two modules (A1-B and B-C1).
	a1 := mkNode(t, store, "function", "m1.A1", "m1/a.go", 1)
	a2 := mkNode(t, store, "function", "m1.A2", "m1/a.go", 2)
	a3 := mkNode(t, store, "function", "m1.A3", "m1/a.go", 3)
	b := mkNode(t, store, "function", "br.B", "br/b.go", 1)
	c1 := mkNode(t, store, "function", "m2.C1", "m2/c.go", 1)
	c2 := mkNode(t, store, "function", "m2.C2", "m2/c.go", 2)
	c3 := mkNode(t, store, "function", "m2.C3", "m2/c.go", 3)

	mkEdge(t, store, a1, a2, "calls")
	mkEdge(t, store, a2, a3, "calls")
	mkEdge(t, store, a3, a1, "calls")
	mkEdge(t, store, c1, c2, "calls")
	mkEdge(t, store, c2, c3, "calls")
	mkEdge(t, store, c3, c1, "calls")
	mkEdge(t, store, a1, b, "calls")
	mkEdge(t, store, b, c1, "calls")

	// Real default source (real metrics analyzer); no churn provider.
	a := newPrSignalsAnalyzer()

	res, err := a.Analyze(context.Background(), store, Params{Diff: "br/b.go:B"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, ok := hasSignal(signalsFor(t, *res.SignalReport, b), SignalBridge); !ok {
		t.Fatalf("expected node B to be annotated bridge")
	}

	// A non-bridge node (A2, inside a triangle) must NOT be a bridge.
	res2, err := a.Analyze(context.Background(), store, Params{Diff: "m1/a.go:A2"})
	if err != nil {
		t.Fatalf("Analyze A2: %v", err)
	}
	if _, ok := hasSignal(signalsFor(t, *res2.SignalReport, a2), SignalBridge); ok {
		t.Fatalf("expected A2 (inside a triangle) NOT to be a bridge")
	}

	// Independently verify the cut-vertex property: removing B disconnects the
	// two modules (no path from A1 to C1 without B).
	if connectedWithout(store, a1, c1, b) {
		t.Fatalf("fixture invalid: A1 and C1 still connected after removing B; B is not a true cut vertex")
	}
	if !connectedWithout(store, a1, a3, b) {
		t.Fatalf("fixture invalid: A1 and A3 should remain connected within module 1 after removing B")
	}
}

// connectedWithout reports whether src can reach dst over the undirected graph
// with the node `omit` (and its incident edges) removed.
func connectedWithout(store *graphstore.MemStore, src, dst, omit model.NodeId) bool {
	ctx := context.Background()
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		panic(err)
	}
	adj := map[model.NodeId][]model.NodeId{}
	for _, e := range edges {
		f, tt := e.From(), e.To()
		if f == omit || tt == omit {
			continue
		}
		adj[f] = append(adj[f], tt)
		adj[tt] = append(adj[tt], f)
	}
	seen := map[model.NodeId]bool{src: true}
	stack := []model.NodeId{src}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == dst {
			return true
		}
		for _, n := range adj[cur] {
			if !seen[n] {
				seen[n] = true
				stack = append(stack, n)
			}
		}
	}
	return false
}

// --- AC3: surprise (low churn + unexpected coupling) + no-surprise control ---

func TestPrSignalsSurpriseLowChurn(t *testing.T) {
	store := graphstore.NewMemStore()
	idRare := mkNode(t, store, "function", "pkg.Rare", "pkg/rare.go", 10)
	idRoutine := mkNode(t, store, "function", "pkg.Routine", "pkg/routine.go", 10)

	src := &stubSignalSource{
		churn: []githistory.ChurnScore{
			{Path: "pkg/rare.go", Commits: 1},     // rarely modified
			{Path: "pkg/routine.go", Commits: 40}, // routinely modified
		},
	}
	a := prSignalsAnalyzer{source: src, config: signalConfig{HubDegreeThreshold: 3, SurpriseChurnMax: 1}}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/rare.go:Rare\npkg/routine.go:Routine"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.SignalReport

	flag, ok := hasSignal(signalsFor(t, rep, idRare), SignalSurprise)
	if !ok {
		t.Fatalf("expected rarely-modified region to be surprise")
	}
	if flag.Detail != SurpriseLowChurn {
		t.Fatalf("expected surprise detail %q, got %q", SurpriseLowChurn, flag.Detail)
	}
	if !strings.Contains(flag.Reason, "rarely-modified") {
		t.Fatalf("expected contributing reason to mention rarely-modified, got %q", flag.Reason)
	}

	// Control: a routinely-changed region must NOT be flagged surprise.
	if _, ok := hasSignal(signalsFor(t, rep, idRoutine), SignalSurprise); ok {
		t.Fatalf("expected routinely-changed region NOT to be surprise")
	}
}

func TestPrSignalsSurpriseUnexpectedCoupling(t *testing.T) {
	store := graphstore.NewMemStore()
	// Region X is coupled (via PDG dep edge) to Y in a DIFFERENT file → surprise.
	// Region P is coupled only to Q in the SAME file → NOT surprising.
	idX := mkNode(t, store, "function", "mod1.X", "mod1/x.go", 10)
	idY := mkNode(t, store, "function", "mod2.Y", "mod2/y.go", 10)
	idP := mkNode(t, store, "function", "same.P", "same/same.go", 10)
	idQ := mkNode(t, store, "function", "same.Q", "same/same.go", 20)

	src := &stubSignalSource{
		pdgRes: pdg.PDGResult{
			DataDepEdges: []pdg.DepEdge{
				{From: idX, To: idY, Kind: pdg.EdgeKindDataDep}, // cross-file
				{From: idP, To: idQ, Kind: pdg.EdgeKindDataDep}, // same-file
			},
			Nodes: []pdg.PDGNode{
				{ID: idX, SourcePath: "mod1/x.go"},
				{ID: idY, SourcePath: "mod2/y.go"},
				{ID: idP, SourcePath: "same/same.go"},
				{ID: idQ, SourcePath: "same/same.go"},
			},
		},
	}
	a := prSignalsAnalyzer{source: src, config: defaultSignalConfig}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "mod1/x.go:X\nsame/same.go:P"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rep := *res.SignalReport

	flag, ok := hasSignal(signalsFor(t, rep, idX), SignalSurprise)
	if !ok {
		t.Fatalf("expected cross-file-coupled region X to be surprise")
	}
	if flag.Detail != SurpriseUnexpectedCoupling {
		t.Fatalf("expected detail %q, got %q", SurpriseUnexpectedCoupling, flag.Detail)
	}
	if !strings.Contains(flag.Reason, "unexpectedly coupled") {
		t.Fatalf("expected reason to mention unexpected coupling, got %q", flag.Reason)
	}

	// Control: same-file coupling must NOT produce an unexpected-coupling surprise.
	recP := signalsFor(t, rep, idP)
	for _, f := range recP.Signals {
		if f.Kind == SignalSurprise && f.Detail == SurpriseUnexpectedCoupling {
			t.Fatalf("expected same-file-coupled region P NOT to be unexpected-coupling surprise")
		}
	}
}

// --- degraded + empty-diff + determinism + security --------------------------

func TestPrSignalsDegradedAndEmpty(t *testing.T) {
	store := graphstore.NewMemStore()
	a := prSignalsAnalyzer{source: &stubSignalSource{}, config: defaultSignalConfig}

	// Unresolved ref → degraded record, not an error.
	res, err := a.Analyze(context.Background(), store, Params{Diff: "nowhere/missing.go:Ghost"})
	if err != nil {
		t.Fatalf("Analyze degraded: %v", err)
	}
	if len(res.SignalReport.Regions) != 1 || !res.SignalReport.Regions[0].Degraded {
		t.Fatalf("expected one degraded record, got %+v", res.SignalReport.Regions)
	}

	// Empty diff → empty report, outcome empty, no error.
	res2, err := a.Analyze(context.Background(), store, Params{Diff: "   "})
	if err != nil {
		t.Fatalf("Analyze empty: %v", err)
	}
	if len(res2.SignalReport.Regions) != 0 || res2.SignalReport.Outcome != string(query.OutcomeEmpty) {
		t.Fatalf("expected empty report, got %+v", res2.SignalReport)
	}
}

func TestPrSignalsDeterministic(t *testing.T) {
	store := graphstore.NewMemStore()
	idHub := mkNode(t, store, "function", "pkg.Hub", "pkg/hub.go", 10)
	src := &stubSignalSource{
		metrics: []NodeScore{{Node: query.ResultNode{ID: idHub}, Kind: MetricHub, Score: 5}},
		churn:   []githistory.ChurnScore{{Path: "pkg/hub.go", Commits: 1}},
	}
	a := prSignalsAnalyzer{source: src, config: defaultSignalConfig}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/hub.go:Hub"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	first, err := Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Twice-run byte-identical guard (the SW-039 determinism discipline).
	for i := 0; i < 25; i++ {
		res2, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/hub.go:Hub"})
		if err != nil {
			t.Fatalf("Analyze iter %d: %v", i, err)
		}
		b, err := Marshal(res2)
		if err != nil {
			t.Fatalf("Marshal iter %d: %v", i, err)
		}
		if string(b) != string(first) {
			t.Fatalf("non-deterministic output at iter %d:\n first=%s\n got  =%s", i, first, b)
		}
	}

	// The canonical output must be the SignalReport shape (routed via Marshal).
	var probe map[string]any
	if err := json.Unmarshal(first, &probe); err != nil {
		t.Fatalf("output not valid json: %v", err)
	}
	if _, ok := probe["schema_version"]; !ok {
		t.Fatalf("expected versioned signal report shape, got %s", first)
	}
}

func TestPrSignalsSummaryRedaction(t *testing.T) {
	store := graphstore.NewMemStore()
	idRare := mkNode(t, store, "function", "pkg.Rare", "pkg/rare.go", 10)
	src := &stubSignalSource{
		churn: []githistory.ChurnScore{{Path: "pkg/rare.go", Commits: 1}},
	}
	a := prSignalsAnalyzer{source: src, config: defaultSignalConfig}

	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/rare.go:Rare", Provenance: ProvenanceSummary})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	flag, ok := hasSignal(signalsFor(t, *res.SignalReport, idRare), SignalSurprise)
	if !ok {
		t.Fatalf("expected surprise")
	}
	// At summary level the path must be redacted (no Refs leaking the path).
	if len(flag.Refs) != 0 {
		t.Fatalf("expected redacted refs at summary provenance, got %v", flag.Refs)
	}
	if !strings.Contains(flag.Reason, "redacted") {
		t.Fatalf("expected redaction note in reason, got %q", flag.Reason)
	}
}

// --- registration / dispatch parity ------------------------------------------

func TestPrSignalsRegisteredAndDiffDriven(t *testing.T) {
	store := graphstore.NewMemStore()
	mkNode(t, store, "function", "pkg.A", "pkg/a.go", 10)
	svc := NewDefaultService(store)

	found := false
	for _, n := range svc.Names() {
		if n == PrSignalsAnalyzerName {
			found = true
		}
	}
	if !found {
		t.Fatalf("pr-signals not registered in default service; names=%v", svc.Names())
	}

	out, err := svc.Dispatch(context.Background(), PrSignalsAnalyzerName, Params{Diff: "pkg/a.go:A"})
	if err != nil {
		t.Fatalf("Dispatch pr-signals: %v", err)
	}
	if out.SignalReport == nil {
		t.Fatalf("expected a SignalReport from pr-signals dispatch")
	}
}
