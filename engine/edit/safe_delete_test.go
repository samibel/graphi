package edit

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// putEdge is a test helper for safe-delete fixtures.
func putEdge(t *testing.T, store interface {
	PutEdge(context.Context, model.Edge) error
}, from, to model.NodeId, kind string, tier model.ConfidenceTier) {
	t.Helper()
	e, err := model.NewEdge(from, to, kind, tier, 0.8, "ref", []string{"x:1"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	if err := store.PutEdge(context.Background(), e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
}

// TestApplySafeDelete_BlockEntryPoint is the WP-11 correctness guard: a live
// framework entry point (here a @Bean method) with ZERO in-graph inbound
// references must NOT be deletable — the framework invokes it by reflection,
// which the static graph cannot see, so removing it would break the build.
func TestApplySafeDelete_BlockEntryPoint(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"app/App.java": "class App { }\n"})
	ctx := context.Background()

	bean, err := model.NewNode("method", "app.dataSource", "app/App.java", 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	bean = bean.WithMeta(model.NewNodeMeta([]string{"Bean"}, nil))
	if err := store.PutNode(ctx, bean); err != nil {
		t.Fatalf("PutNode: %v", err)
	}

	// No inbound edges at all: without the entry-point guard the gate would clear
	// and delete a live Spring bean.
	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(bean.ID())})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteBlocked {
		t.Fatalf("outcome = %q, want blocked (a live @Bean must not be deletable)", res.Outcome)
	}
	if len(res.BlockingRefs) != 1 || res.BlockingRefs[0].Reason != ReasonEntrypoint {
		t.Fatalf("want 1 entrypoint blocking ref, got %+v", res.BlockingRefs)
	}
	if res.BlockingRefs[0].Symbol != bean.ID() {
		t.Fatalf("entrypoint blocking ref should point at the target itself, got %+v", res.BlockingRefs[0])
	}
}

// TestApplySafeDelete_BlockOverride is the WP-14-follow-up correctness guard: a
// method carrying the "override" flag (a Kotlin/C#/TS `override` member) with
// ZERO in-graph inbound references must NOT be deletable — it implements a
// supertype contract invoked polymorphically, an edge the static graph resolves
// to the base type, so removing the concrete override would break the build.
func TestApplySafeDelete_BlockOverride(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"app/Widget.kt": "class Widget\n"})
	ctx := context.Background()

	ovr, err := model.NewNode("method", "app.render", "app/Widget.kt", 1, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	ovr = ovr.WithMeta(model.NewNodeMeta(nil, []string{"override"}))
	if err := store.PutNode(ctx, ovr); err != nil {
		t.Fatalf("PutNode: %v", err)
	}

	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(ovr.ID())})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteBlocked {
		t.Fatalf("outcome = %q, want blocked (an override member must not be deletable)", res.Outcome)
	}
	if len(res.BlockingRefs) != 1 || res.BlockingRefs[0].Reason != ReasonEntrypoint {
		t.Fatalf("want 1 entrypoint blocking ref, got %+v", res.BlockingRefs)
	}
}

func TestApplySafeDelete_BlockLiveReference(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n", "b.go": "use Foo\n"})
	ctx := context.Background()
	foo := putNode(t, store, "const", "Foo", "a.go", 1)
	user := putNode(t, store, "function", "User", "b.go", 1)
	putEdge(t, store, user, foo, "references", model.TierDerived)

	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteBlocked {
		t.Fatalf("outcome = %q, want blocked", res.Outcome)
	}
	if len(res.BlockingRefs) != 1 {
		t.Fatalf("blocking refs = %d, want 1: %+v", len(res.BlockingRefs), res.BlockingRefs)
	}
	br := res.BlockingRefs[0]
	if br.Symbol != user || br.Reason != ReasonLiveReference || br.EdgeKind != "references" {
		t.Fatalf("blocking ref = %+v, want User/live_reference/references", br)
	}
}

func TestApplySafeDelete_BlockTestReference(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n"})
	ctx := context.Background()
	foo := putNode(t, store, "const", "Foo", "a.go", 1)
	tst := putNode(t, store, "function", "TestFoo", "a_test.go", 3)
	putEdge(t, store, tst, foo, "calls", model.TierDerived)

	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteBlocked || len(res.BlockingRefs) != 1 {
		t.Fatalf("want blocked with 1 ref, got %q %+v", res.Outcome, res.BlockingRefs)
	}
	if res.BlockingRefs[0].Reason != ReasonTestReference {
		t.Fatalf("reason = %q, want test_reference (test-only refs still block)", res.BlockingRefs[0].Reason)
	}
}

func TestApplySafeDelete_BlockUnresolved(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n", "b.go": "use Foo\n"})
	ctx := context.Background()
	foo := putNode(t, store, "const", "Foo", "a.go", 1)
	user := putNode(t, store, "function", "User", "b.go", 1)
	putEdge(t, store, user, foo, "references", model.TierHeuristic) // low-confidence

	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteBlocked || len(res.BlockingRefs) != 1 {
		t.Fatalf("want blocked with 1 ref, got %q %+v", res.Outcome, res.BlockingRefs)
	}
	if res.BlockingRefs[0].Reason != ReasonUnresolved {
		t.Fatalf("reason = %q, want unresolved (fail-safe on low-confidence)", res.BlockingRefs[0].Reason)
	}
}

func TestApplySafeDelete_CleanDryRun(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n"})
	ctx := context.Background()
	foo := putNode(t, store, "const", "Foo", "a.go", 1)

	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(foo), DryRun: true})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteApplied || !res.DryRun {
		t.Fatalf("got %q dryRun=%v, want applied/true", res.Outcome, res.DryRun)
	}
	if len(res.BlockingRefs) != 0 {
		t.Fatalf("want no blocking refs, got %+v", res.BlockingRefs)
	}
	if len(res.TouchedFiles) != 1 || res.TouchedFiles[0] != "a.go" {
		t.Fatalf("touched = %v, want [a.go]", res.TouchedFiles)
	}
}

func TestApplySafeDelete_NewlyDeadAdvisory(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n"})
	ctx := context.Background()
	foo := putNode(t, store, "const", "Foo", "a.go", 1)
	bar := putNode(t, store, "function", "Bar", "b.go", 1)
	// Foo references Bar; Bar has no other inbound reference → removing Foo makes
	// Bar newly-dead. Foo itself has no inbound → gate clears.
	putEdge(t, store, foo, bar, "references", model.TierDerived)

	res, err := a.ApplySafeDelete(ctx, SafeDeleteOp{TargetSymbol: string(foo), DryRun: true})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteApplied {
		t.Fatalf("outcome = %q, want applied", res.Outcome)
	}
	if len(res.NewlyDead) != 1 || res.NewlyDead[0] != bar {
		t.Fatalf("newly dead = %v, want [Bar]", res.NewlyDead)
	}
}

func TestApplySafeDelete_Unavailable(t *testing.T) {
	a, _, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n"})
	res, err := a.ApplySafeDelete(context.Background(), SafeDeleteOp{TargetSymbol: "ffffffffffffffff"})
	if err != nil {
		t.Fatalf("ApplySafeDelete: %v", err)
	}
	if res.Outcome != SafeDeleteUnavailable {
		t.Fatalf("outcome = %q, want unavailable", res.Outcome)
	}
}
