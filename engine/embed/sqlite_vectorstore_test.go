package embed_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
)

// A SQLite-backed VectorTable round-trips vectors durably: vectors written by one
// table handle are read back identically by a fresh handle opened from the SAME
// meta dir — the reload-after-restart contract, a pure local read with no
// re-embedding.
func TestSQLiteVectorTable_DurableRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	write, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open write table: %v", err)
	}
	want := []embed.Vector{
		{NodeID: model.NodeId("a"), Values: []float32{0, 1, 0, 0}},
		{NodeID: model.NodeId("b"), Values: []float32{1, 0, 0, 0}},
		{NodeID: model.NodeId("c"), Values: []float32{-1, 0, 0, 0}},
	}
	for _, v := range want {
		if err := write.Upsert(ctx, v); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	_ = write.Close()

	// Fresh process simulation: a brand-new handle on the same dir.
	read, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open read table: %v", err)
	}
	defer read.Close()
	got, err := read.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d vectors, want %d", len(got), len(want))
	}
	// Load returns canonical NodeId order (a, b, c).
	for i, w := range want {
		if got[i].NodeID != w.NodeID {
			t.Fatalf("row %d NodeID = %q, want %q", i, got[i].NodeID, w.NodeID)
		}
		if len(got[i].Values) != len(w.Values) {
			t.Fatalf("row %d dim = %d, want %d", i, len(got[i].Values), len(w.Values))
		}
		for j := range w.Values {
			if got[i].Values[j] != w.Values[j] {
				t.Fatalf("row %d comp %d = %v, want %v", i, j, got[i].Values[j], w.Values[j])
			}
		}
	}
}

// A changed/absent embedder identity invalidates stale vectors: Load scoped to a
// DIFFERENT embedder_id reads zero rows rather than mixing embedding spaces.
func TestSQLiteVectorTable_EmbedderInvalidatesStale(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	write, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := write.Upsert(ctx, embed.Vector{NodeID: model.NodeId("a"), Values: []float32{1, 0, 0, 0}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	_ = write.Close()

	// A different embedder identity reads zero rows (stale vectors invalidated).
	other, err := embed.OpenSQLiteVectorTable(ctx, dir, "ollama:nomic-embed-text", 4)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	defer other.Close()
	got, err := other.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("changed embedder loaded %d stale vectors, want 0", len(got))
	}
}

// TestSQLiteVectorTable_DeleteExceptReplaceSet pins the SW-260 replace-set
// contract on the table seam itself: DeleteExcept removes every row in this
// embedder's scope whose NodeId is not in keep, leaves every kept row intact,
// and NEVER touches rows that belong to a different embedder sharing the same
// sidecar. keep = nil deletes the whole scope.
func TestSQLiteVectorTable_DeleteExceptReplaceSet(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	write, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Three rows for the active embedder ("mock"); one for an unrelated
	// embedder that must survive every delete.
	for _, id := range []model.NodeId{"a", "b", "c"} {
		if err := write.Upsert(ctx, embed.Vector{NodeID: id, Values: []float32{1, 0, 0, 0}}); err != nil {
			t.Fatalf("seed Upsert %s: %v", id, err)
		}
	}
	_ = write.Close()

	// A second embedder sharing the sidecar writes one row, which DeleteExcept
	// must NEVER touch (the replace-set scope is this table's embedder_id).
	other, err := embed.OpenSQLiteVectorTable(ctx, dir, "ollama:nomic-embed-text", 4)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	if err := other.Upsert(ctx, embed.Vector{NodeID: model.NodeId("o"), Values: []float32{0, 1, 0, 0}}); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	_ = other.Close()

	mock, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 4)
	if err != nil {
		t.Fatalf("reopen mock: %v", err)
	}
	defer mock.Close()

	// Keep "b" only — DeleteExcept must drop "a" and "c" but NEVER "o".
	if err := mock.DeleteExcept(ctx, []model.NodeId{model.NodeId("b")}); err != nil {
		t.Fatalf("DeleteExcept: %v", err)
	}
	got, err := mock.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].NodeID != model.NodeId("b") {
		t.Fatalf("mock rows after DeleteExcept = %+v, want exactly {b}", got)
	}
	// The other embedder's row survives the deletion.
	otherReload, err := embed.OpenSQLiteVectorTable(ctx, dir, "ollama:nomic-embed-text", 4)
	if err != nil {
		t.Fatalf("reload other: %v", err)
	}
	defer otherReload.Close()
	otherRows, err := otherReload.Load(ctx)
	if err != nil {
		t.Fatalf("other Load: %v", err)
	}
	if len(otherRows) != 1 || otherRows[0].NodeID != model.NodeId("o") {
		t.Fatalf("other rows after DeleteExcept = %+v, want exactly {o} (replace-set must be embedder-scoped)", otherRows)
	}

	// A nil keep deletes every row in this scope (the empty-set reset path).
	if err := mock.DeleteExcept(ctx, nil); err != nil {
		t.Fatalf("DeleteExcept(nil): %v", err)
	}
	if rows, _ := mock.Load(ctx); len(rows) != 0 {
		t.Fatalf("rows after DeleteExcept(nil) = %d, want 0", len(rows))
	}
	otherRows2, _ := otherReload.Load(ctx)
	if len(otherRows2) != 1 || otherRows2[0].NodeID != model.NodeId("o") {
		t.Fatalf("other rows after DeleteExcept(nil) = %+v, want exactly {o}", otherRows2)
	}
}

