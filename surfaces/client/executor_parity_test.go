package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// This file is the core evidence for SW-224 AC-3 and AC-4: for EVERY adapted
// operation, the executor path and the legacy path return the same bytes and the
// same error, on one shared fixture graph, in both directions.
//
// "Both directions" is meant literally:
//
//	legacy → executor   NewRequest(args) turns typed legacy arguments into an
//	                    addressed Request.
//	executor → legacy   DecodeArguments(req) turns that Request back into the
//	                    typed arguments, and Execute calls the legacy method
//	                    with them.
//
// The round trip is closed at both ends: the decoded arguments re-encode to the
// same bytes as the request they came from, and invoking them directly returns
// the same bytes as Execute.

// executorFixture seeds a small graph and returns a Direct wired with the query,
// search and analysis services — but deliberately WITHOUT a ledger and without a
// memory store, so the capability-unavailable sentinels are live on this fixture
// instead of hypothetical.
func executorFixture(t *testing.T) (*Direct, map[string]model.NodeId) {
	t.Helper()
	store, ids := executorFixtureGraph(t)
	direct := NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	return direct, ids
}

// executorFixtureGraph seeds the shared four-node graph both fixtures below run
// against. It exists so the parity fixture can wire a DIFFERENT set of services
// over the SAME graph without duplicating the seed — two seeds that drifted
// apart would make the two fixtures silently incomparable.
func executorFixtureGraph(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}

	for _, name := range []string{"A", "B", "C", "D"} {
		n, err := model.NewNode("function", "p."+name, "p/"+name+".go", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		ids[name] = n.ID()
		nodes[name] = n
	}
	edge := func(from, to, kind string, tier model.ConfidenceTier, conf float64, reason string, ev []string) {
		t.Helper()
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, tier, conf, reason, ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	edge("A", "B", query.EdgeKindCalls, model.TierConfirmed, 1, "ab", []string{"e1"})
	edge("B", "C", query.EdgeKindCalls, model.TierDerived, 0.8, "bc", []string{"e2"})
	edge("A", "C", query.EdgeKindCalls, model.TierHeuristic, 0.4, "ac", []string{"e3"})
	edge("D", "B", query.EdgeKindReferences, model.TierDerived, 0.7, "db", []string{"e4"})

	return store, ids
}

// executorParityFixture is executorFixture plus the two OPTIONAL services whose
// absence made two parity cases prove nothing (SW-239).
//
// executorFixture deliberately wires neither, so the capability-unavailable
// sentinel and the semantic graceful skip stay live on it. But a case invoked
// against a service that is not wired never reaches the argument at all:
// Direct.Memory returns ErrMemoryUnavailable before it reads Op or Scope, and
// Direct.SemanticSearch returns the fixed graceful-skip document regardless of
// Limit. Both cases therefore passed for the wrong reason — mutating the
// argument left the parity green (backlog :864).
//
// So the byte-parity fixture wires:
//
//   - a memory store, seeded with entries that differ in scope, notebook and
//     tags, so a list/recall/export request can observably depend on all four
//     of the fields Direct.Memory actually reads;
//   - the deterministic, dependency-free embed.MockEmbedder over an in-memory
//     vector index holding every seeded node, so semantic search returns real
//     ranked hits and Limit observably truncates them. It is pure Go: no CGO,
//     no model files and no network, so the CGo-free and zero-egress gates are
//     untouched.
//
// Still deliberately absent: the savings ledger. The savings case is the one
// that must keep proving the capability-unavailable sentinel travels the
// adapter path unchanged.
func executorParityFixture(t *testing.T) (*Direct, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	graph, ids := executorFixtureGraph(t)
	enrichParityGraph(t, graph, ids)

	store, err := memory.NewMemStore(nil)
	if err != nil {
		t.Fatalf("memory.NewMemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Seeded so that scope, notebook and limit each select a DIFFERENT subset:
	// three entries match (project, nb), one matches (project, other) and one
	// matches (session, nb). A mutation of any one of them changes the answer.
	for _, seed := range []struct {
		scope, notebook, payload string
		tags                     []string
	}{
		{"project", "nb", "alpha", []string{"topic/one"}},
		{"project", "nb", "beta", []string{"topic/two"}},
		{"project", "nb", "gamma", []string{"topic/three"}},
		{"project", "other", "delta", []string{"topic/four"}},
		{"session", "nb", "epsilon", []string{"topic/five"}},
	} {
		if _, err := store.StoreMemoryWithProvenance(ctx, memory.ProvenanceInput{
			Scope:    seed.scope,
			Notebook: seed.notebook,
			Tags:     seed.tags,
			Payload:  seed.payload,
		}); err != nil {
			t.Fatalf("seed memory %q: %v", seed.payload, err)
		}
	}

	mock := embed.NewMockEmbedder(16)
	registry := embed.NewRegistry()
	if err := registry.Register(mock); err != nil {
		t.Fatalf("register mock embedder: %v", err)
	}
	index := embed.NewIndex()
	nodes, err := graph.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, n := range nodes {
		vecs, eerr := mock.Embed(ctx, []string{n.QualifiedName()})
		if eerr != nil {
			t.Fatalf("embed %s: %v", n.QualifiedName(), eerr)
		}
		index.Put(n.ID(), vecs[0])
	}
	searchSvc := search.New(graph).WithSemantic(registry, index, graph)
	direct := NewDirect(query.New(graph), searchSvc).
		WithAnalysis(analysis.NewDefaultService(graph)).
		WithMemory(store)

	return direct, ids
}

// enrichParityGraph adds the shapes the four-node base graph cannot express, so
// that arguments which the base graph could not distinguish become observable
// (SW-239). Each addition exists for a NAMED argument; nothing here is decoration:
//
//   - p.TestA / p.TestB in *_test.go files, calling p.A and p.B: test_impact's
//     Depth now selects a different set of test targets (p.TestB is one reverse
//     hop from p.B, p.TestA is two), instead of every depth yielding the same
//     "no test files known" answer.
//   - p.C → p.D: forward impact from p.B now reaches TWO nodes, so MaxNodes = 1
//     truncates instead of being a cap nothing reaches.
//   - four annotated Java symbols: framework_map's providers are annotation
//     tables keyed by source language, so a graph of Go nodes yields the empty
//     document at EVERY MaxItems. With these, MaxItems truncates.
//
// The base graph is deliberately left alone: it is shared with the AX-06 canary
// and migration fixtures, and widening it there would move evidence that has
// nothing to do with this story.
func enrichParityGraph(t *testing.T, store *graphstore.MemStore, ids map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()

	put := func(kind, qualified, path string, annotations []string) model.NodeId {
		t.Helper()
		n, err := model.NewNode(kind, qualified, path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(annotations) > 0 {
			n = n.WithMeta(model.NewNodeMeta(annotations, nil))
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		ids[qualified] = n.ID()
		return n.ID()
	}
	link := func(from, to model.NodeId, kind string) {
		t.Helper()
		e, err := model.NewEdge(from, to, kind, model.TierConfirmed, 1, "sw239", []string{"e-sw239"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	testA := put("function", "p.TestA", "p/A_test.go", nil)
	testB := put("function", "p.TestB", "p/B_test.go", nil)
	link(testA, ids["A"], query.EdgeKindCalls)
	link(testB, ids["B"], query.EdgeKindCalls)
	link(ids["C"], ids["D"], query.EdgeKindCalls)

	for _, java := range []struct{ qualified, annotation string }{
		{"com.x.Ctl", "RestController"},
		{"com.x.Svc", "Service"},
		{"com.x.Repo", "Repository"},
		{"com.x.Cfg", "Configuration"},
	} {
		put("class", java.qualified, "src/main/java/com/x/"+strings.TrimPrefix(java.qualified, "com.x.")+".java",
			[]string{java.annotation})
	}
}

// executorParityCase is one adapted operation invoked two INDEPENDENT ways.
//
// legacy is written out by hand as the Client method call a surface makes today.
// It deliberately does NOT go through the adapter: a baseline computed by
// calling the adapter's own invoke would move with the adapter, so a broken
// adapter would agree with itself and the test would prove nothing. (It did,
// briefly, during this story — a mutation that added 1 to every depth passed.)
type executorParityCase struct {
	operation string
	args      Arguments
	legacy    func(ctx context.Context, c Client) ([]byte, error)
}

// executorParityCases returns one invocation per adapted operation. Every entry
// is a REAL call against the fixture; nothing here is a placeholder that would
// make two empty results look like agreement.
func executorParityCases(ids map[string]model.NodeId) []executorParityCase {
	symbol := string(ids["B"])
	cases := []executorParityCase{
		{
			// SW-226's canary. It is in this table for the same reason as
			// everything else — an adapter without independent byte-parity
			// evidence is unproven — and MaxItems is deliberately non-zero so
			// the case proves the argument reaches the engine rather than
			// proving that two default-shaped calls agree.
			operation: "dead_code",
			args:      &DeadCodeArgs{MaxItems: 3},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.DeadCode(ctx, DeadCodeParams{MaxItems: 3})
			},
		},
		// SW-228 (AX-08): the six agent tools this story migrates. Every one is
		// a REAL call against the shared fixture, and every one carries a
		// non-default argument so the case proves the argument reaches the
		// engine rather than proving two default-shaped calls agree.
		{
			operation: "architecture",
			args:      &ArchitectureArgs{MaxItems: 3},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Architecture(ctx, ArchitectureParams{MaxItems: 3})
			},
		},
		{
			operation: "architecture_violations",
			args:      &ArchitectureViolationsArgs{MaxItems: 3},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.ArchitectureViolations(ctx, ArchitectureViolationsParams{MaxItems: 3})
			},
		},
		{
			operation: "framework_map",
			args:      &FrameworkMapArgs{MaxItems: 3},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.FrameworkMap(ctx, FrameworkMapParams{MaxItems: 3})
			},
		},
		{
			operation: "repo_overview",
			args:      &RepoOverviewArgs{MaxItems: 3, Communities: true},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.RepoOverview(ctx, RepoOverviewParams{MaxItems: 3, Communities: true})
			},
		},
		{
			operation: "search_hybrid",
			args:      &SearchHybridArgs{Query: "p.B", MaxItems: 3},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.SearchHybrid(ctx, SearchHybridParams{Query: "p.B", MaxItems: 3})
			},
		},
		{
			operation: "test_impact",
			args:      &TestImpactArgs{Target: symbol, Depth: 2, MaxItems: 3},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.TestImpact(ctx, TestImpactParams{Target: symbol, Depth: 2, MaxItems: 3})
			},
		},
		{
			operation: "search",
			args:      &SearchArgs{Query: "p.", Limit: 10},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.Search(ctx, "p.", 10) },
		},
		{
			operation: "search_semantic",
			args:      &SemanticSearchArgs{Query: "p.", Limit: 10},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.SemanticSearch(ctx, "p.", 10) },
		},
		{
			operation: "search_ast",
			args:      &SearchASTArgs{Pattern: `{"kind":"function"}`, Limit: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.SearchAST(ctx, `{"kind":"function"}`, 5)
			},
		},
		{
			operation: "find_clones",
			args:      &FindClonesArgs{},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.FindClones(ctx, "") },
		},
		{
			operation: "compound",
			args:      &CompoundArgs{Query: "SEED p.A\nHOP out calls\n"},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Compound(ctx, "SEED p.A\nHOP out calls\n")
			},
		},
		{
			operation: "impact",
			args:      &ImpactArgs{Symbol: symbol, Direction: "forward", MaxNodes: 10},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return AsStable(c).Impact(ctx, ImpactParams{Symbol: symbol, Direction: "forward", MaxNodes: 10})
			},
		},
		{
			operation: "analyze",
			args:      &AnalyzeArgs{AnalyzeParams{Name: "impact", Symbol: symbol, Direction: "forward", MaxNodes: 10}},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Analyze(ctx, AnalyzeParams{Name: "impact", Symbol: symbol, Direction: "forward", MaxNodes: 10})
			},
		},
		{
			operation: "agent_brief",
			args:      &BriefArgs{Topic: "p.B"},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				// Only the CANONICAL bytes; the Markdown rendering is a
				// presentation of them and stays on the surface (BriefArgs docs).
				canonical, _, err := c.Brief(ctx, "p.B")
				return canonical, err
			},
		},
		{
			operation: "explain_symbol",
			args:      &ExplainSymbolArgs{Symbol: symbol, MaxItems: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.ExplainSymbol(ctx, symbol, 5)
			},
		},
		{
			operation: "related_files",
			args:      &RelatedFilesArgs{Target: symbol, Direction: "both", MaxFiles: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.RelatedFiles(ctx, symbol, "both", 5)
			},
		},
		{
			operation: "change_risk",
			args:      &ChangeRiskArgs{Target: symbol, MaxItems: 5},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.ChangeRisk(ctx, symbol, "", 5)
			},
		},
		{
			operation: "savings",
			args:      &SavingsArgs{},
			legacy:    func(ctx context.Context, c Client) ([]byte, error) { return c.Savings(ctx) },
		},
		{
			// SW-239: the fixture now wires a memory store, so this case runs
			// a REAL list against seeded entries. Every field it carries
			// selects a different subset — scope and notebook each exclude a
			// seeded entry, and Limit truncates the three that remain to two —
			// so the case fails when any of them stops reaching the engine
			// (executor_argument_fidelity_test.go proves each one does).
			operation: "memory",
			args: &MemoryArgs{MemoryRequest{
				Op: "list", Scope: "project", Notebook: "nb", Limit: 2,
			}},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Memory(ctx, MemoryRequest{
					Op: "list", Scope: "project", Notebook: "nb", Limit: 2,
				})
			},
		},
	}
	// The ten structural query operations ride one adapter; exercising each id
	// separately is what proves the shared adapter is addressed per operation.
	for _, op := range query.Operations {
		depth := 0
		if op == query.OpNeighborhood {
			depth = 2
		}
		operation, wantDepth := op, depth
		cases = append(cases, executorParityCase{
			operation: operation,
			args:      &QueryArgs{Op: operation, Symbol: symbol, Depth: wantDepth},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Query(ctx, operation, symbol, wantDepth)
			},
		})
	}
	return cases
}

