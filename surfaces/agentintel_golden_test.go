// P0 agent-intelligence golden characterization: the three labs operations
// (symbol_context, task_context, repo_overview) are byte-STABLE across a warm
// cache hit, a cache rebuild (EvictCache → reload from SQLite), and a freshly
// re-indexed store, and byte-IDENTICAL between the Mem and SQLite backends.
// This is the sibling of the twelve-stable-ops golden in
// characterization_golden_test.go — deliberately a separate list, because the
// stable twelve are frozen and these are labs.
package surfaces_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/surfaces/client"
)

var agentIntelOps = []string{"symbol_context", "task_context", "repo_overview", "test_impact", "change_impact", "hotspots", "search_hybrid", "architecture", "architecture_violations", "dead_code"}

// crossBackendAgentIntelOps excludes search_hybrid: its candidate RETRIEVAL
// rides the backend search port, whose recall semantics legitimately differ
// (MemStore matches case-insensitive substrings, SQLite matches FTS tokens).
// The deterministic re-rank orders whatever set was retrieved, so per-backend
// output is byte-stable (covered by the store-conditions golden below), but
// the retrieved sets themselves are not required to agree across backends.
var crossBackendAgentIntelOps = []string{"symbol_context", "task_context", "repo_overview", "test_impact", "change_impact", "hotspots", "architecture", "architecture_violations", "dead_code"}

// runAgentIntelOps drives the three labs ops through the shared surface client
// (rooted at the fixture so snippet reads resolve) and returns op→bytes.
func runAgentIntelOps(t *testing.T, store graphstore.Graphstore) map[string]string {
	t.Helper()
	ctx := context.Background()
	c := charClient(store).WithRepoRoot(charFixtureDir(t))

	hello := findFuncID(t, store, "Hello")

	out := map[string]string{}
	record := func(op string, b []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("op %s: %v", op, err)
		}
		out[op] = string(b)
	}

	sc, err := c.SymbolContext(ctx, client.SymbolContextParams{Symbol: hello})
	record("symbol_context", sc, err)

	tc, err := c.TaskContext(ctx, client.TaskContextParams{Task: "Hello"})
	record("task_context", tc, err)

	ov, err := c.RepoOverview(ctx, client.RepoOverviewParams{})
	record("repo_overview", ov, err)

	ti, err := c.TestImpact(ctx, client.TestImpactParams{Target: hello})
	record("test_impact", ti, err)

	ci, err := c.ChangeImpact(ctx, client.ChangeImpactParams{Target: hello})
	record("change_impact", ci, err)

	// No git provider in the golden harness: hotspots pins its typed
	// unavailable degradation byte-stably.
	hs, err := c.Hotspots(ctx, client.HotspotsParams{})
	record("hotspots", hs, err)

	sh, err := c.SearchHybrid(ctx, client.SearchHybridParams{Query: "hello greeter"})
	record("search_hybrid", sh, err)

	ar, err := c.Architecture(ctx, client.ArchitectureParams{})
	record("architecture", ar, err)

	av, err := c.ArchitectureViolations(ctx, client.ArchitectureViolationsParams{})
	record("architecture_violations", av, err)

	dc, err := c.DeadCode(ctx, client.DeadCodeParams{})
	record("dead_code", dc, err)

	if len(out) != len(agentIntelOps) {
		t.Fatalf("expected %d op outputs, got %d", len(agentIntelOps), len(out))
	}
	return out
}

// TestAgentIntel_ByteStableAcrossStoreConditions mirrors AC1 for the labs ops:
// warm cache hit, repeated warm hit, cache rebuild, and a fresh re-index all
// produce byte-identical output.
func TestAgentIntel_ByteStableAcrossStoreConditions(t *testing.T) {
	ctx := context.Background()

	stA, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	defer func() { _ = stA.Close() }()
	indexCharFixture(t, stA)
	warm := runAgentIntelOps(t, stA)
	warmAgain := runAgentIntelOps(t, stA)

	if err := stA.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	rebuilt := runAgentIntelOps(t, stA)

	stC, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	defer func() { _ = stC.Close() }()
	indexCharFixture(t, stC)
	fresh := runAgentIntelOps(t, stC)

	for _, op := range agentIntelOps {
		if warm[op] != warmAgain[op] {
			t.Errorf("op %s: warm repeat differs from warm", op)
		}
		if warm[op] != rebuilt[op] {
			t.Errorf("op %s: cache rebuild differs from warm", op)
		}
		if warm[op] != fresh[op] {
			t.Errorf("op %s: fresh re-index differs from warm", op)
		}
		if warm[op] == "" {
			t.Errorf("op %s: empty output", op)
		}
	}
}

// TestAgentIntel_MemAndSQLiteBackendsAgree mirrors AC2 for the labs ops: the
// in-memory and durable SQLite backends return byte-identical results.
func TestAgentIntel_MemAndSQLiteBackendsAgree(t *testing.T) {
	mem := graphstore.NewMemStore()
	defer func() { _ = mem.Close() }()
	indexCharFixture(t, mem)
	memOut := runAgentIntelOps(t, mem)

	sq, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	defer func() { _ = sq.Close() }()
	indexCharFixture(t, sq)
	sqOut := runAgentIntelOps(t, sq)

	for _, op := range crossBackendAgentIntelOps {
		if memOut[op] != sqOut[op] {
			t.Errorf("op %s: Mem and SQLite backends disagree:\n mem=%s\n sql=%s", op, memOut[op], sqOut[op])
		}
	}
}
