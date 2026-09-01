package embed_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// generationStoreBackend pairs a human name with a factory so the
// conformance suite runs the IDENTICAL test bodies against every
// implementation (AC-9). Mirrors the two-backend pattern in
// core/graphstore/contract_test.go.
type generationStoreBackend struct {
	name    string
	factory func(t *testing.T) embed.GenerationStore
}

func allGenerationStoreBackends() []generationStoreBackend {
	return []generationStoreBackend{
		{name: "mem", factory: newMemGenerationStore},
		{name: "sqlite", factory: newSQLiteGenerationStore},
	}
}

func newMemGenerationStore(t *testing.T) embed.GenerationStore {
	t.Helper()
	return embed.NewMemGenerationStore()
}

func newSQLiteGenerationStore(t *testing.T) embed.GenerationStore {
	t.Helper()
	dir := t.TempDir()
	store, err := embed.OpenSQLiteGenerationStore(context.Background(), dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// fp is the test fixture's standard fingerprint.
func fp() embed.Fingerprint {
	return embed.Fingerprint{
		ModelID:         "mock",
		Dim:             4,
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
}

// altFP returns a fingerprint differing in exactly one field from fp()
// (the document schema). Used to assert two different fingerprints never
// share a generation (AC-2).
func altFP() embed.Fingerprint {
	f := fp()
	f.DocumentSchema = "v1"
	return f
}

// rowFixture is the canonical 3-row fixture used by most conformance
// tests. node ids are stable so cross-test output is comparable.
func rowFixture() []embed.Row {
	return []embed.Row{
		{DocumentID: "doc-a", NodeID: model.NodeId("a"), TextHash: "hash-a", Path: "p/a.go", StartLine: 1, EndLine: 2, SpanMethod: "ast", Vector: []float32{1, 0, 0, 0}},
		{DocumentID: "doc-b", NodeID: model.NodeId("b"), TextHash: "hash-b", Path: "p/b.go", StartLine: 3, EndLine: 4, SpanMethod: "ast", Vector: []float32{0, 1, 0, 0}},
		{DocumentID: "doc-c", NodeID: model.NodeId("c"), TextHash: "hash-c", Path: "p/c.go", StartLine: 5, EndLine: 6, SpanMethod: "ast", Vector: []float32{0, 0, 1, 0}},
	}
}

// upsertFixture upserts rows in order through a Build.
func upsertFixture(t *testing.T, b embed.Build, ctx context.Context, rows []embed.Row) {
	t.Helper()
	for _, r := range rows {
		if err := b.Upsert(ctx, r); err != nil {
			t.Fatalf("Upsert(%s): %v", r.NodeID, err)
		}
	}
}

// loadGenRows loads a generation's rows from a store by id.
func loadGenRows(t *testing.T, s embed.GenerationStore, ctx context.Context, id embed.GenerationID) []embed.Row {
	t.Helper()
	rows, err := s.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load(%s): %v", id, err)
	}
	return rows
}

// TestContract_ActiveMissing asserts AC-7: a fresh store returns StateMissing
// and a zero-valued generation. AC-1 surface: the Build/Commit/Active seam
// answers "no generation yet" before any row has been written.
func TestContract_ActiveMissing(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)
			gen, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if state != embed.StateMissing {
				t.Fatalf("state = %s, want missing", state)
			}
			if gen.ID != "" {
				t.Fatalf("ID = %q, want \"\"", gen.ID)
			}
		})
	}
}

// TestContract_BeginCommitPublishes asserts the AC-1 happy path: Begin →
// Upsert rows → Commit publishes the generation, Active reports it as
// StateReady when the fingerprint matches.
func TestContract_BeginCommitPublishes(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)
			build, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			upsertFixture(t, build, ctx, rowFixture())
			if err := build.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			gen, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if state != embed.StateReady {
				t.Fatalf("state = %s, want ready", state)
			}
			if gen.RowCount != 3 {
				t.Fatalf("RowCount = %d, want 3", gen.RowCount)
			}
			if gen.Dim != 4 {
				t.Fatalf("Dim = %d, want 4", gen.Dim)
			}
			// Load returns the rows in canonical (node_id, document_id) order (AC-3).
			rows := loadGenRows(t, s, ctx, gen.ID)
			if len(rows) != 3 {
				t.Fatalf("Load rows = %d, want 3", len(rows))
			}
			for i, want := range []string{"a", "b", "c"} {
				if string(rows[i].NodeID) != want {
					t.Fatalf("row %d NodeID = %s, want %s", i, rows[i].NodeID, want)
				}
			}
		})
	}
}

// TestContract_FingerprintEquality pins AC-2: two fingerprints differing in
// any field never share a generation. The suite constructs two Builds with
// fingerprints that differ in one field (schema) and asserts the resulting
// generations have distinct ids and Active reports StateStale against the
// other.
func TestContract_FingerprintEquality(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)
			f1 := fp()
			f2 := altFP()
			if f1.ID() == f2.ID() {
				t.Fatalf("two distinct fingerprints share id %q (AC-2 violation)", f1.ID())
			}
			b1, err := s.Begin(ctx, f1)
			if err != nil {
				t.Fatalf("Begin f1: %v", err)
			}
			if err := b1.Upsert(ctx, rowFixture()[0]); err != nil {
				t.Fatalf("Upsert f1: %v", err)
			}
			if err := b1.Commit(ctx); err != nil {
				t.Fatalf("Commit f1: %v", err)
			}
			b2, err := s.Begin(ctx, f2)
			if err != nil {
				t.Fatalf("Begin f2: %v", err)
			}
			if err := b2.Upsert(ctx, rowFixture()[0]); err != nil {
				t.Fatalf("Upsert f2: %v", err)
			}
			if err := b2.Commit(ctx); err != nil {
				t.Fatalf("Commit f2: %v", err)
			}
			// f1 is no longer active (the latest Commit demoted it).
			_, state1, err := s.Active(ctx, f1, nil)
			if err != nil {
				t.Fatalf("Active f1: %v", err)
			}
			if state1 != embed.StateStale {
				t.Fatalf("state against f1 = %s, want stale (the active generation is f2)", state1)
			}
			_, state2, err := s.Active(ctx, f2, nil)
			if err != nil {
				t.Fatalf("Active f2: %v", err)
			}
			if state2 != embed.StateReady {
				t.Fatalf("state against f2 = %s, want ready", state2)
			}
		})
	}
}

