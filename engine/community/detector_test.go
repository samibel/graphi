package community

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	corecommunity "github.com/samibel/graphi/core/community"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

func node(t *testing.T, qn string) model.Node {
	t.Helper()
	n, err := model.NewNode("function", qn, qn+".go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode(%q): %v", qn, err)
	}
	return n
}

func putNode(t *testing.T, s graphstore.Graphstore, qn string) model.NodeId {
	t.Helper()
	n := node(t, qn)
	if err := s.PutNode(context.Background(), n); err != nil {
		t.Fatalf("PutNode(%q): %v", qn, err)
	}
	return n.ID()
}

func putEdge(t *testing.T, s graphstore.Graphstore, from, to model.NodeId, w float64) {
	t.Helper()
	e, err := model.NewEdge(from, to, "calls", model.TierConfirmed, w, "test", []string{"e.go:1"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	if err := s.PutEdge(context.Background(), e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
}

// layeredCorpus: two features that each couple across the api/svc/db packages.
// Every edge crosses a package boundary, so the package-prefix partition is
// demonstrably suboptimal and Louvain (which groups by coupling) wins.
func layeredCorpus(t *testing.T, s graphstore.Graphstore) {
	t.Helper()
	id := map[string]model.NodeId{}
	for _, qn := range []string{
		"api.GetUser", "api.GetOrder",
		"svc.UserSvc", "svc.OrderSvc",
		"db.UserRepo", "db.OrderRepo",
	} {
		id[qn] = putNode(t, s, qn)
	}
	putEdge(t, s, id["api.GetUser"], id["svc.UserSvc"], 0.9)
	putEdge(t, s, id["svc.UserSvc"], id["db.UserRepo"], 0.9)
	putEdge(t, s, id["api.GetUser"], id["db.UserRepo"], 0.6)
	putEdge(t, s, id["api.GetOrder"], id["svc.OrderSvc"], 0.9)
	putEdge(t, s, id["svc.OrderSvc"], id["db.OrderRepo"], 0.9)
	putEdge(t, s, id["api.GetOrder"], id["db.OrderRepo"], 0.6)
}

func commOf(comms []Community) map[model.NodeId]int {
	out := map[model.NodeId]int{}
	for _, c := range comms {
		for _, m := range c.Members {
			out[m] = c.ID
		}
	}
	return out
}

func graphView(t *testing.T, s graphstore.Graphstore) ([]model.NodeId, []model.Edge) {
	t.Helper()
	ctx := context.Background()
	ns, err := s.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	ids := make([]model.NodeId, len(ns))
	for i, n := range ns {
		ids[i] = n.ID()
	}
	es, err := s.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	return ids, es
}

// AC-1: Louvain achieves higher modularity than the retained package-prefix
// baseline on the fixture corpus, over the SAME weighted undirected graph.
func TestAC1_LouvainModularityBeatsPackagePrefix(t *testing.T) {
	s := graphstore.NewMemStore()
	layeredCorpus(t, s)
	ctx := context.Background()

	pkg, err := (PackagePrefixDetector{}).Detect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	lou, err := (LouvainDetector{}).Detect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}

	ids, edges := graphView(t, s)
	qPkg := corecommunity.Modularity(ids, edges, commOf(pkg))
	qLou := corecommunity.Modularity(ids, edges, commOf(lou))

	if !(qLou > qPkg) {
		t.Fatalf("Louvain modularity (%v) must exceed package-prefix baseline (%v)", qLou, qPkg)
	}
	// Each entity assigned exactly one community.
	seen := map[model.NodeId]int{}
	for _, c := range lou {
		for _, m := range c.Members {
			seen[m]++
		}
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("entity %s assigned %d times, want exactly 1", id, seen[id])
		}
	}
}

// AC-2: byte-identical IDs + membership + serialized bytes across (a) repeated
// in-process runs, (b) a simulated restart (cache eviction → rebuild from the
// durable layer), and (c) permuted insertion order.
func TestAC2_ByteIdenticalAcrossRunsRestartAndPermutation(t *testing.T) {
	ctx := context.Background()
	d := LouvainDetector{}

	s := graphstore.NewMemStore()
	layeredCorpus(t, s)

	first, err := d.Detect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := SerializeCommunities(first)
	if err != nil {
		t.Fatal(err)
	}

	// (a) repeated in-process.
	for i := 0; i < 3; i++ {
		got, _ := d.Detect(ctx, s)
		assertSameCommunities(t, first, got)
	}

	// (b) simulated restart: drop the hot cache, force rebuild from durable.
	if err := s.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}
	afterRestart, _ := d.Detect(ctx, s)
	assertSameCommunities(t, first, afterRestart)
	rb, _ := SerializeCommunities(afterRestart)
	if !bytes.Equal(firstBytes, rb) {
		t.Fatalf("serialized bytes differ after restart")
	}

	// (c) permuted insertion order in a fresh store reaching the same state.
	s2 := graphstore.NewMemStore()
	layeredCorpusPermuted(t, s2)
	permuted, _ := d.Detect(ctx, s2)
	assertSameCommunities(t, first, permuted)
	pb, _ := SerializeCommunities(permuted)
	if !bytes.Equal(firstBytes, pb) {
		t.Fatalf("serialized bytes differ under permuted insertion order")
	}
}

