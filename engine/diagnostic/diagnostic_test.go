package diagnostic

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// seed builds a small graph respecting the store's referential-integrity
// invariant (every edge endpoint exists):
//
//	A --references(heuristic)--> B   (unresolved reference; gives B an inbound ref)
//
// So A has an unresolved reference and no inbound references (dead); B is
// referenced and so not dead.
func seed(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(name string, line int) model.Node {
		n, err := model.NewNode("function", name, "pkg/"+name+".go", line, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", name, err)
		}
		return n
	}
	a, b := mk("A", 10), mk("B", 20)
	for _, n := range []model.Node{a, b} {
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode: %v", err)
		}
	}
	// Heuristic tier == the resolver could not confirm this reference.
	e, err := model.NewEdge(a.ID(), b.ID(), query.EdgeKindReferences, model.TierHeuristic, 0.4, "best-effort", []string{"seed"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	if err := store.PutEdge(ctx, e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	return store, map[string]model.NodeId{"A": a.ID(), "B": b.ID()}
}

func TestDiagnose_UnresolvedRef(t *testing.T) {
	store, ids := seed(t)
	res, err := Diagnose(context.Background(), store, []string{KindUnresolvedRef})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if res.Outcome != OutcomeReported {
		t.Fatalf("outcome = %q, want reported", res.Outcome)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Severity != SeverityWarning || d.Code != "unresolved_reference" {
		t.Fatalf("got severity=%q code=%q, want warning/unresolved_reference", d.Severity, d.Code)
	}
	if d.Symbol != ids["A"] {
		t.Fatalf("anchored at %q, want A (%q)", d.Symbol, ids["A"])
	}
	if len(d.Actions) != 0 {
		t.Fatalf("unresolved_reference should carry no auto-action, got %+v", d.Actions)
	}
}

func TestDiagnose_DeadSymbol(t *testing.T) {
	store, ids := seed(t)
	res, err := Diagnose(context.Background(), store, []string{KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if res.Outcome != OutcomeReported {
		t.Fatalf("outcome = %q, want reported", res.Outcome)
	}
	// Only A is dead (no inbound references); B is referenced by A.
	if len(res.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	d := res.Diagnostics[0]
	if d.Symbol != ids["A"] || d.Code != "dead_symbol" || d.Severity != SeverityWarning {
		t.Fatalf("got %+v, want dead_symbol warning on A", d)
	}
	if len(d.Actions) != 1 || d.Actions[0].Kind != ActionSafeDeleteSymbol {
		t.Fatalf("want one safe_delete_symbol action, got %+v", d.Actions)
	}
	if d.Actions[0].TargetSymbol != ids["A"] {
		t.Fatalf("action target = %q, want A", d.Actions[0].TargetSymbol)
	}
}

func TestDiagnose_Clean_TypedEmpty(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	// Two mutually-referencing functions at derived tier: no unresolved refs,
	// both referenced → no dead symbols.
	a, _ := model.NewNode("function", "A", "pkg/a.go", 1, 1)
	b, _ := model.NewNode("function", "B", "pkg/b.go", 1, 1)
	_ = store.PutNode(ctx, a)
	_ = store.PutNode(ctx, b)
	e1, _ := model.NewEdge(a.ID(), b.ID(), query.EdgeKindCalls, model.TierDerived, 0.9, "r", []string{"e"})
	e2, _ := model.NewEdge(b.ID(), a.ID(), query.EdgeKindCalls, model.TierDerived, 0.9, "r", []string{"e"})
	_ = store.PutEdge(ctx, e1)
	_ = store.PutEdge(ctx, e2)

	res, err := Diagnose(ctx, store, nil) // all analyzers
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if res.Outcome != OutcomeClean {
		t.Fatalf("outcome = %q, want clean", res.Outcome)
	}
	if res.Diagnostics == nil {
		t.Fatal("Diagnostics must be non-nil (typed-empty), got nil")
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("want zero diagnostics, got %+v", res.Diagnostics)
	}
	// Typed-empty must serialize as an explicit empty array, not null.
	out, err := Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(out, []byte(`"diagnostics":[]`)) {
		t.Fatalf("clean result must serialize diagnostics:[]; got %s", out)
	}
}

func TestDiagnose_UnavailableKind(t *testing.T) {
	store, _ := seed(t)
	res, err := Diagnose(context.Background(), store, []string{"no_such_analyzer"})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if res.Outcome != OutcomeUnavailable {
		t.Fatalf("outcome = %q, want unavailable", res.Outcome)
	}
	if len(res.Unavailable) != 1 || res.Unavailable[0] != "no_such_analyzer" {
		t.Fatalf("Unavailable = %v, want [no_such_analyzer]", res.Unavailable)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unavailable run must yield no diagnostics, got %+v", res.Diagnostics)
	}
}

func TestDiagnose_PartialUnavailable(t *testing.T) {
	store, _ := seed(t)
	// One known + one unknown: known runs, unknown recorded as unavailable.
	res, err := Diagnose(context.Background(), store, []string{KindUnresolvedRef, "bogus"})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if res.Outcome != OutcomeReported {
		t.Fatalf("outcome = %q, want reported", res.Outcome)
	}
	if len(res.Unavailable) != 1 || res.Unavailable[0] != "bogus" {
		t.Fatalf("Unavailable = %v, want [bogus]", res.Unavailable)
	}
}

func TestMarshal_Deterministic(t *testing.T) {
	store, _ := seed(t)
	ctx := context.Background()

	first, err := Diagnose(ctx, store, nil)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	a, err := Marshal(first)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Re-run and re-marshal: byte-identical (stands in for full-vs-incremental).
	second, _ := Diagnose(ctx, store, nil)
	b, _ := Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic marshal:\n a=%s\n b=%s", a, b)
	}

	// Shuffled input order must not change the bytes (canonical sort).
	shuffled := Result{Outcome: first.Outcome, Unavailable: first.Unavailable}
	for i := len(first.Diagnostics) - 1; i >= 0; i-- {
		shuffled.Diagnostics = append(shuffled.Diagnostics, first.Diagnostics[i])
	}
	c, _ := Marshal(shuffled)
	if !bytes.Equal(a, c) {
		t.Fatalf("marshal not order-independent:\n a=%s\n c=%s", a, c)
	}
	// Both findings present; at the same anchor, code tie-break puts dead_symbol
	// before unresolved_reference.
	if !strings.Contains(string(a), "unresolved_reference") || !strings.Contains(string(a), "dead_symbol") {
		t.Fatalf("expected both diagnostics present: %s", a)
	}
	if strings.Index(string(a), "dead_symbol") > strings.Index(string(a), "unresolved_reference") {
		t.Fatalf("dead_symbol must sort before unresolved_reference at same anchor: %s", a)
	}
}