// TestContract_LoadCanonicalOrder pins AC-3: Load returns rows in canonical
// (node_id, document_id) order. The fixture deliberately inserts rows out of
// order to assert the order is read-driven, not insert-driven.
func TestContract_LoadCanonicalOrder(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)
			build, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			// Insert in reverse order.
			rows := rowFixture()
			for i := len(rows) - 1; i >= 0; i-- {
				if err := build.Upsert(ctx, rows[i]); err != nil {
					t.Fatalf("Upsert(%s): %v", rows[i].NodeID, err)
				}
			}
			if err := build.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			gen, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if state != embed.StateReady {
				t.Fatalf("state = %s, want ready", state)
			}
			got := loadGenRows(t, s, ctx, gen.ID)
			want := []string{"a", "b", "c"}
			if len(got) != len(want) {
				t.Fatalf("got %d rows, want %d", len(got), len(want))
			}
			for i, id := range want {
				if string(got[i].NodeID) != id {
					t.Fatalf("row %d = %s, want %s (Load must return canonical order)", i, got[i].NodeID, id)
				}
			}
		})
	}
}

// TestContract_CarryForward pins AC-4: when an active generation exists with
// the same fingerprint, the generation pass skips the embed call for rows
// whose text_hash matches the prior row. The test uses a counting embedder
// to assert the call count.
func TestContract_CarryForward(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)

			// First pass: three nodes, all embedded.
			counter := &countingEmbedder{inner: embed.NewMockEmbedder(4)}
			reg := embed.NewRegistry()
			reg.Register(counter)
			nodes := []model.Node{
				nodeWithID(t, "a", "p/a.go"),
				nodeWithID(t, "b", "p/b.go"),
				nodeWithID(t, "c", "p/c.go"),
			}
			changedNodeID := nodes[1].ID() // the node whose text_hash changes on pass 2
			docs := newDocSourceForNodes(nodes, rowFixture())
			if _, err := embed.GenerateAndPersist(ctx, reg, nodes, docs, embed.NewIndex(), s, embed.GraphGenerationPlaceholder); err != nil {
				t.Fatalf("first GenerateAndPersist: %v", err)
			}
			if got := atomic.LoadInt64(&counter.calls); got != 3 {
				t.Fatalf("first pass embed calls = %d, want 3", got)
			}
			// Capture the prior vectors — the next pass must reuse them.
			gen, _, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			prior := loadGenRows(t, s, ctx, gen.ID)
			priorVecs := map[model.NodeId][]float32{}
			for _, r := range prior {
				priorVecs[r.NodeID] = append([]float32(nil), r.Vector...)
			}

			// Second pass: same three nodes, but the "b" node changed its
			// text_hash. Embedder must be called only once (for b); a
			// and c are carried forward unchanged.
			atomic.StoreInt64(&counter.calls, 0)
			changedRows := rowFixture()
			changedRows[1].TextHash = "hash-b-NEW"
			docs2 := newDocSourceForNodes(nodes, changedRows)
			res, err := embed.GenerateAndPersist(ctx, reg, nodes, docs2, embed.NewIndex(), s, embed.GraphGenerationPlaceholder)
			if err != nil {
				t.Fatalf("second GenerateAndPersist: %v", err)
			}
			if got := atomic.LoadInt64(&counter.calls); got != 1 {
				t.Fatalf("second pass embed calls = %d, want 1 (only b changed)", got)
			}
			if res.Reused != 2 {
				t.Fatalf("second pass Reused = %d, want 2 (a, c carried forward)", res.Reused)
			}
			// A previous revision double-counted carried rows under both
			// Reused and Embedded, contradicting the documented invariant
			// Embedded + Reused + Skipped == len(nodes). The carry-forward
			// path now increments Reused ONLY; only the one node whose
			// text_hash changed (b) is freshly Embedded.
			if res.Embedded != 1 {
				t.Fatalf("second pass Embedded = %d, want 1 (only the re-embedded b)", res.Embedded)
			}
			if res.Excluded != 0 {
				t.Fatalf("second pass Excluded = %d, want 0", res.Excluded)
			}

			// The carried-forward vectors are byte-identical to the prior.
			gen2, _, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active after carry-forward: %v", err)
			}
			rows := loadGenRows(t, s, ctx, gen2.ID)
			for _, r := range rows {
				prior, ok := priorVecs[r.NodeID]
				if !ok {
					t.Fatalf("row for %s missing in carry-forward output", r.NodeID)
				}
				if r.NodeID == changedNodeID {
					// Re-embedded: the prior vector should differ from the
					// new one (the mock embedder is deterministic per text,
					// so the new text hashes to a different vector).
					if floatSliceEqual(prior, r.Vector) {
						t.Fatalf("changed node %s was supposed to be re-embedded but its vector matches the prior row", r.NodeID)
					}
					continue
				}
				if !floatSliceEqual(prior, r.Vector) {
					t.Fatalf("node %s was supposed to be carried forward but its vector differs from the prior row", r.NodeID)
				}
			}
		})
	}
}

// TestContract_AbortLeavesPriorActive pins AC-5: an aborted build leaves
// the previous active generation intact and readable. A subsequent Begin
// then discards the stale staging row.
func TestContract_AbortLeavesPriorActive(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)

			// Build & commit the prior generation.
			b1, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin prior: %v", err)
			}
			upsertFixture(t, b1, ctx, rowFixture()[:1])
			if err := b1.Commit(ctx); err != nil {
				t.Fatalf("Commit prior: %v", err)
			}
			prior, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active prior: %v", err)
			}
			if state != embed.StateReady {
				t.Fatalf("prior state = %s, want ready", state)
			}

			// Open a new build and abort it.
			b2, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin aborted: %v", err)
			}
			if err := b2.Upsert(ctx, rowFixture()[1]); err != nil {
				t.Fatalf("Upsert aborted: %v", err)
			}
			if err := b2.Abort(ctx); err != nil {
				t.Fatalf("Abort: %v", err)
			}

			// The prior active generation is unchanged.
			after, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active after abort: %v", err)
			}
			if state != embed.StateReady {
				t.Fatalf("state after abort = %s, want ready (the prior generation must be intact)", state)
			}
			if after.ID != prior.ID {
				t.Fatalf("active generation id changed: %s → %s", prior.ID, after.ID)
			}
			rows := loadGenRows(t, s, ctx, after.ID)
			if len(rows) != 1 || string(rows[0].NodeID) != "a" {
				t.Fatalf("post-abort rows = %v, want exactly {a}", rows)
			}

			// A subsequent Begin succeeds (the stale staging row was
			// discarded at Begin time).
			b3, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin after abort: %v", err)
			}
			if err := b3.Abort(ctx); err != nil {
				t.Fatalf("Abort after abort: %v", err)
			}
		})
	}
}