// layeredCorpusPermuted builds the same final graph as layeredCorpus but inserts
// nodes/edges in a deliberately different arrival order.
func layeredCorpusPermuted(t *testing.T, s graphstore.Graphstore) {
	t.Helper()
	id := map[string]model.NodeId{}
	// Reverse-ish insertion order.
	for _, qn := range []string{
		"db.OrderRepo", "db.UserRepo",
		"svc.OrderSvc", "svc.UserSvc",
		"api.GetOrder", "api.GetUser",
	} {
		id[qn] = putNode(t, s, qn)
	}
	// Edges inserted in a different order too.
	putEdge(t, s, id["api.GetOrder"], id["db.OrderRepo"], 0.6)
	putEdge(t, s, id["svc.OrderSvc"], id["db.OrderRepo"], 0.9)
	putEdge(t, s, id["api.GetOrder"], id["svc.OrderSvc"], 0.9)
	putEdge(t, s, id["api.GetUser"], id["db.UserRepo"], 0.6)
	putEdge(t, s, id["svc.UserSvc"], id["db.UserRepo"], 0.9)
	putEdge(t, s, id["api.GetUser"], id["svc.UserSvc"], 0.9)
}

// AC-3: context-assembly cluster resolution for a target entity resolves via the
// Louvain Detector seam; package-prefix is no longer the default mechanism.
func TestAC3_ClusterResolvesViaLouvainSeam(t *testing.T) {
	ctx := context.Background()
	s := graphstore.NewMemStore()
	layeredCorpus(t, s)

	// Default grouping mechanism is Louvain.
	if got := DefaultDetector().Name(); got != "louvain" {
		t.Fatalf("default detector Name()=%q, want \"louvain\" (package-prefix must not be the default)", got)
	}

	target := node(t, "api.GetUser").ID()
	cluster, ok, err := Cluster(ctx, s, DefaultDetector(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("target entity not resolved to any cluster")
	}

	// The Louvain cluster groups api.GetUser with its coupled svc/db symbols
	// (the user feature), which crosses package boundaries — something the
	// package-prefix mechanism could never produce.
	memberSet := map[model.NodeId]struct{}{}
	for _, m := range cluster.Members {
		memberSet[m] = struct{}{}
	}
	userSvc := node(t, "svc.UserSvc").ID()
	userRepo := node(t, "db.UserRepo").ID()
	if _, in := memberSet[userSvc]; !in {
		t.Fatalf("Louvain cluster of api.GetUser missing coupled svc.UserSvc: %+v", cluster.Members)
	}
	if _, in := memberSet[userRepo]; !in {
		t.Fatalf("Louvain cluster of api.GetUser missing coupled db.UserRepo: %+v", cluster.Members)
	}

	// Contrast: the package-prefix mechanism would put api.GetUser only with
	// api.GetOrder (same package), proving the grouping mechanism really changed.
	pkgCluster, ok, err := Cluster(ctx, s, PackagePrefixDetector{}, target)
	if err != nil || !ok {
		t.Fatalf("package-prefix resolve failed: ok=%v err=%v", ok, err)
	}
	if reflect.DeepEqual(sortedMembers(cluster), sortedMembers(pkgCluster)) {
		t.Fatalf("Louvain and package-prefix clusters are identical — seam not actually swapped")
	}
}

// AC-4: a graph reached via incremental ingestion (puts + a delete that mints a
// new id, then re-adds) vs full re-ingestion to the same state yields
// byte-identical community IDs + membership + serialized bytes.
func TestAC4_FullVsIncrementalByteParity(t *testing.T) {
	ctx := context.Background()
	d := LouvainDetector{}

	// Full ingest.
	full := graphstore.NewMemStore()
	layeredCorpus(t, full)
	fullComms, _ := d.Detect(ctx, full)
	fullBytes, err := SerializeCommunities(fullComms)
	if err != nil {
		t.Fatal(err)
	}

	// Incremental: build a partial/altered graph, then converge to the same
	// final state via deletes + re-adds.
	inc := graphstore.NewMemStore()
	id := map[string]model.NodeId{}
	for _, qn := range []string{"api.GetUser", "svc.UserSvc", "db.UserRepo"} {
		id[qn] = putNode(t, inc, qn)
	}
	putEdge(t, inc, id["api.GetUser"], id["svc.UserSvc"], 0.9)
	putEdge(t, inc, id["api.GetUser"], id["db.UserRepo"], 0.6)
	putEdge(t, inc, id["svc.UserSvc"], id["db.UserRepo"], 0.9)

	// Add a stray node + edge, then delete the node (which removes its incident
	// edges) to exercise the delete path.
	stray := putNode(t, inc, "tmp.Scratch")
	putEdge(t, inc, stray, id["svc.UserSvc"], 0.5)
	if err := inc.DeleteNode(ctx, stray); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	// Now add the order feature to reach the exact full state.
	for _, qn := range []string{"api.GetOrder", "svc.OrderSvc", "db.OrderRepo"} {
		id[qn] = putNode(t, inc, qn)
	}
	putEdge(t, inc, id["api.GetOrder"], id["svc.OrderSvc"], 0.9)
	putEdge(t, inc, id["svc.OrderSvc"], id["db.OrderRepo"], 0.9)
	putEdge(t, inc, id["api.GetOrder"], id["db.OrderRepo"], 0.6)

	incComms, _ := d.Detect(ctx, inc)
	incBytes, err := SerializeCommunities(incComms)
	if err != nil {
		t.Fatal(err)
	}

	assertSameCommunities(t, fullComms, incComms)
	if !bytes.Equal(fullBytes, incBytes) {
		t.Fatalf("full vs incremental serialized bytes differ:\nfull: %s\ninc:  %s", fullBytes, incBytes)
	}
}

// AC-7: degenerate graphs via the detector — empty, single-node,
// fully-disconnected — yield deterministic singletons without panics.
func TestAC7_DegenerateGraphs(t *testing.T) {
	ctx := context.Background()
	d := LouvainDetector{}

	// Empty.
	empty, err := d.Detect(ctx, graphstore.NewMemStore())
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty graph: got %d communities, want 0", len(empty))
	}

	// Single node.
	s1 := graphstore.NewMemStore()
	putNode(t, s1, "solo.Fn")
	one, _ := d.Detect(ctx, s1)
	if len(one) != 1 || len(one[0].Members) != 1 {
		t.Fatalf("single node: want one singleton, got %+v", one)
	}

	// Fully disconnected (no edges).
	sN := graphstore.NewMemStore()
	for _, qn := range []string{"a.A", "b.B", "c.C", "d.D"} {
		putNode(t, sN, qn)
	}
	many, _ := d.Detect(ctx, sN)
	if len(many) != 4 {
		t.Fatalf("disconnected: want 4 singletons, got %d", len(many))
	}
	for i, c := range many {
		if len(c.Members) != 1 || c.ID != i+1 {
			t.Fatalf("disconnected community %d not a canonical singleton: %+v", i, c)
		}
	}
	// Deterministic across a repeat.
	again, _ := d.Detect(ctx, sN)
	assertSameCommunities(t, many, again)
}

// --- helpers ---

func assertSameCommunities(t *testing.T, want, got []Community) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("community count differs: want %d got %d", len(want), len(got))
	}
	for i := range want {
		if want[i].ID != got[i].ID {
			t.Fatalf("community %d ID differs: want %d got %d", i, want[i].ID, got[i].ID)
		}
		if !reflect.DeepEqual(want[i].Members, got[i].Members) {
			t.Fatalf("community %d members differ:\nwant %v\ngot  %v", i, want[i].Members, got[i].Members)
		}
	}
}

func sortedMembers(c Community) []model.NodeId {
	out := append([]model.NodeId(nil), c.Members...)
	return out // detector already returns members sorted by NodeId
}
