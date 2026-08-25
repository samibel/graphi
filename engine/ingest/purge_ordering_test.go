package ingest_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// ============================================================================
// SW-169 — the PARITY-001 hardening: a delete-shaped kill test and an
// incremental write-batch ordering guard.
//
// PARITY-001 was a PHASE-ORDERING fault, not a logic fault: ingestChanged
// called linkFiles BEFORE the deleted-path purge, so the linker's symbol index
// (built by streaming the LIVE store) still contained a deleted file's nodes,
// resolved a cross-package call intra-module, and never minted the interned
// external node a full pass mints. The purge then cascaded the edges away and
// the graph stayed one node and one edge short — permanently.
//
// The defect is closed. What these tests add is the guarantee that the ordering
// cannot silently drift back:
//
//   - TestPurgeOrdering_DeleteShapedKill is the KILL TEST. It is red against the
//     pre-fix ordering and green against the current one, on both stores, in
//     every delete shape the PARITY-001 record's corrected precondition names.
//   - TestIncrementalBatchShape_DeleteShapedOrderingGuard is the BATCH-SHAPE
//     GUARD. It observes the real write batches an incremental pass opens and
//     fails if their number, their content or their order departs from the
//     declared shape — so adding, merging or reordering a batch breaks a test
//     rather than a graph.
//   - TestIncrementalBatchShapeGuard_IsNonVacuous proves the guard OBSERVES a
//     difference where one exists, by feeding its matcher the pre-fix shape and
//     five other mutations and requiring each to be rejected (6 mutations in
//     total), plus a control requiring a REAL log to be accepted.
//
// The disposition ruling (separate committed purge batch, NOT folded into
// batch 1) and the reverse-dependency-translation ordering ruling are recorded
// in docs/adr/0004-ingest-recovery-disposition.md, §"2026-08-19".
// ============================================================================

// ---------------------------------------------------------------------------
// Test-only store instrumentation.
//
// recordingStore wraps a real Graphstore and records the ORDERED sequence of
// write batches an ingest pass opens, the ops issued into each, and whether it
// committed. It changes no behaviour: BeginBatch delegates to the inner store's
// own batch (so SQLite keeps one transaction per session and MemStore keeps its
// pass-through), and every optional port ingest reaches for (GraphScanner,
// TrustAggregatePort) is forwarded, so no fallback path is silently taken
// instead of the native one.
// ---------------------------------------------------------------------------

type graphOp struct {
	Op    string // put-node | put-edge | del-node | del-edge
	ID    string
	Kind  string
	Name  string // node qualified name, or edge "from -> to"
	Batch int    // index of the owning batch, -1 for a direct (unbatched) write
	Seq   int    // global event order
}

type batchRecord struct {
	Index     int
	BeginSeq  int
	EndSeq    int // sequence number of Commit/Rollback, -1 while open
	Committed bool
	Ops       []graphOp
}

func (b *batchRecord) count(op string) int {
	n := 0
	for _, o := range b.Ops {
		if o.Op == op {
			n++
		}
	}
	return n
}

func (b *batchRecord) deletedNodes() []string {
	var out []string
	for _, o := range b.Ops {
		if o.Op == "del-node" {
			out = append(out, o.ID)
		}
	}
	sort.Strings(out)
	return out
}

func (b *batchRecord) putsNode(qualifiedName string) bool {
	for _, o := range b.Ops {
		if o.Op == "put-node" && o.Name == qualifiedName {
			return true
		}
	}
	return false
}

type recordingStore struct {
	graphstore.Graphstore

	mu      sync.Mutex
	on      bool // recording enabled (flipped on after the seeding full pass)
	seq     int
	batches []*batchRecord
	direct  []graphOp // writes issued outside any batch
}

// start begins recording. It is called after the seeding full pass so the log
// describes exactly one incremental pass.
func (r *recordingStore) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.on = true
	r.seq = 0
	r.batches = nil
	r.direct = nil
}

func (r *recordingStore) next() int {
	r.seq++
	return r.seq
}

func (r *recordingStore) record(b *batchRecord, op graphOp) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.on {
		return
	}
	op.Seq = r.next()
	if b == nil {
		op.Batch = -1
		r.direct = append(r.direct, op)
		return
	}
	op.Batch = b.Index
	b.Ops = append(b.Ops, op)
}

func nodeOp(op string, n model.Node) graphOp {
	return graphOp{Op: op, ID: string(n.ID()), Kind: n.Kind(), Name: n.QualifiedName()}
}

func edgeOp(op string, e model.Edge) graphOp {
	return graphOp{Op: op, ID: string(e.ID()), Kind: e.Kind(), Name: string(e.From()) + " -> " + string(e.To())}
}

// Direct (unbatched) writes on the store itself. ingestChanged must issue none;
// recording them is what makes that assertable rather than assumed.

func (r *recordingStore) PutNode(ctx context.Context, n model.Node) error {
	r.record(nil, nodeOp("put-node", n))
	return r.Graphstore.PutNode(ctx, n)
}

func (r *recordingStore) PutEdge(ctx context.Context, e model.Edge) error {
	r.record(nil, edgeOp("put-edge", e))
	return r.Graphstore.PutEdge(ctx, e)
}

func (r *recordingStore) DeleteNode(ctx context.Context, id model.NodeId) error {
	r.record(nil, graphOp{Op: "del-node", ID: string(id)})
	return r.Graphstore.DeleteNode(ctx, id)
}

func (r *recordingStore) DeleteEdge(ctx context.Context, id model.EdgeId) error {
	r.record(nil, graphOp{Op: "del-edge", ID: string(id)})
	return r.Graphstore.DeleteEdge(ctx, id)
}

// BeginBatch makes the wrapper a graphstore.Batcher, so graphstore.BeginBatch
// routes through here while the inner store's native session is still what runs
// underneath.
func (r *recordingStore) BeginBatch(ctx context.Context) (graphstore.Batch, error) {
	inner, err := graphstore.BeginBatch(ctx, r.Graphstore)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	rec := &batchRecord{Index: len(r.batches), EndSeq: -1}
	if r.on {
		rec.BeginSeq = r.next()
		r.batches = append(r.batches, rec)
	}
	r.mu.Unlock()
	return &recordingBatch{store: r, inner: inner, rec: rec}, nil
}

// GraphScanner / TrustAggregatePort forwarding.

func (r *recordingStore) NodeIDs(ctx context.Context) ([]model.NodeId, error) {
	return graphstore.NodeIDsOf(ctx, r.Graphstore)
}

func (r *recordingStore) ScanNodes(ctx context.Context, fn func(model.Node) error) error {
	return graphstore.ForEachNode(ctx, r.Graphstore, fn)
}

func (r *recordingStore) ScanEdges(ctx context.Context, fn func(model.Edge) error) error {
	return graphstore.ForEachEdge(ctx, r.Graphstore, fn)
}