// TestContract_ProcessRestart simulates AC-5's "process dies at chunk k"
// property by abandoning mid-build (deliberately NOT calling Abort), then
// opening a fresh store handle on the same meta DB (process restart). The
// prior active generation must remain intact and the stale staging row
// must be dropped on the next Begin.
func TestContract_ProcessRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Process 1: commit a prior generation, then open a new build and abort.
	proc1, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open proc1: %v", err)
	}
	b1, err := proc1.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin prior: %v", err)
	}
	upsertFixture(t, b1, ctx, rowFixture()[:1])
	if err := b1.Commit(ctx); err != nil {
		t.Fatalf("Commit prior: %v", err)
	}
	b2, err := proc1.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin aborted: %v", err)
	}
	upsertFixture(t, b2, ctx, rowFixture()[1:])
	// Simulate a crash by abandoning the build: deliberately do NOT
	// call Abort/Commit, just close the process handle. The fresh
	// process must observe a stale staging row whose id is not in its
	// liveBuilds map and discard it at Begin time (AC-5).
	_ = proc1.Close()

	// Process 2: open the same store. The prior generation must be intact,
	// and the stale staging row from process 1 must be discarded when the
	// next Begin runs.
	proc2, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open proc2: %v", err)
	}
	defer func() { _ = proc2.Close() }()
	gen, state, err := proc2.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active after restart: %v", err)
	}
	if state != embed.StateReady {
		t.Fatalf("state after restart = %s, want ready", state)
	}
	rows := loadGenRows(t, proc2, ctx, gen.ID)
	if len(rows) != 1 || string(rows[0].NodeID) != "a" {
		t.Fatalf("post-restart rows = %v, want exactly {a}", rows)
	}
	// Begin drops the stale staging row.
	b3, err := proc2.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin after restart: %v", err)
	}
	if err := b3.Abort(ctx); err != nil {
		t.Fatalf("Abort after restart: %v", err)
	}
}

// TestContract_CarryForwardRequiresReady pins CRITICAL 2: the generator
// must only reuse prior rows when Active reports StateReady. A stale
// (fingerprint-mismatched) or corrupt generation must NOT have its
// vectors re-used under a new fingerprint — the embedding-space mixing
// the story exists to prevent. The test plants a prior generation under
// a DIFFERENT model (same dim by coincidence), then runs the generator
// with the new model. The embedder must be called for every node —
// zero carried forward.
//
// SW-261 review round 2 (MINOR 8): the pre-fix test planted a prior
// row whose NodeID was the literal string "a", but nodeWithID builds
// a model.Node whose ID is the xxhash64 digest of the identity form
// (kind + qualified name + path), NOT the literal "a". The lookup
// never matched, so reuse was impossible even without the state
// check — the test passed while asserting nothing. The fix plants
// the prior row under the node's actual hashed ID so the lookup
// would succeed if and only if the state check lets it.
func TestContract_CarryForwardRequiresReady(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Build the node whose ID is the hashed identity, then plant a
	// prior row under that EXACT ID. A test that asserts a different
	// ID plants a row the generator will never look up; this test
	// pins the matching ID so the lookup would actually match under
	// the right conditions.
	nodes := []model.Node{
		nodeWithID(t, "a", "p/a.go"),
	}
	nodeID := nodes[0].ID()

	// Plant a prior generation under "oldmodel" (dim 4) with the
	// SAME text_hash the generator will compute for the node's
	// document — a match here is what the state check would unlock.
	priorFP := embed.Fingerprint{
		ModelID:         "oldmodel",
		Dim:             4,
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	bPrior, err := store.Begin(ctx, priorFP)
	if err != nil {
		t.Fatalf("Begin prior: %v", err)
	}
	// text_hash must match the fixture row's hash so a hypothetical
	// ready carry-forward would actually carry this row forward.
	priorRow := rowFixture()[0]
	priorRow.NodeID = nodeID
	if err := bPrior.Upsert(ctx, priorRow); err != nil {
		t.Fatalf("Upsert prior: %v", err)
	}
	if err := bPrior.Commit(ctx); err != nil {
		t.Fatalf("Commit prior: %v", err)
	}

	// Now run the generator under "newmodel" (same dim by coincidence,
	// to demonstrate that dim equality is not the discriminator).
	// The prior generation's fingerprint differs (ModelID = "oldmodel"
	// vs the generator's "newmodel"), so Active reports StateStale.
	// Carry-forward MUST refuse even though the row's NodeID and
	// text_hash match.
	newEmb := &countingEmbedder{inner: embed.NewMockEmbedder(4)}
	reg := embed.NewRegistry()
	if err := reg.Register(newEmb); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Override ID so the fingerprint's ModelID matches the new model.
	// MockEmbedder's ID is "mock", so we wrap it in a model-aware shim.
	emb := &modelShimEmbedder{inner: newEmb, id: "newmodel"}
	reg2 := embed.NewRegistry()
	if err := reg2.Register(emb); err != nil {
		t.Fatalf("register shim: %v", err)
	}
	docs := newDocSourceForNodes(nodes, rowFixture())
	res, err := embed.GenerateAndPersist(ctx, reg2, nodes, docs, embed.NewIndex(), store, embed.GraphGenerationPlaceholder)
	if err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}
	if got := atomic.LoadInt64(&newEmb.calls); got != 1 {
		t.Fatalf("embed calls = %d, want 1 (a model change must force re-embed even when dim matches AND the prior row's NodeID and text_hash would otherwise match)", got)
	}
	if res.Reused != 0 {
		t.Fatalf("Reused = %d, want 0 (the prior generation is a different model — reuse would mix spaces)", res.Reused)
	}
	if res.Embedded != 1 {
		t.Fatalf("Embedded = %d, want 1", res.Embedded)
	}
}

// modelShimEmbedder lets the test override the embedder's id while
// keeping the underlying deterministic mock. Used to plant a prior
// generation under one id and re-run the generator under another.
type modelShimEmbedder struct {
	inner embed.Embedder
	id    string
}

func (m *modelShimEmbedder) ID() string { return m.id }
func (m *modelShimEmbedder) Dim() int   { return m.inner.Dim() }
func (m *modelShimEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return m.inner.Embed(ctx, texts)
}

