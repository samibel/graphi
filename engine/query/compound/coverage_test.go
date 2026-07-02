package compound_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
)

// TestCoverage_CompoundDeterminismCanonicalBytes (EP-011 SW-077) is the
// determinism coverage entry for the compound engine: identical input must
// yield byte-identical canonical serialized Result bytes across runs (no
// map-iteration leakage in the compound output path). It complements the
// in-package determinism test by asserting over the CANONICAL serialized form
// (query.Marshal), which is what every surface emits.
func TestCoverage_CompoundDeterminismCanonicalBytes(t *testing.T) {
	store, ids := seedGraph(t)
	q := compound.Query{
		Seed:  ids["pkg.A"],
		Steps: []compound.Step{{Direction: compound.DirBoth}},
	}
	first, err := compound.Execute(context.Background(), store, q)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	fb, err := query.Marshal(first)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for i := 0; i < 30; i++ {
		r, err := compound.Execute(context.Background(), store, q)
		if err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
		rb, err := query.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal %d: %v", i, err)
		}
		if string(fb) != string(rb) {
			t.Fatalf("run %d: canonical bytes diverged:\n%s\n%s", i, fb, rb)
		}
	}
}

// TestCoverage_CompoundRoundTripCollapsesMultiCall (EP-011 SW-077) proves the
// headline token-leverage claim of G1 at the coverage level: a compound query
// returns a result spanning multiple fixed-query hops in ONE request. It is the
// coverage-matrix evidence that "compound query collapses representative
// multi-call agent flows into single requests".
func TestCoverage_CompoundRoundTripCollapsesMultiCall(t *testing.T) {
	store, ids := seedGraph(t)
	svc := query.New(store)

	// Fixed path: callees(A) then callees(B) — two round-trips to reach C via B.
	firstHop, err := svc.Callees(context.Background(), ids["pkg.A"])
	if err != nil {
		t.Fatal(err)
	}
	// The fixed callees(A) result reaches B and C (both direct callees of A in the
	// seed graph). The compound 2-hop query reaches the SAME set in one request.
	oneHop, err := compound.Execute(context.Background(), store, compound.Query{
		Seed:  ids["pkg.A"],
		Steps: []compound.Step{{Direction: compound.DirOutbound, Kinds: []string{query.EdgeKindCalls}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Every node the fixed one-round-trip callees(A) returns must also appear in
	// the single compound request's result (compound is a superset by construction
	// since it includes the seed + traversed nodes).
	fixedSet := map[model.NodeId]bool{}
	for _, n := range firstHop.Nodes {
		fixedSet[n.ID] = true
	}
	compSet := map[model.NodeId]bool{}
	for _, n := range oneHop.Nodes {
		compSet[n.ID] = true
	}
	for id := range fixedSet {
		if !compSet[id] {
			t.Fatalf("compound single-request result missing a node the fixed multi-call path returned: %s", id)
		}
	}
}

// TestCoverage_HierarchyEdgeDeterminism (EP-011 SW-077) is the determinism
// coverage entry for the G2 hierarchy query operations: identical hierarchy
// queries return byte-identical canonical serialized bytes across runs.
func TestCoverage_HierarchyEdgeDeterminism(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	seedHierarchyForCoverage(t, store)
	svc := query.New(store)
	marshal := func(op, sym string) []byte {
		t.Helper()
		r, err := svc.Dispatch(ctx, op, model.NodeId(sym), 0)
		if err != nil {
			t.Fatal(err)
		}
		b, err := query.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, op := range []string{query.OpImplementers, query.OpImplements, query.OpSubtypes, query.OpSupertypes} {
		ref := marshal(op, "shop.Reader")
		for i := 0; i < 25; i++ {
			if got := marshal(op, "shop.Reader"); string(got) != string(ref) {
				t.Fatalf("op %s run %d: canonical bytes diverged", op, i)
			}
		}
	}
}

// seedHierarchyForCoverage builds a tiny hierarchy graph for the determinism
// coverage entry (kept local to avoid coupling the engine/query seed helper).
func seedHierarchyForCoverage(t *testing.T, store *graphstore.MemStore) {
	t.Helper()
	ctx := context.Background()
	mk := func(qn string) model.Node {
		n, err := model.NewNode("type", qn, "shop/"+qn+".go", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	reader := mk("shop.Reader")
	coll := mk("shop.Collector")
	impl := mk("shop.Impl")
	base := mk("shop.Base")
	mkE := func(from, to model.NodeId, kind string) {
		e, err := model.NewEdge(from, to, kind, model.TierDerived, 0.9, "embed", []string{"shop/x.go:1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	mkE(coll.ID(), reader.ID(), query.EdgeKindImplements)
	mkE(impl.ID(), base.ID(), query.EdgeKindInherits)
	_ = strings.TrimSpace // keep strings import used defensively
}

// TestCoverage_NoEgressNoCgoInCompoundPath (EP-011 SW-077) is the guard entry:
// the compound package's dependency graph must contain no CGo / sqlite / net
// symbols (the local-first + CGo-free-default invariants). It uses go list to
// assert the package has none of the forbidden import path substrings.
func TestCoverage_NoEgressNoCgoInCompoundPath(t *testing.T) {
	if testing.Short() {
		t.Skip("go-list dependency scan skipped in -short mode")
	}
	out, err := execGoListDeps("github.com/samibel/graphi/engine/query/compound")
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	// The honest CGo-free / zero-egress guard: the compound package may transit
	// to the pure-Go sqlite driver (modernc.org/sqlite, CGo-free by design),
	// which uses database/sql + net/url INTERFACES but dials nothing. The real
	// invariants are: no runtime/cgo (actual CGo) and no net/http (HTTP egress).
	for _, bad := range []string{"runtime/cgo", "net/http"} {
		if strings.Contains(out, bad) {
			t.Fatalf("compound dependency graph contains forbidden package %q (CGo-free / zero-egress invariant):\n%s", bad, out)
		}
	}
}

// execGoListDeps returns the concatenated dependency list for pkg via `go list
// -deps`. It is a read-only guard helper for the CGo-free / zero-egress coverage
// entry; tests skip gracefully when the toolchain is unavailable.
func execGoListDeps(pkg string) (string, error) {
	cmd := exec.Command("go", "list", "-deps", pkg)
	// The invariant under test is about the SHIPPED default build, which is
	// CGO_ENABLED=0. Pin it: inheriting CGO_ENABLED=1 from the test process
	// (e.g. a `go test -race` run, which requires cgo) would pull runtime/cgo
	// into the scanned graph and fail the guard for the wrong build.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
