package analysis_test

import (
	"bytes"
	"context"
	"errors"
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

// seedImpactGraph builds a deterministic synthetic graph in an in-memory store:
//
//	pkg.A --calls--> pkg.B --calls--> pkg.C
//	pkg.A --calls--> pkg.C            (A reaches C directly too)
//	pkg.B --calls--> pkg.D
//	pkg.D --calls--> pkg.B            (cycle B <-> D)
//	pkg.X                            (isolated)
//
// Forward (dependents, incoming edges) and Reverse (dependencies, outgoing
// edges) sets derived from this topology are the correctness oracle below.
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

func TestImpactForwardDependents(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// forward(C): everyone who transitively depends on C (incoming-edge closure)
	// = {A (A->C, A->B->C), B (B->C), D (D->B->C)}.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Forward,
	})
	if err != nil {
		t.Fatalf("Dispatch impact forward: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("outcome = %s, want found", res.Outcome)
	}
	got := reachedNames(res)
	want := []string{"pkg.A", "pkg.B", "pkg.D"}
	if !equalStrings(got, want) {
		t.Fatalf("forward(C) = %v, want %v (no false/missing members)", got, want)
	}
	// X must never appear (isolated, not a dependent of anything).
	for _, rn := range res.Nodes {
		if rn.Node.QualifiedName == "pkg.X" {
			t.Fatalf("isolated pkg.X falsely reached as dependent of C")
		}
	}
}

func TestImpactReverseDependencies(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// reverse(A): what A transitively depends on (outgoing-edge closure)
	// = {B (A->B), C (A->C, A->B->C), D (A->B->D, D<->B cycle)}.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.A"],
		Direction: analysis.Reverse,
	})
	if err != nil {
		t.Fatalf("Dispatch impact reverse: %v", err)
	}
	got := reachedNames(res)
	want := []string{"pkg.B", "pkg.C", "pkg.D"}
	if !equalStrings(got, want) {
		t.Fatalf("reverse(A) = %v, want %v", got, want)
	}
}

func TestImpactCycleSafeEachNodeOnce(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)

	// The B<->D cycle must terminate and list each node exactly once.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Forward,
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
		Direction: analysis.Forward,
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

	// forward(C) has 3 dependents; cap at 2 -> bounded, ranked, truncated flag set.
	res, err := svc.Dispatch(context.Background(), "impact", analysis.Params{
		Symbol:    ids["pkg.C"],
		Direction: analysis.Forward,
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

func TestImpactOutcomesTriState(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// not_found: unknown seed -> typed not-found result, never an error.
	res, err := svc.Dispatch(ctx, "impact", analysis.Params{
		Symbol:    model.NodeId("zzzzzzzzzzzzzzzz"),
		Direction: analysis.Forward,
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
		Direction: analysis.Forward,
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
		Direction: analysis.Forward,
	})
	if err != nil {
		t.Fatalf("C: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("C outcome = %s, want found", res.Outcome)
	}
}

func TestImpactDefaultDirectionForward(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	// Empty direction defaults to Forward.
	res, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["pkg.C"]})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if res.Outcome != query.OutcomeFound {
		t.Fatalf("default-direction outcome = %s, want found", res.Outcome)
	}
	if len(res.Nodes) != 3 {
		t.Fatalf("default direction forward(C) = %d nodes, want 3", len(res.Nodes))
	}
}

func TestImpactDeterminismRepeated(t *testing.T) {
	store, ids := seedImpactGraph(t)
	svc := analysis.NewDefaultService(store)
	ctx := context.Background()

	first, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["pkg.C"], Direction: analysis.Forward})
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
		res, err := svc.Dispatch(ctx, "impact", analysis.Params{Symbol: ids["pkg.C"], Direction: analysis.Forward})
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

	r1, err := svc1.Dispatch(ctx, "impact", analysis.Params{Symbol: ids1["pkg.A"], Direction: analysis.Reverse})
	if err != nil {
		t.Fatalf("svc1: %v", err)
	}
	r2, err := svc2.Dispatch(ctx, "impact", analysis.Params{Symbol: ids2["pkg.A"], Direction: analysis.Reverse})
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