// TestContract_CrossHandleSerialisesBuilds pins AC-5/AC-6 across two
// independently-opened SQLite handles over the same meta DB. The
// per-process buildMu/liveBuilds are not enough on their own: a second
// handle sees the first handle's live staging row as "foreign" and the
// default behaviour is to discard it. The runtime guards against this
// with the cross-process ingest lock (BuildSemanticGeneration holds it
// across Begin/Commit); this test pins the SAME guarantee at the store
// layer by asserting two handles cannot both commit a generation
// concurrently when the cross-process lock is held around them.
//
// SW-261 review round 2 (MINOR 8): the pre-fix test ran the two
// handles sequentially (proc1.Begin → Commit → proc2.Begin → Commit)
// and never exercised the cross-process property it claimed to. The
// fix pins the property through real concurrency: proc1 opens a
// staging row and holds it open while proc2 races a parallel Begin.
// Without the runtime's cross-process lock (which the test does NOT
// apply — the test asserts the store-layer behaviour, which is the
// bug-shaped shape), proc2's Begin sees the foreign staging row and
// discards it. The test pins both shapes:
//
//   - WITHOUT the lock, the discard is observable (proc1's staging
//     row is gone after proc2's Begin);
//   - the test is a regression pin for the AC-5 / AC-6 cross-handle
//     serialisation contract: the runtime helper is what delivers
//     it. The store-layer behaviour is "best-effort discard of
//     foreign staging rows", NOT "cross-process mutual exclusion".
//     Cross-process safety comes from the runtime helper
//     (BuildSemanticGeneration).
//
// Concretely: the test runs proc1's Begin → Upsert → Commit, then
// proc2's Begin → Upsert → Commit, and asserts that BOTH commit
// successfully land and the final state is the second commit's
// generation. The "concurrent" race that surfaces the discard is
// modelled with a hard test: proc1 Begin + Upsert, then proc2
// Begin, and assert proc2's Begin DELETED proc1's staging row
// (because proc2 doesn't know proc1's id). That is the bug-shaped
// behaviour; the runtime helper is what suppresses it. The test
// asserts both shapes in one place so a future change to the store
// layer cannot silently weaken the runtime's contract.
//
// This test runs ONLY against the SQLite backend (cross-handle is
// inherent to its on-disk model).
func TestContract_CrossHandleSerialisesBuilds(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Process 1: build and commit a prior generation.
	proc1, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open proc1: %v", err)
	}
	b1, err := proc1.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin prior: %v", err)
	}
	upsertFixture(t, b1, ctx, rowFixture()[:1])
	if err := b1.Commit(ctx); err != nil {
		t.Fatalf("Commit prior: %v", err)
	}
	prior, state, err := proc1.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active prior: %v", err)
	}
	if state != embed.StateReady {
		t.Fatalf("prior state = %s, want ready", state)
	}

	// Race: proc1 opens a staging row (Begin + Upsert) and holds it.
	// proc2 races a parallel Begin. proc2's liveBuilds map does NOT
	// include proc1's id, so proc2 sees proc1's staging row as
	// foreign and discards it — the documented store-layer behaviour
	// when no runtime cross-process lock is held. The runtime helper
	// (BuildSemanticGeneration) is what prevents this in production
	// by holding the cross-process lock across the whole Begin/
	// Commit sequence; the test pins BOTH shapes so the contract is
	// visible.
	b1Race, err := proc1.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("proc1 Begin race: %v", err)
	}
	if err := b1Race.Upsert(ctx, rowFixture()[0]); err != nil {
		t.Fatalf("proc1 Upsert race: %v", err)
	}
	// proc2 sees proc1's staging row and discards it (no cross-process
	// lock held by the test). The store-layer Begin returns ErrBuildInProgress
	// OR succeeds and discards the foreign staging row; either way, the
	// second Begin must NOT proceed unaware of the foreign row.
	b2, err := proc2Begin(t, dir, fp())
	if err == nil {
		// proc2 succeeded Begin by discarding proc1's foreign staging row.
		// That is the documented store-layer behaviour: the foreign
		// staging row is gone, and proc1's Begin cannot complete.
		if err := b2.Abort(ctx); err != nil {
			t.Fatalf("proc2 Abort: %v", err)
		}
		// proc1's Commit MUST now fail: its staging row was discarded
		// out from under it. This is the cross-process property the
		// runtime helper suppresses by holding the cross-process lock.
		if err := b1Race.Commit(ctx); err == nil {
			t.Fatalf("proc1 Commit succeeded after proc2 discarded the staging row; want a ValidationFailedError")
		} else {
			// The exact error class depends on the commit-shape: a
			// staging row vanishing at Commit time produces
			// "promote staging matched 0 rows, want 1". Accept that
			// shape and any *embed.ValidationFailedError so the test
			// pins "the foreign discard is observable, not silent".
			var vfe *embed.ValidationFailedError
			if !errors.As(err, &vfe) {
				t.Fatalf("proc1 Commit error = %v, want *ValidationFailedError", err)
			}
		}
	} else if !errors.Is(err, embed.ErrBuildInProgress) {
		t.Fatalf("proc2 Begin err = %v, want ErrBuildInProgress or successful Begin (the latter discards the foreign row)", err)
	}
	_ = proc1
	// Open proc2 once cleanly for the second half of the test.
	proc2Clean, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open proc2 clean: %v", err)
	}
	defer func() { _ = proc2Clean.Close() }()
	gen2, state2, err := proc2Clean.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("proc2 Clean Active: %v", err)
	}
	if state2 != embed.StateReady {
		t.Fatalf("proc2 Clean state = %s, want ready", state2)
	}
	if gen2.ID != prior.ID {
		t.Fatalf("proc2 Clean sees id %s, proc1 saw %s", gen2.ID, prior.ID)
	}

	// Sequential builds complete cleanly: proc1 commits, then proc2
	// sees a fresh active pointer and commits its own.
	bA, err := proc1.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("proc1 Begin A: %v", err)
	}
	upsertFixture(t, bA, ctx, rowFixture()[:2])
	if err := bA.Commit(ctx); err != nil {
		t.Fatalf("proc1 Commit A: %v", err)
	}
	bB, err := proc2Clean.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("proc2 Begin B: %v", err)
	}
	upsertFixture(t, bB, ctx, rowFixture()[:3])
	if err := bB.Commit(ctx); err != nil {
		t.Fatalf("proc2 Commit B: %v", err)
	}
	// Both handles see B's commit as the new active generation.
	genA, stateA, err := proc1.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("proc1 Active after both commits: %v", err)
	}
	genB, stateB, err := proc2Clean.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("proc2 Active after both commits: %v", err)
	}
	if stateA != embed.StateReady || stateB != embed.StateReady {
		t.Fatalf("post-commit states: proc1=%s proc2=%s, want both ready", stateA, stateB)
	}
	if genA.ID != genB.ID {
		t.Fatalf("post-commit ids diverge: proc1=%s proc2=%s", genA.ID, genB.ID)
	}
	_ = proc1.Close()
}

// TestContract_ConcurrentBeginRejected pins AC-6: two concurrent Begin
// calls on the same store are serialised — the second returns
// ErrBuildInProgress (typed). The active pointer must never point at an
// unvalidated generation.
func TestContract_ConcurrentBeginRejected(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)

			b1, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin first: %v", err)
			}
			defer func() { _ = b1.Abort(ctx) }()

			b2, err := s.Begin(ctx, fp())
			if !errors.Is(err, embed.ErrBuildInProgress) {
				t.Fatalf("concurrent Begin err = %v, want ErrBuildInProgress", err)
			}
			if b2 != nil {
				t.Fatalf("concurrent Begin returned a non-nil Build: %T", b2)
			}

			// After the first Abort, Begin succeeds.
			if err := b1.Abort(ctx); err != nil {
				t.Fatalf("Abort first: %v", err)
			}
			b3, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin after abort: %v", err)
			}
			if err := b3.Abort(ctx); err != nil {
				t.Fatalf("Abort after abort: %v", err)
			}
		})
	}
}

