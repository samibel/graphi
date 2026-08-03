package graphstore

import (
	"context"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// seedTrustFixture builds a hand-computable graph: three regular functions,
// three external boundary nodes (minted the way engine/link does — kind
// "external", empty source path), and eight edges across all three tiers.
//
// Expectations, computed by hand:
//
//	NodesTotal=6  EdgesTotal=8
//	EdgesByKind:  calls=5  references=3
//	EdgesByTier:  heuristic=4  derived=3  confirmed=1
//	ExternalNodes=3  ExternalEdges=5
//	TopBoundaries(5) = [{net/http.Get 3} {aaa.Tie 1} {fmt.Println 1}]
//	                   (count desc, then qualified name asc — the tie between
//	                   aaa.Tie and fmt.Println is broken by name)
func seedTrustFixture(t *testing.T, st Graphstore) {
	t.Helper()
	ctx := context.Background()

	mkNode := func(kind, qn, path string, line int) model.Node {
		col := 1
		if path == "" {
			line, col = 0, 0 // external nodes carry no position, as engine/link mints them
		}
		n, err := model.NewNode(kind, qn, path, line, col)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", qn, err)
		}
		if err := st.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", qn, err)
		}
		return n
	}
	mkEdge := func(from, to model.NodeId, kind string, tier model.ConfidenceTier, confidence float64) {
		e, err := model.NewEdge(from, to, kind, tier, confidence, "reason:"+kind, []string{"ev.go:1"})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := st.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}

	a := mkNode("function", "pkg.A", "pkg/a.go", 1)
	b := mkNode("function", "pkg.B", "pkg/b.go", 1)
	c := mkNode("function", "pkg.C", "pkg/c.go", 1)
	extFmt := mkNode("external", "fmt.Println", "", 0)
	extHTTP := mkNode("external", "net/http.Get", "", 0)
	extTie := mkNode("external", "aaa.Tie", "", 0)

	mkEdge(a.ID(), b.ID(), "calls", model.TierConfirmed, 1.0)
	mkEdge(b.ID(), c.ID(), "calls", model.TierDerived, 0.9)
	mkEdge(c.ID(), a.ID(), "references", model.TierHeuristic, 0.6)
	mkEdge(a.ID(), extHTTP.ID(), "calls", model.TierHeuristic, 0.6)
	mkEdge(b.ID(), extHTTP.ID(), "calls", model.TierDerived, 0.9)
	mkEdge(c.ID(), extHTTP.ID(), "references", model.TierHeuristic, 0.6)
	mkEdge(a.ID(), extFmt.ID(), "calls", model.TierHeuristic, 0.6)
	mkEdge(b.ID(), extTie.ID(), "references", model.TierDerived, 0.9)
}

func trustFixtureWant(topBoundaries []ExternalBoundary) TrustStats {
	return TrustStats{
		NodesTotal:    6,
		EdgesTotal:    8,
		EdgesByKind:   map[string]int{"calls": 5, "references": 3},
		EdgesByTier:   map[model.ConfidenceTier]int{model.TierHeuristic: 4, model.TierDerived: 3, model.TierConfirmed: 1},
		ExternalNodes: 3,
		ExternalEdges: 5,
		TopBoundaries: topBoundaries,
	}
}

func TestTrustAggregateContract_BackendParityAndCounts(t *testing.T) {
	type backend struct {
		name    string
		factory Factory
	}
	backends := []backend{{"mem", MemFactory}, {"sqlite", SQLiteFactory}}
	want := trustFixtureWant([]ExternalBoundary{
		{QualifiedName: "net/http.Get", IncidentEdges: 3},
		{QualifiedName: "aaa.Tie", IncidentEdges: 1},
		{QualifiedName: "fmt.Println", IncidentEdges: 1},
	})
	var reference TrustStats
	for i, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			st, err := backend.factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			seedTrustFixture(t, st)

			var before int64
			if sqlite, ok := st.(*SQLiteStore); ok {
				before = sqlite.CacheRebuilds()
			}
			got, err := st.(TrustAggregatePort).TrustStats(context.Background(), 5)
			if err != nil {
				t.Fatalf("TrustStats: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("TrustStats mismatch:\n got=%+v\nwant=%+v", got, want)
			}
			if sqlite, ok := st.(*SQLiteStore); ok && sqlite.CacheRebuilds() != before {
				t.Fatalf("TrustStats rebuilt whole-graph cache: before=%d after=%d", before, sqlite.CacheRebuilds())
			}
			if i == 0 {
				reference = got
			} else if !reflect.DeepEqual(got, reference) {
				t.Fatalf("backend aggregate mismatch:\n got=%+v\nwant=%+v", got, reference)
			}
		})
	}
}

func TestTrustAggregateContract_TopNZeroAndTruncation(t *testing.T) {
	type backend struct {
		name    string
		factory Factory
	}
	backends := []backend{{"mem", MemFactory}, {"sqlite", SQLiteFactory}}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			st, err := backend.factory(t.TempDir())
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			defer st.Close()
			seedTrustFixture(t, st)
			ctx := context.Background()

			got, err := st.(TrustAggregatePort).TrustStats(ctx, 0)
			if err != nil {
				t.Fatalf("TrustStats(0): %v", err)
			}
			if got.TopBoundaries == nil || len(got.TopBoundaries) != 0 {
				t.Fatalf("TopBoundaries with topN=0 = %#v, want empty non-nil slice", got.TopBoundaries)
			}

			got, err = st.(TrustAggregatePort).TrustStats(ctx, 2)
			if err != nil {
				t.Fatalf("TrustStats(2): %v", err)
			}
			want := []ExternalBoundary{
				{QualifiedName: "net/http.Get", IncidentEdges: 3},
				{QualifiedName: "aaa.Tie", IncidentEdges: 1},
			}
			if !reflect.DeepEqual(got.TopBoundaries, want) {
				t.Fatalf("TopBoundaries with topN=2 = %+v, want %+v", got.TopBoundaries, want)
			}
		})
	}
}
