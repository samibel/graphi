package diagnostic

import (
	"bytes"
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

func TestDedup_CollapsesIdenticalDeadSymbols(t *testing.T) {
	// Manually construct two dead_symbol diagnostics that share the exact dedup key
	// (same code, file, line, target symbol, reason, suppression) to exercise C3
	// dedup independent of C2 aggregation.
	a := makeNode(t, "function", "pkg.localFunc", "pkg/a.go", 10)
	d1 := Diagnostic{
		Severity: SeverityWarning, Code: "dead_symbol", Reason: ReasonDeadInternalSymbol,
		Message: "dead", Symbol: a.ID(), TargetSymbol: a.ID(),
		File: "pkg/a.go", Line: 10, Column: 1,
		Actions:    []CodeAction{{Kind: ActionSafeDeleteSymbol, TargetSymbol: a.ID()}},
		Confidence: ConfidenceExact, OccurrenceCount: 1,
	}
	d2 := d1

	tally := newTally()
	out := dedupStage()([]Diagnostic{d1, d2}, tally)
	if len(out) != 1 {
		t.Fatalf("expected 1 deduped diagnostic, got %d", len(out))
	}
	if out[0].OccurrenceCount != 2 {
		t.Fatalf("expected occurrence count 2, got %d", out[0].OccurrenceCount)
	}
	if tally.DedupCollapsed != 1 {
		t.Fatalf("expected DedupCollapsed 1, got %d", tally.DedupCollapsed)
	}
}

func TestDedup_C2AggregationNotRecollapsed(t *testing.T) {
	// End-to-end (WP-12): two heuristic references to the same target are
	// aggregated BY THE ANALYZER into a single diagnostic (count 2, merged
	// evidence), so there are no per-edge diagnostics for a later stage to
	// re-collapse. --all and default both show exactly that one diagnostic.
	ctx := context.Background()
	store := graphstore.NewMemStore()
	a1 := makeNode(t, "function", "pkg.A1", "pkg/a.go", 10)
	a2 := makeNode(t, "function", "pkg.A2", "pkg/a.go", 10)
	b := makeNode(t, "function", "pkg.B", "pkg/b.go", 20)
	for _, n := range []model.Node{a1, a2, b} {
		_ = store.PutNode(ctx, n)
	}
	e1, _ := model.NewEdge(a1.ID(), b.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "e1", []string{"seed"})
	e2, _ := model.NewEdge(a2.ID(), b.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "e2", []string{"seed"})
	_ = store.PutEdge(ctx, e1)
	_ = store.PutEdge(ctx, e2)

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true, ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	// The analyzer already aggregated by target, so --all shows one diagnostic.
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 aggregated diagnostic with --all, got %d: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	// Default output shows the same single aggregated diagnostic.
	resDef, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(resDef.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic by default, got %d", len(resDef.Diagnostics))
	}
	if resDef.Diagnostics[0].OccurrenceCount != 2 {
		t.Fatalf("expected occurrence count 2, got %d", resDef.Diagnostics[0].OccurrenceCount)
	}
	// Aggregation now happens in the analyzer, so the suppression-stage
	// aggregated_external_import category is not exercised.
	if resDef.Summary.SuppressedByCategory["aggregated_external_import"] != 0 {
		t.Fatalf("expected 0 aggregated_external_import suppressions (analyzer aggregates), got %+v", resDef.Summary)
	}
	if resDef.Summary.DedupCollapsed != 0 {
		t.Fatalf("C3 should not re-collapse the analyzer aggregate, got DedupCollapsed=%d", resDef.Summary.DedupCollapsed)
	}
	if resDef.Summary.Shown+resDef.Summary.TotalWithheld != resDef.Summary.TotalAnalyzed {
		t.Fatalf("reconciliation violated: shown=%d withheld=%d analyzed=%d", resDef.Summary.Shown, resDef.Summary.TotalWithheld, resDef.Summary.TotalAnalyzed)
	}
}

func TestDedup_PreservesDistinctFindings(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	a := makeNode(t, "function", "pkg.A", "pkg/a.go", 10)
	b := makeNode(t, "function", "pkg.B", "pkg/b.go", 20)
	c := makeNode(t, "function", "pkg.C", "pkg/c.go", 30)
	for _, n := range []model.Node{a, b, c} {
		_ = store.PutNode(ctx, n)
	}
	e1, _ := model.NewEdge(a.ID(), b.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "e1", []string{"seed"})
	e2, _ := model.NewEdge(a.ID(), c.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "e2", []string{"seed"})
	_ = store.PutEdge(ctx, e1)
	_ = store.PutEdge(ctx, e2)

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true, ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 2 {
		t.Fatalf("expected 2 distinct diagnostics, got %d: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Summary.DedupCollapsed != 0 {
		t.Fatalf("expected DedupCollapsed 0, got %+v", res.Summary)
	}
}

func TestDedup_PreservesSafeDeleteAction(t *testing.T) {
	a := makeNode(t, "function", "pkg.localFunc", "pkg/a.go", 10)
	d1 := Diagnostic{
		Severity: SeverityWarning, Code: "dead_symbol", Reason: ReasonDeadInternalSymbol,
		Message: "dead", Symbol: a.ID(), TargetSymbol: a.ID(),
		File: "pkg/a.go", Line: 10, Column: 1, Actions: []CodeAction{},
		Confidence: ConfidenceExact, OccurrenceCount: 1,
	}
	d2 := d1
	d2.Actions = []CodeAction{{Kind: ActionSafeDeleteSymbol, TargetSymbol: a.ID()}}

	tally := newTally()
	out := dedupStage()([]Diagnostic{d1, d2}, tally)
	if len(out) != 1 {
		t.Fatalf("expected 1 collapsed diagnostic, got %d", len(out))
	}
	if len(out[0].Actions) != 1 || out[0].Actions[0].Kind != ActionSafeDeleteSymbol {
		t.Fatalf("safe_delete action should be preserved, got %+v", out[0].Actions)
	}
}

func TestDedup_DoesNotRecountC2Aggregate(t *testing.T) {
	d := Diagnostic{
		Severity: SeverityWarning, Code: "unresolved_reference", Reason: ReasonUnresolvedExternalImport,
		Message: "external", Symbol: "a", TargetSymbol: "ext",
		File: "pkg/a.go", Line: 10, Column: 1, Actions: []CodeAction{},
		Confidence: ConfidenceHeuristic, OccurrenceCount: 5,
	}
	tally := newTally()
	out := dedupStage()([]Diagnostic{d}, tally)
	if out[0].OccurrenceCount != 5 {
		t.Fatalf("C2 aggregate count should be preserved, got %d", out[0].OccurrenceCount)
	}
	if tally.DedupCollapsed != 0 {
		t.Fatalf("C3 should not count single C2 aggregate as collapsed, got %d", tally.DedupCollapsed)
	}
}

func TestDedup_Determinism(t *testing.T) {
	ctx := context.Background()
	store, _ := seed(t)
	opts := DiagnoseOptions{All: true, ConfidenceThreshold: "heuristic"}
	var first []byte
	for i := 0; i < 5; i++ {
		res, err := DiagnoseWithOptions(ctx, store, nil, opts)
		if err != nil {
			t.Fatalf("DiagnoseWithOptions: %v", err)
		}
		out, err := Marshal(res)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if first == nil {
			first = out
		} else if !bytes.Equal(first, out) {
			t.Fatalf("run %d not deterministic:\n a=%s\n b=%s", i, first, out)
		}
	}
}

func TestDedup_C2C3Composition(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	a := makeNode(t, "function", "pkg.A", "pkg/a.go", 10)
	b := makeNode(t, "function", "pkg.B", "pkg/b.go", 20)
	c := makeNode(t, "function", "pkg.C", "pkg/c.go", 30)
	target := makeNode(t, "function", "external.T", "external/t.go", 1)
	for _, n := range []model.Node{a, b, c, target} {
		_ = store.PutNode(ctx, n)
	}
	for _, from := range []model.Node{a, b, c} {
		e, _ := model.NewEdge(from.ID(), target.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "external", []string{"seed"})
		_ = store.PutEdge(ctx, e)
	}

	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 aggregated diagnostic, got %d", len(res.Diagnostics))
	}
	if res.Diagnostics[0].OccurrenceCount != 3 {
		t.Fatalf("expected aggregate count 3, got %d", res.Diagnostics[0].OccurrenceCount)
	}
	if res.Summary.DedupCollapsed != 0 {
		t.Fatalf("C3 should not re-collapse the analyzer aggregate, got DedupCollapsed=%d", res.Summary.DedupCollapsed)
	}
	// The analyzer aggregates by target, so no suppression-stage aggregation runs.
	if res.Summary.SuppressedByCategory["aggregated_external_import"] != 0 {
		t.Fatalf("expected 0 aggregated_external_import suppressions (analyzer aggregates), got %+v", res.Summary)
	}
	if res.Summary.Shown+res.Summary.TotalWithheld != res.Summary.TotalAnalyzed {
		t.Fatalf("reconciliation violated: shown=%d withheld=%d analyzed=%d", res.Summary.Shown, res.Summary.TotalWithheld, res.Summary.TotalAnalyzed)
	}
}
