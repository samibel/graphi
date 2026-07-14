// Package surfaces_test also holds the SW-110 (TEST-01) characterization
// baseline: it pins what graphi does TODAY so every later boundary rebuild is
// provably behavior-preserving. This file owns two of the six characterization
// surfaces:
//
//   - AC1 (golden outputs): the canonical serialized results of the 12 stable
//     read/agent ops on a pinned corpus fixture are byte-STABLE across a warm
//     cache hit, a cache rebuild (EvictCache → transparent reload from SQLite),
//     and a freshly re-indexed store. Reproducibility IS determinism, so equality
//     across independent rebuilds is the golden assertion (no wall-clock / no map
//     order may leak into the bytes).
//   - AC2 (backend conformance): the in-memory backend and the durable SQLite
//     backend return byte-identical canonical results for the same 12 ops, built
//     via the core/graphstore Mem/SQLite factories (the conformance seam).
//
// The fixture is the committed, frozen corpus/fixtures/go sample (a rich call
// graph: Hello, the Greeter hierarchy, the ChainA→ChainB→ChainC→Hello chain,
// taint source/sink, clone pairs) — reused rather than re-invented per the story.
package surfaces_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// twelveStableOps is the frozen list of the 12 stable operations (SCOPE-01 /
// spec §"The 12 stable operations"). `index` is characterized by the canonical
// graph bytes it produces; the other 11 are driven through the shared surface
// client (the honest surface path) so the golden captures real serialized output.
var twelveStableOps = []string{
	"index", "search", "definition", "callers", "callees", "references",
	"neighborhood", "impact", "agent_brief", "related_files", "explain_symbol",
	"change_risk",
}

// moduleRoot walks up from the test's working directory to the module root
// (the directory holding go.mod). Hermetic: it needs no go toolchain at runtime.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// charFixtureDir returns the pinned Go corpus fixture directory.
func charFixtureDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "corpus", "fixtures", "go")
}

// indexCharFixture ingests the pinned fixture into store via the SAME production
// ingest path cmd/graphi uses (NotebookParser over the default registry).
func indexCharFixture(t *testing.T, store graphstore.Graphstore) {
	t.Helper()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer func() { _ = ing.Close() }()
	if err := ing.IngestAll(context.Background(), charFixtureDir(t)); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
}

// charClient wires the read/agent surface client over store exactly as the
// default binary does (query + search + analysis; agent tools ride on the shared
// query/search Deps).
func charClient(store graphstore.Graphstore) *client.Direct {
	return client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
}

// findFuncID returns the deterministic node id of the function whose qualified
// name is (or ends with ".") name, chosen by lowest canonical id when several
// match — so the baseline never depends on map iteration order.
func findFuncID(t *testing.T, store graphstore.Graphstore, name string) string {
	t.Helper()
	ns, err := store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	var matches []model.Node
	for _, n := range ns {
		if n.Kind() != "function" {
			continue
		}
		if n.QualifiedName() == name || strings.HasSuffix(n.QualifiedName(), "."+name) {
			matches = append(matches, n)
		}
	}
	if len(matches) == 0 {
		t.Fatalf("function %q not found in indexed fixture", name)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID() < matches[j].ID() })
	return string(matches[0].ID())
}

// indexBytes renders the whole indexed graph to canonical model bytes — the
// characterization of the `index` op (the deterministic graph an index produces).
func indexBytes(t *testing.T, store graphstore.Graphstore) []byte {
	t.Helper()
	ctx := context.Background()
	ns, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	es, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	b, err := model.NewGraph(ns, es).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return b
}

// runTwelveOps runs every stable op against store and returns op→canonical bytes.
// It is the single characterization harness both AC1 (cross-condition stability)
// and AC2 (cross-backend conformance) compare over.
func runTwelveOps(t *testing.T, store graphstore.Graphstore) map[string]string {
	t.Helper()
	ctx := context.Background()
	c := charClient(store)

	hello := findFuncID(t, store, "Hello")
	chainA := findFuncID(t, store, "ChainA")

	out := map[string]string{}
	out["index"] = string(indexBytes(t, store))

	record := func(op string, b []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("op %s: %v", op, err)
		}
		out[op] = string(b)
	}

	sb, err := c.Search(ctx, "Hello", 20)
	record("search", sb, err)

	for _, op := range []string{"definition", "callers", "callees", "references"} {
		b, err := c.Query(ctx, op, hello, 0)
		record(op, b, err)
	}
	nb, err := c.Query(ctx, "neighborhood", chainA, 2)
	record("neighborhood", nb, err)

	ib, err := c.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: hello})
	record("impact", ib, err)

	bj, _, err := c.Brief(ctx, "Hello")
	record("agent_brief", bj, err)

	rf, err := c.RelatedFiles(ctx, chainA, "both", 10)
	record("related_files", rf, err)

	ex, err := c.ExplainSymbol(ctx, hello, 20)
	record("explain_symbol", ex, err)

	cr, err := c.ChangeRisk(ctx, hello, "", 20)
	record("change_risk", cr, err)

	if len(out) != len(twelveStableOps) {
		t.Fatalf("expected %d op outputs, got %d", len(twelveStableOps), len(out))
	}
	return out
}

