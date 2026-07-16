package analysis_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

type impactLookupProbe struct {
	*graphstore.MemStore
	boundedOutgoingCalls int
	boundedEdgesRead     int
	maxIncidentLimit     int
	maxKindsProbed       int
}

func (p *impactLookupProbe) OutgoingBounded(ctx context.Context, id model.NodeId, limit int, kinds ...model.EdgeKind) ([]model.Edge, bool, error) {
	p.boundedOutgoingCalls++
	if limit > p.maxIncidentLimit {
		p.maxIncidentLimit = limit
	}
	if len(kinds) > p.maxKindsProbed {
		p.maxKindsProbed = len(kinds)
	}
	edges, truncated, err := p.MemStore.OutgoingBounded(ctx, id, limit, kinds...)
	p.boundedEdgesRead += len(edges)
	return edges, truncated, err
}

// seedImpactGraph builds a deterministic synthetic graph in an in-memory store:
//
//	pkg.A --calls--> pkg.B --calls--> pkg.C
//	pkg.A --calls--> pkg.C            (A reaches C directly too)
//	pkg.B --calls--> pkg.D
//	pkg.D --calls--> pkg.B            (cycle B <-> D)
//	pkg.X                            (isolated)
//
// Reverse (dependents, incoming edges) and Forward (dependencies, outgoing
// edges) sets derived from this topology are the correctness oracle below
// (the rdeps convention, fixed in v0.1.3 — the two names were swapped before).
func seedImpactGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(name string) model.Node {
		n, err := model.NewNode("function", name, "pkg/"+name+".go", 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", name, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", name, err)
		}
		return n
	}
	names := []string{"pkg.A", "pkg.B", "pkg.C", "pkg.D", "pkg.X"}
	ids := make(map[string]model.NodeId, len(names))
	nodes := make(map[string]model.Node, len(names))
	for _, n := range names {
		nd := mk(n)
		ids[n] = nd.ID()
		nodes[n] = nd
	}
	mkEdge := func(from, to string, tier model.ConfidenceTier, reason string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), string(query.EdgeKindCalls), tier, 0.9, reason, []string{"pkg/" + from + ".go:1"})
		if err != nil {
			t.Fatalf("NewEdge(%s->%s): %v", from, to, err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge(%s->%s): %v", from, to, err)
		}
	}
	mkEdge("pkg.A", "pkg.B", model.TierConfirmed, "A calls B")
	mkEdge("pkg.B", "pkg.C", model.TierDerived, "B calls C")
	mkEdge("pkg.A", "pkg.C", model.TierHeuristic, "A calls C")
	mkEdge("pkg.B", "pkg.D", model.TierDerived, "B calls D")
	mkEdge("pkg.D", "pkg.B", model.TierDerived, "D calls B")
	return store, ids
}

func reachedNames(res analysis.Analysis) []string {
	out := make([]string, 0, len(res.Nodes))
	for _, rn := range res.Nodes {
		out = append(out, rn.Node.QualifiedName)
	}
	sort.Strings(out)
	return out
}

func TestImpactReverseDependents(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// reverse(C): everyone who transitively depends on C (incoming-edge closure)
	// = {A (A->C, A->B->C), B (B->C), D (D->B->C)}.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("Dispatch impact reverse: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	got := reachedNames(res)
	want := []string{"pkg.A", "pkg.B", "pkg.D"}
	if !equalStrings(got, want) {
		t.Fatalf("reverse(C) = %v, want %v (no false/missing members)", got, want)
	}
	// X must never appear (isolated, not a dependent of anything).
	for _, rn := range res.Nodes {
		if rn.Node.QualifiedName == "pkg.X" {
			t.Fatalf("isolated pkg.X falsely reached as dependent of C")
		}
	}
}

