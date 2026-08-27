package client

// SW-228 (AX-08) — the evidence that decides WHICH operations may migrate.
//
// AX-06 argued its one canary's fitness in a comment. A bulk migration cannot:
// nine more operations, each with its own fixture behaviour, is exactly where
// "it looked fine" stops being a method. So the two things a migration has to
// prove are checked mechanically here, per operation, over the migrated set
// itself rather than over a hand-maintained list that could fall behind it:
//
//  1. CATALOG FITNESS — the five criteria the canary had to satisfy (declared
//     real ports, Labs tier, read-only permissions, deterministic output, a
//     registered executor handler). TestAX08_EveryMigratedOperationMeetsTheCriteria.
//
//  2. ARGUMENT FIDELITY — the criterion AX-06 could only assert. A byte-parity
//     case proves the two paths agree; it does NOT prove the arguments reached
//     the engine, because two calls that both ignore their arguments agree
//     perfectly. TestAX08_EveryMigratedOperationIsArgumentSensitive runs each
//     operation with TWO different argument values and requires the LEGACY bytes
//     to differ before it will accept the parity evidence for that operation.
//
// The second gate is why `memory` and `search_semantic` are named in the
// backlog: with no memory store wired, Direct.Memory short-circuits to
// ErrMemoryUnavailable before it reads Op or Scope, so mutating the argument
// changes nothing and the parity case proves the sentinel rather than the
// arguments. Neither is migrated by this story, and this test is what would
// catch a later story migrating them while that gap is still open.

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// TestAX08_EveryMigratedOperationMeetsTheCriteria checks the five catalog
// criteria against the SPEC of every migrated operation.
//
// It is deliberately driven by MigratedOperations() rather than by a literal
// list: a story that adds an id to the migrated set gets this check for free,
// and cannot add one that fails the criteria.
func TestAX08_EveryMigratedOperationMeetsTheCriteria(t *testing.T) {
	direct, _ := executorFixture(t)
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("Shadow: %v", err)
	}
	ops := MigratedOperations()
	if len(ops) < 2 {
		t.Fatalf("MigratedOperations returned %d ids — AX-08 migrates a set, not a canary", len(ops))
	}
	if !sort.StringsAreSorted(ops) {
		t.Errorf("MigratedOperations is not in canonical order: %v", ops)
	}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			contribution, err := contributionFor(direct, catalog, op)
			if err != nil {
				t.Fatalf("%q is dispatched through the executor but does not resolve a "+
					"contribution: %v", op, err)
			}
			if contribution.Tier != opcatalog.TierLabs {
				t.Fatalf("%q is tier %q — AX-08 migrates Labs only; Stable is AX-12's decision",
					op, contribution.Tier)
			}
			if contribution.Determinism != opcatalog.DeterminismDeterministic {
				t.Fatalf("%q declares determinism %q", op, contribution.Determinism)
			}
			for _, permission := range contribution.Permissions {
				if permission != opcatalog.PermissionGraphRead {
					t.Fatalf("%q requires permission %q — migrated operations are read-only",
						op, permission)
				}
			}
		})
	}
}

