package diagnostic

import (
	"context"
	"fmt"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// TestUnresolvedRef_AggregatedByTarget is the WP-12 gate: N heuristic edges into
// a SINGLE external target collapse to exactly ONE unresolved_reference
// diagnostic with OccurrenceCount=N and merged (deduped, sorted, bounded)
// evidence — not N per-edge diagnostics.
func TestUnresolvedRef_AggregatedByTarget(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()

	const n = 7
	target := makeNode(t, "function", "external.Widen", "external/w.go", 1)
	if err := store.PutNode(ctx, target); err != nil {
		t.Fatalf("PutNode target: %v", err)
	}
	for i := 0; i < n; i++ {
		from := makeNode(t, "function", fmt.Sprintf("pkg.Caller%d", i), fmt.Sprintf("pkg/c%d.go", i), 10+i)
		if err := store.PutNode(ctx, from); err != nil {
			t.Fatalf("PutNode from: %v", err)
		}
		e, err := model.NewEdge(from.ID(), target.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4,
			"heuristic ref", []string{fmt.Sprintf("pkg/c%d.go:%d", i, 10+i)})
		if err != nil {
			t.Fatalf("NewEdge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
	}

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("want exactly 1 aggregated unresolved_reference diagnostic, got %d: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Code != "unresolved_reference" {
		t.Fatalf("code = %q, want unresolved_reference", d.Code)
	}
	if d.OccurrenceCount != n {
		t.Fatalf("OccurrenceCount = %d, want %d", d.OccurrenceCount, n)
	}
	if d.TargetSymbol != target.ID() {
		t.Fatalf("TargetSymbol = %q, want %q", d.TargetSymbol, target.ID())
	}
	// Evidence merges every edge's citation (deduped + sorted): one per referrer.
	if len(d.Evidence) != n {
		t.Fatalf("merged evidence length = %d, want %d: %v", len(d.Evidence), n, d.Evidence)
	}
	for i := 1; i < len(d.Evidence); i++ {
		if d.Evidence[i-1] > d.Evidence[i] {
			t.Fatalf("evidence not sorted: %v", d.Evidence)
		}
	}
}

// TestUnresolvedRef_DistinctTargetsStaySeparate confirms aggregation is per
// target: two targets yield two diagnostics with independent counts, ordered
// deterministically by target qualified name.
func TestUnresolvedRef_DistinctTargetsStaySeparate(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()

	tA := makeNode(t, "function", "external.Alpha", "external/a.go", 1)
	tB := makeNode(t, "function", "external.Beta", "external/b.go", 1)
	for _, n := range []model.Node{tA, tB} {
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
	// 3 edges to Alpha, 1 to Beta.
	mkEdges := func(target model.Node, count int) {
		for i := 0; i < count; i++ {
			from := makeNode(t, "function", fmt.Sprintf("pkg.%s%d", target.QualifiedName(), i), fmt.Sprintf("pkg/%s%d.go", target.QualifiedName(), i), 5)
			_ = store.PutNode(ctx, from)
			e, _ := model.NewEdge(from.ID(), target.ID(), query.EdgeKindCalls, model.TierHeuristic, 0.4, "r", []string{"e"})
			_ = store.PutEdge(ctx, e)
		}
	}
	mkEdges(tA, 3)
	mkEdges(tB, 1)

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 2 {
		t.Fatalf("want 2 diagnostics (one per target), got %d: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	byTarget := map[model.NodeId]int{}
	for _, d := range res.Diagnostics {
		byTarget[d.TargetSymbol] = d.OccurrenceCount
	}
	if byTarget[tA.ID()] != 3 || byTarget[tB.ID()] != 1 {
		t.Fatalf("per-target counts wrong: Alpha=%d (want 3), Beta=%d (want 1)", byTarget[tA.ID()], byTarget[tB.ID()])
	}
}