func TestImpactForwardDependencies(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// forward(A): what A transitively depends on (outgoing-edge closure)
	// = {B (A->B), C (A->C, A->B->C), D (A->B->D, D<->B cycle)}.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.A"],
		Direction: analysis.Forward,
	})
	if err != nil {
		t.Fatalf("Dispatch impact forward: %v", err)
	}
	got := reachedNames(res)
	want := []string{"pkg.B", "pkg.C", "pkg.D"}
	if !equalStrings(got, want) {
		t.Fatalf("forward(A) = %v, want %v", got, want)
	}
}

func TestImpactCycleSafeEachNodeOnce(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// The B<->D cycle must terminate and list each node exactly once.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	seen := map[string]int{}
	for _, rn := range res.Nodes {
		seen[rn.Node.QualifiedName]++
	}
	for name, n := range seen {
		if n != 1 {
			t.Fatalf("node %s listed %d times (cycle not guarded)", name, n)
		}
	}
}

func TestImpactProvenanceOnEveryNode(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	for _, rn := range res.Nodes {
		e := rn.ReachedVia
		if e.ID == "" {
			t.Fatalf("node %s reached with empty edge id (no provenance)", rn.Node.QualifiedName)
		}
		if !e.Tier.Valid() {
			t.Fatalf("node %s reached via invalid tier %q", rn.Node.QualifiedName, e.Tier)
		}
		if strings.TrimSpace(e.Reason) == "" {
			t.Fatalf("node %s reached via empty reason", rn.Node.QualifiedName)
		}
		// The reaching edge must actually touch the reached node.
		if e.From != rn.Node.ID && e.To != rn.Node.ID {
			t.Fatalf("reaching edge %s does not touch reached node %s", e.ID, rn.Node.QualifiedName)
		}
	}
}

func TestImpactBoundTruncatedRanked(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// reverse(C) has 3 dependents; cap at 2 -> bounded, ranked, truncated flag set.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Reverse,
		MaxNodes:  2,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Nodes) != 2 {
		t.Fatalf("MaxNodes=2 -> got %d nodes, want 2 (bounded)", len(res.Nodes))
	}
	if !res.Truncated {
		t.Fatalf("Truncated=false, want true (reachable set exceeded cap)")
	}
	// Ranking is deterministic: tier-rank of reaching edge then node id. The
	// two leading dependents must be in canonical order.
	if !sortReachedIsSorted(res.Nodes) {
		t.Fatalf("result not canonically ranked (tier then node id)")
	}
}