// TestSQLiteVectorTable_DeleteExceptAboveVariableLimit pins the SW-260
// review-round-2 MAJOR-2 contract: DeleteExcept must remain correct when the
// keep set exceeds SQLite's SQLITE_MAX_VARIABLE_NUMBER (32,766 in the pinned
// modernc.org/sqlite build). The previous implementation built one SQL
// parameter per kept node plus one for embedder_id, so any repository with
// ≥32,765 embedded nodes would fail AFTER every Upsert had committed — the
// worst possible moment. The bounded-prune path materialises the keep set
// into a TEMP table and issues a single NOT-IN-SELECT delete, so it is
// correct under arbitrarily large keep sets AND it must remain correct
// under partial failure (the transaction wraps the whole prune).
//
// The fixture seeds 40,000 rows for the active embedder and passes a keep
// set of 40,000 ids — both numbers are well above the variable cap. The
// assertion: every row survives (the keep set covers every seeded row), the
// table is not half-pruned (the transaction made the prune atomic).
// Cross-embedder inertness is covered separately by
// TestGenerateAndPersist_ZeroNodesPrunesScope; this fixture seeds one
// embedder only.
func TestSQLiteVectorTable_DeleteExceptAboveVariableLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("large-fixture test")
	}
	ctx := context.Background()
	dir := t.TempDir()

	const n = 40_000 // > SQLITE_MAX_VARIABLE_NUMBER (32,766)
	dim := 4

	table, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", dim)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = table.Close() })

	// Seed n rows: id "row-<7-digit-zero-padded-index>" so the lexicographic
	// order matches the seeded order. We commit in batches to keep the
	// fixture cheap; the table does not need real vectors per row — the
	// delete-except contract is shape-only (one row per node id).
	const seedBatch = 1000
	for start := 0; start < n; start += seedBatch {
		end := start + seedBatch
		if end > n {
			end = n
		}
		for i := start; i < end; i++ {
			id := model.NodeId(fmt.Sprintf("row-%07d", i))
			vec := make([]float32, dim)
			vec[i%dim] = 1
			if err := table.Upsert(ctx, embed.Vector{NodeID: id, Values: vec}); err != nil {
				t.Fatalf("seed Upsert %s: %v", id, err)
			}
		}
	}
	pre, err := table.Load(ctx)
	if err != nil {
		t.Fatalf("pre Load: %v", err)
	}
	if len(pre) != n {
		t.Fatalf("precondition: seeded %d rows, table has %d", n, len(pre))
	}

	// Build the full keep set (every seeded id survives this prune).
	keep := make([]model.NodeId, 0, n)
	for i := 0; i < n; i++ {
		keep = append(keep, model.NodeId(fmt.Sprintf("row-%07d", i)))
	}
	if err := table.DeleteExcept(ctx, keep); err != nil {
		t.Fatalf("DeleteExcept(keep=%d): %v", n, err)
	}
	post, err := table.Load(ctx)
	if err != nil {
		t.Fatalf("post Load: %v", err)
	}
	if len(post) != n {
		t.Fatalf("post-DeleteExcept rows = %d, want %d (every kept row must survive)", len(post), n)
	}
	for i, v := range post {
		want := model.NodeId(fmt.Sprintf("row-%07d", i))
		if v.NodeID != want {
			t.Fatalf("post row %d = %s, want %s (deterministic order)", i, v.NodeID, want)
		}
	}

	// The reverse case: keep is empty (the bounded prune must ALSO handle
	// >cap-sized scopes when the contract is "delete everything in scope").
	// The rows survive from the previous phase (the keep path left everything
	// intact), so no re-seed is needed; we ask DeleteExcept to drop all but ten.
	if err := table.DeleteExcept(ctx, nil); err != nil {
		t.Fatalf("DeleteExcept(nil) on large scope: %v", err)
	}
	if rows, _ := table.Load(ctx); len(rows) != 0 {
		t.Fatalf("rows after DeleteExcept(nil) on large scope = %d, want 0", len(rows))
	}

	// Third case: keep a strict subset (10 ids) when the SCOPE is large.
	// Re-seed n rows, keep only 10, expect n - 10 deletions.
	for start := 0; start < n; start += seedBatch {
		end := start + seedBatch
		if end > n {
			end = n
		}
		for i := start; i < end; i++ {
			id := model.NodeId(fmt.Sprintf("row-%07d", i))
			vec := make([]float32, dim)
			vec[i%dim] = 1
			if err := table.Upsert(ctx, embed.Vector{NodeID: id, Values: vec}); err != nil {
				t.Fatalf("reseed Upsert %s: %v", id, err)
			}
		}
	}
	smallKeep := keep[:10]
	if err := table.DeleteExcept(ctx, smallKeep); err != nil {
		t.Fatalf("DeleteExcept(keep=10) on scope=%d: %v", n, err)
	}
	after, err := table.Load(ctx)
	if err != nil {
		t.Fatalf("after subset DeleteExcept Load: %v", err)
	}
	if len(after) != 10 {
		t.Fatalf("subset DeleteExcept rows = %d, want 10", len(after))
	}
	for _, v := range after {
		want := false
		for _, k := range smallKeep {
			if v.NodeID == k {
				want = true
				break
			}
		}
		if !want {
			t.Errorf("subset DeleteExcept kept an unexpected id %s", v.NodeID)
		}
	}
}

