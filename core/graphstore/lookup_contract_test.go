package graphstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// This file is the CORE-01 (SW-115) conformance suite for the selective read
// ports (ADR 0003 D1/D2/D4): one suite, parameterized by Factory, runs
// identically against MemStore and SQLiteStore — the same pattern the base
// contract suite uses — plus a direct cross-backend identity check. Any
// semantic divergence between backends is a red G2 gate.

func lookupFactories() map[string]Factory {
	return map[string]Factory{
		"mem":    MemFactory,
		"sqlite": SQLiteFactory,
	}
}

// lookupFixture seeds a deterministic graph exercising every port semantics:
// a hub with fan-in of two kinds, fan-out, a self-loop, an isolated node, and
// two nodes sharing a qualified name in different files (plus two nodes
// sharing a file).
type lookupFixture struct {
	hub, callerA, callerB, refCaller, callee, loop, isolated model.Node
	sameQNOther                                              model.Node

	inCalls  []model.Edge // calls edges INTO hub (callerA, callerB, loop)
	inRefs   []model.Edge // references edges INTO hub (refCaller)
	outCalls []model.Edge // calls edges OUT of hub (callee, loop)
}

func seedLookupFixture(t *testing.T, st Graphstore) lookupFixture {
	t.Helper()
	ctx := context.Background()

	mkNode := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", qn, err)
		}
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", qn, err)
		}
		return n
	}
	mkEdge := func(from, to model.NodeId, kind string) model.Edge {
		e, err := model.NewEdge(from, to, kind, model.TierDerived, 0.9, "reason:"+kind, []string{"ev.go:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
		return e
	}

	f := lookupFixture{}
	f.hub = mkNode("function", "pkg.Hub", "pkg/hub.go", 1)
	f.callerA = mkNode("function", "pkg.CallerA", "pkg/callers.go", 1)
	f.callerB = mkNode("function", "pkg.CallerB", "pkg/callers.go", 10)
	f.refCaller = mkNode("function", "pkg.RefCaller", "pkg/ref.go", 1)
	f.callee = mkNode("function", "pkg.Callee", "pkg/callee.go", 1)
	f.loop = mkNode("function", "pkg.Loop", "pkg/loop.go", 1)
	f.isolated = mkNode("function", "pkg.Isolated", "pkg/isolated.go", 1)
	// Same qualified name as hub, different file → different content-addressed id.
	f.sameQNOther = mkNode("function", "pkg.Hub", "other/hub.go", 1)

	f.inCalls = []model.Edge{
		mkEdge(f.callerA.ID(), f.hub.ID(), "calls"),
		mkEdge(f.callerB.ID(), f.hub.ID(), "calls"),
		mkEdge(f.loop.ID(), f.hub.ID(), "calls"),
	}
	f.inRefs = []model.Edge{
		mkEdge(f.refCaller.ID(), f.hub.ID(), "references"),
	}
	f.outCalls = []model.Edge{
		mkEdge(f.hub.ID(), f.callee.ID(), "calls"),
		mkEdge(f.hub.ID(), f.loop.ID(), "calls"),
	}
	return f
}

func canonEdges(es ...[]model.Edge) []model.Edge {
	var out []model.Edge
	for _, s := range es {
		out = append(out, s...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

func canonEdgesByKindID(es ...[]model.Edge) []model.Edge {
	var out []model.Edge
	for _, edges := range es {
		out = append(out, edges...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind() != out[j].Kind() {
			return out[i].Kind() < out[j].Kind()
		}
		return out[i].ID() < out[j].ID()
	})
	return out
}

func canonNodes(ns ...model.Node) []model.Node {
	out := append([]model.Node(nil), ns...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

func TestLookupContract_IncomingOutgoing(t *testing.T) {
	for name, factory := range lookupFactories() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			lk := st.(GraphLookup)
			f := seedLookupFixture(t, st)

			// All kinds: incoming = calls ∪ references, canonical order, provenance intact.
			got, err := lk.Incoming(ctx, f.hub.ID())
			if err != nil {
				t.Fatalf("Incoming: %v", err)
			}
			if want := canonEdges(f.inCalls, f.inRefs); !reflect.DeepEqual(got, want) {
				t.Fatalf("Incoming(all kinds) mismatch:\n got=%v\nwant=%v", got, want)
			}

			// Single kind filter.
			got, err = lk.Incoming(ctx, f.hub.ID(), "calls")
			if err != nil {
				t.Fatalf("Incoming(calls): %v", err)
			}
			if want := canonEdges(f.inCalls); !reflect.DeepEqual(got, want) {
				t.Fatalf("Incoming(calls) mismatch:\n got=%v\nwant=%v", got, want)
			}

			// Multi-kind filter = union.
			got, err = lk.Incoming(ctx, f.hub.ID(), "calls", "references")
			if err != nil {
				t.Fatalf("Incoming(calls,references): %v", err)
			}
			if want := canonEdges(f.inCalls, f.inRefs); !reflect.DeepEqual(got, want) {
				t.Fatalf("Incoming(calls,references) mismatch:\n got=%v\nwant=%v", got, want)
			}

			// Outgoing mirror.
			got, err = lk.Outgoing(ctx, f.hub.ID(), "calls")
			if err != nil {
				t.Fatalf("Outgoing(calls): %v", err)
			}
			if want := canonEdges(f.outCalls); !reflect.DeepEqual(got, want) {
				t.Fatalf("Outgoing(calls) mismatch:\n got=%v\nwant=%v", got, want)
			}

			// Unknown kind and unknown/isolated node: empty result, not an error.
			for _, probe := range []struct {
				name  string
				id    model.NodeId
				kinds []model.EdgeKind
			}{
				{"unknown kind", f.hub.ID(), []model.EdgeKind{"imports"}},
				{"isolated node", f.isolated.ID(), nil},
				{"unknown node", model.NodeId("nd_nope"), nil},
			} {
				got, err = lk.Incoming(ctx, probe.id, probe.kinds...)
				if err != nil {
					t.Fatalf("Incoming(%s): %v", probe.name, err)
				}
				if len(got) != 0 {
					t.Fatalf("Incoming(%s): want empty, got %d edges", probe.name, len(got))
				}
			}
		})
	}
}

func TestLookupContract_BoundedIncomingOutgoing(t *testing.T) {
	for name, factory := range lookupFactories() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			lookup := st.(BoundedGraphLookup)
			fixture := seedLookupFixture(t, st)
			var cacheRebuilds int64
			if sqlite, ok := st.(*SQLiteStore); ok {
				cacheRebuilds = sqlite.CacheRebuilds()
			}

			allIncoming := canonEdgesByKindID(fixture.inCalls, fixture.inRefs)
			got, truncated, err := lookup.IncomingBounded(ctx, fixture.hub.ID(), 2)
			if err != nil {
				t.Fatalf("IncomingBounded: %v", err)
			}
			if !truncated || !reflect.DeepEqual(got, allIncoming[:2]) {
				t.Fatalf("IncomingBounded(limit=2) = %v, truncated=%v; want first two of %v, true", got, truncated, allIncoming)
			}

			got, truncated, err = lookup.IncomingBounded(ctx, fixture.hub.ID(), len(allIncoming))
			if err != nil {
				t.Fatalf("IncomingBounded(exact): %v", err)
			}
			if truncated || !reflect.DeepEqual(got, allIncoming) {
				t.Fatalf("IncomingBounded(exact) = %v, truncated=%v; want %v, false", got, truncated, allIncoming)
			}

			calls := canonEdges(fixture.inCalls)
			got, truncated, err = lookup.IncomingBounded(ctx, fixture.hub.ID(), 2, "calls", "calls")
			if err != nil {
				t.Fatalf("IncomingBounded(calls): %v", err)
			}
			if !truncated || !reflect.DeepEqual(got, calls[:2]) {
				t.Fatalf("IncomingBounded(calls, duplicate kind) = %v, truncated=%v; want %v, true", got, truncated, calls[:2])
			}

			outgoing := canonEdges(fixture.outCalls)
			got, truncated, err = lookup.OutgoingBounded(ctx, fixture.hub.ID(), 8, "calls")
			if err != nil {
				t.Fatalf("OutgoingBounded: %v", err)
			}
			if truncated || !reflect.DeepEqual(got, outgoing) {
				t.Fatalf("OutgoingBounded = %v, truncated=%v; want %v, false", got, truncated, outgoing)
			}

			got, truncated, err = lookup.IncomingBounded(ctx, model.NodeId("nd_missing"), 1)
			if err != nil || truncated || len(got) != 0 {
				t.Fatalf("IncomingBounded(missing) = %v, truncated=%v, err=%v; want empty/false/nil", got, truncated, err)
			}
			if _, _, err := lookup.IncomingBounded(ctx, fixture.hub.ID(), 0); !errors.Is(err, ErrInvalidLimit) {
				t.Fatalf("IncomingBounded(limit=0) error = %v, want ErrInvalidLimit", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, _, err := lookup.IncomingBounded(canceled, fixture.hub.ID(), 1); !errors.Is(err, context.Canceled) {
				t.Fatalf("IncomingBounded(canceled) error = %v, want context.Canceled", err)
			}
			if sqlite, ok := st.(*SQLiteStore); ok && sqlite.CacheRebuilds() != cacheRebuilds {
				t.Fatalf("bounded reads rebuilt whole-graph cache: before=%d after=%d", cacheRebuilds, sqlite.CacheRebuilds())
			}
		})
	}
}

func TestLookupContract_NodesByID(t *testing.T) {
	for name, factory := range lookupFactories() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			lk := st.(GraphLookup)
			f := seedLookupFixture(t, st)

			// Missing ids skipped, duplicates collapsed, canonical order.
			got, err := lk.NodesByID(ctx, []model.NodeId{
				f.callee.ID(), model.NodeId("nd_missing"), f.hub.ID(), f.callee.ID(),
			})
			if err != nil {
				t.Fatalf("NodesByID: %v", err)
			}
			if want := canonNodes(f.hub, f.callee); !reflect.DeepEqual(got, want) {
				t.Fatalf("NodesByID mismatch:\n got=%v\nwant=%v", got, want)
			}

			// Empty input: empty result.
			got, err = lk.NodesByID(ctx, nil)
			if err != nil {
				t.Fatalf("NodesByID(nil): %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("NodesByID(nil): want empty, got %d", len(got))
			}

			// Cancellation is authoritative even for an empty lookup. Backends
			// must not diverge merely because no SQL statement is needed.
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := lk.NodesByID(canceled, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("NodesByID(canceled, nil): got %v, want context.Canceled", err)
			}
		})
	}
}

