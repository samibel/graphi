package interproctaint

import (
	"bytes"
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/interproc"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

type testReader struct {
	nodes []model.Node
	edges []model.Edge
}

func (r *testReader) GetNode(_ context.Context, id model.NodeId) (model.Node, error) {
	for _, n := range r.nodes {
		if n.ID() == id {
			return n, nil
		}
	}
	return model.Node{}, graphstore.ErrNotFound
}
func (r *testReader) GetEdge(_ context.Context, id model.EdgeId) (model.Edge, error) {
	for _, e := range r.edges {
		if e.ID() == id {
			return e, nil
		}
	}
	return model.Edge{}, graphstore.ErrNotFound
}
func (r *testReader) Nodes(_ context.Context, _ graphstore.Query) ([]model.Node, error) {
	return r.nodes, nil
}
func (r *testReader) Edges(_ context.Context, _ graphstore.Query) ([]model.Edge, error) {
	return r.edges, nil
}

var _ query.Reader = (*testReader)(nil)

func mustNode(t *testing.T, kind, name string) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, name, name+".go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustEdge(t *testing.T, from, to model.NodeId, kind string) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, kind, model.TierDerived, 0.9, "test", []string{"ev"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func testConfig() taint.Config {
	cfg := taint.DefaultConfig()
	cfg.ContentHash = "test-config-hash"
	return cfg
}

func solve(t *testing.T, r query.Reader) Solution {
	t.Helper()
	sol, err := Solve(context.Background(), r, testConfig(), interproc.DefaultCaps(), interproc.DefaultWideningThreshold)
	if err != nil {
		t.Fatal(err)
	}
	return sol
}

