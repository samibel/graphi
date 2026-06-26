package embed_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// ---- deterministic synthetic corpus (no math/rand: a fixed LCG seeded by index) ----

// synthVectors builds n d-dimensional unit-ish vectors via a fixed linear
// congruential generator. It is a pure function of (n, d, seed): identical inputs
// yield identical vectors in CI, so the recall number is reproducible.
func synthVectors(n, d int, seed uint64) []embed.Vector {
	out := make([]embed.Vector, 0, n)
	state := seed | 1
	next := func() float32 {
		state = state*6364136223846793005 + 1442695040888963407
		// top 24 bits → [-1,1)
		return float32(int32(state>>40))/float32(1<<23) - 1.0
	}
	for i := 0; i < n; i++ {
		v := make([]float32, d)
		for j := 0; j < d; j++ {
			v[j] = next()
		}
		id := model.NodeId(fmt.Sprintf("n%06d", i))
		out = append(out, embed.Vector{NodeID: id, Values: v})
	}
	return out
}

func tableFrom(t *testing.T, vecs []embed.Vector) *embed.MemVectorTable {
	t.Helper()
	tbl := embed.NewMemVectorTable()
	for _, v := range vecs {
		if err := tbl.Upsert(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
	return tbl
}

// bruteForceTopK is the ground-truth ranking used as the recall oracle.
func bruteForceTopK(query []float32, vecs []embed.Vector, k int) []model.NodeId {
	type sc struct {
		id model.NodeId
		s  float64
	}
	var qn float64
	for _, x := range query {
		qn += float64(x) * float64(x)
	}
	qn = math.Sqrt(qn)
	scored := make([]sc, 0, len(vecs))
	for _, v := range vecs {
		var dot, bn float64
		for i := range v.Values {
			dot += float64(query[i]) * float64(v.Values[i])
			bn += float64(v.Values[i]) * float64(v.Values[i])
		}
		bn = math.Sqrt(bn)
		var s float64
		if qn != 0 && bn != 0 {
			s = dot / (qn * bn)
		}
		scored = append(scored, sc{v.NodeID, s})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].s != scored[j].s {
			return scored[i].s > scored[j].s
		}
		return scored[i].id < scored[j].id
	})
	out := make([]model.NodeId, 0, k)
	for i := 0; i < k && i < len(scored); i++ {
		out = append(out, scored[i].id)
	}
	return out
}

// AC3: recall@10 >= 0.95 over N>=1000 vectors vs brute-force ground truth.
func TestHNSW_RecallAt10(t *testing.T) {
	const (
		n   = 1200
		d   = 48
		k   = 10
		qry = 40
	)
	vecs := synthVectors(n, d, 0xC0FFEE)
	tbl := tableFrom(t, vecs)

	ix := embed.NewHNSWIndex(embed.HNSWParams{M: 16, EfConstruction: 200, EfSearch: 128})
	if err := ix.Rebuild(context.Background(), tbl); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != n {
		t.Fatalf("indexed %d vectors, want %d", ix.Len(), n)
	}

	queries := synthVectors(qry, d, 0xBEEF) // distinct deterministic query set
	var hitSum, total float64
	for _, q := range queries {
		truth := bruteForceTopK(q.Values, vecs, k)
		truthSet := map[model.NodeId]struct{}{}
		for _, id := range truth {
			truthSet[id] = struct{}{}
		}
		got := ix.Search(q.Values, k)
		found := 0
		for _, h := range got {
			if _, ok := truthSet[h.NodeID]; ok {
				found++
			}
		}
		hitSum += float64(found)
		total += float64(len(truth))
	}
	recall := hitSum / total
	if recall < 0.95 {
		t.Fatalf("recall@10 = %.4f, want >= 0.95", recall)
	}
	t.Logf("recall@10 = %.4f over %d queries / %d vectors", recall, qry, n)
}

// AC6 (determinism): two independent builds over the same vectors produce
// byte-identical query results, and a replay on the same index is identical.
func TestHNSW_DeterministicReplay(t *testing.T) {
	vecs := synthVectors(300, 32, 0x1234)
	tbl := tableFrom(t, vecs)
	q := synthVectors(1, 32, 0x9999)[0].Values

	build := func() []embed.Hit {
		ix := embed.NewHNSWIndex(embed.HNSWParams{M: 12, EfConstruction: 100, EfSearch: 64})
		if err := ix.Rebuild(context.Background(), tbl); err != nil {
			t.Fatal(err)
		}
		return ix.Search(q, 10)
	}
	a, b := build(), build()
	if !hitsEqual(a, b) {
		t.Fatalf("independent builds differ:\n a=%v\n b=%v", a, b)
	}

	// Replay on a single index must also be identical.
	ix := embed.NewHNSWIndex(embed.HNSWParams{M: 12, EfConstruction: 100, EfSearch: 64})
	if err := ix.Rebuild(context.Background(), tbl); err != nil {
		t.Fatal(err)
	}
	if !hitsEqual(ix.Search(q, 10), ix.Search(q, 10)) {
		t.Fatal("replay on same index not identical")
	}
}