func TestBriefAggregateContract_BackendParityAndNoCacheRebuild(t *testing.T) {
	type backend struct {
		name    string
		factory Factory
	}
	backends := []backend{{"mem", MemFactory}, {"sqlite", SQLiteFactory}}
	var reference BriefStats
	for i, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			st, err := backend.factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			seedLookupFixture(t, st)

			var before int64
			if sqlite, ok := st.(*SQLiteStore); ok {
				before = sqlite.CacheRebuilds()
			}
			got, err := st.(BriefAggregatePort).BriefStats(context.Background(), 5)
			if err != nil {
				t.Fatalf("BriefStats: %v", err)
			}
			if got.TotalNodes != 8 || got.TotalEdges != 6 {
				t.Fatalf("totals = %d nodes/%d edges, want 8/6", got.TotalNodes, got.TotalEdges)
			}
			if got.TierCounts[model.TierDerived] != 6 {
				t.Fatalf("derived tier count = %d, want 6", got.TierCounts[model.TierDerived])
			}
			if len(got.TopInbound) == 0 || got.TopInbound[0].Node.QualifiedName() != "pkg.Hub" || got.TopInbound[0].InboundEdges != 4 {
				t.Fatalf("top inbound = %+v, want pkg.Hub with 4", got.TopInbound)
			}
			if sqlite, ok := st.(*SQLiteStore); ok && sqlite.CacheRebuilds() != before {
				t.Fatalf("BriefStats rebuilt whole-graph cache: before=%d after=%d", before, sqlite.CacheRebuilds())
			}
			if i == 0 {
				reference = got
			} else if !reflect.DeepEqual(got, reference) {
				t.Fatalf("backend aggregate mismatch:\n got=%+v\nwant=%+v", got, reference)
			}
		})
	}
}

