package conformance_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/brief"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/explain"
	"github.com/samibel/graphi/engine/agenttools/related"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/watch"
)

// ep017Ops are the four EP-017 canonical operations surfaced by SW-104. Their
// envelopes against the full-parse graph and the incremental-parallel-parse graph
// must be byte-for-byte identical.
//
// ALL FOUR ARE LABS. That is why the 12 Stable operations are asserted
// SEPARATELY, in their own subtest, below (spec decision 3): a Labs envelope
// drifting must not be able to fail the Stable gate, and a Stable envelope
// drifting must not be excused by a Labs failure that fired first.
var ep017Ops = []string{"notebook-ingest", "watcher-status", "taint-query", "communities"}

// sampleNotebook is a minimal valid nbformat >=4.5 notebook with one python code
// cell, so the SW-100 NotebookParser commits notebook_cell provenance and the
// notebook-ingest envelope is non-trivial.
const sampleNotebook = `{
  "cells": [
    {"cell_type": "markdown", "source": ["# title\n"]},
    {"cell_type": "code", "id": "c1", "source": ["def nb_func():\n", "    return 1\n"]}
  ],
  "metadata": {"kernelspec": {"language": "python"}},
  "nbformat": 4,
  "nbformat_minor": 5
}
`

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func removeFile(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}
}

func newIngester(t *testing.T, store graphstore.Graphstore) *ingest.Ingester {
	t.Helper()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	return ing
}

// snapshot serializes the whole graph to canonical bytes (the engine's own
// byte-stable store snapshot), the unit the full-vs-incremental graph-parity
// assertion compares. It is PORTABLE and store-independent, which is what lets
// the change-class table run unchanged on MemStore and SQLite.
func snapshot(t *testing.T, store graphstore.Graphstore) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap")
	if err := store.Snapshot(context.Background(), p); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// envelope dispatches one EP-017 operation through the SAME single dispatch path
// + shared encoder every surface uses (analysis.Service.Dispatch ->
// analysis.Marshal) and returns the canonical serialized envelope bytes.
func envelope(t *testing.T, store graphstore.Graphstore, op string) []byte {
	t.Helper()
	svc := analysis.NewDefaultService(store)
	res, err := svc.Dispatch(context.Background(), op, analysis.Params{})
	if err != nil {
		t.Fatalf("dispatch %s: %v", op, err)
	}
	b, err := analysis.Marshal(res)
	if err != nil {
		t.Fatalf("marshal %s: %v", op, err)
	}
	return b
}

// incBuild is the result of an incremental build: the store it filled, plus the
// ingester and watcher service that filled it. The latter two are exposed so the
// idempotency assertion can RE-APPLY a change set through the very same parallel
// path, rather than approximating it with a second serial pass.
type incBuild struct {
	store graphstore.Graphstore
	ing   *ingest.Ingester
	svc   *watch.Service
}

// buildIncrementalParallel builds a graph by applying the mutation steps
// incrementally through the watcher-driven, bounded-worker-pool parallel parse
// path (watch.Service.Reconcile -> Pool.ParseBatch -> ApplyChangedParsed). A
// schedule hook perturbs worker COMPLETION order so the test exercises arrival
// nondeterminism the canonical-ordered apply must defeat (AC-4 permuted arrival).
//
// The hook and the watch.Config below are the most valuable thing in this file
// and are deliberately unchanged: the arrival nondeterminism they inject is what
// the canonical-ordered apply has to defeat, so removing either would leave a
// harness that compares two orderings that never differed. Only the store became
// a parameter, so the change-class table can run the identical build against both
// backends.
func buildIncrementalParallel(t *testing.T, root string, store graphstore.Graphstore, steps []func()) *incBuild {
	t.Helper()
	ctx := context.Background()
	ing := newIngester(t, store)
	cfg := watch.Config{DebounceMs: 20, PoolSize: 4, PoolHardCap: 8, ReconcileInterval: time.Hour}
	svc, err := watch.NewService(root, ing, cfg)
	if err != nil {
		t.Fatalf("watch.NewService: %v", err)
	}
	var n int64
	svc.Pool().SetScheduleHook(func(string) {
		// Perturb completion order without changing the input set.
		if atomic.AddInt64(&n, 1)%2 == 0 {
			time.Sleep(time.Millisecond)
		}
	})
	for i, step := range steps {
		step()
		if err := svc.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile step %d: %v", i, err)
		}
	}
	return &incBuild{store: store, ing: ing, svc: svc}
}