// TestExecutor_AdapterByteParity is AC-3 and AC-4: for every adapted operation,
// the adapter path and the legacy path agree byte for byte and error for error,
// in both directions.
func TestExecutor_AdapterByteParity(t *testing.T) {
	direct, ids := executorParityFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	ctx := context.Background()

	for _, tc := range executorParityCases(ids) {
		t.Run(tc.operation, func(t *testing.T) {
			// Legacy path: the method call a surface makes today, written out
			// independently of the adapter.
			wantBytes, wantErr := tc.legacy(ctx, direct)

			// legacy → executor.
			req, err := executor.NewRequest(tc.args)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if req.Operation != tc.operation {
				t.Fatalf("NewRequest addressed %q, want %q", req.Operation, tc.operation)
			}
			if req.Version == "" {
				t.Fatal("NewRequest left the contract version empty")
			}

			// executor → legacy.
			gotBytes, gotErr := executor.Execute(ctx, req)
			assertSameOutcome(t, "Execute", wantBytes, wantErr, gotBytes, gotErr)

			// The reverse adapter alone: decode the request back to typed
			// arguments and invoke the legacy method with THOSE. This is the
			// "and vice versa" half — it proves the decode direction carries
			// every argument, not just that Execute happens to work.
			decoded, err := executor.DecodeArguments(req)
			if err != nil {
				t.Fatalf("DecodeArguments: %v", err)
			}
			if decoded.Operation() != tc.operation {
				t.Fatalf("decoded arguments address %q, want %q", decoded.Operation(), tc.operation)
			}
			viaDecoded, viaDecodedErr := decoded.invoke(ctx, direct)
			assertSameOutcome(t, "decoded legacy call", wantBytes, wantErr, viaDecoded, viaDecodedErr)

			// And the round trip closes: re-encoding the decoded arguments
			// reproduces the request's argument bytes exactly, so nothing was
			// dropped, defaulted or renamed in between.
			reEncoded, err := executor.NewRequest(decoded)
			if err != nil {
				t.Fatalf("NewRequest(decoded): %v", err)
			}
			if !bytes.Equal(reEncoded.Arguments, req.Arguments) {
				t.Fatalf("argument round trip changed the payload:\n  first: %s\n  again: %s",
					req.Arguments, reEncoded.Arguments)
			}
			if reEncoded.Version != req.Version {
				t.Fatalf("version round trip: %q then %q", req.Version, reEncoded.Version)
			}
		})
	}
}