// Put-order independence: inserting in scrambled order yields the same graph
// results as the canonical Rebuild order (determinism under incremental embed).
func TestHNSW_PutOrderIndependent(t *testing.T) {
	vecs := synthVectors(200, 24, 0x5151)
	tbl := tableFrom(t, vecs)
	q := synthVectors(1, 24, 0x2222)[0].Values

	viaRebuild := embed.NewHNSWIndex(embed.HNSWParams{M: 16, EfConstruction: 120, EfSearch: 64})
	if err := viaRebuild.Rebuild(context.Background(), tbl); err != nil {
		t.Fatal(err)
	}

	// Insert in reverse order via Put.
	viaPut := embed.NewHNSWIndex(embed.HNSWParams{M: 16, EfConstruction: 120, EfSearch: 64})
	for i := len(vecs) - 1; i >= 0; i-- {
		viaPut.Put(vecs[i].NodeID, vecs[i].Values)
	}
	if !hitsEqual(viaRebuild.Search(q, 10), viaPut.Search(q, 10)) {
		t.Fatalf("Put order changed results:\n rebuild=%v\n put=%v", viaRebuild.Search(q, 10), viaPut.Search(q, 10))
	}
}

// AC2/AC5: the default config selects the brute-force backend (OFF-by-default).
func TestNewVectorIndex_DefaultIsBruteforce(t *testing.T) {
	for _, cfg := range []embed.VectorIndexConfig{
		{},                               // zero value
		embed.DefaultVectorIndexConfig(), // explicit default
		{Index: "bruteforce"},
	} {
		vi, err := embed.NewVectorIndex(cfg)
		if err != nil {
			t.Fatalf("cfg %+v: unexpected err %v", cfg, err)
		}
		if _, ok := vi.(*embed.Index); !ok {
			t.Fatalf("cfg %+v: got %T, want *embed.Index (brute-force)", cfg, vi)
		}
	}
	// HNSW only on explicit opt-in.
	vi, err := embed.NewVectorIndex(embed.VectorIndexConfig{Index: "hnsw", HNSW: embed.HNSWParams{M: 8, EfConstruction: 50, EfSearch: 32}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vi.(*embed.HNSWIndex); !ok {
		t.Fatalf("hnsw cfg: got %T, want *embed.HNSWIndex", vi)
	}
}

// AC7: invalid config is rejected fail-fast with a typed *InvalidConfig, before any
// construction.
func TestVectorIndexConfig_InvalidFailsFast(t *testing.T) {
	cases := []struct {
		name string
		cfg  embed.VectorIndexConfig
	}{
		{"unknown index", embed.VectorIndexConfig{Index: "annoy"}},
		{"bad m", embed.VectorIndexConfig{Index: "hnsw", HNSW: embed.HNSWParams{M: 0, EfConstruction: 200, EfSearch: 64}}},
		{"bad efc", embed.VectorIndexConfig{Index: "hnsw", HNSW: embed.HNSWParams{M: 16, EfConstruction: 0, EfSearch: 64}}},
		{"bad efs", embed.VectorIndexConfig{Index: "hnsw", HNSW: embed.HNSWParams{M: 16, EfConstruction: 200, EfSearch: -1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vi, err := embed.NewVectorIndex(c.cfg)
			if err == nil {
				t.Fatalf("want error, got index %T", vi)
			}
			if vi != nil {
				t.Fatalf("want nil index on error, got %T", vi)
			}
			var ic *embed.InvalidConfig
			if !errors.As(err, &ic) {
				t.Fatalf("want *InvalidConfig, got %T: %v", err, err)
			}
			if ic.Field == "" || ic.Reason == "" {
				t.Errorf("InvalidConfig missing field/reason: %+v", ic)
			}
		})
	}
}

// AC6 (seam invariant): the SAME construction path serves both backends; an HNSW
// index dropped behind the VectorIndex seam ranks the obvious nearest neighbour
// first, just like brute force.
func TestVectorIndex_SeamBothBackends(t *testing.T) {
	vecs := synthVectors(150, 16, 0x4242)
	tbl := tableFrom(t, vecs)
	q := vecs[7].Values // query equals an indexed vector → it must rank #1 (score 1.0)

	for _, cfg := range []embed.VectorIndexConfig{
		embed.DefaultVectorIndexConfig(),
		{Index: "hnsw", HNSW: embed.HNSWParams{M: 16, EfConstruction: 200, EfSearch: 64}},
	} {
		vi, err := embed.NewVectorIndex(cfg)
		if err != nil {
			t.Fatalf("cfg %+v: %v", cfg, err)
		}
		if err := vi.Rebuild(context.Background(), tbl); err != nil {
			t.Fatalf("cfg %+v rebuild: %v", cfg, err)
		}
		hits := vi.Search(q, 5)
		if len(hits) == 0 {
			t.Fatalf("cfg %+v: no hits", cfg)
		}
		if hits[0].NodeID != vecs[7].NodeID {
			t.Errorf("cfg %+v: top hit = %s, want %s (self)", cfg, hits[0].NodeID, vecs[7].NodeID)
		}
		if math.Abs(hits[0].Score-1.0) > 1e-9 {
			t.Errorf("cfg %+v: self score = %.6f, want ~1.0", cfg, hits[0].Score)
		}
	}
}

func TestHNSW_EmptyIndex(t *testing.T) {
	ix := embed.NewHNSWIndex(embed.HNSWParams{M: 16, EfConstruction: 200, EfSearch: 64})
	if got := ix.Search([]float32{1, 2, 3}, 10); len(got) != 0 {
		t.Fatalf("empty index returned %d hits, want 0", len(got))
	}
}

func hitsEqual(a, b []embed.Hit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].NodeID != b[i].NodeID || a[i].Score != b[i].Score {
			return false
		}
	}
	return true
}