func TestDegreeSampleContract_BackendParityAndStratification(t *testing.T) {
	type backend struct {
		name    string
		factory Factory
	}
	backends := []backend{{"mem", MemFactory}, {"sqlite", SQLiteFactory}}
	var reference []model.Node
	for i, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			st, err := backend.factory(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			seedLookupFixture(t, st)
			var before int64
			if sqlite, ok := st.(*SQLiteStore); ok {
				before = sqlite.CacheRebuilds()
			}
			got, err := st.(DegreeSamplePort).DegreeStratifiedSymbols(context.Background(), 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 {
				t.Fatalf("sample size = %d, want 3", len(got))
			}
			if sqlite, ok := st.(*SQLiteStore); ok && sqlite.CacheRebuilds() != before {
				t.Fatalf("degree sample rebuilt whole-graph cache")
			}
			if i == 0 {
				reference = got
			} else if !reflect.DeepEqual(got, reference) {
				t.Fatalf("backend sample mismatch:\n got=%+v\nwant=%+v", got, reference)
			}
		})
	}
}

func TestSQLiteNodesByID_ChunksBeyondHostParameterLimit(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(t.TempDir() + "/graph.db")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer st.Close()

	first, err := model.NewNode("function", "pkg.First", "pkg/first.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	last, err := model.NewNode("function", "pkg.Last", "pkg/last.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutNode(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNode(ctx, last); err != nil {
		t.Fatal(err)
	}

	ids := make([]model.NodeId, 0, 33_000)
	ids = append(ids, first.ID())
	for i := 0; i < 32_998; i++ {
		ids = append(ids, model.NodeId(fmt.Sprintf("missing-%05d", i)))
	}
	ids = append(ids, last.ID(), first.ID()) // duplicate must remain collapsed

	before := st.CacheRebuilds()
	got, err := st.NodesByID(ctx, ids)
	if err != nil {
		t.Fatalf("NodesByID(33k): %v", err)
	}
	if want := canonNodes(first, last); !reflect.DeepEqual(got, want) {
		t.Fatalf("NodesByID(33k) mismatch:\n got=%v\nwant=%v", got, want)
	}
	if st.CacheRebuilds() != before {
		t.Fatalf("NodesByID rebuilt the whole-graph cache: before=%d after=%d", before, st.CacheRebuilds())
	}
}

func TestLookupContract_SymbolLookups(t *testing.T) {
	for name, factory := range lookupFactories() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			sl := st.(SymbolLookupPort)
			f := seedLookupFixture(t, st)

			// QualifiedName: BOTH nodes sharing the name, canonical order.
			got, err := sl.QualifiedName(ctx, "pkg.Hub")
			if err != nil {
				t.Fatalf("QualifiedName: %v", err)
			}
			if want := canonNodes(f.hub, f.sameQNOther); !reflect.DeepEqual(got, want) {
				t.Fatalf("QualifiedName mismatch:\n got=%v\nwant=%v", got, want)
			}

			// SourcePath: both nodes in the shared file.
			got, err = sl.SourcePath(ctx, "pkg/callers.go")
			if err != nil {
				t.Fatalf("SourcePath: %v", err)
			}
			if want := canonNodes(f.callerA, f.callerB); !reflect.DeepEqual(got, want) {
				t.Fatalf("SourcePath mismatch:\n got=%v\nwant=%v", got, want)
			}

			// Exact equality only — no substring/prefix semantics.
			for _, miss := range []string{"pkg.Hu", "pkg.Hubb", "hub.go"} {
				got, err = sl.QualifiedName(ctx, miss)
				if err != nil {
					t.Fatalf("QualifiedName(%q): %v", miss, err)
				}
				if len(got) != 0 {
					t.Fatalf("QualifiedName(%q): want empty (exact equality), got %d", miss, len(got))
				}
			}

			// Search: the ranked lexical contract (SearchNodes verbatim) finds the
			// hub among its matches; empty query yields no results.
			matches, err := sl.Search(ctx, "Hub", 0)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			found := false
			for _, m := range matches {
				if m.Node.ID() == f.hub.ID() {
					found = true
				}
			}
			if !found {
				t.Fatalf("Search(Hub) did not return the hub (got %d matches)", len(matches))
			}
			if empty, err := sl.Search(ctx, "  ", 5); err != nil || len(empty) != 0 {
				t.Fatalf("Search(blank): want empty/no error, got %d, %v", len(empty), err)
			}
		})
	}
}