// assertSameOutcome compares bytes AND error identity. Comparing only bytes
// would let a path that fails where the other succeeds pass as "both empty".
func assertSameOutcome(t *testing.T, path string, wantBytes []byte, wantErr error, gotBytes []byte, gotErr error) {
	t.Helper()
	switch {
	case wantErr == nil && gotErr != nil:
		t.Fatalf("%s failed where the legacy path succeeded: %v", path, gotErr)
	case wantErr != nil && gotErr == nil:
		t.Fatalf("%s succeeded where the legacy path failed with: %v", path, wantErr)
	case wantErr != nil && gotErr != nil:
		if !errors.Is(gotErr, wantErr) || gotErr.Error() != wantErr.Error() {
			t.Fatalf("%s error = %v, legacy error = %v", path, gotErr, wantErr)
		}
		return
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("%s bytes differ from the legacy path\n  legacy (%d bytes): %s\n  adapter (%d bytes): %s",
			path, len(wantBytes), truncateForDiff(wantBytes), len(gotBytes), truncateForDiff(gotBytes))
	}
	if len(wantBytes) == 0 {
		t.Fatalf("%s: the legacy path produced no bytes and no error — this case proves nothing", path)
	}
}

func truncateForDiff(b []byte) string {
	const max = 400
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// TestExecutor_EveryAdaptedOperationHasParityEvidence closes the gap a
// representative-set story invites: an adapter added later without a parity case
// would otherwise ship unproven. The build fails instead.
func TestExecutor_EveryAdaptedOperationHasParityEvidence(t *testing.T) {
	direct, ids := executorParityFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	covered := map[string]bool{}
	for _, tc := range executorParityCases(ids) {
		if covered[tc.operation] {
			t.Errorf("operation %q has two parity cases", tc.operation)
		}
		covered[tc.operation] = true
	}
	var missing []string
	for _, id := range executor.Adapted() {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("adapted operations without byte-parity evidence: %v", missing)
	}
	for id := range covered {
		if _, ok := executor.adapters[id]; !ok {
			t.Errorf("parity case %q names an operation with no adapter", id)
		}
	}
}

// TestExecutor_CapabilityUnavailableIsPreservedExactly is the AC-4 half about
// capability-unavailable behaviour: the optional-service checks in Direct keep
// deciding availability, and the adapter path surfaces the SAME typed sentinel
// the surfaces render today. The executor adds no availability logic of its own,
// so it cannot start disagreeing with Direct about what is wired.
func TestExecutor_CapabilityUnavailableIsPreservedExactly(t *testing.T) {
	direct, _ := executorFixture(t) // no ledger, no memory store
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		args   Arguments
		legacy func(ctx context.Context, c Client) ([]byte, error)
		want   error
	}{
		{
			name:   "savings without a ledger",
			args:   &SavingsArgs{},
			legacy: func(ctx context.Context, c Client) ([]byte, error) { return c.Savings(ctx) },
			want:   ErrSavingsUnavailable,
		},
		{
			name: "memory without a store",
			args: &MemoryArgs{MemoryRequest{Op: "list"}},
			legacy: func(ctx context.Context, c Client) ([]byte, error) {
				return c.Memory(ctx, MemoryRequest{Op: "list"})
			},
			want: ErrMemoryUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, legacyErr := tc.legacy(ctx, direct)
			if !errors.Is(legacyErr, tc.want) {
				t.Fatalf("legacy path returned %v, want %v — the fixture no longer exercises this case", legacyErr, tc.want)
			}
			req, err := executor.NewRequest(tc.args)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			_, execErr := executor.Execute(ctx, req)
			if !errors.Is(execErr, tc.want) {
				t.Fatalf("Execute returned %v, want the legacy sentinel %v", execErr, tc.want)
			}
			if execErr.Error() != legacyErr.Error() {
				t.Fatalf("Execute error text %q differs from the legacy %q", execErr, legacyErr)
			}
		})
	}
}