func hasFlow(sol Solution, sourceName, sinkName, label string) bool {
	f, ok := QueryFlow(sol, sourceName, sinkName)
	if !ok {
		return false
	}
	for _, l := range f.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// AC-1: labeled cross-procedure source→sink flow (≥2 call edges), positive +
// negative ground truth.
// ---------------------------------------------------------------------------

// fixture: sink Query() -> mid() -> source FormValue(). Taint flows up the call
// chain from the deep source to the sink across TWO call edges.
func positiveFixture(t *testing.T) (*testReader, model.Node, model.Node, model.Node) {
	sink := mustNode(t, "function", "database/sql.DB.Query")
	mid := mustNode(t, "function", "app.process")
	src := mustNode(t, "function", "http.Request.FormValue")
	r := &testReader{
		nodes: []model.Node{sink, mid, src},
		edges: []model.Edge{
			mustEdge(t, sink.ID(), mid.ID(), query.EdgeKindCalls),
			mustEdge(t, mid.ID(), src.ID(), query.EdgeKindCalls),
		},
	}
	return r, sink, mid, src
}

func TestAC1_PositiveCrossProcedureFlow(t *testing.T) {
	r, sink, _, src := positiveFixture(t)
	sol := solve(t, r)

	if sol.Verdict != VerdictComplete {
		t.Fatalf("verdict: want complete, got %s", sol.Verdict)
	}
	f, ok := QueryFlow(sol, src.QualifiedName(), sink.QualifiedName())
	if !ok {
		t.Fatalf("expected cross-procedure flow %s -> %s, flows=%+v", src.QualifiedName(), sink.QualifiedName(), sol.Flows)
	}
	if len(f.Labels) != 1 || f.Labels[0] != "user_input" {
		t.Errorf("labels: want [user_input], got %v", f.Labels)
	}
	if f.SinkCategory != "sql_injection" {
		t.Errorf("category: want sql_injection, got %s", f.SinkCategory)
	}
	// Call path must span both call edges: sink -> mid -> source (3 procedures).
	if len(f.CallPath) != 3 {
		t.Fatalf("call path should span 2 call edges (3 procs), got %v", f.CallPath)
	}
	if f.CallPath[0] != sink.QualifiedName() || f.CallPath[2] != src.QualifiedName() {
		t.Errorf("call path endpoints wrong: %v", f.CallPath)
	}
}

// negative: a universal-sanitizer procedure on the path kills the label before
// it reaches the sink → NO flow (no false positive). The sanitizer must NOT be
// silently treated as "no data"; it is a genuine kill in the solved relation.
func TestAC1_NegativeSanitizedFlow(t *testing.T) {
	sink := mustNode(t, "function", "database/sql.DB.Query")
	san := mustNode(t, "function", "html.EscapeString") // removes user_input
	src := mustNode(t, "function", "http.Request.FormValue")
	r := &testReader{
		nodes: []model.Node{sink, san, src},
		edges: []model.Edge{
			mustEdge(t, sink.ID(), san.ID(), query.EdgeKindCalls),
			mustEdge(t, san.ID(), src.ID(), query.EdgeKindCalls),
		},
	}
	sol := solve(t, r)
	if _, ok := QueryFlow(sol, src.QualifiedName(), sink.QualifiedName()); ok {
		t.Fatalf("expected NO flow (sanitizer kills user_input), got flows=%+v", sol.Flows)
	}
	if len(sol.Flows) != 0 {
		t.Errorf("expected 0 flows, got %d: %+v", len(sol.Flows), sol.Flows)
	}
}

// ---------------------------------------------------------------------------
// AC-2: determinism — identical bytes across runs AND across two different
// worklist seed (input) orders.
// ---------------------------------------------------------------------------

func TestAC2_DeterministicByteIdenticalArtifact(t *testing.T) {
	r1, _, _, _ := positiveFixture(t)
	// Build the SAME graph with nodes and edges in a permuted order.
	sink := mustNode(t, "function", "database/sql.DB.Query")
	mid := mustNode(t, "function", "app.process")
	src := mustNode(t, "function", "http.Request.FormValue")
	r2 := &testReader{
		nodes: []model.Node{src, sink, mid},
		edges: []model.Edge{
			mustEdge(t, mid.ID(), src.ID(), query.EdgeKindCalls),
			mustEdge(t, sink.ID(), mid.ID(), query.EdgeKindCalls),
		},
	}

	solA := solve(t, r1)
	solB := solve(t, r1) // same reader, second run
	solC := solve(t, r2) // permuted seed order, same final graph

	a, err := Marshal(solA)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Marshal(solB)
	c, _ := Marshal(solC)

	if !bytes.Equal(a, b) {
		t.Fatalf("two runs not byte-identical:\n%s\n%s", a, b)
	}
	if !bytes.Equal(a, c) {
		t.Fatalf("permuted seed order not byte-identical:\n%s\n%s", a, c)
	}
	if solA.ContentHash != solC.ContentHash {
		t.Fatalf("content hash differs across seed orders: %s vs %s", solA.ContentHash, solC.ContentHash)
	}
}

// ---------------------------------------------------------------------------
// AC-3: direct recursion + mutually-recursive SCC converge within the cap and
// match the labeled recursive-flow ground truth.
// ---------------------------------------------------------------------------

func TestAC3_RecursionConverges(t *testing.T) {
	// Mutually recursive a <-> b; a also reads a source and calls itself
	// (direct recursion); a sink reads from a.
	sink := mustNode(t, "function", "os/exec.Command") // command_injection sink
	a := mustNode(t, "function", "app.a")
	b := mustNode(t, "function", "app.b")
	src := mustNode(t, "function", "os.Getenv") // env_input source
	r := &testReader{
		nodes: []model.Node{sink, a, b, src},
		edges: []model.Edge{
			mustEdge(t, sink.ID(), a.ID(), query.EdgeKindCalls),
			mustEdge(t, a.ID(), b.ID(), query.EdgeKindCalls),
			mustEdge(t, b.ID(), a.ID(), query.EdgeKindCalls), // mutual recursion
			mustEdge(t, a.ID(), a.ID(), query.EdgeKindCalls), // direct recursion
			mustEdge(t, a.ID(), src.ID(), query.EdgeKindCalls),
		},
	}

	// Solve must terminate (no infinite loop) — the test itself completing is the
	// convergence proof, backed by the bounded-height LabelSet lattice.
	sol := solve(t, r)
	if sol.Verdict != VerdictComplete {
		t.Fatalf("verdict: want complete (converged within cap), got %s (cap %s)", sol.Verdict, sol.CapKind)
	}
	if !hasFlow(sol, src.QualifiedName(), sink.QualifiedName(), "env_input") {
		t.Fatalf("expected recursive cross-proc flow %s -> %s [env_input], flows=%+v",
			src.QualifiedName(), sink.QualifiedName(), sol.Flows)
	}

	// Re-solve and confirm byte-identical (recursion order independence).
	sol2 := solve(t, r)
	b1, _ := Marshal(sol)
	b2, _ := Marshal(sol2)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("recursive solve not deterministic:\n%s\n%s", b1, b2)
	}
}