// TestLookupContract_WritesKeepIndexesFresh proves the selective indexes track
// every mutation path: delete an edge, delete a node (cascade), and re-put —
// the ports never serve stale results. This is the atomic-maintenance clause
// of ADR 0003 D4.
func TestLookupContract_WritesKeepIndexesFresh(t *testing.T) {
	for name, factory := range lookupFactories() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			lk := st.(GraphLookup)
			sl := st.(SymbolLookupPort)
			f := seedLookupFixture(t, st)

			// Delete one incoming call edge → it disappears from Incoming.
			if err := st.DeleteEdge(ctx, f.inCalls[0].ID()); err != nil {
				t.Fatalf("DeleteEdge: %v", err)
			}
			got, err := lk.Incoming(ctx, f.hub.ID(), "calls")
			if err != nil {
				t.Fatalf("Incoming after DeleteEdge: %v", err)
			}
			if want := canonEdges(f.inCalls[1:]); !reflect.DeepEqual(got, want) {
				t.Fatalf("Incoming after DeleteEdge mismatch:\n got=%v\nwant=%v", got, want)
			}
			bounded, truncated, err := st.(BoundedGraphLookup).IncomingBounded(ctx, f.hub.ID(), 8, "calls")
			if err != nil || truncated || !reflect.DeepEqual(bounded, canonEdges(f.inCalls[1:])) {
				t.Fatalf("IncomingBounded after DeleteEdge = %v, truncated=%v, err=%v", bounded, truncated, err)
			}

			// Delete the hub → cascade: no incident edges remain anywhere, and the
			// symbol lookup no longer returns the deleted node (the same-QN node in
			// the other file survives).
			if err := st.DeleteNode(ctx, f.hub.ID()); err != nil {
				t.Fatalf("DeleteNode: %v", err)
			}
			for _, side := range []struct {
				name string
				fn   func(context.Context, model.NodeId, ...model.EdgeKind) ([]model.Edge, error)
				id   model.NodeId
			}{
				{"Incoming(hub)", lk.Incoming, f.hub.ID()},
				{"Outgoing(hub)", lk.Outgoing, f.hub.ID()},
				{"Outgoing(callerA)", lk.Outgoing, f.callerA.ID()},
				{"Incoming(callee)", lk.Incoming, f.callee.ID()},
			} {
				es, err := side.fn(ctx, side.id)
				if err != nil {
					t.Fatalf("%s after DeleteNode: %v", side.name, err)
				}
				if len(es) != 0 {
					t.Fatalf("%s after DeleteNode: want empty (cascade), got %d edges", side.name, len(es))
				}
			}
			byQN, err := sl.QualifiedName(ctx, "pkg.Hub")
			if err != nil {
				t.Fatalf("QualifiedName after DeleteNode: %v", err)
			}
			if want := canonNodes(f.sameQNOther); !reflect.DeepEqual(byQN, want) {
				t.Fatalf("QualifiedName after DeleteNode mismatch:\n got=%v\nwant=%v", byQN, want)
			}

			// Re-put node + one edge → served again.
			if err := st.PutNode(ctx, f.hub); err != nil {
				t.Fatalf("re-PutNode: %v", err)
			}
			if err := st.PutEdge(ctx, f.outCalls[0]); err != nil {
				t.Fatalf("re-PutEdge: %v", err)
			}
			got, err = lk.Outgoing(ctx, f.hub.ID())
			if err != nil {
				t.Fatalf("Outgoing after re-put: %v", err)
			}
			if want := canonEdges(f.outCalls[:1]); !reflect.DeepEqual(got, want) {
				t.Fatalf("Outgoing after re-put mismatch:\n got=%v\nwant=%v", got, want)
			}
		})
	}
}