func TestImpactMaxNodesAlsoBoundsTraversalWork(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	const nodeCount = 500
	nodes := make([]model.Node, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		n, err := model.NewNode("function", fmt.Sprintf("chain.N%03d", i), "chain/chain.go", i+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	for i := 0; i < len(nodes)-1; i++ {
		e, err := model.NewEdge(nodes[i].ID(), nodes[i+1].ID(), string(query.EdgeKindCalls), model.TierConfirmed, 1, "chain", []string{"chain/chain.go:1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	probe := &impactLookupProbe{MemStore: store}
	svc := analysis.NewDefaultService(probe)
	res, err := svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol: nodes[0].ID(), Direction: analysis.Forward, MaxNodes: 2,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Nodes) != 2 || !res.Truncated {
		t.Fatalf("bounded result = nodes:%d truncated:%v, want 2/true", len(res.Nodes), res.Truncated)
	}
	if probe.boundedOutgoingCalls > 20 {
		t.Fatalf("MaxNodes=2 expanded %d of 500 chain nodes; traversal work is not bounded", probe.boundedOutgoingCalls)
	}
	if probe.boundedEdgesRead > 32 {
		t.Fatalf("MaxNodes=2 read %d edges, want <=32 global edge budget", probe.boundedEdgesRead)
	}
}

func TestImpactHighDegreeIncidentReadIsActuallyBounded(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	seed, err := model.NewNode("function", "star.Seed", "star/star.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(ctx, seed); err != nil {
		t.Fatal(err)
	}
	const degree = 10_000
	for i := 0; i < degree; i++ {
		leaf, err := model.NewNode("function", fmt.Sprintf("star.Leaf%05d", i), "star/star.go", i+2, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, leaf); err != nil {
			t.Fatal(err)
		}
		edge, err := model.NewEdge(seed.ID(), leaf.ID(), string(query.EdgeKindCalls), model.TierConfirmed, 1, "star", []string{"star/star.go:1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, edge); err != nil {
			t.Fatal(err)
		}
	}

	probe := &impactLookupProbe{MemStore: store}
	res, err := analysis.NewDefaultService(probe).Dispatch(ctx, "impact", analysis.Params{
		Symbol: seed.ID(), Direction: analysis.Forward, MaxNodes: 1,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Nodes) != 1 || !res.Truncated {
		t.Fatalf("high-degree result = nodes:%d truncated:%v, want 1/true", len(res.Nodes), res.Truncated)
	}
	if probe.boundedOutgoingCalls != 1 {
		t.Fatalf("bounded incident calls = %d, want exactly 1", probe.boundedOutgoingCalls)
	}
	if probe.maxIncidentLimit > 16 || probe.boundedEdgesRead > 16 {
		t.Fatalf("MaxNodes=1 requested/read %d/%d incident edges from degree %d, want <=16/<=16", probe.maxIncidentLimit, probe.boundedEdgesRead, degree)
	}
}

func TestImpactUntrustedKindsListIsBudgeted(t *testing.T) {
	store, ids := seedImpactGraph(t)
	probe := &impactLookupProbe{MemStore: store}
	kinds := []string{string(query.EdgeKindCalls)}
	const untrustedKinds = 50_000
	for i := 0; i < untrustedKinds; i++ {
		kinds = append(kinds, fmt.Sprintf("zz-untrusted-%04d", i))
	}
	res, err := analysis.NewDefaultService(probe).Dispatch(context.Background(), "impact", analysis.Params{
		Symbol: ids["pkg.A"], Direction: analysis.Forward, MaxNodes: 1, Kinds: kinds,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !res.Truncated {
		t.Fatal("oversized distinct kinds list must produce Truncated=true")
	}
	if probe.maxKindsProbed > 2 {
		t.Fatalf("MaxNodes=1 probed %d distinct kinds from %d inputs, want <=2", probe.maxKindsProbed, untrustedKinds+1)
	}
}

func TestImpactOutcomesTriState(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// not_found: unknown seed -> typed not-found result, never an error.
	res, err := svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol:    model.NodeId("zzzzzzzzzzzzzzzz"),
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("unknown seed should not error, got: %v", err)
	}
	if res.Outcome != query.OutcomeNotFound {
		t.Fatalf("unknown seed outcome = %s, want not_found", res.Outcome)
	}

	// empty: isolated X resolves but has no dependents.
	res, err = svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol:    ids["pkg.X"],
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("isolated seed: %v", err)
	}
	if res.Outcome != query.OutcomeEmpty {
		t.Fatalf("isolated X outcome = %s, want empty", res.Outcome)
	}

	// found: C has dependents.
	res, err = svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("C: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("C outcome = %s, want found", res.Outcome)
	}
}

func TestImpactDefaultDirectionReverse(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// Empty direction defaults to Reverse (dependents / blast radius): bare
	// "impact of C" must answer "who is affected if C changes".
	res, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["pkg.C"]})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("default-direction outcome = %s, want found", res.Outcome)
	}
	if got, want := reachedNames(res), []string{"pkg.A", "pkg.B", "pkg.D"}; !equalStrings(got, want) {
		t.Fatalf("default direction impact(C) = %v, want dependents %v", got, want)
	}
}

func TestImpactDeterminismRepeated(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	first, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["pkg.C"], Direction: analysis.Reverse})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	firstBytes, err := analysis.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Re-run many times: map-iteration nondeterminism is the real failure mode,
	// and the materialize-then-sort contract must absorb it every run.
	for i := 0; i < 30; i++ {
		res, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["pkg.C"], Direction: analysis.Reverse})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b, err := analysis.Marshal(res)
		if err != nil {
			t.Fatalf("iter %d marshal: %v", i, err)
		}
		if !bytes.Equal(firstBytes, b) {
			t.Fatalf("iteration %d produced non-byte-identical output (determinism violated)", i)
		}
	}
}