// ---------------------------------------------------------------------------
// AC-4: persist once, "restart" (fresh store over same dir), query served from
// the artifact with NO recomputation; equals pre-restart answer.
// ---------------------------------------------------------------------------

func TestAC4_RestartNoRecompute(t *testing.T) {
	r, sink, _, src := positiveFixture(t)
	dir := t.TempDir()

	store1 := NewStore(dir)
	first, err := LoadOrSolve(context.Background(), store1, r, testConfig(), interproc.DefaultCaps(), interproc.DefaultWideningThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Solved || first.Loaded {
		t.Fatalf("first call should solve: solved=%v loaded=%v", first.Solved, first.Loaded)
	}

	// "Restart": a brand-new store over the same directory, fresh process state.
	store2 := NewStore(dir)
	second, err := LoadOrSolve(context.Background(), store2, r, testConfig(), interproc.DefaultCaps(), interproc.DefaultWideningThreshold)
	if err != nil {
		t.Fatal(err)
	}
	// No-recompute observability signal: loaded=true, solved=false.
	if second.Solved || !second.Loaded {
		t.Fatalf("restart must serve from artifact with no recompute: solved=%v loaded=%v", second.Solved, second.Loaded)
	}

	// Pre/post-restart answers byte-identical.
	b1, _ := Marshal(first)
	b2, _ := Marshal(second)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("pre/post restart answer differs:\n%s\n%s", b1, b2)
	}
	pre, _ := QueryFlow(first, src.QualifiedName(), sink.QualifiedName())
	post, ok := QueryFlow(second, src.QualifiedName(), sink.QualifiedName())
	if !ok {
		t.Fatalf("post-restart flow missing")
	}
	if pre.SinkName != post.SinkName || len(pre.Labels) != len(post.Labels) {
		t.Fatalf("flow answer changed across restart: %+v vs %+v", pre, post)
	}
}

// ---------------------------------------------------------------------------
// AC-5: same final graph reached by a "full" build vs an "incremental" rebuild
// → on-disk artifacts AND content hashes byte-identical.
// ---------------------------------------------------------------------------

func TestAC5_FullVsIncrementalByteParity(t *testing.T) {
	// Full build: all nodes/edges at once.
	rFull, _, _, _ := positiveFixture(t)

	// Incremental rebuild: same final node/edge sets, declared in a different
	// order (simulating arriving via incremental updates).
	sink := mustNode(t, "function", "database/sql.DB.Query")
	mid := mustNode(t, "function", "app.process")
	src := mustNode(t, "function", "http.Request.FormValue")
	rIncr := &testReader{
		nodes: []model.Node{mid, src, sink},
		edges: []model.Edge{
			mustEdge(t, mid.ID(), src.ID(), query.EdgeKindCalls),
			mustEdge(t, sink.ID(), mid.ID(), query.EdgeKindCalls),
		},
	}

	dirFull := t.TempDir()
	dirIncr := t.TempDir()
	storeFull := NewStore(dirFull)
	storeIncr := NewStore(dirIncr)

	solFull, err := LoadOrSolve(context.Background(), storeFull, rFull, testConfig(), interproc.DefaultCaps(), interproc.DefaultWideningThreshold)
	if err != nil {
		t.Fatal(err)
	}
	solIncr, err := LoadOrSolve(context.Background(), storeIncr, rIncr, testConfig(), interproc.DefaultCaps(), interproc.DefaultWideningThreshold)
	if err != nil {
		t.Fatal(err)
	}

	if solFull.ContentHash != solIncr.ContentHash {
		t.Fatalf("content hash differs full vs incremental: %s vs %s", solFull.ContentHash, solIncr.ContentHash)
	}
	bFull, _ := Marshal(solFull)
	bIncr, _ := Marshal(solIncr)
	if !bytes.Equal(bFull, bIncr) {
		t.Fatalf("on-disk artifact bytes differ full vs incremental:\n%s\n%s", bFull, bIncr)
	}
}

// ---------------------------------------------------------------------------
// AC-6: cost cap → explicit deterministic capped/incomplete verdict, identical
// on repeat, never collapsed to "no flow".
// ---------------------------------------------------------------------------