// Determinism: indexing the same nodes twice with the deterministic mock embedder
// produces IDENTICAL durable vectors AND identical ranked hits — the double-index
// equality contract (story AC: "determinism").
func TestGenerateAndPersist_DoubleIndexEquality(t *testing.T) {
	ctx := context.Background()
	nodes := mustNodes(t)
	mock := embed.NewMockEmbedder(16)

	index := func() (*embed.Index, *embed.MemVectorTable) {
		reg := embed.NewRegistry()
		reg.Register(mock)
		ix := embed.NewIndex()
		table := embed.NewMemVectorTable()
		res, err := embed.GenerateAndPersist(ctx, reg, nodes, embed.V1DocumentSource{}, ix, table)
		if err != nil {
			t.Fatalf("GenerateAndPersist: %v", err)
		}
		if !res.Configured || res.Embedded != len(nodes) {
			t.Fatalf("unexpected result: %+v", res)
		}
		return ix, table
	}

	ix1, t1 := index()
	ix2, t2 := index()

	// Durable vectors are byte-identical across the two passes.
	v1, _ := t1.Load(ctx)
	v2, _ := t2.Load(ctx)
	if len(v1) != len(v2) || len(v1) != len(nodes) {
		t.Fatalf("vector counts differ: %d vs %d (want %d)", len(v1), len(v2), len(nodes))
	}
	for i := range v1 {
		if v1[i].NodeID != v2[i].NodeID {
			t.Fatalf("vector %d NodeID differs: %q vs %q", i, v1[i].NodeID, v2[i].NodeID)
		}
		for j := range v1[i].Values {
			if v1[i].Values[j] != v2[i].Values[j] {
				t.Fatalf("vector %d comp %d differs", i, j)
			}
		}
	}

	// Ranked hits are identical for the same query vector.
	q, _ := mock.Embed(ctx, []string{embed.NodeText(nodes[0])})
	h1 := ix1.Search(q[0], 0)
	h2 := ix2.Search(q[0], 0)
	if len(h1) != len(h2) {
		t.Fatalf("hit counts differ: %d vs %d", len(h1), len(h2))
	}
	for i := range h1 {
		if h1[i].NodeID != h2[i].NodeID || h1[i].Score != h2[i].Score {
			t.Fatalf("hit %d differs: %+v vs %+v", i, h1[i], h2[i])
		}
	}
	// The exact-match node ranks first (cosine 1.0).
	if h1[0].NodeID != nodes[0].ID() {
		t.Fatalf("top hit = %q, want %q", h1[0].NodeID, nodes[0].ID())
	}
}

