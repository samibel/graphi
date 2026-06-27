package community

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// --- fixtures ---

func mkNode(t *testing.T, qn string) model.Node {
	t.Helper()
	n, err := model.NewNode("function", qn, qn+".go", 1, 1)
	if err != nil {
		t.Fatalf("NewNode(%q): %v", qn, err)
	}
	return n
}

func mkEdge(t *testing.T, from, to model.NodeId, w float64) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, "calls", model.TierConfirmed, w, "test", []string{"e.go:1"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	return e
}

// crossPackageCorpus is a layered architecture where two features each couple
// across the api/svc/db packages. ALL edges cross package boundaries, so a
// package-prefix partition has zero internal edges (low modularity) while the
// coupling-based partition (two feature clusters) is strong.
func crossPackageCorpus(t *testing.T) ([]model.NodeId, []model.Edge) {
	t.Helper()
	names := []string{
		"api.GetUser", "api.GetOrder",
		"svc.UserSvc", "svc.OrderSvc",
		"db.UserRepo", "db.OrderRepo",
	}
	id := map[string]model.NodeId{}
	ids := make([]model.NodeId, 0, len(names))
	for _, qn := range names {
		n := mkNode(t, qn)
		id[qn] = n.ID()
		ids = append(ids, n.ID())
	}
	edges := []model.Edge{
		// user feature cluster
		mkEdge(t, id["api.GetUser"], id["svc.UserSvc"], 0.9),
		mkEdge(t, id["svc.UserSvc"], id["db.UserRepo"], 0.9),
		mkEdge(t, id["api.GetUser"], id["db.UserRepo"], 0.6),
		// order feature cluster
		mkEdge(t, id["api.GetOrder"], id["svc.OrderSvc"], 0.9),
		mkEdge(t, id["svc.OrderSvc"], id["db.OrderRepo"], 0.9),
		mkEdge(t, id["api.GetOrder"], id["db.OrderRepo"], 0.6),
	}
	return ids, edges
}

// --- partition coverage ---

func TestDetect_PartitionCoversEveryNodeExactlyOnce(t *testing.T) {
	ids, edges := crossPackageCorpus(t)
	res := Detect(ids, edges)

	seen := map[model.NodeId]int{}
	for _, c := range res.Communities {
		for _, m := range c.Members {
			seen[m]++
		}
	}
	if len(seen) != len(ids) {
		t.Fatalf("covered %d nodes, want %d", len(seen), len(ids))
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("node %s assigned to %d communities, want exactly 1", id, seen[id])
		}
	}
}

// --- AC-2 determinism: repeated runs + permuted input order ---

func TestDetect_Deterministic_RepeatedRuns(t *testing.T) {
	ids, edges := crossPackageCorpus(t)
	first := Detect(ids, edges)
	for i := 0; i < 5; i++ {
		got := Detect(ids, edges)
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d differs:\nfirst: %+v\ngot:   %+v", i, first, got)
		}
	}
}

func TestDetect_Deterministic_PermutedInput(t *testing.T) {
	ids, edges := crossPackageCorpus(t)
	want := Detect(ids, edges)

	rng := rand.New(rand.NewSource(12345)) // permute INPUT only; not the algorithm
	for trial := 0; trial < 20; trial++ {
		pid := append([]model.NodeId(nil), ids...)
		rng.Shuffle(len(pid), func(i, j int) { pid[i], pid[j] = pid[j], pid[i] })
		ped := append([]model.Edge(nil), edges...)
		rng.Shuffle(len(ped), func(i, j int) { ped[i], ped[j] = ped[j], ped[i] })

		got := Detect(pid, ped)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("trial %d: permuted input changed output\nwant: %+v\ngot:  %+v", trial, want, got)
		}
	}
}

// --- AC-1 supporting: Louvain partition modularity beats trivial partitions ---

func TestDetect_ModularityBeatsTrivialPartitions(t *testing.T) {
	ids, edges := crossPackageCorpus(t)
	res := Detect(ids, edges)

	louvainOf := res.CommunityOf()
	qLouvain := Modularity(ids, edges, louvainOf)

	// All-in-one community.
	allOne := map[model.NodeId]int{}
	for _, id := range ids {
		allOne[id] = 1
	}
	qAllOne := Modularity(ids, edges, allOne)

	// All singletons.
	singletons := map[model.NodeId]int{}
	for i, id := range ids {
		singletons[id] = i + 1
	}
	qSingletons := Modularity(ids, edges, singletons)

	if !(qLouvain > qAllOne) {
		t.Fatalf("Louvain Q=%v not greater than all-in-one Q=%v", qLouvain, qAllOne)
	}
	if !(qLouvain > qSingletons) {
		t.Fatalf("Louvain Q=%v not greater than singletons Q=%v", qLouvain, qSingletons)
	}
}

// --- AC-7 degenerate graphs ---

func TestDetect_Empty(t *testing.T) {
	res := Detect(nil, nil)
	if len(res.Communities) != 0 {
		t.Fatalf("empty graph should yield 0 communities, got %d", len(res.Communities))
	}
}

func TestDetect_SingleNode(t *testing.T) {
	n := mkNode(t, "solo.Func")
	res := Detect([]model.NodeId{n.ID()}, nil)
	if len(res.Communities) != 1 || len(res.Communities[0].Members) != 1 {
		t.Fatalf("single node should be one singleton community: %+v", res.Communities)
	}
	if res.Communities[0].ID != 1 || res.Communities[0].Members[0] != n.ID() {
		t.Fatalf("unexpected singleton: %+v", res.Communities[0])
	}
}

func TestDetect_FullyDisconnected(t *testing.T) {
	var ids []model.NodeId
	for _, qn := range []string{"p.A", "p.B", "p.C", "p.D", "p.E"} {
		ids = append(ids, mkNode(t, qn).ID())
	}
	res := Detect(ids, nil) // no edges
	if len(res.Communities) != len(ids) {
		t.Fatalf("fully-disconnected should yield %d singletons, got %d", len(ids), len(res.Communities))
	}
	for i, c := range res.Communities {
		if len(c.Members) != 1 {
			t.Fatalf("community %d not a singleton: %+v", i, c)
		}
		if c.ID != i+1 {
			t.Fatalf("ids not canonical 1..n: %+v", res.Communities)
		}
	}
	// Deterministic: IDs follow canonical NodeId order of representatives.
	again := Detect(ids, nil)
	if !reflect.DeepEqual(res, again) {
		t.Fatalf("disconnected detection not deterministic")
	}
}

// Modularity of an empty/edgeless graph is defined as 0 (no panic).
func TestModularity_EdgelessIsZero(t *testing.T) {
	n := mkNode(t, "x.Y")
	if q := Modularity([]model.NodeId{n.ID()}, nil, map[model.NodeId]int{n.ID(): 1}); q != 0 {
		t.Fatalf("edgeless modularity = %v, want 0", q)
	}
}