// TestFullVsIncremental_ByteParity is the FR-7 full-vs-incremental parity gate,
// driven by the DECLARATIVE change-class table in changeclass_test.go rather than
// by the hardcoded four-step `[]func()` literal it replaced. One subtest per
// (backend, change class): a graph built by a single full parse and a graph built
// by an incremental watcher-driven parallel parse over the SAME change serialize
// identically, the class's non-vacuity witness holds, and re-applying the change
// set leaves the graph unmoved.
//
// The table is bound to docs/rc/parity-classes.yaml, so a class declared there
// with no row here fails TestParityMatrix_DriftGuard rather than silently going
// unproven. This test is what docs/rc/parity-classes.yaml cites; keep its name
// and this file in step with harnessTestFile / harnessTestName if it ever moves.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: this is a hermetic proof over t.TempDir()
// fixtures. It is NOT the PRD §12.3 gate and must never be reported as one —
// that needs the real-repository matrix (SW-144) and the lifecycle rows (SW-158).
func TestFullVsIncremental_ByteParity(t *testing.T) {
	table := changeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, row := range table {
				row := row
				t.Run(row.id, func(t *testing.T) {
					t.Parallel()
					runChangeClassRow(t, b, row)
				})
			}
		})
	}
}

// TestFullVsIncremental_EnvelopeParity asserts that the operation ENVELOPES —
// not just the serialized graph — are byte-identical between the full-parse graph
// and the incremental-parallel-parse graph, over a composite change sequence that
// exercises add, modify, notebook-add and delete in one tree.
//
// It is one composite fixture rather than one per change class on purpose: the
// envelope layer is downstream of the graph, the graph is already proven
// class-by-class byte-identical by TestFullVsIncremental_ByteParity, and
// re-dispatching sixteen operations per class per backend would buy nothing but
// wall clock.
//
// The two tiers are asserted in SEPARATE subtests (spec decision 3). The four
// EP-017 operations are all Labs; the twelve frozen operations are the product
// promise. Neither tier's drift may fail the other's gate, and a t.Run boundary
// is what makes that structural instead of a comment.
//
// BACKEND SCOPE, STATED RATHER THAN LEFT TO BE DISCOVERED: this test runs on
// MemStore ONLY. AC-7's both-backends requirement binds "the full table", and
// this is not a table row — it is a downstream check on the envelope layer. The
// reasoning for the restriction, so it can be challenged rather than inherited:
// an envelope is a pure function of the graph plus the operation, the graph is
// already proven byte-identical class-by-class on BOTH backends by
// TestFullVsIncremental_ByteParity, and the snapshot envelope is store-
// independent (engine/ingest/faultmatrix_test.go:231 relies on that too). A
// backend can therefore only reach an envelope THROUGH the graph, which is
// already covered. If that argument is ever wrong, the fix is to parameterize
// this test over parityBackends() exactly as the table is.
func TestFullVsIncremental_EnvelopeParity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// The composite fixture carries every shape the twelve Stable operations need
	// to answer non-trivially: an intra-file call (Source -> helper) for
	// callees/callers, a cross-file cross-package call (Use -> b.Sink) so
	// related_files has a file to relate to, and a type mentioned in a parameter
	// list (Consume(t T)) so references is non-empty. Without them four of the
	// twelve compare two identical EMPTY envelopes, which is parity over nothing —
	// the assertion below rejects exactly that.
	//
	// The delete step removes c/c.go, which nothing references. That is deliberate:
	// deleting the SOLE file of a package an importer still names is the shape that
	// trips PARITY-001 (see the delete_file row in changeclass_test.go), and letting
	// this test fail on a known graph defect would hide whatever the envelope layer
	// is doing. The delete class is proven by its own row; this test's subject is
	// envelopes.
	aInitial := `package a

import "example.com/m/b"

type T struct{ n int }

func Source() string { return helper() }

func helper() string { return "x" }

func Use() { b.Sink(Source()) }

func Consume(t T) {}
`
	aModified := `package a

import "example.com/m/b"

type T struct{ n int }

func Source() string { return helper() }

func helper() string { return "y" }

func Use() { b.Sink(Source()) }

func Consume(t T) {}

func Extra() {}
`
	steps := []func(){
		func() {
			writeFile(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
			writeFile(t, root, "a/a.go", aInitial)
			writeFile(t, root, "b/b.go", "package b\n\nfunc Sink(s string) {}\n")
		},
		func() {
			writeFile(t, root, "a/a.go", aModified)
			writeFile(t, root, "c/c.go", "package c\n\nfunc C() {}\n")
		},
		func() {
			writeFile(t, root, "nb.ipynb", sampleNotebook)
		},
		func() {
			removeFile(t, root, "c/c.go")
		},
	}

	incStore := graphstore.NewMemStore()
	defer incStore.Close()
	buildIncrementalParallel(t, root, incStore, steps)

	fullStore := graphstore.NewMemStore()
	defer fullStore.Close()
	fullIng := newIngester(t, fullStore)
	if err := fullIng.IngestAll(ctx, root); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}

	// The graph itself, so an envelope mismatch cannot be blamed on the graph.
	if !bytes.Equal(snapshot(t, incStore), snapshot(t, fullStore)) {
		t.Fatal("composite sequence: serialized graphs are NOT byte-identical; the envelope " +
			"subtests below would be reporting a graph defect, not an envelope defect")
	}

	t.Run("labs_ep017", func(t *testing.T) {
		for _, op := range ep017Ops {
			inc := envelope(t, incStore, op)
			full := envelope(t, fullStore, op)
			if !bytes.Equal(inc, full) {
				t.Errorf("LABS envelope %q differs:\n incremental: %s\n full:        %s", op, inc, full)
			}
		}
		// Sanity: the notebook-ingest envelope actually surfaced cells (the harness
		// would otherwise prove parity over an empty payload).
		if !bytes.Contains(envelope(t, fullStore, "notebook-ingest"), []byte(`"cells":[{`)) {
			t.Fatalf("notebook-ingest surfaced no cells; envelope=%s", envelope(t, fullStore, "notebook-ingest"))
		}
	})

	t.Run("stable_twelve", func(t *testing.T) {
		assertStableEnvelopeParity(t, incStore, fullStore)
	})
}