// Reload reproduces the SAME ranking as the in-memory index it was persisted from,
// WITHOUT re-embedding: Rebuild from the durable table reads local rows only.
func TestGenerateAndPersist_ReloadMatchesInMemory(t *testing.T) {
	ctx := context.Background()
	nodes := mustNodes(t)
	dir := t.TempDir()
	mock := embed.NewMockEmbedder(16)
	reg := embed.NewRegistry()
	reg.Register(mock)

	// Generate into an in-memory index + durable SQLite table.
	liveIndex := embed.NewIndex()
	table, err := embed.OpenSQLiteVectorTable(ctx, dir, mock.ID(), mock.Dim())
	if err != nil {
		t.Fatalf("open table: %v", err)
	}
	if _, err := embed.GenerateAndPersist(ctx, reg, nodes, embed.V1DocumentSource{}, liveIndex, table); err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}
	_ = table.Close()

	// Simulate a restart: a fresh table handle + a fresh index Rebuilt from durable
	// storage. A failEmbedder would fail if Rebuild re-embedded; Rebuild never
	// touches an embedder, so reload is a pure local read by construction.
	reloadTable, err := embed.OpenSQLiteVectorTable(ctx, dir, mock.ID(), mock.Dim())
	if err != nil {
		t.Fatalf("reopen table: %v", err)
	}
	defer reloadTable.Close()
	reloaded := embed.NewIndex()
	if err := reloaded.Rebuild(ctx, reloadTable); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if reloaded.Len() != liveIndex.Len() {
		t.Fatalf("reloaded Len = %d, want %d", reloaded.Len(), liveIndex.Len())
	}

	q, _ := mock.Embed(ctx, []string{embed.NodeText(nodes[1])})
	live := liveIndex.Search(q[0], 0)
	rl := reloaded.Search(q[0], 0)
	if len(live) != len(rl) {
		t.Fatalf("hit counts differ after reload: %d vs %d", len(live), len(rl))
	}
	for i := range live {
		if live[i].NodeID != rl[i].NodeID || live[i].Score != rl[i].Score {
			t.Fatalf("reload ranking diverged at %d: %+v vs %+v", i, live[i], rl[i])
		}
	}
}

