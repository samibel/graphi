package diagnostic

import (
	"bytes"
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

func TestActionGate_QualifyingDeadSymbolGetsSafeDelete(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.localFunc", "pkg/a.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(res.Diagnostics))
	}
	if len(res.Diagnostics[0].Actions) != 1 || res.Diagnostics[0].Actions[0].Kind != ActionSafeDeleteSymbol {
		t.Fatalf("expected one safe_delete_symbol action, got %+v", res.Diagnostics[0].Actions)
	}
}

func TestActionGate_SuppressedDeadSymbolLosesSafeDelete(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.localFunc", "pkg/a_test.go", 10)
	_ = store.PutNode(ctx, n)

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("test-code finding should be suppressed from default output, got %+v", res.Diagnostics)
	}
	// With --all, the suppressed diagnostic should have no safe_delete_symbol.
	resAll, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(resAll.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic with --all, got %d", len(resAll.Diagnostics))
	}
	for _, a := range resAll.Diagnostics[0].Actions {
		if a.Kind == ActionSafeDeleteSymbol {
			t.Fatalf("suppressed finding should not have safe_delete_symbol, got %+v", resAll.Diagnostics[0].Actions)
		}
	}
}

func TestActionGate_PublicAPIDeadSymbolLosesSafeDelete(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	n := makeNode(t, "function", "pkg.PublicFunc", "pkg/api.go", 10)
	_ = store.PutNode(ctx, n)

	resAll, err := DiagnoseWithOptions(ctx, store, []string{KindDeadSymbol}, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(resAll.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic with --all, got %d", len(resAll.Diagnostics))
	}
	for _, a := range resAll.Diagnostics[0].Actions {
		if a.Kind == ActionSafeDeleteSymbol {
			t.Fatalf("public API finding should not have safe_delete_symbol, got %+v", resAll.Diagnostics[0].Actions)
		}
	}
}

func TestActionGate_HeuristicFindingHasNoMutatingAction(t *testing.T) {
	ctx := context.Background()
	store, _ := seed(t)
	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true, ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatalf("expected at least one unresolved diagnostic")
	}
	for _, d := range res.Diagnostics {
		for _, a := range d.Actions {
			if a.Kind == ActionSafeDeleteSymbol {
				t.Fatalf("heuristic finding should not have safe_delete_symbol, got %+v", d.Actions)
			}
		}
	}
}

func TestActionGate_InspectReferenceIsPreview(t *testing.T) {
	ctx := context.Background()
	store, _ := seed(t)
	res, err := DiagnoseWithOptions(ctx, store, []string{KindUnresolvedRef}, DiagnoseOptions{All: true, ConfidenceThreshold: "heuristic"})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	for _, d := range res.Diagnostics {
		hasInspect := false
		for _, a := range d.Actions {
			if a.Kind == ActionInspectReference {
				hasInspect = true
				if !a.Preview {
					t.Fatalf("inspect_reference should be preview-only, got %+v", a)
				}
			}
		}
		if !hasInspect {
			t.Fatalf("heuristic finding should have inspect_reference action, got %+v", d.Actions)
		}
	}
}

func TestActionGate_ZeroUnsafeSuggestedActions(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	a := makeNode(t, "function", "pkg.localFunc", "pkg/a.go", 10)
	pub := makeNode(t, "function", "pkg.PublicFunc", "pkg/api.go", 20)
	test := makeNode(t, "function", "pkg.TestFunc", "pkg/a_test.go", 30)
	for _, n := range []model.Node{a, pub, test} {
		_ = store.PutNode(ctx, n)
	}

	res, err := Diagnose(ctx, store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for _, d := range res.Diagnostics {
		for _, a := range d.Actions {
			if a.Kind == ActionSafeDeleteSymbol && a.Preview {
				t.Fatalf("preview safe_delete_symbol is unsafe: %+v", a)
			}
		}
	}
}

func TestActionGate_Determinism(t *testing.T) {
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

func TestMetrics_DerivableFromResult(t *testing.T) {
	ctx := context.Background()
	store, _ := seed(t)
	res, err := Diagnose(ctx, store, nil)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	m := res.Metrics()
	if m.TotalDiagnostics != len(res.Diagnostics) {
		t.Fatalf("TotalDiagnostics mismatch")
	}
	if m.DefaultCount != res.Summary.Shown {
		t.Fatalf("DefaultCount mismatch")
	}
	if m.AllCount != res.Summary.TotalAnalyzed {
		t.Fatalf("AllCount mismatch")
	}
}