// stableOpsUndispatchable records, by name, every one of the 12 frozen Stable
// operations this harness cannot dispatch, WITH the reason. AC-9 forbids silently
// dropping one, so the set is declared rather than implied by omission and the
// count is asserted below.
var stableOpsUndispatchable = map[string]string{
	"index": "index is the ingest LIFECYCLE operation, not a read operation, and is the only " +
		"member of the frozen 12 with no MCP tool (surfaces/mcp/tools.go StableMCPToolNames " +
		"excludes it). It produces no envelope to compare — it produces the graph. Both graphs " +
		"under comparison here were built by it, so this harness exercises `index` as its " +
		"SUBJECT rather than asserting an envelope over it, and there is nothing to drop.",
}

// assertStableEnvelopeParity asserts envelope byte-equality for the 12 frozen
// Stable operations (surfaces/mcp/tools.go StableOperations).
//
// The op list is spelled out here rather than imported from surfaces/mcp because
// this is an engine-layer package and `cmd -> surfaces -> engine -> core` forbids
// the upward import. The list is pinned against engine/scenario.KnownOps()
// instead, which is the engine-layer dispatcher for exactly these operations, so
// a 13th Stable op or a rename still fails a test rather than passing unnoticed.
//
// Each operation is dispatched through its OWN canonical encoder — the single
// place a result becomes bytes for every surface — so what is compared is the
// real wire envelope and not a test-local rendering:
//
//	callers/callees/references/definition/neighborhood -> query.Marshal
//	search                                             -> search.Marshal
//	impact                                             -> analysis.Marshal
//	explain_symbol/related_files/change_risk/agent_brief -> contract.Serialize
//	index                                              -> see stableOpsUndispatchable
func assertStableEnvelopeParity(t *testing.T, incStore, fullStore graphstore.Graphstore) {
	t.Helper()
	ctx := context.Background()

	// The frozen 12, sorted, mirroring surfaces/mcp/tools.go StableOperations.
	stable := []string{
		"agent_brief", "callees", "callers", "change_risk", "definition", "explain_symbol",
		"impact", "index", "neighborhood", "references", "related_files", "search",
	}
	if len(stable) != 12 {
		t.Fatalf("the Stable set must be exactly 12 operations; got %d", len(stable))
	}

	// FOUR targets, not one, and the reason is non-vacuity. The fixture's call
	// direction is a.Source -> a.helper, so a single target leaves every
	// reverse-direction operation (callers, references, impact) comparing two
	// identical EMPTY envelopes — parity over nothing. a.helper supplies the
	// reverse direction, a.T supplies a type mention for references, and a.Use
	// supplies the cross-file edge related_files needs. The loop below REQUIRES
	// every dispatched operation to produce at least one non-empty outcome across
	// the four, so this list cannot silently rot back into vacuity.
	//
	// The graphs are already byte-identical at this point, so a qualified name
	// present in one is present in the other with the same NodeId.
	incView, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("read incremental graph: %v", err)
	}
	targets := []string{"a.Source", "a.helper", "a.T", "a.Use"}
	ids := map[string]model.NodeId{}
	for _, qn := range targets {
		n, ok := incView.node(qn)
		if !ok {
			t.Fatalf("fixture symbol %q absent; graph has %s", qn, incView.qnList())
		}
		ids[qn] = n.ID()
	}

	envelopeFor := func(st graphstore.Graphstore, op, target string) ([]byte, error) {
		deps := resolve.Deps{Query: query.New(st), Search: search.New(st)}
		id := ids[target]
		switch op {
		case "callers":
			r, err := deps.Query.Callers(ctx, id)
			if err != nil {
				return nil, err
			}
			return query.Marshal(r)
		case "callees":
			r, err := deps.Query.Callees(ctx, id)
			if err != nil {
				return nil, err
			}
			return query.Marshal(r)
		case "references":
			r, err := deps.Query.References(ctx, id)
			if err != nil {
				return nil, err
			}
			return query.Marshal(r)
		case "definition":
			r, err := deps.Query.Definition(ctx, id)
			if err != nil {
				return nil, err
			}
			return query.Marshal(r)
		case "neighborhood":
			r, err := deps.Query.Neighborhood(ctx, id, 2)
			if err != nil {
				return nil, err
			}
			return query.Marshal(r)
		case "search":
			// Search is name-input, not symbol-input; the target's local name is
			// the query so the two targets still exercise two different inputs.
			r, err := deps.Search.Search(ctx, strings.TrimPrefix(target, "a."), 25)
			if err != nil {
				return nil, err
			}
			return search.Marshal(r)
		case "impact":
			a, err := analysis.NewDefaultService(st).Dispatch(ctx, "impact", analysis.Params{Symbol: id})
			if err != nil {
				return nil, err
			}
			return analysis.Marshal(a)
		case "explain_symbol":
			r, err := explain.Explain(ctx, deps, target, 10)
			if err != nil {
				return nil, err
			}
			return contract.Serialize(r)
		case "related_files":
			r, err := related.Files(ctx, deps, target, "", 10)
			if err != nil {
				return nil, err
			}
			return contract.Serialize(r)
		case "change_risk":
			r, err := risk.Assess(ctx, deps, target, "", 10)
			if err != nil {
				return nil, err
			}
			return contract.Serialize(r)
		case "agent_brief":
			r, err := brief.Assemble(ctx, brief.Params{Topic: target, ProjectName: "fixture", Deps: deps})
			if err != nil {
				return nil, err
			}
			return contract.Serialize(r)
		default:
			return nil, fmt.Errorf("no dispatcher for stable op %q", op)
		}
	}

	dispatched := 0
	for _, op := range stable {
		if reason, skipped := stableOpsUndispatchable[op]; skipped {
			t.Logf("stable op %q NOT dispatched, recorded reason: %s", op, reason)
			continue
		}
		compared, nonEmpty := 0, 0
		for _, target := range targets {
			incBytes, err := envelopeFor(incStore, op, target)
			if err != nil {
				t.Errorf("STABLE op %q [%s]: incremental dispatch failed: %v", op, target, err)
				continue
			}
			fullBytes, err := envelopeFor(fullStore, op, target)
			if err != nil {
				t.Errorf("STABLE op %q [%s]: full dispatch failed: %v", op, target, err)
				continue
			}
			if !bytes.Equal(incBytes, fullBytes) {
				t.Errorf("STABLE envelope %q [%s] differs:\n incremental: %s\n full:        %s",
					op, target, incBytes, fullBytes)
				continue
			}
			compared++
			// An envelope whose outcome is empty/not_found carries no graph
			// content, so parity over it proves nothing about the graph. At least
			// one of the two targets must produce real content.
			if len(incBytes) > 0 &&
				!bytes.Contains(incBytes, []byte(`"outcome":"empty"`)) &&
				!bytes.Contains(incBytes, []byte(`"outcome":"not_found"`)) {
				nonEmpty++
			}
		}
		if compared != len(targets) {
			continue // already reported above
		}
		if nonEmpty == 0 {
			t.Errorf("STABLE envelope %q compared equal over every target but was empty/not_found "+
				"for all of them; parity over an empty payload proves nothing about the graph", op)
			continue
		}
		dispatched++
	}
	if want := len(stable) - len(stableOpsUndispatchable); dispatched != want {
		t.Errorf("dispatched %d Stable envelopes, want %d (12 frozen ops minus %d recorded as "+
			"undispatchable); an operation was dropped without a recorded reason",
			dispatched, want, len(stableOpsUndispatchable))
	}
}