// Graceful skip: with an unconfigured registry, GenerateAndPersist embeds nothing,
// persists nothing, and reports Configured=false — no error, no dial.
func TestGenerateAndPersist_GracefulSkip(t *testing.T) {
	ctx := context.Background()
	nodes := mustNodes(t)
	table := embed.NewMemVectorTable()
	res, err := embed.GenerateAndPersist(ctx, embed.NewRegistry(), nodes, embed.V1DocumentSource{}, embed.NewIndex(), table)
	if err != nil {
		t.Fatalf("GenerateAndPersist error on graceful-skip path: %v", err)
	}
	if res.Configured {
		t.Fatal("Configured = true with no embedder, want false")
	}
	if res.Embedded != 0 {
		t.Fatalf("Embedded = %d on graceful-skip path, want 0", res.Embedded)
	}
	if got, _ := table.Load(ctx); len(got) != 0 {
		t.Fatalf("graceful skip persisted %d vectors, want 0", len(got))
	}
}

// TestGenerateAndPersist_ZeroNodesPrunesScope pins the SW-260 review-round-2
// MAJOR-1 contract: a configured embedder running over ZERO nodes is a
// successful pass over an emptied graph — it must clear the embedder's
// persisted scope, not leave every prior vector in place. The persisted set
// equals the documents just embedded; with zero documents just embedded, the
// persisted set must be empty. The nil-source guard must still precede this
// path (a configured embedder with zero nodes and a nil DocumentSource
// errors, never silently no-ops); the unconfigured-registry graceful skip
// must still come first (a no-op returns Configured=false).
func TestGenerateAndPersist_ZeroNodesPrunesScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mock := embed.NewMockEmbedder(8)
	reg := embed.NewRegistry()
	reg.Register(mock)

	// Seed the durable sidecar with stale rows as if a v1 pass had embedded
	// every node of a now-emptied repo. The post-pass table must be empty.
	file, _ := model.NewNode(parse.KindFile, "shop/cart.go", "shop/cart.go", 1, 1)
	pkg, _ := model.NewNode(parse.KindPackage, "com.x", "", 0, 0)
	sym1, _ := model.NewNode("function", "shop.Price", "shop/price.go", 3, 1)
	sym2, _ := model.NewNode("function", "shop.Total", "shop/price.go", 7, 1)
	stale := []model.Node{file, pkg, sym1, sym2}
	table, err := embed.OpenSQLiteVectorTable(ctx, dir, mock.ID(), mock.Dim())
	if err != nil {
		t.Fatalf("open table: %v", err)
	}
	for _, n := range stale {
		vec, _ := mock.Embed(ctx, []string{embed.NodeText(n)})
		if err := table.Upsert(ctx, embed.Vector{NodeID: n.ID(), Values: vec[0]}); err != nil {
			t.Fatalf("seed Upsert %s: %v", n.QualifiedName(), err)
		}
	}
	pre, _ := table.Load(ctx)
	if len(pre) != len(stale) {
		t.Fatalf("precondition: seeded %d rows, table has %d", len(stale), len(pre))
	}

	// Pass over ZERO nodes: the persisted set for this embedder must be
	// empty after the pass, the result must report Configured=true (an
	// embedder IS active), and Embedded must remain 0.
	res, err := embed.GenerateAndPersist(ctx, reg, nil, embed.V1DocumentSource{}, embed.NewIndex(), table)
	if err != nil {
		t.Fatalf("GenerateAndPersist(zero nodes): %v", err)
	}
	if !res.Configured {
		t.Fatalf("Configured = false on zero-node pass with an active embedder, want true (zero nodes is NOT a graceful skip)")
	}
	if res.Embedded != 0 || res.Skipped != 0 {
		t.Errorf("zero-node result = %+v, want Embedded=0 Skipped=0", res)
	}
	after, err := table.Load(ctx)
	if err != nil {
		t.Fatalf("Load after zero-node pass: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("zero-node pass left %d vectors in the embedder scope, want 0 (the persisted set must equal the documents just embedded)", len(after))
	}

	// The nil-source guard still precedes the zero-node path: a configured
	// embedder with zero nodes AND a nil DocumentSource errors rather than
	// silently pruning. The empty-scope reset only runs when the caller
	// actually provided a source.
	if _, err := embed.GenerateAndPersist(ctx, reg, nil, nil, embed.NewIndex(), table); err == nil {
		t.Error("nil DocumentSource with zero nodes must error (the guard precedes the prune)")
	}

	// The unconfigured-registry graceful skip still comes first: a fresh,
	// empty registry returns Configured=false and does NOT touch the table
	// (the table may have rows from a different embedder, or rows the
	// graceful skip has no jurisdiction over).
	other := embed.NewMockEmbedder(8)
	regOther := embed.NewRegistry()
	regOther.Register(other)
	tableOther, err := embed.OpenSQLiteVectorTable(ctx, dir, "mock", 8) // different embedder
	if err != nil {
		t.Fatalf("open other table: %v", err)
	}
	for _, n := range stale {
		vec, _ := other.Embed(ctx, []string{embed.NodeText(n)})
		if err := tableOther.Upsert(ctx, embed.Vector{NodeID: n.ID(), Values: vec[0]}); err != nil {
			t.Fatalf("seed other %s: %v", n.QualifiedName(), err)
		}
	}
	otherBefore, _ := tableOther.Load(ctx)
	emptyReg := embed.NewRegistry()
	resGraceful, err := embed.GenerateAndPersist(ctx, emptyReg, nil, nil, embed.NewIndex(), tableOther)
	if err != nil {
		t.Fatalf("GenerateAndPersist(unconfigured): %v", err)
	}
	if resGraceful.Configured {
		t.Fatalf("Configured = true with an empty registry, want false")
	}
	otherAfter, _ := tableOther.Load(ctx)
	if len(otherAfter) != len(otherBefore) {
		t.Fatalf("unconfigured-registry graceful skip touched the table (%d → %d rows); the graceful skip must be inert", len(otherBefore), len(otherAfter))
	}
}