// TestLookupContract_MemLoadRebuildsIndexes proves Load (snapshot restore)
// re-derives MemStore's selective indexes from the snapshot content.
func TestLookupContract_MemLoadRebuildsIndexes(t *testing.T) {
	ctx := context.Background()
	src := NewMemStore()
	defer src.Close()
	f := seedLookupFixture(t, src)

	snap := t.TempDir() + "/snap.graphi"
	if err := src.Snapshot(ctx, snap); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := NewMemStore()
	defer dst.Close()
	if err := dst.Load(ctx, snap); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, err := dst.Incoming(ctx, f.hub.ID(), "calls")
	if err != nil {
		t.Fatalf("Incoming after Load: %v", err)
	}
	if want := canonEdges(f.inCalls); !reflect.DeepEqual(got, want) {
		t.Fatalf("Incoming after Load mismatch:\n got=%v\nwant=%v", got, want)
	}
	bounded, truncated, err := dst.IncomingBounded(ctx, f.hub.ID(), 2, "calls")
	if err != nil || !truncated || !reflect.DeepEqual(bounded, canonEdges(f.inCalls)[:2]) {
		t.Fatalf("IncomingBounded after Load = %v, truncated=%v, err=%v", bounded, truncated, err)
	}
	byPath, err := dst.SourcePath(ctx, "pkg/callers.go")
	if err != nil {
		t.Fatalf("SourcePath after Load: %v", err)
	}
	if want := canonNodes(f.callerA, f.callerB); !reflect.DeepEqual(byPath, want) {
		t.Fatalf("SourcePath after Load mismatch:\n got=%v\nwant=%v", byPath, want)
	}
}