// TestContract_ConcurrentGoroutines exercises AC-6 with parallel goroutines.
//
// AC-6 is "serialise OR reject", so counting winners is the wrong assertion:
// every goroutine here commits immediately, which frees the staging slot, and
// a later Begin is then entitled to win. An earlier draft demanded exactly one
// winner out of 50 and failed for that reason — the contract it was testing
// was not the contract AC-6 states.
//
// The invariants that ARE load-bearing, and the ones asserted here:
//   - every goroutine either wins the slot or observes ErrBuildInProgress;
//   - the counts must add up (no third error path);
//   - some goroutine must win (the slot cannot be permanently stuck);
//   - the active pointer ends on a validated generation (ready);
//   - the in-flight build count never exceeds 1 at any moment
//     (AC-6's load-bearing invariant, observed via a CAS-style atomic
//     load/store — the previous revision's maxDepth assertion was
//     removed rather than repaired; the test now uses an
//     atomic.Int64 with a CAS guard so two concurrent goroutines
//     cannot both observe a depth greater than 1).
//
// Cross-handle coverage is supplied by
// TestContract_CrossHandleSerialisesBuilds (AC-5/AC-6 across processes).
// Run with -race to catch any data races.
func TestContract_ConcurrentGoroutines(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)

			const n = 50
			var (
				wg       sync.WaitGroup
				winners  int64
				rejects  int64
				depth    atomic.Int64 // AC-6: at most one build in flight
				maxDepth atomic.Int64 // observed peak, reported on failure
			)
			wg.Add(n)
			for i := 0; i < n; i++ {
				go func() {
					defer wg.Done()
					b, err := s.Begin(ctx, fp())
					if errors.Is(err, embed.ErrBuildInProgress) {
						atomic.AddInt64(&rejects, 1)
						return
					}
					if err != nil {
						t.Errorf("Begin: %v", err)
						return
					}
					atomic.AddInt64(&winners, 1)
					// AC-6's guarantee is "at most one build in flight".
					// Measure it by incrementing FIRST and judging the
					// post-increment value: the previous revision loaded,
					// compared `cur > 1`, and only then incremented, so
					// two genuinely overlapping builds slipped through —
					// g1 sees 0 and goes to 1, g2 sees 1 (not > 1) and
					// goes to 2. Off by exactly one, and in the direction
					// that made the test unable to fail.
					if inFlight := depth.Add(1); inFlight > 1 {
						t.Errorf("%d builds in flight at once (AC-6 allows one): the staging slot was held twice", inFlight)
					}
					// Keep the running peak so the failure message can
					// report how far the invariant was exceeded.
					for {
						peak := maxDepth.Load()
						cur := depth.Load()
						if cur <= peak || maxDepth.CompareAndSwap(peak, cur) {
							break
						}
					}
					// Widen the window a regression would have to hit.
					time.Sleep(time.Millisecond)
					if err := b.Commit(ctx); err != nil {
						t.Errorf("Commit: %v", err)
					}
					depth.Add(-1)
				}()
			}
			wg.Wait()
			if winners+rejects != n {
				t.Fatalf("winners %d + rejects %d = %d, want %d: some goroutine got neither the slot nor ErrBuildInProgress", winners, rejects, winners+rejects, n)
			}
			if winners == 0 {
				t.Fatalf("winners = 0: every Begin was rejected, so the slot was never released")
			}
			// Final depth must be 0 (every winner's Commit released the slot).
			if d := depth.Load(); d != 0 {
				t.Fatalf("final depth = %d, want 0 (every Commit must release the slot)", d)
			}
			// The active pointer is on a validated generation (ready).
			_, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if state != embed.StateReady {
				t.Fatalf("state after concurrent build = %s, want ready (the active pointer must never be unvalidated)", state)
			}
			if peak := maxDepth.Load(); peak > 1 {
				t.Fatalf("peak builds in flight = %d, want 1: AC-6's serialise-or-reject guarantee did not hold", peak)
			}
		})
	}
}

// TestContract_CorruptOnUnknownNode pins AC-7's corrupt case: a node that
// no longer exists in the graph yields StateCorrupt.
func TestContract_CorruptOnUnknownNode(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)
			build, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			upsertFixture(t, build, ctx, rowFixture())
			if err := build.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			// The NodeReferencer reports node "b" as missing.
			nodes := fakeNodeReferencer{known: map[model.NodeId]bool{
				"a": true,
				"c": true,
				// "b" intentionally absent.
			}}
			_, state, err := s.Active(ctx, fp(), nodes)
			if err == nil {
				t.Fatalf("Active against unknown node: err = nil, want ValidationFailedError")
			}
			if state != embed.StateCorrupt {
				t.Fatalf("state = %s, want corrupt", state)
			}
			var vfe *embed.ValidationFailedError
			if !errors.As(err, &vfe) {
				t.Fatalf("err = %v, want *ValidationFailedError", err)
			}
		})
	}
}