// TestGenerateAndPersist_ReplaceSetDropsExcluded is the SW-260 review-round-1
// regression: a v1-shaped durable sidecar that holds vectors for a
// file/package/external/generated node must, after a v2 generation pass that
// excludes that node, no longer serve a vector for it. The pass replaces the
// persisted set with the documents it just embedded, so a re-index over an
// existing v1 sidecar cannot leak an excluded node's stale vector.
func TestGenerateAndPersist_ReplaceSetDropsExcluded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mock := embed.NewMockEmbedder(8)
	reg := embed.NewRegistry()
	reg.Register(mock)

	// Build a small node set covering every kind the v2 builder is expected to
	// exclude: a file node, a package node, an external node, and a generated
	// node (matches engine/classify.IsGeneratedPath). Two real symbol nodes are
	// also present so the post-pass persisted set has a non-empty "kept" side.
	file, _ := model.NewNode(parse.KindFile, "shop/cart.go", "shop/cart.go", 1, 1)
	pkg, _ := model.NewNode(parse.KindPackage, "com.x", "", 0, 0)
	ext, _ := model.NewNode(parse.KindExternal, "database/sql.DB.Query", "", 0, 0)
	gen, _ := model.NewNode("function", "v.Gen", "gen/api.gen.go", 4, 1)
	sym1, _ := model.NewNode("function", "shop.Price", "shop/price.go", 3, 1)
	sym2, _ := model.NewNode("function", "shop.Total", "shop/price.go", 7, 1)
	excluded := []model.Node{file, pkg, ext, gen}
	kept := []model.Node{sym1, sym2}
	all := append(append([]model.Node{}, excluded...), kept...)

	// Seed the durable sidecar as if a v1 pass had embedded every node — each
	// excluded kind is present, and the durable table only knows vectors, not
	// the v1/v2 shape (the bug surfaces because the v2 Load returns every
	// row the embedder wrote, regardless of node kind).
	table, err := embed.OpenSQLiteVectorTable(ctx, dir, mock.ID(), mock.Dim())
	if err != nil {
		t.Fatalf("open table: %v", err)
	}
	for _, n := range all {
		vec, _ := mock.Embed(ctx, []string{embed.NodeText(n)})
		if err := table.Upsert(ctx, embed.Vector{NodeID: n.ID(), Values: vec[0]}); err != nil {
			t.Fatalf("seed Upsert %s: %v", n.QualifiedName(), err)
		}
	}
	before, _ := table.Load(ctx)
	if len(before) != len(all) {
		t.Fatalf("precondition: v1-style seed has %d rows, want %d", len(before), len(all))
	}

	// v2 DocumentSource: every excluded node returns (zero, false); the real
	// symbols return their v2 document.
	docs := keepOnly(kept)
	if _, err := embed.GenerateAndPersist(ctx, reg, all, docs, embed.NewIndex(), table); err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}

	after, err := table.Load(ctx)
	if err != nil {
		t.Fatalf("Load after pass: %v", err)
	}
	if len(after) != len(kept) {
		t.Fatalf("post-pass rows = %d, want %d (every excluded node must be removed)", len(after), len(kept))
	}
	keptIDs := map[model.NodeId]struct{}{}
	for _, n := range kept {
		keptIDs[n.ID()] = struct{}{}
	}
	for _, v := range after {
		if _, ok := keptIDs[v.NodeID]; !ok {
			t.Errorf("post-pass row for %s must not survive (excluded by the v2 builder)", v.NodeID)
		}
	}
	// And every kept symbol is still in the table (Upsert+DeleteExcept compose
	// to a no-op on the kept set; we never want a delete-after-upsert to drop
	// something we did embed).
	for _, n := range kept {
		found := false
		for _, v := range after {
			if v.NodeID == n.ID() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("kept node %s disappeared after the replace-set pass", n.QualifiedName())
		}
	}
}