// TestCharacterization_TwelveOps_ByteStableAcrossStoreConditions is AC1: the 12
// stable ops produce byte-identical output across a repeated warm cache hit, a
// cache rebuild (EvictCache → reload from SQLite), and a freshly re-indexed
// store. Any difference means a cache/rebuild path leaked nondeterminism.
func TestCharacterization_TwelveOps_ByteStableAcrossStoreConditions(t *testing.T) {
	ctx := context.Background()

	// Condition A: durable SQLite, warm cache (first read populates + hits cache).
	stA, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	defer func() { _ = stA.Close() }()
	indexCharFixture(t, stA)
	warm := runTwelveOps(t, stA)

	// Condition A': repeat on the same warm store — pure cache-hit reproducibility.
	warmAgain := runTwelveOps(t, stA)

	// Condition B: same store after a full cache eviction (rebuilt from SQLite).
	if err := stA.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	rebuilt := runTwelveOps(t, stA)

	// Condition C: an independent, freshly re-indexed SQLite store.
	stC, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	defer func() { _ = stC.Close() }()
	indexCharFixture(t, stC)
	fresh := runTwelveOps(t, stC)

	for _, op := range twelveStableOps {
		if warm[op] != warmAgain[op] {
			t.Errorf("op %q not stable across a repeated cache hit", op)
		}
		if warm[op] != rebuilt[op] {
			t.Errorf("op %q not stable across a cache rebuild (EvictCache):\n warm=%q\n rebuilt=%q", op, warm[op], rebuilt[op])
		}
		if warm[op] != fresh[op] {
			t.Errorf("op %q not stable across a fresh re-index:\n warm =%q\n fresh=%q", op, warm[op], fresh[op])
		}
	}
}

// searchResult is the subset of the canonical search payload the backend
// conformance check reasons about: the ordered matched node identities (the
// "canonical-ordered results") and the per-match rank (which is backend-specific).
type searchResult struct {
	Query   string `json:"query"`
	Matches []struct {
		NodeID string  `json:"node_id"`
		Rank   float64 `json:"rank"`
	} `json:"matches"`
}

func parseSearch(t *testing.T, b string) searchResult {
	t.Helper()
	var r searchResult
	if err := json.Unmarshal([]byte(b), &r); err != nil {
		t.Fatalf("decode search payload %q: %v", b, err)
	}
	return r
}

func searchNodeOrder(r searchResult) []string {
	ids := make([]string, len(r.Matches))
	for i, m := range r.Matches {
		ids[i] = m.NodeID
	}
	return ids
}

// TestCharacterization_TwelveOps_MemoryVsSQLiteConformance is AC2: the in-memory
// and SQLite backends (built via the core/graphstore conformance-seam factories)
// return identical canonical-ordered results for the 12 stable ops.
//
// CHARACTERIZED DIVERGENCE (documented, not omitted): 11 of the 12 ops are
// byte-identical across backends. The 12th, lexical `search`, agrees on the
// matched node SET and canonical ORDER, but its `rank` field is inherently
// backend-specific — SQLite ranks via FTS5 BM25 (a negative relevance score)
// while the in-memory backend has no FTS index and reports rank 0. The existing
// graphstore contract suite (TestContract_SearchNodes) likewise only asserts
// per-backend ordering/limit, never a cross-backend rank equality. This baseline
// records that boundary so a later selective-read change that touches ranking is
// a reviewed, intentional diff against this pinned fact.
func TestCharacterization_TwelveOps_MemoryVsSQLiteConformance(t *testing.T) {
	mem, err := graphstore.MemFactory("")
	if err != nil {
		t.Fatalf("MemFactory: %v", err)
	}
	defer func() { _ = mem.Close() }()
	indexCharFixture(t, mem)
	memOut := runTwelveOps(t, mem)

	sqlite, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	defer func() { _ = sqlite.Close() }()
	indexCharFixture(t, sqlite)
	sqliteOut := runTwelveOps(t, sqlite)

	for _, op := range twelveStableOps {
		if op == "search" {
			continue // handled below with the documented rank divergence
		}
		if memOut[op] != sqliteOut[op] {
			t.Errorf("op %q differs Memory vs SQLite:\n mem   =%q\n sqlite=%q", op, memOut[op], sqliteOut[op])
		}
	}

	// search: identical matched-node identities in identical canonical order …
	memSearch := parseSearch(t, memOut["search"])
	sqliteSearch := parseSearch(t, sqliteOut["search"])
	memOrder := searchNodeOrder(memSearch)
	sqliteOrder := searchNodeOrder(sqliteSearch)
	if len(memOrder) == 0 {
		t.Fatalf("search returned no matches on the pinned fixture; baseline needs a hit")
	}
	if strings.Join(memOrder, ",") != strings.Join(sqliteOrder, ",") {
		t.Errorf("search matched-node order differs Memory vs SQLite:\n mem   =%v\n sqlite=%v", memOrder, sqliteOrder)
	}
	// … and the CHARACTERIZED rank divergence: mem ranks are 0, SQLite FTS5 ranks
	// are non-zero. Pinning this makes the boundary an explicit, reviewable fact.
	for _, m := range memSearch.Matches {
		if m.Rank != 0 {
			t.Errorf("baseline expected in-memory rank 0 (no FTS), got %v — backend ranking changed", m.Rank)
		}
	}
	sqliteRanked := false
	for _, m := range sqliteSearch.Matches {
		if m.Rank != 0 {
			sqliteRanked = true
		}
	}
	if !sqliteRanked {
		t.Errorf("baseline expected SQLite FTS5 to report a non-zero rank; got all-zero — FTS ranking changed")
	}
}