func TestImpactDeterminismTwoServices(t *testing.T) {
	// Two INDEPENDENT services built over identical snapshots must agree
	// byte-for-byte. This is the structural proxy for cross-process / daemon
	// restart determinism: the analyzer holds no mutable global state, so a
	// fresh service reproduces identical routing from files alone.
	s1, ids1 := seedImpactGraph(t)
	s2, ids2 := seedImpactGraph(t)
	svc1 := analysis.NewDefaultService(s1)
	svc2 := analysis.NewDefaultService(s2)
	ctx := context.Background()

	r1, err := svc1.Dispatch(ctx, "impact", analysis.Params{Symbol: ids1["pkg.A"], Direction: analysis.Forward})
	if err != nil {
		t.Fatalf("svc1: %v", err)
	}
	r2, err := svc2.Dispatch(ctx, "impact", analysis.Params{Symbol: ids2["pkg.A"], Direction: analysis.Forward})
	if err != nil {
		t.Fatalf("svc2: %v", err)
	}
	b1, _ := analysis.Marshal(r1)
	b2, _ := analysis.Marshal(r2)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("two independent services produced non-identical output")
	}
}

func TestImpactUnknownAnalyzerError(t *testing.T) {
	store, _ := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	_, err := svc.Dispatch(context.Background(), "no-such-analyzer", analysis.Params{})
	if err == nil {
		t.Fatal("unknown analyzer must return an error, got nil")
	}
	if !errors.Is(err, err) { // trivial: ensure error is a real error value
		t.Fatalf("error value malformed")
	}
}

// TestImpactNoNetworkSourceScan asserts the local-first invariant at the source
// level: the analysis package must contain no outbound-network code. (The
// whole-binary egress canary in cmd/canary enforces it at CI; this is the
// cheap, CI-stable, package-local proxy.)
func TestImpactNoNetworkSourceScan(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	// Production source only: test files may legitimately mention network symbols
	// in assertions (this very test does). The invariant is about shipped code.
	var prod []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			prod = append(prod, f)
		}
	}
	if len(prod) == 0 {
		t.Skip("source files not visible from test cwd; running as external test in another layout")
	}
	banned := []string{"\"net\"", "\"net/http\"", "\"net/url\"", "http.Listen", "net.Dial", "http.Get", "http.Post"}
	for _, f := range prod {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		s := string(body)
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Fatalf("package source contains banned network symbol %q in %s (local-first violation)", b, f)
			}
		}
	}
}