// keepOnly returns a DocumentSource that yields a deterministic v2-shaped
// document for every node in keep and skips everything else. The mock embedder
// hashes (kind, qualified_name) so identical nodes produce identical vectors
// across runs; the document text only matters here as the unit the builder
// cuts from source (the test runs over a MemVectorTable-backed GenerateAndPersist,
// which does not parse source bytes).
func keepOnly(keep []model.Node) docSource {
	out := docSource{}
	for _, n := range keep {
		text := embed.NodeText(n) // any non-empty text is enough — bytes are not parsed
		hash := model.FormatID(xxhash.Sum64String(text))
		out[n.ID()] = embed.SemanticDocument{
			DocumentID:     model.FormatID(xxhash.Sum64String(string(n.ID()) + hash + "v2")),
			NodeID:         n.ID(),
			Kind:           n.Kind(),
			QualifiedName:  n.QualifiedName(),
			Path:           n.SourcePath(),
			TextHash:       hash,
			DocumentSchema: "v2",
			Text:           text,
		}
	}
	return out
}

func mustNodes(t *testing.T) []model.Node {
	t.Helper()
	specs := []struct {
		kind, qn, path string
	}{
		{"function", "pkg/foo.ParseGraph", "pkg/foo.go"},
		{"function", "pkg/foo.ParseGraphLite", "pkg/foo.go"},
		{"type", "pkg/bar.Graph", "pkg/bar.go"},
	}
	out := make([]model.Node, 0, len(specs))
	for i, s := range specs {
		n, err := model.NewNode(s.kind, s.qn, s.path, i+1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		out = append(out, n)
	}
	return out
}