func (r *recordingStore) TrustStats(ctx context.Context, topN int) (graphstore.TrustStats, error) {
	agg, ok := r.Graphstore.(graphstore.TrustAggregatePort)
	if !ok {
		return graphstore.TrustStats{}, fmt.Errorf("recordingStore: inner store has no TrustAggregatePort")
	}
	return agg.TrustStats(ctx, topN)
}

type recordingBatch struct {
	store *recordingStore
	inner graphstore.Batch
	rec   *batchRecord
}

func (b *recordingBatch) PutNode(ctx context.Context, n model.Node) error {
	b.store.record(b.rec, nodeOp("put-node", n))
	return b.inner.PutNode(ctx, n)
}

func (b *recordingBatch) PutEdge(ctx context.Context, e model.Edge) error {
	b.store.record(b.rec, edgeOp("put-edge", e))
	return b.inner.PutEdge(ctx, e)
}

func (b *recordingBatch) DeleteNode(ctx context.Context, id model.NodeId) error {
	b.store.record(b.rec, graphOp{Op: "del-node", ID: string(id)})
	return b.inner.DeleteNode(ctx, id)
}

func (b *recordingBatch) DeleteEdge(ctx context.Context, id model.EdgeId) error {
	b.store.record(b.rec, graphOp{Op: "del-edge", ID: string(id)})
	return b.inner.DeleteEdge(ctx, id)
}

func (b *recordingBatch) Commit(ctx context.Context) error {
	err := b.inner.Commit(ctx)
	b.store.mu.Lock()
	if b.store.on && err == nil {
		b.rec.Committed = true
		b.rec.EndSeq = b.store.next()
	}
	b.store.mu.Unlock()
	return err
}

func (b *recordingBatch) Rollback() error {
	err := b.inner.Rollback()
	b.store.mu.Lock()
	if b.store.on && b.rec.EndSeq < 0 {
		b.rec.EndSeq = b.store.next()
	}
	b.store.mu.Unlock()
	return err
}