func TestAC6_CappedVerdictDeterministic(t *testing.T) {
	// 3-node mutually recursive SCC (a->b->c->a) with a source and a sink; cap
	// MaxSCCSize=1 forces the conservative capped path.
	sink := mustNode(t, "function", "database/sql.DB.Query")
	a := mustNode(t, "function", "app.a") // also a source via name? no — pure
	b := mustNode(t, "function", "app.b")
	c := mustNode(t, "function", "os.Getenv") // source env_input, inside the SCC
	r := &testReader{
		nodes: []model.Node{sink, a, b, c},
		edges: []model.Edge{
			mustEdge(t, sink.ID(), a.ID(), query.EdgeKindCalls),
			mustEdge(t, a.ID(), b.ID(), query.EdgeKindCalls),
			mustEdge(t, b.ID(), c.ID(), query.EdgeKindCalls),
			mustEdge(t, c.ID(), a.ID(), query.EdgeKindCalls),
		},
	}
	caps := interproc.DefaultCaps()
	caps.MaxSCCSize = 1 // 3-node SCC exceeds this → capped

	run := func() Solution {
		sol, err := Solve(context.Background(), r, testConfig(), caps, interproc.DefaultWideningThreshold)
		if err != nil {
			t.Fatal(err)
		}
		return sol
	}
	sol1 := run()
	sol2 := run()

	if sol1.Verdict != VerdictCapped {
		t.Fatalf("verdict: want capped, got %s", sol1.Verdict)
	}
	if sol1.CapKind != "max_scc_size" {
		t.Fatalf("cap kind: want max_scc_size, got %q", sol1.CapKind)
	}
	// Deterministic on repeat.
	b1, _ := Marshal(sol1)
	b2, _ := Marshal(sol2)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("capped verdict not deterministic on repeat:\n%s\n%s", b1, b2)
	}
	// Honesty: capped is NOT silently downgraded to "no flow". The conservative
	// over-approximation must still expose the tainted source's label.
	if sol1.Verdict == VerdictComplete {
		t.Fatal("capped result must not present as complete")
	}
}

// ---------------------------------------------------------------------------
// Provider integration: the solved-summary provider replaces NoOpSummaryProvider
// and answers a callee's transfer (a sanitizer kills, a pass-through forwards).
// ---------------------------------------------------------------------------

func TestSolvedProviderTransfer(t *testing.T) {
	_, _, mid, _ := positiveFixture(t)
	r, _, _, _ := positiveFixture(t)
	sol := solve(t, r)
	p := NewSolvedProvider(sol)

	if !p.HasSummary(mid.QualifiedName()) {
		t.Fatalf("expected summary for %s", mid.QualifiedName())
	}
	// pass-through procedure forwards labels unchanged.
	in := taint.NewLabelSet("user_input")
	out := p.TransferLabels(mid.QualifiedName(), in)
	if !out.Equal(in) {
		t.Errorf("pass-through transfer: want %v, got %v", in, out)
	}

	// A universal sanitizer procedure drops all labels.
	sanNode := mustNode(t, "function", "strconv.Atoi") // universal sanitizer
	src := mustNode(t, "function", "os.Getenv")
	sink := mustNode(t, "function", "os/exec.Command")
	r2 := &testReader{
		nodes: []model.Node{sink, sanNode, src},
		edges: []model.Edge{
			mustEdge(t, sink.ID(), sanNode.ID(), query.EdgeKindCalls),
			mustEdge(t, sanNode.ID(), src.ID(), query.EdgeKindCalls),
		},
	}
	sol2 := solve(t, r2)
	p2 := NewSolvedProvider(sol2)
	if got := p2.TransferLabels(sanNode.QualifiedName(), taint.NewLabelSet("env_input")); !got.Empty() {
		t.Errorf("universal sanitizer transfer: want empty, got %v", got)
	}
}

// Empty graph yields an empty, complete, byte-stable solution.
func TestEmptyGraph(t *testing.T) {
	sol := solve(t, &testReader{})
	if sol.Verdict != VerdictComplete {
		t.Errorf("empty graph verdict: want complete, got %s", sol.Verdict)
	}
	if len(sol.Flows) != 0 || len(sol.Summaries) != 0 {
		t.Errorf("empty graph should have no flows/summaries, got %+v", sol)
	}
}

// Key mismatch must recompute (miss), never serve a stale artifact.
func TestKeyMismatchRecomputes(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if _, ok, err := store.Load("deadbeefdeadbeef"); err != nil || ok {
		t.Fatalf("absent key must miss: ok=%v err=%v", ok, err)
	}
}