// TestLookupContract_CrossBackendIdentity runs the same reads against BOTH
// backends over the same seeded graph and requires deeply equal results — the
// G2 "Memory/SQLite deterministically identical" gate for the new ports.
func TestLookupContract_CrossBackendIdentity(t *testing.T) {
	ctx := context.Background()
	mem, err := MemFactory(t.TempDir())
	if err != nil {
		t.Fatalf("mem factory: %v", err)
	}
	defer mem.Close()
	sq, err := SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite factory: %v", err)
	}
	defer sq.Close()

	fm := seedLookupFixture(t, mem)
	_ = seedLookupFixture(t, sq) // identical deterministic seed

	mlk, slk := mem.(GraphLookup), sq.(GraphLookup)
	msl, ssl := mem.(SymbolLookupPort), sq.(SymbolLookupPort)

	edgesM, err := mlk.Incoming(ctx, fm.hub.ID())
	if err != nil {
		t.Fatalf("mem Incoming: %v", err)
	}
	edgesS, err := slk.Incoming(ctx, fm.hub.ID())
	if err != nil {
		t.Fatalf("sqlite Incoming: %v", err)
	}
	if !reflect.DeepEqual(edgesM, edgesS) {
		t.Fatalf("cross-backend Incoming diverged:\n mem   =%v\n sqlite=%v", edgesM, edgesS)
	}

	nodesM, err := msl.QualifiedName(ctx, "pkg.Hub")
	if err != nil {
		t.Fatalf("mem QualifiedName: %v", err)
	}
	nodesS, err := ssl.QualifiedName(ctx, "pkg.Hub")
	if err != nil {
		t.Fatalf("sqlite QualifiedName: %v", err)
	}
	if !reflect.DeepEqual(nodesM, nodesS) {
		t.Fatalf("cross-backend QualifiedName diverged:\n mem   =%v\n sqlite=%v", nodesM, nodesS)
	}

	byIDM, err := mlk.NodesByID(ctx, []model.NodeId{fm.hub.ID(), fm.callee.ID()})
	if err != nil {
		t.Fatalf("mem NodesByID: %v", err)
	}
	byIDS, err := slk.NodesByID(ctx, []model.NodeId{fm.hub.ID(), fm.callee.ID()})
	if err != nil {
		t.Fatalf("sqlite NodesByID: %v", err)
	}
	if !reflect.DeepEqual(byIDM, byIDS) {
		t.Fatalf("cross-backend NodesByID diverged:\n mem   =%v\n sqlite=%v", byIDM, byIDS)
	}
}