// dump renders the recorded log; every guard failure prints it, because the
// log is what a future reader needs to see to know what the shape became.
func (r *recordingStore) dump() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sb strings.Builder
	for _, b := range r.batches {
		fmt.Fprintf(&sb, "batch %d: begin@%d end@%d committed=%v ops=%d\n", b.Index, b.BeginSeq, b.EndSeq, b.Committed, len(b.Ops))
		for _, op := range b.Ops {
			fmt.Fprintf(&sb, "    [%d] %-9s kind=%-10s %s (%s)\n", op.Seq, op.Op, op.Kind, op.Name, op.ID)
		}
	}
	for _, op := range r.direct {
		fmt.Fprintf(&sb, "DIRECT  [%d] %-9s kind=%-10s %s (%s)\n", op.Seq, op.Op, op.Kind, op.Name, op.ID)
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Fixtures. Every shape below deletes a file that declares a symbol ANOTHER
// package calls through an INTRA-MODULE import — the corrected PARITY-001
// precondition. The shapes differ in exactly the dimensions review round 1
// found to matter.
// ---------------------------------------------------------------------------

const (
	// The plain shape: the deleted file is the package's only file.
	shapeSoleFile = "sole_file_of_package"
	// Corrected precondition, half 1: the PACKAGE SURVIVES via a second file.
	// The first statement of the defect ("the sole file of a package") was too
	// narrow; this shape diverged identically before the fix.
	shapePkgSurvives = "package_survives_via_second_file"
	// Corrected precondition, half 2: the IMPORTER IS NAMED EXPLICITLY in the
	// change set, so it is certainly re-parsed and re-linked. This shape is what
	// refuted the dependent-cascade explanation; it diverged identically too.
	shapeImporterNamed = "importer_named_in_change_set"
	// Adversarial (story test note): a change set that MIXES the delete with a
	// same-package MOVE, so the purge runs beside an identity-preserving,
	// path-changing rename.
	shapeMixedMove = "delete_plus_same_package_move"
	// Adversarial: MIXES the delete with a cross-package RENAME of a DIFFERENT
	// package's symbol, so one import in the same file resolves and one does not.
	shapeMixedRename = "delete_plus_cross_package_rename"
	// From the adversarial review: a SURVIVING file loses a symbol in the same
	// pass, so `commitParsed` issues an identity-not-reproduced DeleteNode into
	// the parse-write batch. A guard that reads "batch 1 contains a delete" as
	// "the purge was folded in" false-positives here, on correct product code.
	shapeShrinkingSurvivor = "delete_plus_surviving_file_loses_a_symbol"
	// From the adversarial review: the deleted file was the SOLE referencer of a
	// stdlib symbol, so `sweepOrphanExternalNodes` opens its own extra batch.
	// A strictly positional four-batch shape false-positives here too.
	shapeOrphansExternal = "delete_that_also_orphans_an_external"
)

const (
	fixtureGoMod = "module example.com/m\n\ngo 1.21\n"
	fixtureCart  = "package shop\n\nimport \"example.com/m/tax\"\n\nfunc Checkout() int { return price() + tax.Rate() }\n"
	fixturePrice = "package shop\n\nfunc price() int { return 10 }\n"
	fixtureTax   = "package tax\n\nfunc Rate() int { return 7 }\n"
	fixtureHelp  = "package tax\n\nfunc Helper() int { return 1 }\n"
)

type deleteShape struct {
	name string
	// initial is the tree the seeding FULL pass indexes.
	initial map[string]string
	// mutate applies the change to that tree on disk.
	mutate func(t *testing.T, repo string)
	// after is the resulting tree. A full pass over it is the reference the
	// incremental snapshot must byte-match.
	after map[string]string
	// changed is the change set reported to IngestChanged.
	changed []string
	// deletedFile is the path whose nodes the purge must remove.
	deletedFile string
	// The witness. wantAbsent proves the purge actually happened (a stale row
	// that was never purged is indistinguishable from a correctly retained one
	// without it); wantPresent proves the pass did not simply delete everything;
	// wantEdge is the edge whose absence WAS the defect.
	wantPresent []string
	wantAbsent  []string
	wantEdge    [2]string
}

func mergeFiles(base map[string]string, extra map[string]string, drop ...string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	for _, k := range drop {
		delete(out, k)
	}
	return out
}

func removeFiles(rel ...string) func(t *testing.T, repo string) {
	return func(t *testing.T, repo string) {
		t.Helper()
		for _, r := range rel {
			if err := os.Remove(filepath.Join(repo, filepath.FromSlash(r))); err != nil {
				t.Fatalf("remove %s: %v", r, err)
			}
		}
	}
}

func deleteShapedFixture(name string) deleteShape {
	base := map[string]string{
		"go.mod":        fixtureGoMod,
		"shop/cart.go":  fixtureCart,
		"shop/price.go": fixturePrice,
		"tax/tax.go":    fixtureTax,
	}
	// The external node a FULL pass mints once tax.Rate is gone: the call site
	// survives, its import no longer resolves, so it is interned as external.
	const wantExternal = "example.com/m/tax.Rate"

	switch name {
	case shapeSoleFile:
		return deleteShape{
			name: name, initial: base,
			mutate:      removeFiles("tax/tax.go"),
			after:       mergeFiles(base, nil, "tax/tax.go"),
			changed:     []string{"tax/tax.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "shop.price", wantExternal},
			wantAbsent:  []string{"tax.Rate"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	case shapePkgSurvives:
		initial := mergeFiles(base, map[string]string{"tax/helper.go": fixtureHelp})
		return deleteShape{
			name: name, initial: initial,
			mutate:      removeFiles("tax/tax.go"),
			after:       mergeFiles(initial, nil, "tax/tax.go"),
			changed:     []string{"tax/tax.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "shop.price", "tax.Helper", wantExternal},
			wantAbsent:  []string{"tax.Rate"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	case shapeImporterNamed:
		return deleteShape{
			name: name, initial: base,
			mutate:      removeFiles("tax/tax.go"),
			after:       mergeFiles(base, nil, "tax/tax.go"),
			changed:     []string{"tax/tax.go", "shop/cart.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "shop.price", wantExternal},
			wantAbsent:  []string{"tax.Rate"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	case shapeMixedMove:
		return deleteShape{
			name: name, initial: base,
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				removeFiles("tax/tax.go", "shop/price.go")(t, repo)
				mustWrite(t, repo, "shop/moved.go", fixturePrice)
			},
			after:       mergeFiles(base, map[string]string{"shop/moved.go": fixturePrice}, "tax/tax.go", "shop/price.go"),
			changed:     []string{"tax/tax.go", "shop/price.go", "shop/moved.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "shop.price", wantExternal},
			wantAbsent:  []string{"tax.Rate"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	case shapeMixedRename:
		const cart2 = "package shop\n\nimport (\n\t\"example.com/m/tax\"\n\t\"example.com/m/fee\"\n)\n\nfunc Checkout() int { return tax.Rate() + fee.Flat() }\n"
		const cart3 = "package shop\n\nimport (\n\t\"example.com/m/tax\"\n\t\"example.com/m/fee\"\n)\n\nfunc Checkout() int { return tax.Rate() + fee.Fixed() }\n"
		initial := map[string]string{
			"go.mod":       fixtureGoMod,
			"shop/cart.go": cart2,
			"tax/tax.go":   fixtureTax,
			"fee/fee.go":   "package fee\n\nfunc Flat() int { return 3 }\n",
		}
		return deleteShape{
			name: name, initial: initial,
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				removeFiles("tax/tax.go")(t, repo)
				mustWrite(t, repo, "fee/fee.go", "package fee\n\nfunc Fixed() int { return 3 }\n")
				mustWrite(t, repo, "shop/cart.go", cart3)
			},
			after: map[string]string{
				"go.mod":       fixtureGoMod,
				"shop/cart.go": cart3,
				"fee/fee.go":   "package fee\n\nfunc Fixed() int { return 3 }\n",
			},
			// The importer is named too: it is rewritten on disk, so this is what
			// DriftSet reports. Leaving it out would make the subtest depend on
			// the reverse-dep cascade for a reason unrelated to purge ordering
			// (adversarial review, Q1 fixture-hygiene note).
			changed:     []string{"tax/tax.go", "fee/fee.go", "shop/cart.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "fee.Fixed", wantExternal},
			wantAbsent:  []string{"tax.Rate", "fee.Flat"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	case shapeShrinkingSurvivor:
		const priceWithExtra = "package shop\n\nfunc price() int { return 10 }\n\nfunc Old() int { return 1 }\n"
		initial := mergeFiles(base, map[string]string{"shop/price.go": priceWithExtra})
		return deleteShape{
			name: name, initial: initial,
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				removeFiles("tax/tax.go")(t, repo)
				mustWrite(t, repo, "shop/price.go", fixturePrice) // shop.Old disappears
			},
			after:       mergeFiles(base, nil, "tax/tax.go"),
			changed:     []string{"tax/tax.go", "shop/price.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "shop.price", wantExternal},
			wantAbsent:  []string{"tax.Rate", "shop.Old"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	case shapeOrphansExternal:
		// tax/tax.go is the SOLE referencer of fmt.Println, so deleting it
		// orphans that interned external node and the sweep must reap it.
		const taxWithStdlib = "package tax\n\nimport \"fmt\"\n\nfunc Rate() int { fmt.Println(\"rate\"); return 7 }\n"
		initial := mergeFiles(base, map[string]string{"tax/tax.go": taxWithStdlib})
		return deleteShape{
			name: name, initial: initial,
			mutate:      removeFiles("tax/tax.go"),
			after:       mergeFiles(base, nil, "tax/tax.go"),
			changed:     []string{"tax/tax.go"},
			deletedFile: "tax/tax.go",
			wantPresent: []string{"shop.Checkout", "shop.price", wantExternal},
			wantAbsent:  []string{"tax.Rate", "fmt.Println"},
			wantEdge:    [2]string{"shop.Checkout", wantExternal},
		}
	}
	panic("unknown delete shape " + name)
}

func newBackendStore(t *testing.T, backend string) graphstore.Graphstore {
	t.Helper()
	switch backend {
	case "mem":
		s := graphstore.NewMemStore()
		t.Cleanup(func() { _ = s.Close() })
		return s
	case "sqlite":
		s, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "graphi.db"))
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	panic("unknown backend " + backend)
}

func goRegistry() ingest.Parser { return parse.NewDefaultRegistry() }

func storeSnapshotBytes(t *testing.T, s graphstore.Graphstore) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap")
	if err := s.Snapshot(context.Background(), p); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// assertWitness checks the shape's present/absent symbols and its edge against
// a store. Run against BOTH sides on purpose: on the incremental side it is the
// kill assertion, on the full side it is the ANTI-VACUITY assertion — if the
// full pass did not mint the external node either, the byte comparison would be
// satisfiable by two equally empty graphs and would prove nothing.
func assertWitness(t *testing.T, side string, s graphstore.Graphstore, sh deleteShape) {
	t.Helper()
	ctx := context.Background()
	nodes, err := s.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("%s: nodes: %v", side, err)
	}
	byName := make(map[string]model.NodeId, len(nodes))
	for _, n := range nodes {
		byName[n.QualifiedName()] = n.ID()
	}
	for _, want := range sh.wantPresent {
		if _, ok := byName[want]; !ok {
			t.Errorf("%s/%s: expected node %q to be present, graph has %v", sh.name, side, want, sortedKeys(byName))
		}
	}
	for _, gone := range sh.wantAbsent {
		if _, ok := byName[gone]; ok {
			t.Errorf("%s/%s: node %q survived — a stale row that was never purged, not a correctly retained one", sh.name, side, gone)
		}
	}
	from, okF := byName[sh.wantEdge[0]]
	to, okT := byName[sh.wantEdge[1]]
	if !okF || !okT {
		return // already reported above
	}
	edges, err := s.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	if err != nil {
		t.Fatalf("%s: edges: %v", side, err)
	}
	for _, e := range edges {
		if e.From() == from && e.To() == to {
			return
		}
	}
	t.Errorf("%s/%s: missing the edge the defect suppressed: %s --calls--> %s", sh.name, side, sh.wantEdge[0], sh.wantEdge[1])
}

func sortedKeys(m map[string]model.NodeId) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// AC-3 / AC-4 — THE KILL TEST.
// ---------------------------------------------------------------------------

// TestPurgeOrdering_DeleteShapedKill asserts the ordering invariant directly:
// deleting a file that declares a symbol another package calls through an
// intra-module import must produce a graph BYTE-IDENTICAL to a full pass over
// the resulting tree.
//
// It is red against the pre-fix ordering (linkFiles before the purge) and green
// against the current one, on MemStore and SQLite, in all seven shapes
// (14 axes = 7 shapes x {mem, sqlite}).
//
// Read the witness assertions as the load-bearing half: byte parity alone can
// be satisfied by two identically wrong graphs, so each side is additionally
// required to have PURGED tax.Rate, RETAINED the untouched symbols, and MINTED
// the external node plus the call edge whose absence was PARITY-001.
func TestPurgeOrdering_DeleteShapedKill(t *testing.T) {
	ctx := context.Background()
	shapes := []string{shapeSoleFile, shapePkgSurvives, shapeImporterNamed, shapeMixedMove, shapeMixedRename, shapeShrinkingSurvivor, shapeOrphansExternal}
	for _, name := range shapes {
		for _, backend := range []string{"mem", "sqlite"} {
			t.Run(name+"/"+backend, func(t *testing.T) {
				sh := deleteShapedFixture(name)

				incStore := newBackendStore(t, backend)
				inc := newIngester(t, incStore, goRegistry())
				repo := writeRepo(t, sh.initial)
				if err := inc.IngestAll(ctx, repo); err != nil {
					t.Fatalf("seed IngestAll: %v", err)
				}
				sh.mutate(t, repo)
				if err := inc.IngestChanged(ctx, repo, sh.changed); err != nil {
					t.Fatalf("IngestChanged: %v", err)
				}
				incBytes := storeSnapshotBytes(t, incStore)

				fullStore := newBackendStore(t, backend)
				full := newIngester(t, fullStore, goRegistry())
				if err := full.IngestAll(ctx, writeRepo(t, sh.after)); err != nil {
					t.Fatalf("reference IngestAll: %v", err)
				}
				fullBytes := storeSnapshotBytes(t, fullStore)

				// Anti-vacuity first: if the reference graph lacks the external
				// node and the edge, the parity comparison below proves nothing.
				assertWitness(t, "full", fullStore, sh)
				assertWitness(t, "incremental", incStore, sh)

				if !bytes.Equal(incBytes, fullBytes) {
					t.Fatalf("PARITY-001 ordering regression (%s/%s): incremental != full after a delete-shaped change.\n"+
						"The deleted-path purge must run AND COMMIT before linkFiles (see the PARITY-001 FIX comment in engine/ingest/ingest.go).\ninc =%s\nfull=%s",
						name, backend, incBytes, fullBytes)
				}
			})
		}
	}
}

// TestPurgeOrdering_DeleteThenReaddConverges is the executable form of the
// AC-1 DISPOSITION ruling: the deleted-path purge keeps its OWN committed
// batch and is NOT folded into batch 1 the way IngestAll folds it.
//
// Folding it is not merely a style choice. Measured on a folded prototype: the
// whole suite still passes, conformance still passes, the delete-shaped kill
// test above still passes — and THIS sequence flips from converging to
// diverging. The reason is the reverse-dependency translation, which sits
// between batch 1's commit and the purge: with the purge folded in, the
// translation runs over a store the deleted package has already left, so
// DirsForImport finds no directory and the importer's cascade row is written
// under the unresolvable import path "example.com/m/tax" instead of the
// directory "tax". dependentsOf only ever looks a row up by directory or by
// file path, so the row is unreachable, the importer is never re-linked on the
// re-add, and `graphi sync` stays diverged from `graphi rebuild` — the exact
// defect PARITY-004 records for the full pass, imported into the incremental
// path by the fold.
//
// So this test is the guard on the disposition itself, where the batch-shape
// guard is the guard on the phase order.
func TestPurgeOrdering_DeleteThenReaddConverges(t *testing.T) {
	ctx := context.Background()
	for _, backend := range []string{"mem", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			sh := deleteShapedFixture(shapeSoleFile)
			store := newBackendStore(t, backend)
			ing := newIngester(t, store, goRegistry())
			repo := writeRepo(t, sh.initial)
			if err := ing.IngestAll(ctx, repo); err != nil {
				t.Fatalf("seed IngestAll: %v", err)
			}
			sh.mutate(t, repo)
			if err := ing.IngestChanged(ctx, repo, sh.changed); err != nil {
				t.Fatalf("delete IngestChanged: %v", err)
			}
			mustWrite(t, repo, "tax/tax.go", fixtureTax)
			if err := ing.IngestChanged(ctx, repo, []string{"tax/tax.go"}); err != nil {
				t.Fatalf("re-add IngestChanged: %v", err)
			}
			got := storeSnapshotBytes(t, store)

			refStore := newBackendStore(t, backend)
			ref := newIngester(t, refStore, goRegistry())
			final := mergeFiles(sh.after, map[string]string{"tax/tax.go": fixtureTax})
			if err := ref.IngestAll(ctx, writeRepo(t, final)); err != nil {
				t.Fatalf("reference IngestAll: %v", err)
			}
			want := storeSnapshotBytes(t, refStore)
			if !bytes.Equal(got, want) {
				t.Fatalf("delete-then-re-add diverges from a full rebuild (%s).\n"+
					"The likely cause is the deleted-path purge having been folded into batch 1, which moves the reverse-dependency translation "+
					"to the far side of the purge and makes the importer's cascade row unreachable. See docs/adr/0004-ingest-recovery-disposition.md, §2026-08-19.\ninc =%s\nfull=%s",
					backend, got, want)
			}
			// Non-vacuity: the reference must actually contain the re-linked
			// cross-package edge, or the comparison is between two empty graphs.
			nodes, err := refStore.Nodes(ctx, graphstore.Query{})
			if err != nil {
				t.Fatalf("nodes: %v", err)
			}
			var found bool
			for _, n := range nodes {
				if n.QualifiedName() == "tax.Rate" {
					found = true
				}
			}
			if !found {
				t.Fatal("reference graph has no tax.Rate after the re-add — the fixture stopped exercising the sequence")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// AC-1 / AC-2 — THE BATCH-SHAPE GUARD.
// ---------------------------------------------------------------------------

// declaredIncrementalBatchShape is the write-batch shape an incremental pass
// over a Go change set that DELETES a file is declared to have. It is the
// disposition SW-169 adopted and ADR 0004 records: the purge keeps its OWN
// batch, committed between the parse-write batch and the link batch, rather
// than being folded into batch 1 the way IngestAll folds it.
//
// The specs are matched against the observed batches IN ORDER, so a batch that
// is added, removed, merged into a neighbour or swapped with one breaks this
// guard. Two of the phases are CONDITIONAL in `ingestChanged` and are therefore
// declared optional here; the rest are required.
//
// This started out as a strictly positional four-entry list, and an adversarial
// review defeated that version with two ordinary change sets against unmodified
// product code. Both are now permanent fixtures (`shapeShrinkingSurvivor`,
// `shapeOrphansExternal`) rather than caveats:
//
//   - `commitParsed` legitimately issues `DeleteNode` into the parse-write batch
//     for a previously-committed node the new parse does NOT re-produce (a
//     surviving file that lost a symbol; engine/ingest/parsefile.go). So "batch
//     1 contains a delete" is NOT evidence that the purge was folded in. What
//     IS evidence is a delete of a node belonging to a PURGED PATH, and that is
//     what spec 1 tests.
//   - `sweepOrphanExternalNodes` opens its OWN batch between the link batch and
//     typeresolve, but only when this pass orphaned an interned external node.
//     It is declared optional and identified by CONTENT (a pure delete batch
//     touching no purged-path node), never by position.
//
// What this guard cannot see, stated rather than left for the next reviewer to
// find: it observes WRITES. PARITY-001 was caused by a READ — `linkFiles`
// streaming the live store — so a drift in read ordering that leaves the write
// shape intact is invisible here. The real example is sharing the reverse-dep
// pass's SymbolIndex with linkFiles, which reintroduces PARITY-001 with an
// unchanged batch shape (caught by the kill test, and by the graphstore's own
// `edge references unknown node` endpoint check, never by this guard).
//
// CORRECTED in review round 1. This comment used to name a SECOND such blind
// spot — hoisting `sweepOrphanExternalNodes` to just after the purge commit —
// and claimed it passes this guard. It does NOT. The hoist was performed and
// measured: this guard FAILS on both stores on shapeOrphansExternal with
//
//	batch 2 does not match required phase "3-link (cross-file re-link; mints the
//	  interned external node)": the link batch did not mint the external node
//	  "example.com/m/tax.Rate"
//
// because hoisting the sweep puts its batch BEFORE the link batch, so spec 3's
// content check lands on a pure-delete batch. The hoist is invisible to the
// guard only on shapes that orphan NO external node and so open no sweep batch:
// measured PASS on shapeSoleFile, shapePkgSurvives, shapeImporterNamed and
// shapeShrinkingSurvivor, FAIL on shapeOrphansExternal. So the optional,
// content-identified row 4 is not just false-positive insurance — it is what
// makes the hoist visible at all. TestPurgeOrdering_DeleteThenReaddConverges and
// TestLink_GoExternalNode_InterningLifecycle do also catch it, as first stated.
// The guard is one layer of three; it is not the whole net.
type batchSpec struct {
	name     string
	optional bool
	check    func(b *batchRecord, ctx shapeContext) error
}

type shapeContext struct {
	// purgedNodeIDs are the ids of the nodes the deleted file owned, read from
	// the graph BEFORE the incremental pass ran.
	purgedNodeIDs []string
	// externalName is the external node the link phase must mint.
	externalName string
}

func (sc shapeContext) isPurged(id string) bool {
	for _, p := range sc.purgedNodeIDs {
		if p == id {
			return true
		}
	}
	return false
}

// purgedDeletes returns the batch's deletes that belong to a purged path, and
// the ones that do not. The split is what separates the deleted-path purge from
// `commitParsed`'s identity-not-reproduced deletes and from the orphan sweep.
func (sc shapeContext) purgedDeletes(b *batchRecord) (purged, other []string) {
	for _, id := range b.deletedNodes() {
		if sc.isPurged(id) {
			purged = append(purged, id)
		} else {
			other = append(other, id)
		}
	}
	return purged, other
}

func declaredIncrementalBatchShape() []batchSpec {
	return []batchSpec{
		{
			name: "1-parse-write (nodes/edges of the reprocessed files)",
			check: func(b *batchRecord, sc shapeContext) error {
				if b.count("put-node") == 0 {
					return fmt.Errorf("expected the parse-write batch to put nodes, got %d put-node ops", b.count("put-node"))
				}
				// Deletes are allowed here ONLY for nodes a reprocessed file no
				// longer declares. A delete of a PURGED path's node means the
				// deleted-path purge has been folded into batch 1.
				if purged, _ := sc.purgedDeletes(b); len(purged) != 0 {
					return fmt.Errorf("the parse-write batch deleted %d node(s) of a purged path %v: the deleted-path purge has been folded into batch 1", len(purged), purged)
				}
				return nil
			},
		},
		{
			name: "2-purge (deleted paths, deletes ONLY, committed before the linker runs)",
			check: func(b *batchRecord, sc shapeContext) error {
				for _, op := range b.Ops {
					if op.Op != "del-node" {
						return fmt.Errorf("the purge batch issued a %s op: it is no longer a pure purge", op.Op)
					}
				}
				got := b.deletedNodes()
				want := append([]string(nil), sc.purgedNodeIDs...)
				sort.Strings(want)
				if len(want) == 0 {
					return fmt.Errorf("the fixture purged NOTHING — the guard would be vacuous")
				}
				if strings.Join(got, ",") != strings.Join(want, ",") {
					return fmt.Errorf("purge deleted %v, expected exactly the deleted file's nodes %v", got, want)
				}
				if !b.Committed {
					return fmt.Errorf("the purge batch did not commit — linkFiles reads COMMITTED state, so an uncommitted purge is invisible to it")
				}
				return nil
			},
		},
		{
			name: "3-link (cross-file re-link; mints the interned external node)",
			check: func(b *batchRecord, sc shapeContext) error {
				if !b.putsNode(sc.externalName) {
					return fmt.Errorf("the link batch did not mint the external node %q — either the phases moved or the fixture stopped exercising the defect", sc.externalName)
				}
				if n := b.count("del-node"); n != 0 {
					return fmt.Errorf("the link batch deleted %d node(s): the purge has been folded back into the link batch — this IS the PARITY-001 ordering", n)
				}
				return nil
			},
		},
		{
			name:     "4-orphan-sweep (OPTIONAL: only when this pass orphaned an interned external)",
			optional: true,
			check: func(b *batchRecord, sc shapeContext) error {
				if len(b.Ops) == 0 {
					return fmt.Errorf("an empty batch is not an orphan sweep")
				}
				for _, op := range b.Ops {
					if op.Op != "del-node" {
						return fmt.Errorf("issued a %s op, expected node deletes only", op.Op)
					}
				}
				if purged, _ := sc.purgedDeletes(b); len(purged) != 0 {
					return fmt.Errorf("deleted %d purged-path node(s) %v — that is the deleted-path purge, running in the wrong place", len(purged), purged)
				}
				return nil
			},
		},
		{
			name: "5-typeresolve (whole-repo confirmed-tier upserts)",
			check: func(b *batchRecord, _ shapeContext) error {
				for _, op := range b.Ops {
					if op.Op != "put-edge" {
						return fmt.Errorf("the typeresolve batch issued a %s op, expected edge upserts only", op.Op)
					}
				}
				return nil
			},
		},
	}
}

// checkBatchShape is the matcher. It is a free function taking a recorded log
// so TestIncrementalBatchShapeGuard_IsNonVacuous can feed it SYNTHETIC logs and
// prove it rejects them.
//
// Every observed batch must be consumed by a spec, and the required specs must
// all match, in order — so an extra batch, a missing phase, a merged pair or a
// swapped pair is still a failure. Optional specs may be skipped, and skipping
// one can never swallow a misplaced purge: both optional and non-purge specs
// reject a batch that deletes a purged path's node.
func checkBatchShape(batches []*batchRecord, direct []graphOp, sc shapeContext) error {
	if len(direct) != 0 {
		return fmt.Errorf("incremental pass issued %d graph write(s) OUTSIDE any batch", len(direct))
	}
	specs := declaredIncrementalBatchShape()
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.optional {
			names = append(names, "["+s.name+"]")
			continue
		}
		names = append(names, s.name)
	}
	shape := strings.Join(names, " | ")

	// Commit + non-overlap hold for every observed batch, independent of which
	// spec matches it: a batch that opens before its predecessor commits lets a
	// later phase read state an earlier one has not made durable.
	for i, b := range batches {
		if !b.Committed {
			return fmt.Errorf("batch %d never committed; declared shape: %s", i, shape)
		}
		if i > 0 && b.BeginSeq < batches[i-1].EndSeq {
			return fmt.Errorf("batch %d began at seq %d, before batch %d ended at seq %d: the batches overlap, so a later phase can observe an uncommitted earlier one",
				i, b.BeginSeq, i-1, batches[i-1].EndSeq)
		}
	}

	si, bi := 0, 0
	var lastErr error
	for si < len(specs) && bi < len(batches) {
		spec := specs[si]
		err := spec.check(batches[bi], sc)
		if err == nil {
			si++
			bi++
			continue
		}
		if spec.optional {
			si++ // this conditional phase simply did not run
			continue
		}
		lastErr = fmt.Errorf("batch %d does not match required phase %q: %w", bi, spec.name, err)
		break
	}
	if lastErr != nil {
		return lastErr
	}
	if bi != len(batches) {
		return fmt.Errorf("incremental pass opened %d write batches; %d of them matched the declared shape, and batch %d matches no phase. Declared shape: %s",
			len(batches), bi, bi, shape)
	}
	for ; si < len(specs); si++ {
		if !specs[si].optional {
			return fmt.Errorf("incremental pass opened %d write batches, but required phase %q never ran. Declared shape: %s",
				len(batches), specs[si].name, shape)
		}
	}
	return nil
}

// nodeIDsOfFile reads the ids of every node the given source file owns.
func nodeIDsOfFile(t *testing.T, s graphstore.Graphstore, rel string) []string {
	t.Helper()
	nodes, err := s.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	var out []string
	for _, n := range nodes {
		if n.SourcePath() == rel {
			out = append(out, string(n.ID()))
		}
	}
	sort.Strings(out)
	return out
}

// TestIncrementalBatchShape_DeleteShapedOrderingGuard is the AC-2 guard: it
// observes the REAL write batches of an incremental delete-shaped pass and
// fails if their number, content or order departs from the declared shape.
//
// It is a runtime observation, not a source-shape assertion, so it also catches
// a reordering achieved without touching ingestChanged's text.
//
// COVERAGE, stated because review round 1 found it silently narrower than the
// kill test's and unexplained. The guard runs on SIX of the kill test's SEVEN
// shapes. The one omission is shapeMixedMove, and the reason is a HARNESS
// limitation, not a product one: shapeContext.purgedNodeIDs is built from a
// single sh.deletedFile (see the seeding below), but shapeMixedMove vanishes TWO
// paths (tax/tax.go is deleted and shop/price.go is moved to shop/moved.go). The
// real purge correctly deletes the nodes of both, so spec 2's exact-equality
// check under-declares the expected set and the guard rejects CORRECT product
// code. Measured: declaring every vanished path instead makes it pass. Deriving
// purgedNodeIDs from "every path in initial absent from after" is the proper
// fix; it is deliberately NOT taken here because this story is pinned
// test-and-docs-only under AC-7 and the change would widen the diff on the one
// file the reviewer asked to ship as-is. Filed as a follow-up in the story.
//
// shapeMixedRename was ALSO excluded in the first version, and that exclusion
// was simply unnecessary — it passes as shipped (verified), so it is now in the
// list. The exclusion set is therefore exactly one shape, for exactly one
// measured reason.
func TestIncrementalBatchShape_DeleteShapedOrderingGuard(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{shapeSoleFile, shapePkgSurvives, shapeImporterNamed, shapeMixedRename, shapeShrinkingSurvivor, shapeOrphansExternal} {
		for _, backend := range []string{"mem", "sqlite"} {
			t.Run(name+"/"+backend, func(t *testing.T) {
				sh := deleteShapedFixture(name)
				inner := newBackendStore(t, backend)
				rs := &recordingStore{Graphstore: inner}
				ing := newIngester(t, rs, goRegistry())
				repo := writeRepo(t, sh.initial)
				if err := ing.IngestAll(ctx, repo); err != nil {
					t.Fatalf("seed IngestAll: %v", err)
				}
				sc := shapeContext{
					purgedNodeIDs: nodeIDsOfFile(t, inner, sh.deletedFile),
					externalName:  sh.wantEdge[1],
				}
				if len(sc.purgedNodeIDs) == 0 {
					t.Fatalf("fixture defect: %s owns no nodes, so the purge would delete nothing and the guard would be vacuous", sh.deletedFile)
				}

				rs.start()
				sh.mutate(t, repo)
				if err := ing.IngestChanged(ctx, repo, sh.changed); err != nil {
					t.Fatalf("IngestChanged: %v", err)
				}
				if err := checkBatchShape(rs.batches, rs.direct, sc); err != nil {
					t.Fatalf("incremental write-batch shape changed: %v\n\nobserved log:\n%s\n"+
						"If this change is intended, update declaredIncrementalBatchShape AND the ordering ruling in docs/adr/0004-ingest-recovery-disposition.md — the shape is a documented invariant, not an accident.", err, rs.dump())
				}
			})
		}
	}
}

// TestIncrementalBatchShapeGuard_IsNonVacuous answers the review's own
// question — does the guard OBSERVE a difference where one exists? — without
// relying on a source revert. It records one real pass, proves the matcher
// ACCEPTS it, then mutates the log five ways and requires a rejection each
// time. Mutation 1 is the PARITY-001 pre-fix shape itself.
func TestIncrementalBatchShapeGuard_IsNonVacuous(t *testing.T) {
	ctx := context.Background()
	sh := deleteShapedFixture(shapeSoleFile)
	inner := newBackendStore(t, "mem")
	rs := &recordingStore{Graphstore: inner}
	ing := newIngester(t, rs, goRegistry())
	repo := writeRepo(t, sh.initial)
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("seed IngestAll: %v", err)
	}
	sc := shapeContext{
		purgedNodeIDs: nodeIDsOfFile(t, inner, sh.deletedFile),
		externalName:  sh.wantEdge[1],
	}
	rs.start()
	sh.mutate(t, repo)
	if err := ing.IngestChanged(ctx, repo, sh.changed); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	real := rs.batches

	// Control: the matcher must accept the real log. Without this the four
	// rejections below would be satisfied by a matcher that rejects everything.
	if err := checkBatchShape(real, rs.direct, sc); err != nil {
		t.Fatalf("matcher rejected the REAL log, so the rejections below prove nothing: %v\n%s", err, rs.dump())
	}

	clone := func() []*batchRecord {
		out := make([]*batchRecord, 0, len(real))
		for _, b := range real {
			c := *b
			c.Ops = append([]graphOp(nil), b.Ops...)
			out = append(out, &c)
		}
		return out
	}

	mutations := []struct {
		name    string
		mutate  func([]*batchRecord) []*batchRecord
		wantSub string
	}{
		{
			// The PARITY-001 pre-fix shape: no separate purge batch; the deletes
			// ride along in the link batch, AFTER the linker has already read the
			// committed store. Three batches instead of four.
			name: "pre-fix ordering: purge folded into the link batch",
			mutate: func(bs []*batchRecord) []*batchRecord {
				purge, link := bs[1], bs[2]
				link.Ops = append(append([]graphOp(nil), link.Ops...), purge.Ops...)
				out := []*batchRecord{bs[0], link, bs[3]}
				for i, b := range out {
					b.Index = i
				}
				return out
			},
			wantSub: "the purge batch issued a del-edge op",
		},
		{
			name: "an extra write batch is added",
			mutate: func(bs []*batchRecord) []*batchRecord {
				extra := &batchRecord{Index: 4, BeginSeq: 100, EndSeq: 101, Committed: true}
				return append(bs, extra)
			},
			wantSub: "opened 5 write batches",
		},
		{
			// The OPS are swapped, not the records, so the sequence numbers stay
			// monotone: this proves the CONTENT check catches the reordering
			// rather than the cheaper overlap check masking it.
			name: "purge and link phases are swapped",
			mutate: func(bs []*batchRecord) []*batchRecord {
				bs[1].Ops, bs[2].Ops = bs[2].Ops, bs[1].Ops
				return bs
			},
			wantSub: "is no longer a pure purge",
		},
		{
			// The orphan-sweep phase is optional; a batch that deletes a PURGED
			// path's nodes must not be able to hide behind it.
			name: "a second purge hides in the optional orphan-sweep slot",
			mutate: func(bs []*batchRecord) []*batchRecord {
				extra := &batchRecord{Index: 4, BeginSeq: 100, EndSeq: 101, Committed: true}
				for _, id := range real[1].deletedNodes() {
					extra.Ops = append(extra.Ops, graphOp{Op: "del-node", ID: id})
				}
				return append(bs, extra)
			},
			wantSub: "matches no phase",
		},
		{
			name: "the purge batch deletes nothing (vacuous purge)",
			mutate: func(bs []*batchRecord) []*batchRecord {
				bs[1].Ops = nil
				return bs
			},
			wantSub: "purge deleted []",
		},
		{
			name: "the purge batch does not commit before the linker begins",
			mutate: func(bs []*batchRecord) []*batchRecord {
				bs[1].Committed = false
				return bs
			},
			wantSub: "never committed",
		},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			err := checkBatchShape(m.mutate(clone()), nil, sc)
			if err == nil {
				t.Fatalf("the guard ACCEPTED %q — it is vacuous for this mutation", m.name)
			}
			if !strings.Contains(err.Error(), m.wantSub) {
				t.Fatalf("guard rejected %q, but not for the expected reason:\n  got: %v\n want it to mention: %s", m.name, err, m.wantSub)
			}
			t.Logf("rejected as required: %v", err)
		})
	}
}

// ---------------------------------------------------------------------------
// AC-5 — the reverse-dependency translation's ordering.
// ---------------------------------------------------------------------------

// TestReverseDepTranslation_WritesNoGraphBytes pins the ONE invariant that
// actually holds about the incremental reverse-dependency translation running
// BEFORE the purge while the full pass runs it AFTER: the translation itself
// issues no graph write at all. It reads the committed node set and writes only
// meta rows, so its position relative to the purge cannot move a graph byte
// WITHIN the pass that runs it.
//
// Read this together with TestParity004_... below, which measures the part the
// invariant does NOT cover: the metadata it writes is not order-invariant, and
// that metadata steers a LATER pass's cascade.
func TestReverseDepTranslation_WritesNoGraphBytes(t *testing.T) {
	ctx := context.Background()
	sh := deleteShapedFixture(shapeSoleFile)
	inner := newBackendStore(t, "mem")
	rs := &recordingStore{Graphstore: inner}
	ing := newIngester(t, rs, goRegistry())
	repo := writeRepo(t, sh.initial)
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("seed IngestAll: %v", err)
	}
	rs.start()
	sh.mutate(t, repo)
	if err := ing.IngestChanged(ctx, repo, sh.changed); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	// The translation runs in the window between the parse-write batch's commit
	// and the purge batch's begin. Nothing may be written to the graph there:
	// no unbatched write at all, and no batch open across the window.
	if len(rs.direct) != 0 {
		t.Fatalf("the incremental pass issued %d unbatched graph write(s); the reverse-dep window must write no graph bytes:\n%s", len(rs.direct), rs.dump())
	}
	if len(rs.batches) < 2 {
		t.Fatalf("expected at least a write batch and a purge batch, got %d:\n%s", len(rs.batches), rs.dump())
	}
	window := [2]int{rs.batches[0].EndSeq, rs.batches[1].BeginSeq}
	if window[0] >= window[1] {
		t.Fatalf("no reverse-dep window between batch 0 (ends @%d) and batch 1 (begins @%d):\n%s", window[0], window[1], rs.dump())
	}
	for _, b := range rs.batches {
		for _, op := range b.Ops {
			if op.Seq > window[0] && op.Seq < window[1] {
				t.Errorf("graph write %s inside the reverse-dep translation window (@%d, between @%d and @%d)", op.Op, op.Seq, window[0], window[1])
			}
		}
	}
}

// readReverseDeps reads the meta sidecar's reverse-dependency index.
func readReverseDeps(t *testing.T, metaDir string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(metaDir, "ingest-meta.db"))
	if err != nil {
		t.Fatalf("open meta db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT path, dependents FROM reverse_deps ORDER BY path")
	if err != nil {
		t.Fatalf("query reverse_deps: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestParity004_DanglingIntraModuleImportBreaksTheCascade is a KNOWN-DEFECT PIN.
// It asserts the CURRENT, WRONG behaviour, so the defect is executable data that
// runs on every PR — and it fails, with instructions, the moment the defect is
// fixed. It does not assert parity, because parity does not hold.
//
// PARITY-004, found while ruling on AC-5. reverseDepKeys translates a forward
// import ref into the DIRECTORY key space through idx.DirsForImport; when the
// imported package has no directory in the graph — a DANGLING intra-module
// import — the raw import path is kept verbatim ("example.com/m/tax"). But
// dependentsOf only ever looks a key up by the changed file's DIRECTORY
// ("tax") or its raw path ("tax/tax.go"), so that row is unreachable forever.
//
// Consequence, reproduced end to end through the built CLI on SQLite: index a
// tree whose import is dangling, then restore the missing package and sync —
// the importer is never cascaded, the stale interned external node and its
// heuristic `calls` edge survive beside the now-correct confirmed edge, and the
// `imports` edge a full pass emits is missing. `graphi sync` = 7 nodes,
// `graphi rebuild` = 6, and three further syncs do not repair it.
//
// The incremental path does NOT have this defect, because its reverse-dep
// translation runs BEFORE the purge and therefore still sees the doomed
// directory, keying the row "tax". That is the asymmetry AC-5 asks about, and
// it is why the asymmetry was left in place rather than "aligned": aligning the
// incremental path with the full one would spread this defect, not fix it.
func TestParity004_DanglingIntraModuleImportBreaksTheCascade(t *testing.T) {
	ctx := context.Background()
	sh := deleteShapedFixture(shapeSoleFile)

	// Route A — the incremental delete. Keys the row by directory.
	incMeta := t.TempDir()
	incStore := newBackendStore(t, "mem")
	incIng, err := ingest.New(incStore, goRegistry(), incMeta)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = incIng.Close() })
	repo := writeRepo(t, sh.initial)
	if err := incIng.IngestAll(ctx, repo); err != nil {
		t.Fatalf("seed IngestAll: %v", err)
	}
	sh.mutate(t, repo)
	if err := incIng.IngestChanged(ctx, repo, sh.changed); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}

	// Route B — a full pass over the SAME resulting tree. Keys the row by the
	// unresolvable import path.
	fullMeta := t.TempDir()
	fullStore := newBackendStore(t, "mem")
	fullIng, err := ingest.New(fullStore, goRegistry(), fullMeta)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = fullIng.Close() })
	fullRepo := writeRepo(t, sh.after)
	if err := fullIng.IngestAll(ctx, fullRepo); err != nil {
		t.Fatalf("reference IngestAll: %v", err)
	}

	incKeys, fullKeys := readReverseDeps(t, incMeta), readReverseDeps(t, fullMeta)
	// The VALUE is asserted, not merely the key: folding the purge into batch 1
	// leaves a row `tax => []` behind beside the new, unreachable
	// `example.com/m/tax => ["shop/cart.go"]`, and a key-only check would pass
	// on it. Measured, on the folded prototype, before this assertion was
	// written this way.
	if !strings.Contains(incKeys["tax"], "shop/cart.go") {
		t.Errorf("PARITY-004 pin: expected the INCREMENTAL reverse-dep row keyed by directory \"tax\" to list the importer, got %v.\n"+
			"If the incremental translation was moved AFTER the purge, this is the alignment ADR 0004 (2026-08-19) rules against — read that section before changing it.", incKeys)
	}
	if _, ok := fullKeys["example.com/m/tax"]; !ok {
		t.Errorf("PARITY-004 pin: expected the FULL pass's reverse-dep row to be keyed by the unresolvable import path \"example.com/m/tax\", got %v.\n"+
			"If this key is now a directory, PARITY-004 may be FIXED: re-measure the CLI reproduction in the record, then delete this pin and this defect entry.", fullKeys)
	}

	// The consequence, driven through the library: re-add the package on the
	// full-pass store and sync. The importer is not cascaded, so the graph
	// diverges from a rebuild of the same tree — permanently.
	mustWrite(t, fullRepo, "tax/tax.go", fixtureTax)
	if err := fullIng.IngestChanged(ctx, fullRepo, []string{"tax/tax.go"}); err != nil {
		t.Fatalf("re-add IngestChanged: %v", err)
	}
	got := storeSnapshotBytes(t, fullStore)

	refStore := newBackendStore(t, "mem")
	refIng := newIngester(t, refStore, goRegistry())
	if err := refIng.IngestAll(ctx, writeRepo(t, mergeFiles(sh.after, map[string]string{"tax/tax.go": fixtureTax}))); err != nil {
		t.Fatalf("rebuild reference: %v", err)
	}
	want := storeSnapshotBytes(t, refStore)
	if bytes.Equal(got, want) {
		t.Fatalf("PARITY-004 appears FIXED: re-adding a package after a full pass over a dangling intra-module import now converges with a rebuild.\n" +
			"That is good news, not a test failure to silence. Do this: re-run the CLI reproduction in projects/graphi backlog entry PARITY-004, " +
			"RETRACT the PARITY-004 disclosure on BOTH D8 surfaces in the SAME change — the canonical defect page docs/known-defects.md " +
			"(its bullet AND its node and arrows in the mermaid diagram) AND the doctor known-defects check (internal/doctor/checks.go, " +
			"plus its assertions and its id-set pin in checks_test.go) — and delete this pin. D8 permits retraction ONLY in the change " +
			"that closes the defect, so leaving either surface up is now the violation.")
	}
	// Pin the exact shape of the divergence, so a DIFFERENT divergence also
	// fails here rather than passing as "still broken".
	if !strings.Contains(string(got), `"qualified_name":"example.com/m/tax.Rate"`) {
		t.Errorf("PARITY-004 pin: expected the stale interned external node to survive the re-add; it did not. The defect changed shape — re-diagnose before editing this test.")
	}
	if strings.Contains(string(got), `"kind":"imports"`) {
		t.Errorf("PARITY-004 pin: expected the imports edge to be MISSING on the synced side; it is present. The defect changed shape — re-diagnose before editing this test.")
	}
	if !strings.Contains(string(want), `"kind":"imports"`) {
		t.Errorf("PARITY-004 pin: the rebuild reference has no imports edge, so the comparison above proves nothing. Re-check the fixture.")
	}
}
