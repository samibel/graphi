package embed_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

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
			if _, err := embed.GenerateAndPersist(ctx, reg, nodes, docs, embed.NewIndex(), s); err != nil {
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
			res, err := embed.GenerateAndPersist(ctx, reg, nodes, docs2, embed.NewIndex(), s)
			if err != nil {
				t.Fatalf("second GenerateAndPersist: %v", err)
			}
			if got := atomic.LoadInt64(&counter.calls); got != 1 {
				t.Fatalf("second pass embed calls = %d, want 1 (only b changed)", got)
			}
			if res.Reused != 2 {
				t.Fatalf("second pass Reused = %d, want 2 (a, c carried forward)", res.Reused)
			}
			if res.Embedded != 3 {
				t.Fatalf("second pass Embedded = %d, want 3", res.Embedded)
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
// property by aborting mid-build, then opening a fresh store handle on the
// same meta DB (process restart). The prior active generation must remain
// intact and the stale staging row must be dropped on the next Begin.
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
	// Simulate a crash: do NOT call Abort/Commit. Just close the process.
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
// The invariant that IS load-bearing, and the one asserted here: **at most one
// build is ever in flight at a time**. Each goroutine either wins the slot or
// observes ErrBuildInProgress; a winner increments a depth counter for the
// duration of its build, and a depth above one at any moment means two builds
// overlapped — which is what "the active pointer must never point at an
// unvalidated generation" ultimately rests on. Every outcome must be one of
// those two (no third error), the counts must add up, and the store must end
// on a validated generation. Run with -race to catch any data races.
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
				inFlight int64
				maxDepth int64
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
					// Depth is raised for exactly the window in which this
					// build owns the staging slot. Two overlapping builds
					// would show a depth of 2 here even if both later
					// committed cleanly.
					if d := atomic.AddInt64(&inFlight, 1); d > atomic.LoadInt64(&maxDepth) {
						atomic.StoreInt64(&maxDepth, d)
					}
					err = b.Commit(ctx)
					atomic.AddInt64(&inFlight, -1)
					if err != nil {
						t.Errorf("Commit: %v", err)
					}
				}()
			}
			wg.Wait()
			if got := atomic.LoadInt64(&maxDepth); got > 1 {
				t.Fatalf("max concurrent builds in flight = %d, want 1: two builds owned the staging slot at once", got)
			}
			if winners+rejects != n {
				t.Fatalf("winners %d + rejects %d = %d, want %d: some goroutine got neither the slot nor ErrBuildInProgress", winners, rejects, winners+rejects, n)
			}
			if winners == 0 {
				t.Fatalf("winners = 0: every Begin was rejected, so the slot was never released")
			}
			// The active pointer is on a validated generation (ready).
			_, state, err := s.Active(ctx, fp(), nil)
			if err != nil {
				t.Fatalf("Active: %v", err)
			}
			if state != embed.StateReady {
				t.Fatalf("state after concurrent build = %s, want ready (the active pointer must never be unvalidated)", state)
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
// sidecar contains a legacy `vectors` table migrates it into a v1
// generation marked stale; the migration is idempotent (running twice is a
// no-op); and the lexical graphstore is byte-untouched.
//
// We synthesize a sidecar containing the legacy `vectors` table plus a
// stub "graph" file (whose sha256 we capture before and after the
// migration).
func TestContract_MigrateFromLegacyVectors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// Create a stub graph file the migration MUST NOT touch.
	graphPath := filepath.Join(dir, "graph.db")
	graphBefore := []byte("graphi graph sidecar (stub) - sha256 must be invariant under migrate")
	if err := writeFile(graphPath, graphBefore); err != nil {
		t.Fatalf("write graph stub: %v", err)
	}
	hashBefore := sha256Hex(graphBefore)

	// Open the meta db, create the legacy vectors table, seed two rows.
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
		{"mock", "legacy-a", 4, float32Bytes([]float32{1, 0, 0, 0})},
		{"mock", "legacy-b", 4, float32Bytes([]float32{0, 1, 0, 0})},
		{"ollama:nomic-embed-text", "legacy-c", 4, float32Bytes([]float32{0, 0, 1, 0})},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO vectors (node_id, embedder_id, dim, vec) VALUES (?, ?, ?, ?)`,
			seed.node, seed.embedder, seed.dim, seed.vec); err != nil {
			_ = db.Close()
			t.Fatalf("seed %s: %v", seed.node, err)
		}
	}
	_ = db.Close()

	// First migration.
	res, err := embed.MigrateFromLegacyVectors(ctx, mustReopenSQLite(t, metaPath))
	if err != nil {
		t.Fatalf("MigrateFromLegacyVectors: %v", err)
	}
	if !res.Migrated {
		t.Fatalf("first migration Migrated = false, want true")
	}
	if res.RowsMigrated != 3 {
		t.Fatalf("RowsMigrated = %d, want 3", res.RowsMigrated)
	}
	if res.GenerationID == "" {
		t.Fatalf("GenerationID empty after first migration")
	}
	if len(res.EmbedderIDs) != 2 {
		t.Fatalf("EmbedderIDs = %v, want 2 distinct ids", res.EmbedderIDs)
	}
	// The lexical graph file is byte-untouched.
	graphAfterBytes := mustReadFile(t, graphPath)
	if sha256Hex(graphAfterBytes) != hashBefore {
		t.Fatalf("graph file sha256 changed: %s → %s", hashBefore, sha256Hex(graphAfterBytes))
	}

	// Open the new store on the same sidecar. Active against the current
	// (v2) fingerprint must report StateStale (the v1 generation lacks
	// the new fields); the v1 generation id must be the migration's.
	store, err := embed.OpenSQLiteGenerationStore(ctx, dir)
	if err != nil {
		t.Fatalf("OpenSQLiteGenerationStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	_, state, err := store.Active(ctx, fp(), nil)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if state != embed.StateStale {
		t.Fatalf("state = %s, want stale (the v1 generation lacks the new fingerprint fields)", state)
	}
	// The migrated rows are readable through Load (so an operator can
	// inspect or decide to discard them).
	rows := loadGenRows(t, store, ctx, res.GenerationID)
	if len(rows) != 3 {
		t.Fatalf("migrated rows = %d, want 3", len(rows))
	}

	// Second migration is a no-op (idempotent).
	res2, err := embed.MigrateFromLegacyVectors(ctx, mustReopenSQLite(t, metaPath))
	if err != nil {
		t.Fatalf("second MigrateFromLegacyVectors: %v", err)
	}
	if res2.Migrated {
		t.Fatalf("second migration Migrated = true, want false (idempotent)")
	}
	if res2.RowsMigrated != 3 {
		t.Fatalf("second migration RowsMigrated = %d, want 3 (the prior migration's count)", res2.RowsMigrated)
	}
	if res2.GenerationID != res.GenerationID {
		t.Fatalf("second migration GenerationID = %s, want %s (same v1 id)", res2.GenerationID, res.GenerationID)
	}
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

// writeFile is the helper used by the migration test to create a stub
// graph file (and keep the test self-contained).
func writeFile(path string, data []byte) error {
	return writeBytes(path, data)
}

// mustReadFile reads a file or fails the test.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := readBytes(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// mustReopenSQLite opens a sqlite db at path and returns the handle plus a
// cleanup func. Used by the migration test for the second migration call.
func mustReopenSQLite(t *testing.T, path string) *sqlDBHandle {
	t.Helper()
	h, err := openSQLiteForTest(path)
	if err != nil {
		t.Fatalf("reopen %s: %v", path, err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}