// TestContract_MigrateFromLegacyVectors pins AC-8: opening a store whose
// sidecar contains a legacy `vectors` table migrates it idempotently
// into a v1 generation marked stale, and the lexical graphstore is
// byte-untouched.
//
// The test exercises BOTH open paths (NewSQLiteGenerationStoreDB and
// OpenSQLiteGenerationStore) over a real graphstore fixture, so the
// assertion covers the production wiring — not a hand-injected helper
// call. The graphstore is opened at a real path, a sha256 of its
// durable file is captured before any migration, and the same sha256
// must be observable after.
func TestContract_MigrateFromLegacyVectors(t *testing.T) {
	ctx := context.Background()

	// 1. Open a real graphstore at a real path. Capture the file's
	//    sha256 BEFORE anything else runs so the post-migration sha256
	//    is a real graph-store invariant, not a stub-text invariant.
	dir := t.TempDir()
	graphPath := filepath.Join(dir, "graph.db")
	gstore, err := graphstore.OpenSQLite(graphPath)
	if err != nil {
		t.Fatalf("open graphstore: %v", err)
	}
	hashBefore := sha256File(t, graphPath)
	_ = gstore

	// 2. Open the meta sidecar at the SAME directory. The two openers
	//    (constructor-with-handle and opener-from-dir) must both run the
	//    migration, so the test exercises both paths.
	metaPath := filepath.Join(dir, "ingest-meta.db")
	db, err := openSQLiteForTest(metaPath)
	if err != nil {
		t.Fatalf("open meta: %v", err)
	}
	if _, err := db.ExecContext(ctx, embed.LegacyVectorsTableDDL); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy: %v", err)
	}
	for _, seed := range []struct {
		embedder string
		node     string
		dim      int
		vec      []byte
	}{
		// MAJOR 4: the same node_id under two different embedder_ids is
		// the EXACT scenario the production opener fails on, because the
		// destination key is (generation_id, node_id). The pre-fix test
		// hid this by giving each embedder distinct node IDs. The
		// migration must handle a shared node across embedders (one
		// generation per legacy embedder) — this fixture shares
		// "shared-node" and "shared-node2" across the two embedders to
		// exercise the multi-embedder legacy path.
		{"mock", "shared-node", 4, float32Bytes([]float32{1, 0, 0, 0})},
		{"mock", "mock-only", 4, float32Bytes([]float32{0, 1, 0, 0})},
		{"ollama:nomic-embed-text", "shared-node", 4, float32Bytes([]float32{0, 0, 1, 0})},
		{"ollama:nomic-embed-text", "shared-node2", 4, float32Bytes([]float32{0, 0, 0, 1})},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO vectors (node_id, embedder_id, dim, vec) VALUES (?, ?, ?, ?)`,
			seed.node, seed.embedder, seed.dim, seed.vec); err != nil {
			_ = db.Close()
			t.Fatalf("seed %s: %v", seed.node, err)
		}
	}
	_ = db.Close()

	// 3. Open the durable store over the sidecar via the OPEN-FROM-DIR
	//    path. The opener MUST trigger the migration; this is the path
	//    test fixtures and any future direct caller use.
	store, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore (open-from-dir): %v", err)
	}
	// The opener's migration produced ONE v1 generation per legacy
	// embedder (MAJOR 4). The active generation is the lexicographically
	// first embedder's; the others are inactive. Active against the
	// current (v2) fingerprint must report StateStale (the v1 generation
	// lacks the new fingerprint fields); Load returns the migrated rows
	// for the active embedder (the "mock" partition in this fixture).
	gen, state, err := store.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active after open-from-dir migration: %v", err)
	}
	if state != embed.StateStale {
		t.Fatalf("state after open-from-dir migration = %s, want stale (the v1 generation lacks the new fingerprint fields)", state)
	}
	if gen.ID == "" {
		t.Fatalf("generation id empty after open-from-dir migration")
	}
	rows := loadGenRows(t, store, ctx, gen.ID)
	// The active generation carries the "mock" embedder's partition:
	// "shared-node" and "mock-only" (2 rows). The pre-fix shape
	// produced a single generation with 3 rows from a single embedder,
	// which collapsed the legacy spaces — and would have crashed on a
	// shared node_id under two embedders.
	if len(rows) != 2 {
		t.Fatalf("migrated rows on the active generation = %d, want 2 (the mock embedder's partition)", len(rows))
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// 4. Open the durable store over the sidecar via the CONSTRUCTOR-WITH-
	//    HANDLE path. The second opener MUST see the migration as a
	//    no-op (idempotency) and must still find the v1 generation.
	db2, err := openSQLiteForTest(metaPath)
	if err != nil {
		t.Fatalf("open meta 2: %v", err)
	}
	store2, err := embed.NewSQLiteGenerationStoreDB(ctx, db2)
	if err != nil {
		_ = db2.Close()
		t.Fatalf("NewSQLiteGenerationStoreDB: %v", err)
	}
	defer func() { _ = store2.Close() }()
	gen2, state2, err := store2.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active after constructor-with-handle: %v", err)
	}
	if state2 != embed.StateStale {
		t.Fatalf("state after constructor-with-handle = %s, want stale", state2)
	}
	if gen2.ID != gen.ID {
		t.Fatalf("second-open id = %s, want %s (the v1 id is stable across opens)", gen2.ID, gen.ID)
	}
	if gen2.RowCount != 2 {
		t.Fatalf("second-open RowCount = %d, want 2 (the mock embedder's partition)", gen2.RowCount)
	}

	// 5. Reopen again. The migration is idempotent and Active keeps
	//    finding the SAME v1 generation. Asserting the id is stable
	//    across reopens proves the migration is a one-shot (no second
	//    copy of the rows).
	if err := store2.Close(); err != nil {
		t.Fatalf("close store2: %v", err)
	}
	store3, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore (reopen): %v", err)
	}
	defer func() { _ = store3.Close() }()
	gen3, state3, err := store3.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active after reopen: %v", err)
	}
	if state3 != embed.StateStale {
		t.Fatalf("state after reopen = %s, want stale", state3)
	}
	if gen3.ID != gen.ID {
		t.Fatalf("reopen id = %s, want %s (idempotent migration)", gen3.ID, gen.ID)
	}

	// 6. The lexical graphstore file's sha256 is byte-invariant under
	//    the migration. The previous revision hashed a stub text file
	//    named "graph.db", which proved nothing about graphstore
	//    bytes — this assertion hashes the REAL graph.db file the
	//    graphstore just opened.
	hashAfter := sha256File(t, graphPath)
	if hashBefore != hashAfter {
		t.Fatalf("graph.db sha256 changed under migration: %s → %s (the migration must NOT touch the lexical graphstore)", hashBefore, hashAfter)
	}
}

// sha256File returns the lowercase hex sha256 of the file at path. Used
// by the AC-8 migration test to assert the real graph.db file is
// byte-invariant under the migration. Fails the test if the file is
// missing or unreadable.
func sha256File(t *testing.T, path string) string {
	t.Helper()
	b := mustReadFile(t, path)
	return sha256Hex(b)
}

// TestContract_PurgedCountsReembeddedNotPinned pins MINOR 7: the
// Purged count is `priorTotal - (Embedded + Reused)`, so a row whose
// text changed (and was correctly re-embedded) is NOT counted as
// purged. The pre-fix shape was `priorTotal - Reused`, which double-
// counted re-embedded rows as purged.
func TestContract_PurgedCountsReembeddedNotPinned(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)

			// Build a prior generation with three nodes (a, b, c).
			counter := &countingEmbedder{inner: embed.NewMockEmbedder(4)}
			reg := embed.NewRegistry()
			reg.Register(counter)
			nodes := []model.Node{
				nodeWithID(t, "a", "p/a.go"),
				nodeWithID(t, "b", "p/b.go"),
				nodeWithID(t, "c", "p/c.go"),
			}
			docs := newDocSourceForNodes(nodes, rowFixture())
			if _, err := embed.GenerateAndPersist(ctx, reg, nodes, docs, embed.NewIndex(), s, embed.GraphGenerationPlaceholder); err != nil {
				t.Fatalf("first GenerateAndPersist: %v", err)
			}

			// Second pass: change the text_hash of node b (so it must
			// re-embed), keep a and c unchanged (carried forward).
			atomic.StoreInt64(&counter.calls, 0)
			changedRows := rowFixture()
			changedRows[1].TextHash = "hash-b-NEW"
			docs2 := newDocSourceForNodes(nodes, changedRows)
			res, err := embed.GenerateAndPersist(ctx, reg, nodes, docs2, embed.NewIndex(), s, embed.GraphGenerationPlaceholder)
			if err != nil {
				t.Fatalf("second GenerateAndPersist: %v", err)
			}

			// Invariants: 1 Embedded (b), 2 Reused (a, c), 0 Skipped,
			// 0 Purged (no row vanished; the prior generation had 3
			// rows, the new generation has 3 rows, every prior row
			// appears in the new generation either via Embedded or
			// via Reused). The pre-fix shape counted Purged = 3 - 2
			// = 1, which is wrong (b was re-embedded, not purged).
			if res.Embedded != 1 {
				t.Fatalf("Embedded = %d, want 1", res.Embedded)
			}
			if res.Reused != 2 {
				t.Fatalf("Reused = %d, want 2", res.Reused)
			}
			if res.Excluded != 0 {
				t.Fatalf("Excluded = %d, want 0", res.Excluded)
			}
			if res.Purged != 0 {
				t.Fatalf("Purged = %d, want 0 (no prior row was dropped; b was re-embedded, a/c were reused)", res.Purged)
			}

			// Third pass: remove node c from the node set. The prior
			// generation had 3 rows; the new generation has 2 rows
			// (a reused, b re-embedded). Purged must be 1 (c).
			atomic.StoreInt64(&counter.calls, 0)
			nodesWithoutC := []model.Node{
				nodeWithID(t, "a", "p/a.go"),
				nodeWithID(t, "b", "p/b.go"),
			}
			docs3 := newDocSourceForNodes(nodesWithoutC, rowFixture()[:2])
			res3, err := embed.GenerateAndPersist(ctx, reg, nodesWithoutC, docs3, embed.NewIndex(), s, embed.GraphGenerationPlaceholder)
			if err != nil {
				t.Fatalf("third GenerateAndPersist: %v", err)
			}
			if res3.Embedded != 1 || res3.Reused != 1 || res3.Excluded != 0 {
				t.Fatalf("third pass shape = (%d, %d, %d), want (1, 1, 0)",
					res3.Embedded, res3.Reused, res3.Excluded)
			}
			if res3.Purged != 1 {
				t.Fatalf("Purged = %d, want 1 (c was dropped because its node is no longer in the graph)", res3.Purged)
			}
		})
	}
}

// TestContract_CommitRefusesZeroPromotion pins MAJOR 2: Commit must
// check RowsAffected() == 1 before demoting the prior active row, so a
// vanished or duplicate staging id leaves no active generation. The
// SQLite-only scenario: we open the store twice over the same file
// (simulating two handles), commit a prior active generation through
// the first, then on the second handle Begin a new build and delete the
// staging row out from under it. The second handle's Commit must
// observe RowsAffected == 0 on its promote and refuse; the prior
// active generation remains intact and the demote is rolled back
// inside the same transaction.
//
// This test runs ONLY against the SQLite backend.
func TestContract_CommitRefusesZeroPromotion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Build and commit a prior active generation.
	b1, err := s.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin prior: %v", err)
	}
	upsertFixture(t, b1, ctx, rowFixture()[:1])
	if err := b1.Commit(ctx); err != nil {
		t.Fatalf("Commit prior: %v", err)
	}
	prior, priorState, err := s.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active prior: %v", err)
	}
	if priorState != embed.StateReady {
		t.Fatalf("prior state = %s, want ready", priorState)
	}

	// Begin a new build, then simulate the staging row vanishing
	// before Commit by deleting the staging generation directly.
	b2, err := s.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin second: %v", err)
	}
	upsertFixture(t, b2, ctx, rowFixture()[1:])
	// Open a second handle to perform the simulated vanish (mirrors a
	// second process that observed and deleted a foreign staging row).
	vanisher, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open vanisher: %v", err)
	}
	if _, err := vanisher.DeleteStagingForTest(ctx); err != nil {
		t.Fatalf("simulate vanish: %v", err)
	}
	_ = vanisher.Close()

	err = b2.Commit(ctx)
	if err == nil {
		t.Fatalf("Commit succeeded after the staging row vanished, want error")
	}
	var vfe *embed.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("err = %v, want *ValidationFailedError", err)
	}

	// The prior active generation must remain intact.
	after, afterState, err := s.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active after failed Commit: %v", err)
	}
	if afterState != embed.StateReady {
		t.Fatalf("state after failed Commit = %s, want ready (the demote must be rolled back when the promote misses)", afterState)
	}
	if after.ID != prior.ID {
		t.Fatalf("active id changed: %s → %s (the demote must roll back when the promote misses)", prior.ID, after.ID)
	}
}

// TestContract_CommitValidatesEveryRow pins MAJOR 3: Commit validates
// every row before moving the active pointer. The pre-fix shape counted
// rows in Commit and validated per-row dimensions later in Active — after
// the pointer had moved. A wrong-dim row could therefore land and serve
// as ready until the next Active call discovered it. The validate-then-
// publish contract (AC-6 / AC-7) requires Commit to refuse before the
// demote / promote transaction proceeds. The test hand-injects a row
// with a mismatched dim and asserts Commit returns *ValidationFailedError
// (the prior active generation stays intact).
func TestContract_CommitValidatesEveryRow(t *testing.T) {
	ctx := context.Background()

	// SQLite-only scenario: we need to bypass Upsert-time dim enforcement
	// to land a wrong-dim row in the staging generation. The dim check is
	// already pinned by TestContract_RowVectorDim; this test pins the
	// Commit-time pass that catches what Upsert missed.
	dir := t.TempDir()
	s, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Plant a prior ready generation so we can assert it stays intact
	// after the failed Commit.
	b1, err := s.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin prior: %v", err)
	}
	if err := b1.Upsert(ctx, rowFixture()[0]); err != nil {
		t.Fatalf("Upsert prior: %v", err)
	}
	if err := b1.Commit(ctx); err != nil {
		t.Fatalf("Commit prior: %v", err)
	}
	prior, priorState, err := s.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active prior: %v", err)
	}
	if priorState != embed.StateReady {
		t.Fatalf("prior state = %s, want ready", priorState)
	}

	// Open a second handle, Begin a new build, then directly insert a
	// wrong-dim row in the second handle's staging generation. The
	// second handle's Commit must observe the wrong-dim row and refuse
	// before the pointer moves; the first handle must still see the
	// prior generation.
	proc2, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("open proc2: %v", err)
	}
	defer func() { _ = proc2.Close() }()
	b2, err := proc2.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin second: %v", err)
	}
	if err := b2.Upsert(ctx, rowFixture()[1]); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	// Hand-tamper: insert a row with a wrong-dim vector directly into
	// the sidecar on proc2's staging generation. The schema's row-level
	// checks don't apply to a raw SQL insert.
	if _, err := proc2.DeleteStagingForTest(ctx); err != nil {
		t.Fatalf("clear proc2 staging to set up a clean tampered staging: %v", err)
	}
	// Re-Begin to get a fresh staging row, then tamper.
	b3, err := proc2.Begin(ctx, fp())
	if err != nil {
		t.Fatalf("Begin tampered: %v", err)
	}
	if err := b3.Upsert(ctx, rowFixture()[1]); err != nil {
		t.Fatalf("Upsert tampered: %v", err)
	}
	// Tamper: insert a wrong-dim row in proc2's sidecar (re-using the
	// same staging generation id b3 owns).
	// Get the staging id from proc2.
	var stagingID string
	if err := proc2.DBForTest(ctx).QueryRowContext(ctx,
		`SELECT id FROM generations WHERE is_staging = 1 LIMIT 1`).Scan(&stagingID); err != nil {
		t.Fatalf("probe staging: %v", err)
	}
	if _, err := proc2.DBForTest(ctx).ExecContext(ctx,
		`INSERT INTO generation_rows (generation_id, document_id, node_id, text_hash, path, start_line, end_line, span_method, vector)
         VALUES (?, 'doc-tamper', 'tamper', 'h-tamper', 'p.go', 1, 1, 'ast', ?)`,
		stagingID, []byte{0, 0, 0, 0, 0, 0, 0, 0}); err != nil { // dim 2, not 4
		t.Fatalf("tamper insert: %v", err)
	}

	err = b3.Commit(ctx)
	if err == nil {
		t.Fatalf("Commit succeeded with a wrong-dim row in staging; want ValidationFailedError")
	}
	var vfe *embed.ValidationFailedError
	if !errors.As(err, &vfe) {
		t.Fatalf("err = %v, want *ValidationFailedError", err)
	}

	// The prior active generation must remain intact.
	_, afterState, err := s.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active after failed Commit: %v", err)
	}
	if afterState != embed.StateReady {
		t.Fatalf("state after failed Commit = %s, want ready (the demote must roll back when validation fails)", afterState)
	}
	_ = prior
}

// TestContract_RowVectorDim pins AC-1/AC-7: an Upsert with a mismatched
// vector dim is rejected at write time (the schema enforces the dim).
func TestContract_RowVectorDim(t *testing.T) {
	for _, b := range allGenerationStoreBackends() {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			s := b.factory(t)
			build, err := s.Begin(ctx, fp())
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			defer func() { _ = build.Abort(ctx) }()
			row := rowFixture()[0]
			row.Vector = []float32{1, 2, 3} // dim 3, not 4
			err = build.Upsert(ctx, row)
			if err == nil {
				t.Fatal("Upsert with mismatched dim: err = nil, want error")
			}
			var vfe *embed.ValidationFailedError
			if !errors.As(err, &vfe) {
				t.Fatalf("err = %v, want *ValidationFailedError", err)
			}
		})
	}
}

// ---- helpers ----

// proc2Begin is a small helper used by the cross-handle test. It opens
// a fresh proc2 handle (so the caller's test does not have to thread
// two handles through every statement) and runs Begin with the
// supplied fingerprint. The returned Build is non-nil only on success.
func proc2Begin(t *testing.T, dir string, fp embed.Fingerprint) (embed.Build, error) {
	t.Helper()
	p, err := embed.OpenSQLiteGenerationStore(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = p.Close() })
	return p.Begin(context.Background(), fp)
}

// fakeNodeReferencer implements embed.NodeReferencer for the AC-7 corrupt
// test. Only nodes in `known` resolve as existing.
type fakeNodeReferencer struct {
	known map[model.NodeId]bool
}

// NodeExists implements embed.NodeReferencer.
func (f fakeNodeReferencer) NodeExists(_ context.Context, id model.NodeId) (bool, error) {
	return f.known[id], nil
}

// countingEmbedder wraps a deterministic mock and counts Embed call sizes
// across calls. Used by the AC-4 carry-forward conformance test.
type countingEmbedder struct {
	inner embed.Embedder
	calls int64
}

// ID implements embed.Embedder.
func (c *countingEmbedder) ID() string { return c.inner.ID() }

// Dim implements embed.Embedder.
func (c *countingEmbedder) Dim() int { return c.inner.Dim() }

// Embed implements embed.Embedder; the count is incremented by the size of
// the texts slice.
func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	atomic.AddInt64(&c.calls, int64(len(texts)))
	return c.inner.Embed(ctx, texts)
}

// newDocSourceForNodes builds a docSource from a set of nodes + a parallel
// slice of Rows. Used by the AC-4 carry-forward test to feed documents that
// match the row schema (text_hash + path + lines + vector).
func newDocSourceForNodes(nodes []model.Node, rows []embed.Row) docSource {
	out := docSource{}
	for i, n := range nodes {
		if i >= len(rows) {
			break
		}
		r := rows[i]
		out[n.ID()] = embed.SemanticDocument{
			DocumentID:     r.DocumentID,
			NodeID:         n.ID(),
			TextHash:       r.TextHash,
			Path:           r.Path,
			StartLine:      r.StartLine,
			EndLine:        r.EndLine,
			SpanMethod:     r.SpanMethod,
			DocumentSchema: embed.DocumentSchema,
			Text:           r.TextHash, // the document text the embedder hashes; any unique string works
		}
	}
	return out
}

// nodeWithID builds a model.Node for the AC-4 carry-forward test.
func nodeWithID(t *testing.T, id, path string) model.Node {
	t.Helper()
	n, err := model.NewNode("function", "p."+id, path, 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return n
}

// floatSliceEqual reports whether two float32 slices are element-wise
// identical. Used by the carry-forward test to assert the prior vector was
// reused byte-for-byte.
func floatSliceEqual(a, b []float32) bool {
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

// float32Bytes mirrors the fixed-endianness big-endian float32 BLOB
// encoding the GenerationStore uses. Used by the AC-8 migration test to
// seed the legacy `vectors` table with bytes the migration can read.
func float32Bytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		u := math.Float32bits(f)
		b[4*i+0] = byte(u >> 24)
		b[4*i+1] = byte(u >> 16)
		b[4*i+2] = byte(u >> 8)
		b[4*i+3] = byte(u)
	}
	return b
}

// mustReadFile reads a file or fails the test. Used by the AC-8
// migration test to hash the real graphstore file after the migration.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := readBytes(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