// TestStableOpSet_MatchesEngineDispatcher pins the Stable list this package
// spells out against engine/scenario.KnownOps(), the engine-layer dispatcher for
// the same operations. It is the substitute for importing surfaces/mcp, which
// `cmd -> surfaces -> engine -> core` forbids: if a Stable op is renamed or a
// 13th appears, the scenario dispatcher changes too and this fails.
func TestStableOpSet_MatchesEngineDispatcher(t *testing.T) {
	known := map[string]bool{}
	for _, op := range scenario.KnownOps() {
		known[op] = true
	}
	stable := []string{
		"agent_brief", "callees", "callers", "change_risk", "definition", "explain_symbol",
		"impact", "index", "neighborhood", "references", "related_files", "search",
	}
	for _, op := range stable {
		if !known[op] {
			t.Errorf("Stable op %q is not dispatchable by engine/scenario.KnownOps(); either the "+
				"frozen 12 changed or this list is stale", op)
		}
	}
}

// TestRepeatRun_Determinism is the AC-4 gate: repeated dispatch of each EP-017
// operation against the same graph (built via the parallel parse path) yields
// byte-identical envelopes run-to-run — no map-iteration / goroutine-order /
// wall-clock dependence.
//
// THIS IS NOT THE FR-7 IDEMPOTENCY PROOF. It repeats DISPATCH against one
// already-built graph and never re-enters the ingest path, so it cannot observe a
// double-apply that duplicates an edge or resurrects a purged node. Repeated
// APPLICATION is asserted by assertIdempotentReapplication (changeclass_test.go),
// once per change class per backend. Do not collapse the two.
func TestRepeatRun_Determinism(t *testing.T) {
	root := t.TempDir()
	steps := []func(){
		func() {
			writeFile(t, root, "a/a.go", "package a\n\nfunc F1() string { return F2() }\nfunc F2() string { return \"x\" }\n")
			writeFile(t, root, "b/b.go", "package b\n\nfunc G() {}\n")
			writeFile(t, root, "nb.ipynb", sampleNotebook)
		},
	}
	store := graphstore.NewMemStore()
	defer store.Close()
	buildIncrementalParallel(t, root, store, steps)

	for _, op := range ep017Ops {
		first := envelope(t, store, op)
		for i := 0; i < 8; i++ {
			again := envelope(t, store, op)
			if !bytes.Equal(first, again) {
				t.Fatalf("op %q not deterministic across runs:\n run0: %s\n run%d: %s", op, first, i+1, again)
			}
		}
	}
}