// TestExecutor_GracefulSkipStaysGraceful pins the one optional service whose
// absence is NOT an error: semantic search returns a typed unavailable response
// with a nil error. An adapter that turned that into an error would change a
// documented graceful-skip contract, so it is asserted rather than assumed.
func TestExecutor_GracefulSkipStaysGraceful(t *testing.T) {
	direct, _ := executorFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	args := &SemanticSearchArgs{Query: "p.", Limit: 5}
	req, err := executor.NewRequest(args)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute returned an error for the graceful-skip path: %v", err)
	}
	var decoded struct {
		Available bool `json:"available"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("semantic response is not the typed engine response: %v (%s)", err, got)
	}
	if decoded.Available {
		t.Fatal("no embedder is wired, so the typed response must report available=false")
	}
}

// TestExecutorParity_SearchSemanticFourStates is the SW-265 AC-8 migration
// gate. It exercises both the hand-written legacy call and Executor.Execute ten
// times for each state; parity alone is insufficient, so each fixture also
// asserts the state and whether configured has real hits.
func TestExecutorParity_SearchSemanticFourStates(t *testing.T) {
	type fixture struct {
		name      string
		wantState embed.State
		available bool
		build     func(*testing.T) *Direct
	}
	withState := func(t *testing.T, state embed.State, reason string) *Direct {
		store := graphstore.NewMemStore()
		t.Cleanup(func() { _ = store.Close() })
		reg := embed.NewRegistry()
		if err := reg.Register(embed.NewMockEmbedder(8)); err != nil {
			t.Fatal(err)
		}
		service := search.New(store).WithSemantic(reg, embed.NewIndex(), store).
			WithSemanticState(search.SemanticState{State: state, Reason: reason})
		return NewDirect(query.New(store), service)
	}
	cases := []fixture{
		{name: "configured", wantState: embed.StateReady, available: true, build: func(t *testing.T) *Direct { direct, _ := executorParityFixture(t); return direct }},
		{name: "unavailable", wantState: embed.StateMissing, build: func(t *testing.T) *Direct { direct, _ := executorFixture(t); return direct }},
		{name: "stale", wantState: embed.StateStale, build: func(t *testing.T) *Direct { return withState(t, embed.StateStale, search.ReasonStale) }},
		{name: "corrupt", wantState: embed.StateCorrupt, build: func(t *testing.T) *Direct { return withState(t, embed.StateCorrupt, search.ReasonCorrupt) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			direct := tc.build(t)
			executor, err := NewExecutor(direct)
			if err != nil {
				t.Fatal(err)
			}
			req, err := executor.NewRequest(&SemanticSearchArgs{Query: "p.A", Limit: 3})
			if err != nil {
				t.Fatal(err)
			}
			var firstLegacy, firstExecutor []byte
			for run := 0; run < 10; run++ {
				legacy, legacyErr := direct.SemanticSearch(context.Background(), "p.A", 3)
				got, gotErr := executor.Execute(context.Background(), req)
				assertSameOutcome(t, "legacy/executor", legacy, legacyErr, got, gotErr)
				if run == 0 {
					firstLegacy = append([]byte(nil), legacy...)
					firstExecutor = append([]byte(nil), got...)
				} else if !bytes.Equal(firstLegacy, legacy) || !bytes.Equal(firstExecutor, got) {
					t.Fatalf("run %d varied:\nlegacy first=%s now=%s\nexecutor first=%s now=%s", run, firstLegacy, legacy, firstExecutor, got)
				}
			}
			var response struct {
				Available bool                 `json:"available"`
				State     embed.State          `json:"state"`
				Hits      []search.SemanticHit `json:"hits"`
			}
			if err := json.Unmarshal(firstExecutor, &response); err != nil {
				t.Fatal(err)
			}
			if response.State != tc.wantState || response.Available != tc.available {
				t.Fatalf("state=%s available=%v, want %s/%v: %s", response.State, response.Available, tc.wantState, tc.available, firstExecutor)
			}
			if tc.name == "configured" && len(response.Hits) == 0 {
				t.Fatalf("configured fixture is vacuous: no hits: %s", firstExecutor)
			}
		})
	}
}