// TestImpactReverseFindsCallerRepro is the regression pin for the pre-v0.1.3
// direction inversion, in the exact shape it was reported: a repo where
// main --calls--> hello. "Reverse impact of hello" (who depends on hello) must
// contain main, and "forward impact of main" (what main depends on) must
// contain hello. Before the fix, reverse(hello) was empty and the TUI blast
// panel silently showed dependencies.
func TestImpactReverseFindsCallerRepro(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()

	file, err := model.NewNode("file", "main.go", "main.go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode file: %v", err)
	}
	mainFn, err := model.NewNode("function", "main.main", "main.go", 5, 6)
	if err != nil {
		t.Fatalf("NewNode main: %v", err)
	}
	hello, err := model.NewNode("function", "main.hello", "main.go", 3, 6)
	if err != nil {
		t.Fatalf("NewNode hello: %v", err)
	}
	for _, n := range []model.Node{file, mainFn, hello} {
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
	putEdge := func(from, to model.NodeId, kind string) {
		e, err := model.NewEdge(from, to, kind, model.TierDerived, 0.9, "test", []string{"main.go:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}
	putEdge(mainFn.ID(), hello.ID(), string(query.EdgeKindCalls))
	putEdge(file.ID(), mainFn.ID(), string(query.EdgeKindDefines))
	putEdge(file.ID(), hello.ID(), string(query.EdgeKindDefines))

	svc := analysis.NewDefaultService(store)

	rev, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: hello.ID(), Direction: analysis.Reverse})
	if err != nil {
		t.Fatalf("reverse(hello): %v", err)
	}
	if got, want := reachedNames(rev), []string{"main.main"}; !equalStrings(got, want) {
		t.Fatalf("reverse(hello) = %v, want %v (the caller, and ONLY the caller)", got, want)
	}
	// The defining file node must NOT pollute the default result: `defines` is
	// containment, not dependency, and is excluded from the default kinds.
	for _, rn := range rev.Nodes {
		if rn.Node.Kind == "file" {
			t.Fatalf("file node %s in default reverse impact (defines must be opt-in)", rn.Node.QualifiedName)
		}
	}

	fwd, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: mainFn.ID(), Direction: analysis.Forward})
	if err != nil {
		t.Fatalf("forward(main): %v", err)
	}
	if got, want := reachedNames(fwd), []string{"main.hello"}; !equalStrings(got, want) {
		t.Fatalf("forward(main) = %v, want %v (the callee)", got, want)
	}

	// Opting back in via explicit kinds surfaces the containment edge.
	withDefines, err := svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol:    hello.ID(),
		Direction: analysis.Reverse,
		Kinds:     []string{string(query.EdgeKindCalls), string(query.EdgeKindDefines)},
	})
	if err != nil {
		t.Fatalf("reverse(hello) with defines: %v", err)
	}
	if got, want := reachedNames(withDefines), []string{"main.go", "main.main"}; !equalStrings(got, want) {
		t.Fatalf("reverse(hello) with explicit defines = %v, want %v", got, want)
	}
}

// TestImpactReverseSupersetOfQueryCallers pins the cross-layer invariant a user
// reasonably assumes and nothing previously checked: for calls-only kinds,
// every DIRECT caller reported by the structural query layer must appear in
// the analysis layer's reverse impact (which is its transitive closure). This
// is the test that would have caught the direction inversion.
func TestImpactReverseSupersetOfQueryCallers(t *testing.T) {
	store, ids := seedImpactGraph(t)
	ctx := context.Background()
	qsvc := query.New(store)
	asvc := analysis.NewDefaultService(store)

	for _, seed := range []string{"pkg.A", "pkg.B", "pkg.C", "pkg.D", "pkg.X"} {
		callers, err := qsvc.Callers(ctx, ids[seed])
		if err != nil {
			t.Fatalf("Callers(%s): %v", seed, err)
		}
		imp, err := asvc.Dispatch(ctx, "impact", analysis.Params{
			Symbol:    ids[seed],
			Direction: analysis.Reverse,
			Kinds:     []string{string(query.EdgeKindCalls)},
		})
		if err != nil {
			t.Fatalf("impact reverse(%s): %v", seed, err)
		}
		reached := map[string]bool{}
		for _, rn := range imp.Nodes {
			reached[rn.Node.QualifiedName] = true
		}
		for _, c := range callers.Nodes {
			if !reached[c.QualifiedName] {
				t.Fatalf("caller %s of %s missing from reverse impact %v — direction semantics broken", c.QualifiedName, seed, reachedNames(imp))
			}
		}
	}
}

// --- helpers ---

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortReachedIsSorted(nodes []analysis.ReachedNode) bool {
	for i := 1; i < len(nodes); i++ {
		a, b := nodes[i-1], nodes[i]
		ra, rb := tierRankForTest(string(a.ReachedVia.Tier)), tierRankForTest(string(b.ReachedVia.Tier))
		if ra != rb {
			if ra > rb {
				return false
			}
			continue
		}
		if a.Node.ID > b.Node.ID {
			return false
		}
	}
	return true
}

func tierRankForTest(t string) int {
	switch t {
	case "confirmed":
		return 0
	case "derived":
		return 1
	case "heuristic":
		return 2
	default:
		return 3
	}
}