// fidelityFixture is executorFixture's graph plus the two shapes that graph
// cannot express, so that every migrated operation has an answer that MOVES.
//
// It is a SECOND fixture rather than an enrichment of the first, deliberately.
// executorFixture is the byte-parity fixture and is shared with the AX-06 and
// AX-10 evidence in surfaces/; growing it to satisfy this test would have moved
// bytes that other stories' tests were written against, for a reason that has
// nothing to do with what those tests check. Two small fixtures that each prove
// one thing beat one large fixture that proves both less clearly.
//
// What it adds, and why each addition is needed:
//
//   - a near-clone pair (s1/s2, four shared outbound edges and one extra) so
//     find_clones has a group at threshold 0.8 that DISAPPEARS at 0.95. Without
//     it the operation answers "no groups" for every config, and a config
//     argument that changes nothing cannot be shown to have been read. The shape
//     is the one engine/query's own TestFindClones_StructuralJaccard uses;
//   - annotated route/component nodes so framework_map derives facts at all. The
//     shared fixture records no annotations, which is exactly why SW-226
//     rejected framework_map as the canary: "byte parity on an empty envelope is
//     two empty results looking like agreement".
func fidelityFixture(t *testing.T) (*Direct, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}

	put := func(kind, qn, path string, line int, annotations ...string) model.NodeId {
		t.Helper()
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(annotations) > 0 {
			n = n.WithMeta(model.NewNodeMeta(annotations, nil))
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		return n.ID()
	}
	edge := func(from, to model.NodeId, kind string, tier model.ConfidenceTier, conf float64, reason string) {
		t.Helper()
		e, err := model.NewEdge(from, to, kind, tier, conf, reason, []string{"ev"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	// The executorFixture graph, so the operations that already have fidelity
	// evidence there keep answering the same shape of question.
	for _, name := range []string{"A", "B", "C", "D"} {
		ids[name] = put("function", "p."+name, "p/"+name+".go", 1)
	}
	edge(ids["A"], ids["B"], query.EdgeKindCalls, model.TierConfirmed, 1, "ab")
	edge(ids["B"], ids["C"], query.EdgeKindCalls, model.TierDerived, 0.8, "bc")
	edge(ids["A"], ids["C"], query.EdgeKindCalls, model.TierHeuristic, 0.4, "ac")
	edge(ids["D"], ids["B"], query.EdgeKindReferences, model.TierDerived, 0.7, "db")

	// find_clones: a structural near-clone pair whose grouping is threshold
	// sensitive (Jaccard 4/5 = 0.8).
	fn := put("function", "lib.fn", "lib/lib.go", 1)
	ty := put("type", "lib.Ty", "lib/lib.go", 2)
	va := put("variable", "lib.Va", "lib/lib.go", 3)
	co := put("constant", "lib.Co", "lib/lib.go", 4)
	me := put("method", "lib.Me", "lib/lib.go", 5)
	s1 := put("function", "app.s1", "app/s1.go", 10)
	s2 := put("function", "app.s2", "app/s2.go", 20)
	for _, target := range []model.NodeId{fn} {
		edge(s1, target, query.EdgeKindCalls, model.TierConfirmed, 1, "calls")
		edge(s2, target, query.EdgeKindCalls, model.TierConfirmed, 1, "calls")
	}
	for _, target := range []model.NodeId{ty, va, co} {
		edge(s1, target, query.EdgeKindReferences, model.TierConfirmed, 1, "references")
		edge(s2, target, query.EdgeKindReferences, model.TierConfirmed, 1, "references")
	}
	edge(s2, me, query.EdgeKindCalls, model.TierConfirmed, 1, "calls")

	// framework_map: annotated route and component nodes, so the operation has
	// facts to derive and a cap to apply to them.
	put("type", "shop.UserController", "src/main/java/shop/UserController.java", 10, "RestController")
	put("method", "shop.UserController.getUser", "src/main/java/shop/UserController.java", 14, "GetMapping")
	put("method", "shop.OrderListener.onOrder", "src/main/java/shop/OrderListener.java", 8, "KafkaListener")
	put("type", "shop.PricingService", "src/main/java/shop/PricingService.java", 5, "Service")

	direct := NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	return direct, ids
}

// fidelityPair is one operation invoked with two DIFFERENT argument values.
//
// `a` is the value the parity table already exercises; `b` differs from it in
// exactly the arguments this fixture can make observable. If the legacy bytes
// for a and b are equal, this fixture cannot tell whether the arguments were
// read at all, and the operation has no argument-fidelity evidence here.
type fidelityPair struct {
	operation string
	a, b      Arguments
	legacyA   func(ctx context.Context, c Client) ([]byte, error)
	legacyB   func(ctx context.Context, c Client) ([]byte, error)
}

func fidelityPairs(ids map[string]model.NodeId) []fidelityPair {
	b := string(ids["B"])
	c := string(ids["C"])
	return []fidelityPair{
		{
			operation: "dead_code",
			a:         &DeadCodeArgs{MaxItems: 3},
			b:         &DeadCodeArgs{MaxItems: 1},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.DeadCode(ctx, DeadCodeParams{MaxItems: 3})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.DeadCode(ctx, DeadCodeParams{MaxItems: 1})
			},
		},
		{
			operation: "compound",
			a:         &CompoundArgs{Query: "SEED p.A\nHOP out calls\n"},
			b:         &CompoundArgs{Query: "SEED p.B\nHOP out calls\n"},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.Compound(ctx, "SEED p.A\nHOP out calls\n")
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.Compound(ctx, "SEED p.B\nHOP out calls\n")
			},
		},
		{
			operation: "search_ast",
			a:         &SearchASTArgs{Pattern: `{"kind":"function"}`, Limit: 5},
			b:         &SearchASTArgs{Pattern: `{"kind":"function"}`, Limit: 1},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.SearchAST(ctx, `{"kind":"function"}`, 5)
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.SearchAST(ctx, `{"kind":"function"}`, 1)
			},
		},
		{
			operation: "find_clones",
			a:         &FindClonesArgs{Config: `{"threshold":0.8}`},
			b:         &FindClonesArgs{Config: `{"threshold":0.95}`},
			legacyA:   func(ctx context.Context, cl Client) ([]byte, error) { return cl.FindClones(ctx, `{"threshold":0.8}`) },
			legacyB:   func(ctx context.Context, cl Client) ([]byte, error) { return cl.FindClones(ctx, `{"threshold":0.95}`) },
		},
		{
			operation: "architecture",
			a:         &ArchitectureArgs{MaxItems: 3},
			b:         &ArchitectureArgs{MaxItems: 1},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.Architecture(ctx, ArchitectureParams{MaxItems: 3})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.Architecture(ctx, ArchitectureParams{MaxItems: 1})
			},
		},
		{
			operation: "architecture_violations",
			a:         &ArchitectureViolationsArgs{MaxItems: 3},
			b:         &ArchitectureViolationsArgs{MaxItems: 1},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.ArchitectureViolations(ctx, ArchitectureViolationsParams{MaxItems: 3})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.ArchitectureViolations(ctx, ArchitectureViolationsParams{MaxItems: 1})
			},
		},
		{
			operation: "framework_map",
			a:         &FrameworkMapArgs{MaxItems: 3},
			b:         &FrameworkMapArgs{MaxItems: 1},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.FrameworkMap(ctx, FrameworkMapParams{MaxItems: 3})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.FrameworkMap(ctx, FrameworkMapParams{MaxItems: 1})
			},
		},
		{
			operation: "repo_overview",
			a:         &RepoOverviewArgs{MaxItems: 3, Communities: true},
			b:         &RepoOverviewArgs{MaxItems: 1, Communities: false},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.RepoOverview(ctx, RepoOverviewParams{MaxItems: 3, Communities: true})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.RepoOverview(ctx, RepoOverviewParams{MaxItems: 1, Communities: false})
			},
		},
		{
			operation: "search_hybrid",
			a:         &SearchHybridArgs{Query: "p.B", MaxItems: 3},
			b:         &SearchHybridArgs{Query: "p.C", MaxItems: 3},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.SearchHybrid(ctx, SearchHybridParams{Query: "p.B", MaxItems: 3})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.SearchHybrid(ctx, SearchHybridParams{Query: "p.C", MaxItems: 3})
			},
		},
		{
			operation: "test_impact",
			a:         &TestImpactArgs{Target: b, Depth: 2, MaxItems: 3},
			b:         &TestImpactArgs{Target: c, Depth: 2, MaxItems: 3},
			legacyA: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.TestImpact(ctx, TestImpactParams{Target: b, Depth: 2, MaxItems: 3})
			},
			legacyB: func(ctx context.Context, cl Client) ([]byte, error) {
				return cl.TestImpact(ctx, TestImpactParams{Target: c, Depth: 2, MaxItems: 3})
			},
		},
	}
}

// TestAX08_EveryMigratedOperationIsArgumentSensitive is the gate described at
// the top of this file: a migrated operation must have a fixture invocation
// whose bytes MOVE when its arguments move, and the executor must reproduce
// both of them.
func TestAX08_EveryMigratedOperationIsArgumentSensitive(t *testing.T) {
	direct, ids := fidelityFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	ctx := context.Background()

	covered := map[string]bool{}
	for _, pair := range fidelityPairs(ids) {
		covered[pair.operation] = true
		t.Run(pair.operation, func(t *testing.T) {
			bytesA, errA := pair.legacyA(ctx, direct)
			bytesB, errB := pair.legacyB(ctx, direct)
			if errA != nil || errB != nil {
				t.Fatalf("the legacy path failed on this fixture (a: %v, b: %v) — a failing "+
					"call proves nothing about argument fidelity", errA, errB)
			}
			if bytes.Equal(bytesA, bytesB) {
				t.Fatalf("%q returned identical bytes for two different argument values, so this "+
					"fixture cannot show that the arguments were read at all. Byte parity on such "+
					"a case would be satisfied by an adapter that discards its arguments. Either "+
					"give the fixture a case whose answer moves, or take %q out of "+
					"MigratedOperations with a recorded reason.", pair.operation, pair.operation)
			}
			for _, side := range []struct {
				name  string
				args  Arguments
				want  []byte
				wantE error
			}{
				{"a", pair.a, bytesA, errA},
				{"b", pair.b, bytesB, errB},
			} {
				req, err := executor.NewRequest(side.args)
				if err != nil {
					t.Fatalf("NewRequest(%s): %v", side.name, err)
				}
				got, gotErr := executor.Execute(ctx, req)
				assertSameOutcome(t, "Execute("+side.name+")", side.want, side.wantE, got, gotErr)
			}
		})
	}

	var missing []string
	for _, op := range MigratedOperations() {
		if !covered[op] {
			missing = append(missing, op)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("these operations dispatch through the executor with no argument-fidelity "+
			"evidence: %v. Byte parity alone cannot distinguish an adapter that carries its "+
			"arguments from one that ignores them.", missing)
	}
	for op := range covered {
		if !isMigratedOperation(op) {
			t.Errorf("fidelity pair %q names an operation that does not dispatch through the "+
				"executor — a stale pair hides a missing one", op)
		}
	}
}

// TestAX08_ExcludedOperationsAreRejectedByName pins the exclusions this story
// recorded, so a later change that migrates one of them has to edit this list
// and answer for it. Both are adapted (their parity cases exist) and both are
// deliberately NOT dispatched.
func TestAX08_ExcludedOperationsAreRejectedByName(t *testing.T) {
	direct, _ := executorFixture(t)
	for _, tc := range []struct {
		operation string
		args      Arguments
		reason    string
	}{
		{"memory", &MemoryArgs{MemoryRequest{Op: "list"}}, "no argument-fidelity evidence: Direct.Memory short-circuits before reading Op"},
		{"search_semantic", &SemanticSearchArgs{Query: "p."}, "no argument-fidelity evidence for Limit on an embedder-free fixture"},
		{"agent_brief", &BriefArgs{Topic: "p.B"}, "Client.Brief returns two byte slices; the executor transports only the canonical one"},
		{"savings", &SavingsArgs{}, "determinism environment-dependent (ledger state)"},
		{"analyze", &AnalyzeArgs{AnalyzeParams{Name: "impact"}}, "determinism environment-dependent"},
		{"search", &SearchArgs{Query: "p."}, "Stable tier — AX-12 owns Stable migration"},
		{"impact", &ImpactArgs{Symbol: "p.B"}, "Stable tier — AX-12 owns Stable migration"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			if isMigratedOperation(tc.operation) {
				t.Fatalf("%q is dispatched through the executor, but SW-228 excluded it: %s",
					tc.operation, tc.reason)
			}
			if _, err := DispatchOperation(context.Background(), direct, tc.args); err == nil {
				t.Fatalf("DispatchOperation accepted %q, which is not a migrated operation", tc.operation)
			}
		})
	}
}
